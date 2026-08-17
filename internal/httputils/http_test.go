package httputils

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDoRequestWithRetryKeepsContextAliveWhileReading(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       &contextBody{ctx: req.Context(), reader: strings.NewReader("hello")},
			Request:    req,
		}, nil
	})}

	resp, err := DoRequestWithRetry(
		context.Background(),
		http.MethodGet,
		"https://example.test",
		nil,
		nil,
		time.Second,
		time.Second,
		client,
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "hello", string(body))
}

func TestDoRequestWithRetryRetriesServerErrors(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusNoContent
		if attempts.Add(1) == 1 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}

	resp, err := DoRequestWithRetry(
		context.Background(),
		http.MethodGet,
		"https://example.test",
		nil,
		nil,
		time.Second,
		2*time.Second,
		client,
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, int32(2), attempts.Load())
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type contextBody struct {
	ctx    context.Context
	reader io.Reader
}

func (b *contextBody) Read(p []byte) (int, error) {
	select {
	case <-time.After(20 * time.Millisecond):
		return b.reader.Read(p)
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
}

func (b *contextBody) Close() error { return nil }
