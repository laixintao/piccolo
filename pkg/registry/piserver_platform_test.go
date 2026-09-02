package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/containerd/containerd/images"
	"github.com/go-logr/logr"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/laixintao/piccolo/pkg/oci"
)

const (
	testARM64Digest = digest.Digest("sha256:00f88ca381be101a6512e55ad3ca641b30daf08b6272ffe9f3c57e51c46b1e0d")
	testAMD64Digest = digest.Digest("sha256:4c0e1a6490f9bff111e94f5704ab646f3da2b29212876990ac75db1704ec15ca")
)

func TestPiServerSelectsLeafManifestForPlatform(t *testing.T) {
	t.Parallel()

	client, rootDigest, rootBody, leafBodies := newMultiPlatformClient(t)
	tests := []struct {
		name       string
		platform   ocispec.Platform
		expected   digest.Digest
		header     string
		statusCode int
	}{
		{
			name:       "arm64 Pi returns arm64 leaf",
			platform:   ocispec.Platform{OS: "linux", Architecture: "arm64"},
			expected:   testARM64Digest,
			header:     "linux/arm64",
			statusCode: http.StatusOK,
		},
		{
			name:       "amd64 Pi returns amd64 leaf",
			platform:   ocispec.Platform{OS: "linux", Architecture: "amd64"},
			expected:   testAMD64Digest,
			header:     "linux/amd64",
			statusCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := NewPiServer(client, "test", logr.Discard(), nil, WithPiPlatform(tt.platform))
			httpServer, err := server.Server("")
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodGet,
				"http://pi/v2/kube-galio/nginx-debug/manifests/v0.0.1?ns=harbor.shopeemobile.com", nil)
			req.Header.Set(PlatformHeaderKey, tt.header)
			rw := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(rw, req)

			require.Equal(t, tt.statusCode, rw.Code)
			require.Equal(t, tt.expected.String(), rw.Header().Get("Docker-Content-Digest"))
			require.Equal(t, images.MediaTypeDockerSchema2Manifest, rw.Header().Get("Content-Type"))
			require.Equal(t, leafBodies[tt.expected], rw.Body.Bytes())
		})
	}

	t.Run("legacy request without platform returns index", func(t *testing.T) {
		server := NewPiServer(client, "test", logr.Discard(), nil,
			WithPiPlatform(ocispec.Platform{OS: "linux", Architecture: "arm64"}))
		httpServer, err := server.Server("")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet,
			"http://pi/v2/kube-galio/nginx-debug/manifests/v0.0.1?ns=harbor.shopeemobile.com", nil)
		rw := httptest.NewRecorder()
		httpServer.Handler.ServeHTTP(rw, req)

		require.Equal(t, http.StatusOK, rw.Code)
		require.Equal(t, rootDigest.String(), rw.Header().Get("Docker-Content-Digest"))
		require.Equal(t, images.MediaTypeDockerSchema2ManifestList, rw.Header().Get("Content-Type"))
		require.Equal(t, rootBody, rw.Body.Bytes())
	})

	t.Run("Pi rejects a different platform", func(t *testing.T) {
		server := NewPiServer(client, "test", logr.Discard(), nil,
			WithPiPlatform(ocispec.Platform{OS: "linux", Architecture: "arm64"}))
		httpServer, err := server.Server("")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet,
			"http://pi/v2/kube-galio/nginx-debug/manifests/v0.0.1?ns=harbor.shopeemobile.com", nil)
		req.Header.Set(PlatformHeaderKey, "linux/amd64")
		rw := httptest.NewRecorder()
		httpServer.Handler.ServeHTTP(rw, req)

		require.Equal(t, http.StatusNotFound, rw.Code)
	})
}

type manifestFixture struct {
	body      []byte
	mediaType string
}

type memoryOCIClient struct {
	ref       string
	root      digest.Digest
	manifests map[digest.Digest]manifestFixture
	blobs     map[digest.Digest][]byte
}

