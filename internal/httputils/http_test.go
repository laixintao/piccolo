package httputils

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Model a response body that still needs the request context while being read.
type contextBody struct {
	io.Reader
	ctx    context.Context
	closed bool
}

func (b *contextBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.Reader.Read(p)
}

func (b *contextBody) Close() error {
	b.closed = true
	return nil
}

func TestDoRequestWithRetryResponseBodyLifetime(t *testing.T) {
	t.Parallel()
	var body *contextBody
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body = &contextBody{Reader: strings.NewReader("response payload"), ctx: req.Context()}
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})}

	resp, err := DoRequestWithRetry(context.Background(), http.MethodGet, "http://example.test", nil, nil, time.Second, 2*time.Second, client)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.NoError(t, body.ctx.Err(), "returning the response must not cancel its body")
	payload, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "response payload", string(payload))
	require.NoError(t, resp.Body.Close())
	require.True(t, body.closed)
	require.ErrorIs(t, body.ctx.Err(), context.Canceled)
}

func TestDoRequestWithRetryStreamingResponse(t *testing.T) {
	t.Parallel()
	sendBody := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-sendBody:
			_, _ = io.WriteString(w, "streamed payload")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)

	resp, err := DoRequestWithRetry(context.Background(), http.MethodGet, server.URL, nil, nil, 5*time.Second, 10*time.Second, server.Client())
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	// Send the body only after the retry helper has returned its headers.
	close(sendBody)
	payload, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "streamed payload", string(payload))
}

func TestDoRequestWithRetryReadsClientErrorBody(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var body *contextBody
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body = &contextBody{Reader: strings.NewReader("error details"), ctx: req.Context()}
				return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: body, Header: make(http.Header)}, nil
			})}
			resp, err := DoRequestWithRetry(context.Background(), http.MethodGet, "http://example.test", nil, nil, time.Second, 2*time.Second, client)
			require.Nil(t, resp)
			require.ErrorContains(t, err, "error details")
			if status == http.StatusNotFound {
				require.ErrorIs(t, err, ErrNotFound)
			}
			require.True(t, body.closed)
			require.ErrorIs(t, body.ctx.Err(), context.Canceled)
		})
	}
}

func TestDoRequestWithRetryCleansUpFailedAttempts(t *testing.T) {
	t.Parallel()
	for _, transportError := range []bool{false, true} {
		name := "server error"
		if transportError {
			name = "transport error"
		}
		t.Run(name, func(t *testing.T) {
			attempts := 0
			var firstCtx context.Context
			var firstBody *contextBody
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					firstCtx = req.Context()
					if transportError {
						return nil, errors.New("connection reset")
					}
					firstBody = &contextBody{Reader: strings.NewReader("unavailable"), ctx: firstCtx}
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: firstBody, Header: make(http.Header)}, nil
				}
				require.ErrorIs(t, firstCtx.Err(), context.Canceled)
				if firstBody != nil {
					require.True(t, firstBody.closed)
				}
				body := &contextBody{Reader: strings.NewReader("retried payload"), ctx: req.Context()}
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
			})}
			resp, err := DoRequestWithRetry(context.Background(), http.MethodGet, "http://example.test", nil, nil, time.Second, 2*time.Second, client)
			require.NoError(t, err)
			defer resp.Body.Close()
			payload, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "retried payload", string(payload))
			require.Equal(t, 2, attempts)
		})
	}
}

func TestDoRequestWithRetryBodyHonorsParentCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := &contextBody{Reader: strings.NewReader("payload"), ctx: req.Context()}
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})}
	resp, err := DoRequestWithRetry(ctx, http.MethodGet, "http://example.test", nil, nil, time.Second, 2*time.Second, client)
	require.NoError(t, err)
	defer resp.Body.Close()
	cancel()
	_, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDoRequestWithRetryBodyHonorsTimeouts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		singleTimeout time.Duration
		totalTimeout  time.Duration
	}{
		{name: "request timeout", singleTimeout: 100 * time.Millisecond, totalTimeout: 10 * time.Second},
		{name: "total timeout", singleTimeout: 10 * time.Second, totalTimeout: 100 * time.Millisecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var reqCtx context.Context
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				reqCtx = req.Context()
				body := &contextBody{Reader: strings.NewReader("payload"), ctx: reqCtx}
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
			})}
			resp, err := DoRequestWithRetry(context.Background(), http.MethodGet, "http://example.test", nil, nil, tt.singleTimeout, tt.totalTimeout, client)
			require.NoError(t, err)
			defer resp.Body.Close()
			select {
			case <-reqCtx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("response body outlived its timeout")
			}
			_, err = io.ReadAll(resp.Body)
			require.ErrorIs(t, err, context.DeadlineExceeded)
		})
	}
}
