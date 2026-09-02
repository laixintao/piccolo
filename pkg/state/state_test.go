package state

import (
	"context"
	"io"
	"net/netip"
	"testing"

	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestStartupSyncRecomputesImageReferenceMetrics(t *testing.T) {
	registry := "registry.example"
	client := &stateOCIClient{images: []oci.Image{
		{
			Name:       registry + "/team/tagged:v1",
			Registry:   registry,
			Repository: "team/tagged",
			Tag:        "v1",
			Digest:     digest.FromString("tagged"),
		},
		{
			Name:       registry + "/team/digest@" + digest.FromString("digest").String(),
			Registry:   registry,
			Repository: "team/digest",
			Digest:     digest.FromString("digest"),
		},
	}}
	discovery := &stateServiceDiscover{}

	metrics.AdvertisedImages.Reset()
	metrics.AdvertisedImageTags.Reset()
	metrics.AdvertisedImageDigests.Reset()
	metrics.AdvertisedKeys.Reset()
	t.Cleanup(func() {
		metrics.AdvertisedImages.Reset()
		metrics.AdvertisedImageTags.Reset()
		metrics.AdvertisedImageDigests.Reset()
		metrics.AdvertisedKeys.Reset()
	})

	startStartupSync(context.Background(), client, discovery, true, 0)

	require.Equal(t, 1, discovery.syncCalls)
	require.Equal(t, float64(2), testutil.ToFloat64(metrics.AdvertisedImages.WithLabelValues(registry)))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.AdvertisedImageTags.WithLabelValues(registry)))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.AdvertisedImageDigests.WithLabelValues(registry)))
}

type stateOCIClient struct {
	images []oci.Image
}

func (c *stateOCIClient) Name() string { return "state-test" }

func (c *stateOCIClient) Verify(context.Context) error { return nil }

func (c *stateOCIClient) Subscribe(context.Context) (<-chan oci.ImageEvent, <-chan error, <-chan error, error) {
	return nil, nil, nil, nil
}

func (c *stateOCIClient) ListImages(context.Context) ([]oci.Image, error) {
	return c.images, nil
}

func (c *stateOCIClient) Resolve(context.Context, string) (digest.Digest, error) {
	return "", nil
}

func (c *stateOCIClient) Size(context.Context, digest.Digest) (int64, error) {
	return 0, nil
}

func (c *stateOCIClient) GetManifest(context.Context, digest.Digest) ([]byte, string, error) {
	return []byte(`{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"size": 1
		},
		"layers": []
	}`), ocispec.MediaTypeImageManifest, nil
}

func (c *stateOCIClient) GetBlob(context.Context, digest.Digest) (io.ReadSeekCloser, error) {
	return nil, nil
}

type stateServiceDiscover struct {
	syncCalls int
}

func (s *stateServiceDiscover) Ready(context.Context) (bool, error) { return true, nil }

func (s *stateServiceDiscover) Resolve(context.Context, string, int) ([]netip.AddrPort, error) {
	return nil, nil
}

func (s *stateServiceDiscover) Advertise(context.Context, []string) error { return nil }

func (s *stateServiceDiscover) Sync(context.Context, []string) error {
	s.syncCalls++
	return nil
}

func (s *stateServiceDiscover) DoKeepAlive(context.Context) error { return nil }