func newMultiPlatformClient(t *testing.T) (*memoryOCIClient, digest.Digest, []byte, map[digest.Digest][]byte) {
	t.Helper()

	armConfig := mustJSON(t, ocispec.Image{Platform: ocispec.Platform{OS: "linux", Architecture: "arm64"}})
	amdConfig := mustJSON(t, ocispec.Image{Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"}})
	armConfigDigest := digest.FromBytes(armConfig)
	amdConfigDigest := digest.FromBytes(amdConfig)

	armManifest := mustJSON(t, ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: images.MediaTypeDockerSchema2Manifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    armConfigDigest,
			Size:      int64(len(armConfig)),
		},
	})
	amdManifest := mustJSON(t, ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: images.MediaTypeDockerSchema2Manifest,
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    amdConfigDigest,
			Size:      int64(len(amdConfig)),
		},
	})
	index := mustJSON(t, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: images.MediaTypeDockerSchema2ManifestList,
		Manifests: []ocispec.Descriptor{
			{
				MediaType: images.MediaTypeDockerSchema2Manifest,
				Digest:    testAMD64Digest,
				Size:      int64(len(amdManifest)),
				Platform:  &ocispec.Platform{OS: "linux", Architecture: "amd64"},
			},
			{
				MediaType: images.MediaTypeDockerSchema2Manifest,
				Digest:    testARM64Digest,
				Size:      int64(len(armManifest)),
				Platform:  &ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
			},
		},
	})
	rootDigest := digest.FromBytes(index)

	return &memoryOCIClient{
			ref:  "harbor.shopeemobile.com/kube-galio/nginx-debug:v0.0.1",
			root: rootDigest,
			manifests: map[digest.Digest]manifestFixture{
				rootDigest:      {body: index, mediaType: images.MediaTypeDockerSchema2ManifestList},
				testARM64Digest: {body: armManifest, mediaType: images.MediaTypeDockerSchema2Manifest},
				testAMD64Digest: {body: amdManifest, mediaType: images.MediaTypeDockerSchema2Manifest},
			},
			blobs: map[digest.Digest][]byte{
				armConfigDigest: armConfig,
				amdConfigDigest: amdConfig,
			},
		}, rootDigest, index, map[digest.Digest][]byte{
			testARM64Digest: armManifest,
			testAMD64Digest: amdManifest,
		}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return b
}

func (c *memoryOCIClient) Name() string { return "memory" }

func (c *memoryOCIClient) Verify(context.Context) error { return nil }

func (c *memoryOCIClient) Subscribe(context.Context) (<-chan oci.ImageEvent, <-chan error, <-chan error, error) {
	return nil, nil, nil, errors.New("not implemented")
}

func (c *memoryOCIClient) ListImages(context.Context) ([]oci.Image, error) {
	return nil, errors.New("not implemented")
}

func (c *memoryOCIClient) Resolve(_ context.Context, ref string) (digest.Digest, error) {
	if ref != c.ref {
		return "", oci.ErrNotFound
	}
	return c.root, nil
}

func (c *memoryOCIClient) Size(_ context.Context, dgst digest.Digest) (int64, error) {
	if manifest, ok := c.manifests[dgst]; ok {
		return int64(len(manifest.body)), nil
	}
	if blob, ok := c.blobs[dgst]; ok {
		return int64(len(blob)), nil
	}
	return 0, oci.ErrNotFound
}

func (c *memoryOCIClient) GetManifest(_ context.Context, dgst digest.Digest) ([]byte, string, error) {
	manifest, ok := c.manifests[dgst]
	if !ok {
		return nil, "", oci.ErrNotFound
	}
	return manifest.body, manifest.mediaType, nil
}

func (c *memoryOCIClient) GetBlob(_ context.Context, dgst digest.Digest) (io.ReadSeekCloser, error) {
	blob, ok := c.blobs[dgst]
	if !ok {
		return nil, oci.ErrNotFound
	}
	return &readSeekCloser{Reader: bytes.NewReader(blob)}, nil
}

type readSeekCloser struct {
	*bytes.Reader
}

func (*readSeekCloser) Close() error { return nil }
