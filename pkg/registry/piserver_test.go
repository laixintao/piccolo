package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/go-logr/logr"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/laixintao/piccolo/pkg/oci"
)

type fakeOCIClient struct {
	resolve map[string]digest.Digest
	blobs   map[digest.Digest][]byte
}

func (c *fakeOCIClient) Name() string {
	return "fake"
}

func (c *fakeOCIClient) Verify(ctx context.Context) error {
	return nil
}

func (c *fakeOCIClient) Subscribe(ctx context.Context) (<-chan oci.ImageEvent, <-chan error, <-chan error, error) {
	return nil, nil, nil, nil
}

func (c *fakeOCIClient) ListImages(ctx context.Context) ([]oci.Image, error) {
	return nil, nil
}

func (c *fakeOCIClient) Resolve(ctx context.Context, ref string) (digest.Digest, error) {
	dgst, ok := c.resolve[ref]
	if !ok {
		return "", fmt.Errorf("image %s not found", ref)
	}
	return dgst, nil
}

func (c *fakeOCIClient) Size(ctx context.Context, dgst digest.Digest) (int64, error) {
	b, ok := c.blobs[dgst]
	if !ok {
		return 0, oci.ErrNotFound
	}
	return int64(len(b)), nil
}

func (c *fakeOCIClient) GetManifest(ctx context.Context, dgst digest.Digest) ([]byte, string, error) {
	b, ok := c.blobs[dgst]
	if !ok {
		return nil, "", oci.ErrNotFound
	}
	mt, err := oci.DetermineMediaType(b)
	if err != nil {
		return nil, "", err
	}
	return b, mt, nil
}

type nopReadSeekCloser struct {
	*bytes.Reader
}

func (nopReadSeekCloser) Close() error {
	return nil
}

func (c *fakeOCIClient) GetBlob(ctx context.Context, dgst digest.Digest) (io.ReadSeekCloser, error) {
	b, ok := c.blobs[dgst]
	if !ok {
		return nil, oci.ErrNotFound
	}
	return nopReadSeekCloser{bytes.NewReader(b)}, nil
}

type fakeSD struct {
	resolvedKeys []string
	peers        []netip.AddrPort
}

func (s *fakeSD) Ready(ctx context.Context) (bool, error) {
	return true, nil
}

func (s *fakeSD) Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error) {
	s.resolvedKeys = append(s.resolvedKeys, key)
	return s.peers, nil
}

func (s *fakeSD) Advertise(ctx context.Context, keys []string) error {
	return nil
}

func (s *fakeSD) Sync(ctx context.Context, keys []string) error {
	return nil
}

func (s *fakeSD) DoKeepAlive(ctx context.Context) error {
	return nil
}

func marshal(t *testing.T, v interface{}) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// newManifestFixtures returns an oci client with a tag pointing to an amd64
// platform specific manifest, and another tag pointing to a multi arch index.
func newManifestFixtures(t *testing.T) (*fakeOCIClient, digest.Digest, digest.Digest, []byte, []byte) {
	t.Helper()

	configBytes := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	configDgst := digest.FromBytes(configBytes)
	manifestBytes := marshal(t, map[string]interface{}{
		"schemaVersion": 2,
		"mediaType":     ocispec.MediaTypeImageManifest,
		"config": map[string]interface{}{
			"mediaType": ocispec.MediaTypeImageConfig,
			"digest":    configDgst.String(),
			"size":      len(configBytes),
		},
		"layers": []interface{}{},
	})
	manifestDgst := digest.FromBytes(manifestBytes)
	indexBytes := marshal(t, map[string]interface{}{
		"schemaVersion": 2,
		"mediaType":     ocispec.MediaTypeImageIndex,
		"manifests": []interface{}{
			map[string]interface{}{
				"mediaType": ocispec.MediaTypeImageManifest,
				"digest":    manifestDgst.String(),
				"size":      len(manifestBytes),
				"platform": map[string]interface{}{
					"architecture": "amd64",
					"os":           "linux",
				},
			},
		},
	})
	indexDgst := digest.FromBytes(indexBytes)

	ociClient := &fakeOCIClient{
		resolve: map[string]digest.Digest{
			"harbor.example.com/org/single-arch:v1": manifestDgst,
			"harbor.example.com/org/multi-arch:v1":  indexDgst,
		},
		blobs: map[digest.Digest][]byte{
			configDgst:   configBytes,
			manifestDgst: manifestBytes,
			indexDgst:    indexBytes,
		},
	}
	return ociClient, manifestDgst, indexDgst, manifestBytes, indexBytes
}

