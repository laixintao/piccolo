package oci

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/namespaces"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func newTestdataContainerd(t *testing.T) (*Containerd, context.Context) {
	t.Helper()

	contentPath := t.TempDir()
	contentStore, err := local.NewStore(contentPath)
	require.NoError(t, err)
	ctx := namespaces.WithNamespace(context.TODO(), "k8s.io")

	fileItems, err := os.ReadDir("./testdata/blobs/sha256")
	require.NoError(t, err)
	for _, item := range fileItems {
		if item.IsDir() {
			continue
		}
		dgst, err := digest.Parse(fmt.Sprintf("sha256:%s", item.Name()))
		require.NoError(t, err)
		b, err := os.ReadFile(path.Join("./testdata/blobs/sha256", item.Name()))
		require.NoError(t, err)
		writer, err := contentStore.Writer(ctx, content.WithRef(dgst.String()))
		require.NoError(t, err)
		_, err = writer.Write(b)
		require.NoError(t, err)
		err = writer.Commit(ctx, int64(len(b)), dgst)
		require.NoError(t, err)
		writer.Close()
	}

	containerdClient, err := containerd.New("", containerd.WithServices(containerd.WithContentStore(contentStore)))
	require.NoError(t, err)
	return &Containerd{client: containerdClient}, ctx
}

func TestArchTagKey(t *testing.T) {
	t.Parallel()

	require.Equal(t, "example.com/org/image:v1|amd64", ArchTagKey("example.com/org/image:v1", "amd64"))
}

func TestImageArchitectures(t *testing.T) {
	t.Parallel()

	ociClient, ctx := newTestdataContainerd(t)

	tests := []struct {
		name           string
		dgst           digest.Digest
		expectedArches []string
	}{
		{
			// Index with amd64, arm/v7 and arm64 children with local content.
			// The "fake" architecture child and the attestation manifests
			// (platform unknown/unknown) have no local content and must be
			// excluded.
			name:           "multi arch index",
			dgst:           digest.Digest("sha256:9506c8e7a2d0a098d43cadfd7ecdc3c91697e8188d3a1245943b669f717747b4"),
			expectedArches: []string{"amd64", "arm", "arm64"},
		},
		{
			name:           "single arch index",
			dgst:           digest.Digest("sha256:9430beb291fa7b96997711fc486bc46133c719631aefdbeebe58dd3489217bfe"),
			expectedArches: []string{"amd64"},
		},
		{
			name:           "index without local content",
			dgst:           digest.Digest("sha256:addc990c58744bdf96364fe89bd4aab38b1e824d51c688edb36c75247cd45fa9"),
			expectedArches: []string{},
		},
		{
			name:           "platform specific manifest",
			dgst:           digest.Digest("sha256:aec8273a5e5aca369fcaa8cecef7bf6c7959d482f5c8cfa2236a6a16e46bbdcf"),
			expectedArches: []string{"amd64"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arches, err := ImageArchitectures(ctx, ociClient, tt.dgst)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expectedArches, arches)
		})
	}

	_, err := ImageArchitectures(ctx, ociClient, digest.FromString("does not exist"))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestManifestArchitecture(t *testing.T) {
	t.Parallel()

	ociClient, ctx := newTestdataContainerd(t)

	manifestBytes, mediaType, err := ociClient.GetManifest(ctx, digest.Digest("sha256:aec8273a5e5aca369fcaa8cecef7bf6c7959d482f5c8cfa2236a6a16e46bbdcf"))
	require.NoError(t, err)
	require.True(t, mediaType == "application/vnd.oci.image.manifest.v1+json")

	arch, err := ManifestArchitecture(ctx, ociClient, manifestBytes)
	require.NoError(t, err)
	require.Equal(t, "amd64", arch)
}
