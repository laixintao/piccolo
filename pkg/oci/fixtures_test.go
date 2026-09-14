package oci

import (
	"context"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestContentFixturesUseIndependentWriterRefs(t *testing.T) {
	t.Parallel()

	// Containerd locks writer references across all stores in this process.
	// Hold the bare digest until both fixtures finish so a collision fails
	// deterministically, without relying on concurrent writes overlapping.
	dgst := digest.Digest("sha256:9430beb291fa7b96997711fc486bc46133c719631aefdbeebe58dd3489217bfe")
	store, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	writer, err := store.Writer(context.Background(), content.WithRef(dgst.String()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, writer.Close()) })

	t.Run("architecture fixture", func(t *testing.T) {
		t.Parallel()

		client, ctx := newTestdataContainerd(t)
		manifest, _, err := client.GetManifest(ctx, dgst)
		require.NoError(t, err)
		require.NotEmpty(t, manifest)
	})
	// Reuse the existing OCI fixture and its local/remote content assertions.
	t.Run("OCI client fixture", TestOCIClient)
}
