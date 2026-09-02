package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	ErrNotFound = errors.New("content not found")
)

type Client interface {
	// Name returns the name of the Client implementation.
	Name() string

	// Verify checks that all expected configuration is set.
	Verify(ctx context.Context) error

	// Subscribe will notify for any image events ocuring in the store backend.
	Subscribe(ctx context.Context) (<-chan ImageEvent, <-chan error, <-chan error, error)

	// ListImages returns a list of all local images.
	ListImages(ctx context.Context) ([]Image, error)

	// Resolve returns the digest for the tagged image name reference.
	// The ref is expected to be in the format `registry/name:tag`.
	Resolve(ctx context.Context, ref string) (digest.Digest, error)

	// Size returns the content byte size for the given digest.
	// Will return ErrNotFound if the digest cannot be found.
	Size(ctx context.Context, dgst digest.Digest) (int64, error)

	// GetManifest returns the manifest content for the given digest.
	// Will return ErrNotFound if the digest cannot be found.
	GetManifest(ctx context.Context, dgst digest.Digest) ([]byte, string, error)

	// GetBlob returns a stream of the blob content for the given digest.
	// Will return ErrNotFound if the digest cannot be found.
	GetBlob(ctx context.Context, dgst digest.Digest) (io.ReadSeekCloser, error)
}

type UnknownDocument struct {
	MediaType string `json:"mediaType"`
	specs.Versioned
}

func DetermineMediaType(b []byte) (string, error) {
	var ud UnknownDocument
	if err := json.Unmarshal(b, &ud); err != nil {
		return "", err
	}
	if ud.SchemaVersion == 2 && ud.MediaType != "" {
		return ud.MediaType, nil
	}
	data := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &data); err != nil {
		return "", err
	}
	_, architectureOk := data["architecture"]
	_, osOk := data["os"]
	_, rootfsOk := data["rootfs"]
	if architectureOk && osOk && rootfsOk {
		return ocispec.MediaTypeImageConfig, nil
	}
	_, manifestsOk := data["manifests"]
	if ud.SchemaVersion == 2 && manifestsOk {
		return ocispec.MediaTypeImageIndex, nil
	}
	_, configOk := data["config"]
	if ud.SchemaVersion == 2 && configOk {
		return ocispec.MediaTypeImageManifest, nil
	}
	return "", errors.New("not able to determine media type")
}

// ArchTagKey returns the architecture scoped advertisement key for a tag
// reference. A bare tag key does not say which architectures the holder
// actually has content for, which matters for multi arch images.
func ArchTagKey(tagName, arch string) string {
	return fmt.Sprintf("%s|%s", tagName, arch)
}

// ImageArchitectures returns the architectures for which the local content
// store holds a platform specific manifest of the given image digest.
func ImageArchitectures(ctx context.Context, client Client, dgst digest.Digest) ([]string, error) {
	b, mt, err := client.GetManifest(ctx, dgst)
	if err != nil {
		return nil, err
	}
	switch mt {
	case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		var idx ocispec.Index
		if err := json.Unmarshal(b, &idx); err != nil {
			return nil, err
		}
		archSet := map[string]struct{}{}
		for _, m := range idx.Manifests {
			// Skip attestation manifests and entries without platform information.
			if m.Platform == nil || m.Platform.Architecture == "" || m.Platform.Architecture == "unknown" {
				continue
			}
			// Only count architectures whose manifest content exists locally.
			_, err := client.Size(ctx, m.Digest)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			archSet[m.Platform.Architecture] = struct{}{}
		}
		arches := make([]string, 0, len(archSet))
		for arch := range archSet {
			arches = append(arches, arch)
		}
		sort.Strings(arches)
		return arches, nil
	case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		arch, err := ManifestArchitecture(ctx, client, b)
		if err != nil {
			return nil, err
		}
		if arch == "" {
			return nil, nil
		}
		return []string{arch}, nil
	default:
		return nil, fmt.Errorf("unexpected media type %s for digest %s", mt, dgst)
	}
}

// ManifestArchitecture reads the config blob referenced by a platform
// specific image manifest and returns its architecture.
func ManifestArchitecture(ctx context.Context, client Client, manifestBytes []byte) (string, error) {
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", err
	}
	rc, err := client.GetBlob(ctx, manifest.Config.Digest)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	var config struct {
		Architecture string `json:"architecture"`
	}
	if err := json.Unmarshal(b, &config); err != nil {
		return "", err
	}
	return config.Architecture, nil
}

func WalkImage(ctx context.Context, client Client, img Image) ([]string, error) {
	keys := []string{}
	err := walk(ctx, []digest.Digest{img.Digest}, func(dgst digest.Digest) ([]digest.Digest, error) {
		b, mt, err := client.GetManifest(ctx, dgst)
		if err != nil {
			return nil, err
		}
		keys = append(keys, dgst.String())
		switch mt {
		case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
			var idx ocispec.Index
			if err := json.Unmarshal(b, &idx); err != nil {
				return nil, err
			}
			manifestDgsts := []digest.Digest{}
			for _, m := range idx.Manifests {
				_, err := client.Size(ctx, m.Digest)
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				manifestDgsts = append(manifestDgsts, m.Digest)
			}
			if len(manifestDgsts) == 0 {
				return nil, fmt.Errorf("could not find any platforms with local content in manifest %s", dgst)
			}
			return manifestDgsts, nil
		case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
			var manifest ocispec.Manifest
			err := json.Unmarshal(b, &manifest)
			if err != nil {
				return nil, err
			}
			keys = append(keys, manifest.Config.Digest.String())
			for _, layer := range manifest.Layers {
				keys = append(keys, layer.Digest.String())
			}
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected media type %s for digest %s", mt, dgst)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk image manifests: %w", err)
	}
	if len(keys) == 0 {
		return nil, errors.New("no image digests found")
	}
	return keys, nil
}

func walk(ctx context.Context, dgsts []digest.Digest, handler func(dgst digest.Digest) ([]digest.Digest, error)) error {
	for _, dgst := range dgsts {
		children, err := handler(dgst)
		if err != nil {
			return err
		}
		if len(children) == 0 {
			continue
		}
		err = walk(ctx, children, handler)
		if err != nil {
			return err
		}
	}
	return nil
}
