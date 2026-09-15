package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd"
	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

type namespaceFixture struct {
	client      *Containerd
	images      images.Store
	content     content.Store
	events      *exchange.Exchange
	contentPath string
}

func newNamespaceFixture(t *testing.T, selected []string) *namespaceFixture {
	t.Helper()
	contentPath := t.TempDir()
	store, err := local.NewStore(contentPath)
	require.NoError(t, err)
	boltDB, err := bolt.Open(filepath.Join(t.TempDir(), "metadata.db"), 0o600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, boltDB.Close()) })
	db := metadata.NewDB(boltDB, store, nil)
	require.NoError(t, db.Init(context.Background()))
	f := &namespaceFixture{
		images:      metadata.NewImageStore(db),
		content:     db.ContentStore(),
		events:      exchange.NewExchange(),
		contentPath: contentPath,
	}
	f.client, err = NewContainerd(context.Background(), "", selected, mustURLs(t, "https://example.com"))
	require.NoError(t, err)
	f.client.client, err = containerd.New("", containerd.WithServices(
		containerd.WithImageStore(f.images),
		containerd.WithContentStore(f.content),
		containerd.WithEventService(f.events),
	))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.client.client.Close()) })
	return f
}

func (f *namespaceFixture) addImage(t *testing.T, ns, name string, dgst digest.Digest) {
	t.Helper()
	_, err := f.images.Create(namespaces.WithNamespace(context.Background(), ns), images.Image{
		Name:   name,
		Target: ocispec.Descriptor{Digest: dgst, MediaType: ocispec.MediaTypeImageManifest, Size: 1},
	})
	require.NoError(t, err)
}

func (f *namespaceFixture) addContent(t *testing.T, ns string, data []byte, mediaType string) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{Digest: digest.FromBytes(data), Size: int64(len(data)), MediaType: mediaType}
	ctx := namespaces.WithNamespace(context.Background(), ns)
	require.NoError(t, content.WriteBlob(ctx, f.content, desc.Digest.String(), bytes.NewReader(data), desc))
	return desc
}

func TestContainerdNamespaceImages(t *testing.T) {
	t.Parallel()
	f := newNamespaceFixture(t, []string{"k8s.io", "default"})
	ctx := context.Background()
	shared := digest.FromString("shared")
	k8sDigest := digest.FromString("k8s")
	defaultDigest := digest.FromString("default")
	for _, ns := range []string{"k8s.io", "default"} {
		f.addImage(t, ns, "example.com/shared:latest", shared)
	}
	f.addImage(t, "k8s.io", "example.com/conflict:v1", k8sDigest)
	f.addImage(t, "default", "example.com/conflict:v1", defaultDigest)
	f.addImage(t, "default", "example.com/nodemanager:latest", defaultDigest)
	f.addImage(t, "excluded", "example.com/excluded:v1", digest.FromString("excluded"))
	f.addImage(t, "default", "other.example.com/filtered:v1", digest.FromString("filtered"))

	listed, err := f.client.ListImages(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 4) // shared copy is deduplicated, distinct targets are retained
	for _, img := range listed {
		require.Equal(t, "example.com", img.Registry)
		require.NotEqual(t, "example.com/excluded:v1", img.Name)
	}
	resolved, err := f.client.Resolve(ctx, "example.com/nodemanager:latest")
	require.NoError(t, err)
	require.Equal(t, defaultDigest, resolved)
	resolved, err = f.client.Resolve(ctx, "example.com/conflict:v1")
	require.NoError(t, err)
	require.Equal(t, k8sDigest, resolved)
	defaultFirst, err := NewContainerd(ctx, "", []string{"default", "k8s.io"}, mustURLs(t, "https://example.com"))
	require.NoError(t, err)
	defaultFirst.client = f.client.client
	resolved, err = defaultFirst.Resolve(ctx, "example.com/conflict:v1")
	require.NoError(t, err)
	require.Equal(t, defaultDigest, resolved)
	_, err = f.client.Resolve(ctx, "example.com/excluded:v1")
	require.ErrorIs(t, err, ErrNotFound)

	// Removing the first namespace's copy must preserve the second copy in
	// the union used by full sync, and permit resolving it there.
	require.NoError(t, f.images.Delete(namespaces.WithNamespace(ctx, "k8s.io"), "example.com/shared:latest"))
	listed, err = f.client.ListImages(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 4)
	resolved, err = f.client.Resolve(ctx, "example.com/shared:latest")
	require.NoError(t, err)
	require.Equal(t, shared, resolved)
}