func TestPiServerManifestArchCheck(t *testing.T) {
	t.Parallel()

	ociClient, manifestDgst, _, manifestBytes, indexBytes := newManifestFixtures(t)

	piServer := NewPiServer(ociClient, "group", logr.Discard(), &fakeSD{})
	srv, err := piServer.Server("127.0.0.1:0")
	require.NoError(t, err)

	tests := []struct {
		name           string
		path           string
		arch           string
		expectedStatus int
		expectedBody   []byte
	}{
		{
			name:           "tag resolving to platform manifest with matching architecture",
			path:           "/v2/org/single-arch/manifests/v1",
			arch:           "amd64",
			expectedStatus: http.StatusOK,
			expectedBody:   manifestBytes,
		},
		{
			name:           "tag resolving to platform manifest with mismatching architecture",
			path:           "/v2/org/single-arch/manifests/v1",
			arch:           "arm64",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "tag resolving to platform manifest without architecture header",
			path:           "/v2/org/single-arch/manifests/v1",
			arch:           "",
			expectedStatus: http.StatusOK,
			expectedBody:   manifestBytes,
		},
		{
			name:           "tag resolving to index is served for any architecture",
			path:           "/v2/org/multi-arch/manifests/v1",
			arch:           "arm64",
			expectedStatus: http.StatusOK,
			expectedBody:   indexBytes,
		},
		{
			name:           "digest requests are content addressed and never rejected",
			path:           "/v2/org/single-arch/manifests/" + manifestDgst.String(),
			arch:           "arm64",
			expectedStatus: http.StatusOK,
			expectedBody:   manifestBytes,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path+"?ns=harbor.example.com", nil)
			if tt.arch != "" {
				req.Header.Set(ArchHeaderKey, tt.arch)
			}
			rw := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rw, req)

			require.Equal(t, tt.expectedStatus, rw.Code)
			if tt.expectedStatus == http.StatusOK {
				require.Equal(t, tt.expectedBody, rw.Body.Bytes())
			}
		})
	}
}

func TestRegistryResolvesTagsScopedToArchitecture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		expectedKey string
	}{
		{
			name:        "tag requests are resolved with the local architecture",
			path:        "/v2/org/multi-arch/manifests/v1",
			expectedKey: "harbor.example.com/org/multi-arch:v1|arm64",
		},
		{
			name:        "digest requests are resolved as is",
			path:        "/v2/org/multi-arch/blobs/sha256:44cb2cf712c060f69df7310e99339c1eb51a085446f1bb6d44469acff35b4355",
			expectedKey: "sha256:44cb2cf712c060f69df7310e99339c1eb51a085446f1bb6d44469acff35b4355",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceDiscover := &fakeSD{}
			registry := NewRegistry(serviceDiscover, logr.Discard(), WithLocalArch("arm64"), WithResolveRetries(1))
			srv, err := registry.Server("127.0.0.1:0")
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodGet, tt.path+"?ns=harbor.example.com", nil)
			rw := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rw, req)

			// No peers hold the key so the mirror falls back with 404, which
			// makes containerd pull from the upstream registry instead.
			require.Equal(t, http.StatusNotFound, rw.Code)
			require.Equal(t, []string{tt.expectedKey}, serviceDiscover.resolvedKeys)
			require.Equal(t, "arm64", req.Header.Get(ArchHeaderKey))
		})
	}
}
