package sd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/stretchr/testify/require"
)

type keyWrite struct {
	operation string
	keys      []string
}

type keyWriteTransport struct {
	mu     sync.Mutex
	writes []keyWrite
	// Set before issuing requests. Tests can delay or reject a particular write.
	reply func(*http.Request, keyWrite) (*http.Response, error)
}

func (r *keyWriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var payload model.ImageAdvertiseRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		return nil, err
	}
	write := keyWrite{operation: path.Base(req.URL.Path), keys: payload.Keys}
	r.mu.Lock()
	r.writes = append(r.writes, write)
	r.mu.Unlock()
	if r.reply != nil {
		return r.reply(req, write)
	}
	return keyWriteResponse(http.StatusCreated), nil
}

func (r *keyWriteTransport) recorded() []keyWrite {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.writes)
}

func keyWriteResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"success":true}`)),
	}
}

func newKeyWriteClient(t *testing.T, opts ...Option) (*PiccoloServiceDiscover, *keyWriteTransport) {
	t.Helper()
	p, err := NewPiccoloServiceDiscover(url.URL{Scheme: "http", Host: "piccolo.example"}, logr.Discard(), "10.0.0.1:5127", "test", opts...)
	require.NoError(t, err)
	r := &keyWriteTransport{}
	p.httpClient = &http.Client{Transport: r}
	return p, r
}

func TestAdvertiseDeduplicatesKeysForFiveHours(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t)
	start := time.Now()
	now := start
	p.now = func() time.Time { return now }
	ctx := context.Background()

	require.NoError(t, p.Advertise(ctx, nil))
	require.Empty(t, transport.recorded(), "an empty batch must not issue an API request")
	require.NoError(t, p.Advertise(ctx, []string{"a", "a", "b"}))
	require.NoError(t, p.Advertise(ctx, []string{"a", "b"}))
	now = start.Add(4 * time.Hour)
	require.NoError(t, p.Advertise(ctx, []string{"b", "c"}))
	now = start.Add(5*time.Hour - time.Nanosecond)
	require.NoError(t, p.Advertise(ctx, []string{"a", "b", "c"}))
	require.Equal(t, []keyWrite{{"advertise", []string{"a", "b"}}, {"advertise", []string{"c"}}}, transport.recorded())

	// Hits do not extend the expiry. Only a and b are due at the five-hour boundary.
	now = start.Add(5 * time.Hour)
	require.NoError(t, p.Advertise(ctx, []string{"a", "b", "c"}))
	require.NoError(t, p.Advertise(ctx, []string{"a", "b", "c"}))
	require.Equal(t, []keyWrite{
		{"advertise", []string{"a", "b"}}, {"advertise", []string{"c"}},
		{"advertise", []string{"a", "b"}},
	}, transport.recorded())
}

func TestAdvertiseTTLStartsAfterSuccessfulResponse(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t)
	start := time.Now()
	now := start
	p.now = func() time.Time { return now }
	transport.reply = func(_ *http.Request, _ keyWrite) (*http.Response, error) {
		if now.Equal(start) {
			now = start.Add(time.Minute)
		}
		return keyWriteResponse(http.StatusCreated), nil
	}
	ctx := context.Background()
	require.NoError(t, p.Advertise(ctx, []string{"a"}))
	now = start.Add(5 * time.Hour)
	require.NoError(t, p.Advertise(ctx, []string{"a"}))
	require.Len(t, transport.recorded(), 1)
	now = now.Add(time.Minute)
	require.NoError(t, p.Advertise(ctx, []string{"a"}))
	require.Len(t, transport.recorded(), 2)
}

func TestFailedAdvertiseRemainsRetryable(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"HTTP error", "response body error"} {
		t.Run(failure, func(t *testing.T) {
			p, transport := newKeyWriteClient(t)
			failed := false
			transport.reply = func(_ *http.Request, write keyWrite) (*http.Response, error) {
				if slices.Contains(write.keys, "new") && !failed {
					failed = true
					if failure == "HTTP error" {
						return keyWriteResponse(http.StatusNotFound), nil
					}
					resp := keyWriteResponse(http.StatusCreated)
					resp.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
					return resp, nil
				}
				return keyWriteResponse(http.StatusCreated), nil
			}
			ctx := context.Background()
			require.NoError(t, p.Advertise(ctx, []string{"old"}))
			require.Error(t, p.Advertise(ctx, []string{"old", "new"}))
			require.NoError(t, p.Advertise(ctx, []string{"old", "new"}))
			require.NoError(t, p.Advertise(ctx, []string{"old", "new"}))
			require.Equal(t, []keyWrite{
				{"advertise", []string{"old"}}, {"advertise", []string{"new"}}, {"advertise", []string{"new"}},
			}, transport.recorded())
		})
	}
}

func TestSyncSendsCompleteSetAndReconcilesAdvertisementCache(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t)
	ctx := context.Background()
	require.NoError(t, p.Advertise(ctx, []string{"a", "b"}))
	require.NoError(t, p.Sync(ctx, []string{"b", "c"}))
	require.NoError(t, p.Advertise(ctx, []string{"a", "b", "c"}))
	require.NoError(t, p.Sync(ctx, []string{}))
	require.NoError(t, p.Advertise(ctx, []string{"b"}))
	require.Equal(t, []keyWrite{
		{"advertise", []string{"a", "b"}},
		{"sync", []string{"b", "c"}}, // b must not be filtered from a full snapshot.
		{"advertise", []string{"a"}}, // a was withdrawn and can be advertised again immediately.
		{"sync", []string{}},
		{"advertise", []string{"b"}},
	}, transport.recorded())
}

func TestFailedSyncInvalidatesAdvertisementCache(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t)
	transport.reply = func(_ *http.Request, write keyWrite) (*http.Response, error) {
		if write.operation == "sync" {
			// A response body failure can happen after the server changed its keys.
			resp := keyWriteResponse(http.StatusCreated)
			resp.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
			return resp, nil
		}
		return keyWriteResponse(http.StatusCreated), nil
	}
	ctx := context.Background()
	require.NoError(t, p.Advertise(ctx, []string{"a"}))
	require.Error(t, p.Sync(ctx, []string{"b"}))
	require.NoError(t, p.Advertise(ctx, []string{"a", "b"}))
	require.Equal(t, []keyWrite{
		{"advertise", []string{"a"}}, {"sync", []string{"b"}}, {"advertise", []string{"a", "b"}},
	}, transport.recorded())
}

func TestAdvertiseCacheIsLocalToEachPi(t *testing.T) {
	t.Parallel()
	first, firstTransport := newKeyWriteClient(t)
	restarted, restartedTransport := newKeyWriteClient(t)
	require.NoError(t, first.Advertise(context.Background(), []string{"a"}))
	require.NoError(t, restarted.Advertise(context.Background(), []string{"a"}))
	require.Len(t, firstTransport.recorded(), 1)
	require.Len(t, restartedTransport.recorded(), 1)
}

func TestAdvertiseCacheReclaimsExpiredKeys(t *testing.T) {
	t.Parallel()
	p, _ := newKeyWriteClient(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	require.NoError(t, p.Advertise(context.Background(), []string{"a", "b", "c"}))
	now = now.Add(5 * time.Hour)
	require.NoError(t, p.Advertise(context.Background(), []string{"d"}))
	require.Len(t, p.advertisements.keys, 1, "expired keys must not accumulate indefinitely")
	require.Contains(t, p.advertisements.keys, "d")
}

func TestAdvertiseCacheEvictsLeastRecentlyUsedKey(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t, WithAdvertiseCacheMaxKeys(2))
	ctx := context.Background()
	require.NoError(t, p.Advertise(ctx, []string{"a", "b"}))
	require.NoError(t, p.Advertise(ctx, []string{"a"})) // Keep the frequently used key.
	require.NoError(t, p.Advertise(ctx, []string{"c"}))
	require.Len(t, p.advertisements.keys, 2)
	require.Equal(t, 2, p.advertisements.order.Len())
	require.Contains(t, p.advertisements.keys, "a")
	require.Contains(t, p.advertisements.keys, "c")
	require.NotContains(t, p.advertisements.keys, "b")
	require.NoError(t, p.Advertise(ctx, []string{"a", "c"}))
	require.NoError(t, p.Advertise(ctx, []string{"b"}))
	require.Len(t, p.advertisements.keys, 2)
	require.Equal(t, []keyWrite{
		{"advertise", []string{"a", "b"}}, {"advertise", []string{"c"}}, {"advertise", []string{"b"}},
	}, transport.recorded())
}

func TestCacheLimitDoesNotTruncateAPIWrites(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t, WithAdvertiseCacheMaxKeys(2))
	ctx := context.Background()
	keys := []string{"a", "b", "c", "d", "e"}
	require.NoError(t, p.Advertise(ctx, keys))
	require.Len(t, p.advertisements.keys, 2)
	require.Equal(t, 2, p.advertisements.order.Len())
	require.NoError(t, p.Advertise(ctx, []string{"d", "e"}))
	require.NoError(t, p.Sync(ctx, keys))
	require.Len(t, p.advertisements.keys, 2)
	require.Equal(t, 2, p.advertisements.order.Len())
	require.Equal(t, []keyWrite{{"advertise", keys}, {"sync", keys}}, transport.recorded())
}

func TestAdvertiseCacheLimitMustBePositive(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{0, -1} {
		_, err := NewPiccoloServiceDiscover(url.URL{}, logr.Discard(), "10.0.0.1:5127", "test", WithAdvertiseCacheMaxKeys(limit))
		require.ErrorContains(t, err, "advertise cache max keys must be positive")
	}
	p, _ := newKeyWriteClient(t)
	require.Equal(t, DefaultAdvertiseCacheMaxKeys, p.advertisements.maxKeys)
}

func TestConcurrentAdvertisementsShareSuccessfulWrite(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const callers = 20
	errors := make(chan error, callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			<-start
			errors <- p.Advertise(ctx, []string{"a", "b"})
		}()
	}
	close(start)
	for range callers {
		select {
		case err := <-errors:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	require.Equal(t, []keyWrite{{"advertise", []string{"a", "b"}}}, transport.recorded())
}

func TestAdvertiseWaitsForSyncAndSupportsCancellation(t *testing.T) {
	t.Parallel()
	p, transport := newKeyWriteClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	syncStarted := make(chan struct{})
	finishSync := make(chan struct{})
	transport.reply = func(req *http.Request, write keyWrite) (*http.Response, error) {
		if write.operation == "sync" {
			close(syncStarted)
			select {
			case <-finishSync:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}
		return keyWriteResponse(http.StatusCreated), nil
	}
	require.NoError(t, p.Advertise(ctx, []string{"a"}))
	syncDone := make(chan error, 1)
	go func() { syncDone <- p.Sync(ctx, []string{}) }()
	select {
	case <-syncStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A canceled caller must not hang behind the network request or issue a POST.
	canceled, cancelWaiter := context.WithCancel(ctx)
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- p.Advertise(canceled, []string{"new"}) }()
	cancelWaiter()
	select {
	case err := <-waiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	advertiseDone := make(chan error, 1)
	go func() { advertiseDone <- p.Advertise(ctx, []string{"a"}) }()
	select {
	case err := <-advertiseDone:
		t.Fatalf("advertisement returned before sync completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(finishSync)
	require.NoError(t, <-syncDone)
	require.NoError(t, <-advertiseDone)
	require.Equal(t, []keyWrite{
		{"advertise", []string{"a"}}, {"sync", []string{}}, {"advertise", []string{"a"}},
	}, transport.recorded())
}