func TestContainerdNamespaceContent(t *testing.T) {
	t.Parallel()
	f := newNamespaceFixture(t, []string{"k8s.io", "default"})
	ctx := context.Background()
	configBytes := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	layerBytes := []byte("layer held only in default")
	config := f.addContent(t, "default", configBytes, ocispec.MediaTypeImageConfig)
	layer := f.addContent(t, "default", layerBytes, ocispec.MediaTypeImageLayer)
	manifestBytes, err := json.Marshal(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest,
		Config: config, Layers: []ocispec.Descriptor{layer},
	})
	require.NoError(t, err)
	manifest := f.addContent(t, "default", manifestBytes, ocispec.MediaTypeImageManifest)
	f.addImage(t, "default", "example.com/nodemanager:latest", manifest.Digest)
	excluded := f.addContent(t, "excluded", []byte("excluded blob"), ocispec.MediaTypeImageLayer)

	// Confirm this test really uses namespace-isolated metadata, even though
	// the physical content directory is shared.
	_, err = f.content.Info(namespaces.WithNamespace(ctx, "k8s.io"), layer.Digest)
	require.ErrorIs(t, err, errdefs.ErrNotFound)

	for _, mode := range []string{"content API", "direct file"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "direct file" {
				f.client.contentPath = f.contentPath
			}
			b, mt, err := f.client.GetManifest(ctx, manifest.Digest)
			require.NoError(t, err)
			require.Equal(t, manifestBytes, b)
			require.Equal(t, ocispec.MediaTypeImageManifest, mt)
			size, err := f.client.Size(ctx, layer.Digest)
			require.NoError(t, err)
			require.Equal(t, int64(len(layerBytes)), size)
			rc, err := f.client.GetBlob(ctx, layer.Digest)
			require.NoError(t, err)
			b, err = io.ReadAll(rc)
			require.NoError(t, err)
			require.NoError(t, rc.Close())
			require.Equal(t, layerBytes, b)

			listed, err := f.client.ListImages(ctx)
			require.NoError(t, err)
			require.Len(t, listed, 1)
			keys, err := WalkImage(ctx, f.client, listed[0])
			require.NoError(t, err)
			require.ElementsMatch(t, []string{manifest.Digest.String(), config.Digest.String(), layer.Digest.String()}, keys)
			arches, err := ImageArchitectures(ctx, f.client, manifest.Digest)
			require.NoError(t, err)
			require.Equal(t, []string{"amd64"}, arches)

			// A namespace on the incoming context cannot widen the configured
			// search scope, including for direct filesystem reads.
			excludedCtx := namespaces.WithNamespace(ctx, "excluded")
			_, err = f.client.Size(excludedCtx, excluded.Digest)
			require.ErrorIs(t, err, ErrNotFound)
			_, _, err = f.client.GetManifest(excludedCtx, excluded.Digest)
			require.ErrorIs(t, err, ErrNotFound)
			_, err = f.client.GetBlob(excludedCtx, excluded.Digest)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}

	// An index in the first namespace can be traversed using a platform's
	// manifest and blobs available only in the second namespace.
	manifest.Platform = &ocispec.Platform{OS: "linux", Architecture: "amd64"}
	indexBytes, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{manifest},
	})
	require.NoError(t, err)
	index := f.addContent(t, "k8s.io", indexBytes, ocispec.MediaTypeImageIndex)
	keys, err := WalkImage(ctx, f.client, Image{Digest: index.Digest})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{index.Digest.String(), manifest.Digest.String(), config.Digest.String(), layer.Digest.String()}, keys)
}

