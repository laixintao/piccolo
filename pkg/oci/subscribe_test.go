package oci

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/containerd/containerd"
	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/images"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

type subscriptionEvents struct {
	containerd.EventService
	envelopes <-chan *events.Envelope
}

func (s subscriptionEvents) Subscribe(context.Context, ...string) (<-chan *events.Envelope, <-chan error) {
	return s.envelopes, nil
}

type subscriptionImages struct {
	images.Store
	image images.Image
	err   error
}

func (s subscriptionImages) Get(context.Context, string) (images.Image, error) {
	return s.image, s.err
}

func TestSubscribeForwardingCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		event   any
		image   images.Image
		getErr  error
		wantErr string
	}{
		{name: "image event", event: &eventtypes.ImageDelete{Name: "registry.example/image:tag"}},
		{name: "decode error", wantErr: "any cannot be nil"},
		{name: "lookup error", event: &eventtypes.ImageCreate{Name: "missing"}, getErr: errors.New("image lookup failed"), wantErr: "image lookup failed"},
		{
			name:    "parse error",
			event:   &eventtypes.ImageUpdate{Name: "https://registry.example/image"},
			image:   images.Image{Name: "https://registry.example/image", Target: ocispec.Descriptor{Digest: digest.FromString("image")}},
			wantErr: "invalid reference",
		},
	} {
		for _, receive := range []bool{false, true} {
			mode := "blocked receiver"
			if receive {
				mode = "active receiver"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				envelopes := make(chan *events.Envelope)
				client, err := containerd.New("", containerd.WithServices(
					containerd.WithEventService(subscriptionEvents{envelopes: envelopes}),
					containerd.WithImageStore(subscriptionImages{image: tc.image, err: tc.getErr}),
				))
				require.NoError(t, err)
				t.Cleanup(func() { client.Close() })
				c := &Containerd{client: client}
				imgCh, errCh, _, err := c.Subscribe(ctx)
				require.NoError(t, err)
				// Also release the old implementation's blocked sender after a failing assertion.
				t.Cleanup(func() {
					cancel()
					timer := time.NewTimer(2 * time.Second)
					defer timer.Stop()
					for imgCh != nil || errCh != nil {
						select {
						case _, ok := <-imgCh:
							if !ok {
								imgCh = nil
							}
						case _, ok := <-errCh:
							if !ok {
								errCh = nil
							}
						case <-timer.C:
							t.Error("subscription failed to stop during cleanup")
							return
						}
					}
				})
				var event typeurl.Any
				if tc.event != nil {
					event, err = typeurl.MarshalAny(tc.event)
					require.NoError(t, err)
				}
				select {
				case envelopes <- &events.Envelope{Event: event}:
				case <-time.After(2 * time.Second):
					t.Fatal("subscriber did not receive upstream event")
				}

				if receive {
					if tc.wantErr == "" {
						select {
						case img := <-imgCh:
							require.Equal(t, DeleteEvent, img.Type)
							require.Equal(t, "registry.example/image:tag", img.ImageName)
						case <-time.After(2 * time.Second):
							t.Fatal("image event was not forwarded")
						}
					} else {
						select {
						case err := <-errCh:
							require.ErrorContains(t, err, tc.wantErr)
						case <-time.After(2 * time.Second):
							t.Fatal("error was not forwarded")
						}
					}
				}
				cancel()
				// Observe the other channel first: receiving the pending value itself
				// would unblock the buggy sender and hide the cancellation failure.
				if tc.wantErr == "" {
					select {
					case _, ok := <-errCh:
						require.False(t, ok)
					case <-time.After(2 * time.Second):
						t.Fatal("image forwarding ignored cancellation")
					}
				} else {
					select {
					case _, ok := <-imgCh:
						require.False(t, ok)
					case <-time.After(2 * time.Second):
						t.Fatal("error forwarding ignored cancellation")
					}
				}
			})
		}
	}
}
