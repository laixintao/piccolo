package registry

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func TestTryDoesNotCommitResponseWhenPeerFails(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(nil, logr.Discard(), WithTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("peer unavailable")
	})))
	w := &trackingResponseWriter{header: make(http.Header)}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/v2/library/alpine/manifests/latest", nil)

	err := registry.try(netip.MustParseAddrPort("10.0.0.2:5001"), w, req)
	require.EqualError(t, err, "failed to mirror request")
	require.False(t, w.committed, "a failed peer must leave the response available for the next peer")
	require.Empty(t, w.body)
}

func TestTryAcceptsPartialContent(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(nil, logr.Discard(), WithTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			Status:        "206 Partial Content",
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader("partial")),
			ContentLength: 7,
			Request:       req,
		}, nil
	})))
	w := &trackingResponseWriter{header: make(http.Header)}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/v2/library/alpine/blobs/sha256:abc", nil)

	err := registry.try(netip.MustParseAddrPort("10.0.0.2:5001"), w, req)
	require.NoError(t, err)
	require.True(t, w.committed)
	require.Equal(t, http.StatusPartialContent, w.status)
	require.Equal(t, "partial", string(w.body))
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackingResponseWriter struct {
	header    http.Header
	body      []byte
	status    int
	committed bool
	err       error
}

func (w *trackingResponseWriter) Header() http.Header { return w.header }

func (w *trackingResponseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	w.committed = true
	w.status = status
}

func (w *trackingResponseWriter) Write(p []byte) (int, error) {
	w.committed = true
	w.body = append(w.body, p...)
	return len(p), nil
}

func (w *trackingResponseWriter) WriteError(status int, err error) {
	w.err = err
	w.WriteHeader(status)
}

func (w *trackingResponseWriter) Error() error { return w.err }
func (w *trackingResponseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
func (w *trackingResponseWriter) Size() int64 { return int64(len(w.body)) }