func TestContainerdNamespaceEvents(t *testing.T) {
	t.Parallel()
	for _, selected := range [][]string{{"k8s.io"}, {"k8s.io", "default"}} {
		t.Run(strings.Join(selected, ","), func(t *testing.T) {
			f := newNamespaceFixture(t, selected)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			imageCh, errCh, _, err := f.client.Subscribe(ctx)
			require.NoError(t, err)
			receive := func() ImageEvent {
				t.Helper()
				select {
				case event, ok := <-imageCh:
					require.True(t, ok)
					return event
				case err := <-errCh:
					t.Fatalf("unexpected subscriber error: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for image event")
				}
				return ImageEvent{}
			}
			publish := func(ns, topic string, data any) {
				t.Helper()
				require.NoError(t, f.events.Publish(namespaces.WithNamespace(ctx, ns), topic, data))
			}

			// These events must be excluded by the subscription, even though
			// they either have a selected namespace or a matching registry.
			publish("excluded", "/images/create", &eventtypes.ImageCreate{Name: "example.com/ignored:v1"})
			publish("k8s.io", "/images/create", &eventtypes.ImageCreate{Name: "other.example.com/ignored:v1"})
			publish("k8s.io", "/containers/create", &eventtypes.ContainerCreate{})
			if len(selected) == 1 {
				publish("default", "/images/create", &eventtypes.ImageCreate{Name: "example.com/ignored:v1"})
			}
			k8sDigest := digest.FromString("k8s image")
			f.addImage(t, "k8s.io", "example.com/k8s:v1", k8sDigest)
			publish("k8s.io", "/images/create", &eventtypes.ImageCreate{Name: "example.com/k8s:v1"})
			event := receive()
			require.Equal(t, "k8s.io", event.Namespace)
			require.Equal(t, k8sDigest, event.Image.Digest)

			if len(selected) > 1 {
				name := "example.com/nodemanager:latest"
				defaultDigest := digest.FromString("default image")
				f.addImage(t, "default", name, defaultDigest)
				publish("default", "/images/create", &eventtypes.ImageCreate{Name: name})
				event = receive()
				require.Equal(t, "default", event.Namespace)
				require.Equal(t, defaultDigest, event.Image.Digest)
				require.Equal(t, CreateEvent, event.Type)

				// The same name in the first namespace must not override the
				// digest of an event emitted by the second namespace.
				f.addImage(t, "k8s.io", name, k8sDigest)
				publish("default", "/images/update", &eventtypes.ImageUpdate{Name: name})
				event = receive()
				require.Equal(t, defaultDigest, event.Image.Digest)
				require.Equal(t, UpdateEvent, event.Type)

				require.NoError(t, f.images.Delete(namespaces.WithNamespace(ctx, "default"), name))
				publish("default", "/images/delete", &eventtypes.ImageDelete{Name: name})
				event = receive()
				require.Equal(t, DeleteEvent, event.Type)
				require.Equal(t, "default", event.Namespace)
				require.Equal(t, name, event.ImageName)

				// A genuinely missing image still produces an actionable error.
				publish("default", "/images/update", &eventtypes.ImageUpdate{Name: name})
				select {
				case err := <-errCh:
					require.ErrorIs(t, err, errdefs.ErrNotFound)
					require.ErrorContains(t, err, `namespace "default"`)
				case <-imageCh:
					t.Fatal("missing image was resolved from the wrong namespace")
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for missing image error")
				}
			}
			cancel()
			select {
			case _, ok := <-imageCh:
				require.False(t, ok)
			case <-time.After(5 * time.Second):
				t.Fatal("subscriber did not stop")
			}
		})
	}
}

func TestNamespaceLookupPreservesErrors(t *testing.T) {
	t.Parallel()
	want := errors.New("containerd unavailable")
	var calls []string
	_, err := lookupInNamespaces(context.Background(), []string{"k8s.io", "default"}, func(ctx context.Context) (int, error) {
		ns, _ := namespaces.Namespace(ctx)
		calls = append(calls, ns)
		return 0, want
	})
	require.ErrorIs(t, err, want)
	require.NotErrorIs(t, err, ErrNotFound)
	require.Equal(t, []string{"k8s.io"}, calls)
}

func TestContainerdConcurrentClientInitialization(t *testing.T) {
	t.Parallel()
	f := newNamespaceFixture(t, []string{"k8s.io", "default"})
	var calls int
	c := &Containerd{clientGetter: func() (*containerd.Client, error) {
		calls++
		return f.client.client, nil
	}}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := c.Client()
			if err != nil || client != f.client.client {
				t.Errorf("Client() = %v, %v", client, err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, calls)
}
