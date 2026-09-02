package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/containerd/images"
	containerdplatforms "github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var ErrPlatformNotFound = errors.New("platform manifest not found")

// PlatformManifest is the concrete image manifest selected for a platform.
type PlatformManifest struct {
	Digest    digest.Digest
	MediaType string
	Content   []byte
}

// ParsePlatform parses and normalizes an OCI platform. An empty value resolves
// to the platform of the running Pi binary.
func ParsePlatform(value string) (ocispec.Platform, error) {
	var platform ocispec.Platform
	var err error
	if value == "" {
		platform = containerdplatforms.DefaultSpec()
	} else {
		parts := strings.Split(value, "/")
		if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
			return ocispec.Platform{}, fmt.Errorf("invalid platform %q: expected OS/ARCH[/VARIANT]", value)
		}
		platform, err = containerdplatforms.Parse(value)
		if err != nil {
			return ocispec.Platform{}, fmt.Errorf("invalid platform %q: %w", value, err)
		}
	}
	platform = containerdplatforms.Normalize(platform)
	if platform.OS == "" || platform.Architecture == "" {
		return ocispec.Platform{}, fmt.Errorf("invalid platform %q: OS and architecture are required", value)
	}
	return platform, nil
}

func FormatPlatform(platform ocispec.Platform) string {
	return containerdplatforms.Format(containerdplatforms.Normalize(platform))
}

// ManifestForPlatform resolves a root manifest or index digest to a concrete
// platform manifest. The returned digest always matches the returned content.
func ManifestForPlatform(ctx context.Context, client Client, root digest.Digest, platform ocispec.Platform) (PlatformManifest, error) {
	platform = containerdplatforms.Normalize(platform)
	if platform.OS == "" || platform.Architecture == "" {
		return PlatformManifest{}, errors.New("platform OS and architecture are required")
	}
	return manifestForPlatform(ctx, client, root, platform, map[digest.Digest]struct{}{})
}

func manifestForPlatform(
	ctx context.Context,
	client Client,
	dgst digest.Digest,
	platform ocispec.Platform,
	visited map[digest.Digest]struct{},
) (PlatformManifest, error) {
	if _, ok := visited[dgst]; ok {
		return PlatformManifest{}, fmt.Errorf("manifest graph contains a cycle at %s", dgst)
	}
	visited[dgst] = struct{}{}
	defer delete(visited, dgst)

	b, mediaType, err := client.GetManifest(ctx, dgst)
	if err != nil {
		return PlatformManifest{}, fmt.Errorf("could not read manifest %s: %w", dgst, err)
	}

	switch mediaType {
	case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		var index ocispec.Index
		if err := json.Unmarshal(b, &index); err != nil {
			return PlatformManifest{}, fmt.Errorf("could not decode image index %s: %w", dgst, err)
		}

		matcher := containerdplatforms.OnlyStrict(platform)
		candidates := make([]ocispec.Descriptor, 0, len(index.Manifests))
		unknownPlatform := make([]ocispec.Descriptor, 0)
		for _, manifest := range index.Manifests {
			if manifest.Platform == nil {
				unknownPlatform = append(unknownPlatform, manifest)
				continue
			}
			if matcher.Match(*manifest.Platform) {
				candidates = append(candidates, manifest)
			}
		}
		candidates = append(candidates, unknownPlatform...)

		var candidateErrors []error
		for _, candidate := range candidates {
			selected, err := manifestForPlatform(ctx, client, candidate.Digest, platform, visited)
			if err == nil {
				return selected, nil
			}
			candidateErrors = append(candidateErrors, err)
		}
		if len(candidateErrors) > 0 {
			return PlatformManifest{}, errors.Join(append([]error{
				fmt.Errorf("%w: %s in index %s", ErrPlatformNotFound, FormatPlatform(platform), dgst),
			}, candidateErrors...)...)
		}
		return PlatformManifest{}, fmt.Errorf("%w: %s in index %s", ErrPlatformNotFound, FormatPlatform(platform), dgst)

	case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		var manifest ocispec.Manifest
		if err := json.Unmarshal(b, &manifest); err != nil {
			return PlatformManifest{}, fmt.Errorf("could not decode image manifest %s: %w", dgst, err)
		}
		configPlatform, err := readConfigPlatform(ctx, client, manifest.Config.Digest)
		if err != nil {
			return PlatformManifest{}, fmt.Errorf("could not determine platform for manifest %s: %w", dgst, err)
		}
		if !containerdplatforms.OnlyStrict(platform).Match(configPlatform) {
			return PlatformManifest{}, fmt.Errorf(
				"%w: manifest %s is %s, requested %s",
				ErrPlatformNotFound,
				dgst,
				FormatPlatform(configPlatform),
				FormatPlatform(platform),
			)
		}
		return PlatformManifest{Digest: dgst, MediaType: mediaType, Content: b}, nil

	default:
		return PlatformManifest{}, fmt.Errorf("unexpected media type %s for manifest %s", mediaType, dgst)
	}
}

func readConfigPlatform(ctx context.Context, client Client, dgst digest.Digest) (ocispec.Platform, error) {
	rc, err := client.GetBlob(ctx, dgst)
	if err != nil {
		return ocispec.Platform{}, err
	}
	defer rc.Close()

	var config ocispec.Image
	if err := json.NewDecoder(rc).Decode(&config); err != nil {
		return ocispec.Platform{}, err
	}
	platform := containerdplatforms.Normalize(config.Platform)
	if platform.OS == "" || platform.Architecture == "" {
		return ocispec.Platform{}, errors.New("image config does not declare OS and architecture")
	}
	return platform, nil
}
