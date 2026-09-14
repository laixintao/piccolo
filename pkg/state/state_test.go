package state

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
	"github.com/stretchr/testify/require"
)

type trackerClient struct {
	oci.Client
	subscribe  func(context.Context) (<-chan oci.ImageEvent, <-chan error, <-chan error, error)
	listImages func(context.Context) ([]oci.Image, error)
}

func (c trackerClient) Subscribe(ctx context.Context) (<-chan oci.ImageEvent, <-chan error, <-chan error, error) {
	return c.subscribe(ctx)
}

func (c trackerClient) ListImages(ctx context.Context) ([]oci.Image, error) {
	return c.listImages(ctx)
}

type trackerDiscovery struct{ sd.ServiceDiscover }

func (trackerDiscovery) DoKeepAlive(ctx context.Context) error { return ctx.Err() }

func waitForStop(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestFullUpdateProcessorStopsOnCancellation(t *testing.T) {
	for _, pendingEvent := range []bool{false, true} {
		name := "idle"
		if pendingEvent {
			name = "pending event"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := make(chan string)
			done := make(chan struct{})
			go func() {
				fullUpdateProcessor(events, ctx, nil, nil, false)
				close(done)
			}()
			if pendingEvent {
				select {
				case events <- "deleteEvent":
				case <-time.After(2 * time.Second):
					t.Fatal("processor did not receive event")
				}
			}
			cancel()
			waitForStop(t, done)
		})
	}
}

func TestFullUpdateProcessorStopsWhenEventsClose(t *testing.T) {
	events := make(chan string)
	close(events)
	done := make(chan struct{})
	client := trackerClient{listImages: func(context.Context) ([]oci.Image, error) {
		return nil, errors.New("unexpected refresh after events closed")
	}}
	go func() {
		fullUpdateProcessor(events, context.Background(), client, nil, false)
		close(done)
	}()
	waitForStop(t, done)
}

func TestStartIntervalSyncCancelsBlockedSend(t *testing.T) {
	ready := make(chan struct{})
	var once sync.Once
	log := funcr.New(func(_, message string) {
		if strings.Contains(message, "Interval update first trigger full sync") {
			once.Do(func() { close(ready) })
		}
	}, funcr.Options{})
	ctx, cancel := context.WithCancel(logr.NewContext(context.Background(), log))
	defer cancel()
	done := make(chan struct{})
	go func() {
		// A zero initial delay and no receiver put the worker at its first send.
		startIntervalSync(ctx, 0, nil)
		close(done)
	}()
	waitForStop(t, ready)
	cancel()
	waitForStop(t, done)
}

func TestTrackWaitsForFullUpdateCleanup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backlog int
	}{
		{name: "no backlog"},
		{name: "full update queue is full", backlog: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := make(chan oci.ImageEvent)
			subscribed := make(chan context.Context, 1)
			refreshStarted := make(chan struct{})
			cleanupStarted := make(chan struct{})
			releaseCleanup := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseCleanup) }) }
			defer release()
			client := trackerClient{
				subscribe: func(ctx context.Context) (<-chan oci.ImageEvent, <-chan error, <-chan error, error) {
					subscribed <- ctx
					return events, nil, nil, nil
				},
				listImages: func(ctx context.Context) ([]oci.Image, error) {
					close(refreshStarted)
					<-ctx.Done()
					close(cleanupStarted)
					<-releaseCleanup
					return nil, ctx.Err()
				},
			}
			done := make(chan error, 1)
			go func() { done <- Track(ctx, client, trackerDiscovery{}, 60, false) }()

			var subscriptionCtx context.Context
			select {
			case subscriptionCtx = <-subscribed:
			case <-time.After(2 * time.Second):
				t.Fatal("tracker did not subscribe")
			}
			for range MAX_DELETION_EVENTS {
				select {
				case events <- oci.ImageEvent{Type: oci.DeleteEvent}:
				case <-time.After(2 * time.Second):
					t.Fatal("tracker did not receive deletion event")
				}
			}
			waitForStop(t, refreshStarted)
			// The queue holds ten events. Once Track has received an eleventh event,
			// forwarding it cannot finish while the refresh is still running.
			for range tc.backlog {
				select {
				case events <- oci.ImageEvent{Type: oci.DeleteEvent}:
				case <-time.After(2 * time.Second):
					t.Fatal("tracker did not receive deletion backlog")
				}
			}
			cancel()
			waitForStop(t, subscriptionCtx.Done())
			waitForStop(t, cleanupStarted)
			select {
			case err := <-done:
				t.Fatalf("Track returned before full update cleanup completed: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			release()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Track did not return after cleanup")
			}
		})
	}
}

func TestTrackReleasesFailedSubscription(t *testing.T) {
	for _, subscribeError := range []bool{false, true} {
		name := "closed event channel"
		if subscribeError {
			name = "subscription error"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			subscribed := make(chan context.Context, 1)
			client := trackerClient{subscribe: func(ctx context.Context) (<-chan oci.ImageEvent, <-chan error, <-chan error, error) {
				subscribed <- ctx
				if subscribeError {
					return nil, nil, nil, errors.New("subscription failed")
				}
				events := make(chan oci.ImageEvent)
				close(events)
				return events, nil, nil, nil
			}}
			done := make(chan error, 1)
			go func() { done <- Track(ctx, client, trackerDiscovery{}, 60, false) }()
			select {
			case subscriptionCtx := <-subscribed:
				waitForStop(t, subscriptionCtx.Done())
			case <-time.After(2 * time.Second):
				t.Fatal("tracker did not subscribe")
			}
			require.NoError(t, ctx.Err(), "restarting must release the old subscription before the tracker is canceled")
			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Track did not stop during subscription backoff")
			}
		})
	}
}
