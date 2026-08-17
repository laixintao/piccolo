package httputils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
)

var ErrNotFound = errors.New("404 not found")

const (
	initialBackoff = 200 * time.Millisecond
	maxBackoff     = 10 * time.Second
)

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	c.cancel()
	return c.ReadCloser.Close()
}

const maxErrorBodyBytes = 64 << 10

func readErrorBody(body io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	return string(b)
}

func DoRequestWithRetry(
	ctx context.Context,
	method, url string,
	body []byte,
	headers map[string]string,
	singleTimeout time.Duration,
	totalTimeout time.Duration,
	client *http.Client,
	requestMetrics ...*prometheus.CounterVec,
) (*http.Response, error) {
	var metrics *prometheus.CounterVec
	if len(requestMetrics) > 0 {
		metrics = requestMetrics[0]
	}
	if client == nil {
		client = http.DefaultClient
	}

	backoff := initialBackoff
	var lastErr error
	attempt := 0

	log := logr.FromContextOrDiscard(ctx)

	ctx, cancelTotal := context.WithTimeout(ctx, totalTimeout)
	keepContextAlive := false
	defer func() {
		if !keepContextAlive {
			cancelTotal()
		}
	}()

	for {
		attempt++

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("context done after %d attempts: %w (last error: %v)", attempt-1, ctx.Err(), lastErr)
			}
			return nil, fmt.Errorf("context done after %d attempts: %w", attempt-1, ctx.Err())
		default:
		}

		remaining := time.Until(getDeadline(ctx))
		reqTimeout := minDuration(singleTimeout, remaining)
		if reqTimeout <= 0 {
			return nil, fmt.Errorf("no time left for retry after %d attempts", attempt-1)
		}

		reqCtx, cancelReq := context.WithTimeout(ctx, reqTimeout)
		req, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(body))
		if err != nil {
			cancelReq()
			return nil, fmt.Errorf("create request failed: %w", err)
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)

		if err != nil {
			cancelReq()
			lastErr = err
			log.Error(err, "http request get error", "attemp", attempt)
			if metrics != nil {
				metrics.WithLabelValues("fail").Inc()
			}
		} else {
			switch {
			case resp.StatusCode >= 500:
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
				resp.Body.Close()
				cancelReq()
				lastErr = fmt.Errorf("server error: %s", resp.Status)
				log.Info("http request status code 5xx", "attemp", attempt, "status_code", resp.Status)
				if metrics != nil {
					metrics.WithLabelValues("fail").Inc()
				}
			case resp.StatusCode == http.StatusNotFound:
				respBody := readErrorBody(resp.Body)
				resp.Body.Close()
				cancelReq()
				if metrics != nil {
					metrics.WithLabelValues("fail").Inc()
				}
				return nil, fmt.Errorf("url: %s, err: %w, respBody: %s", url, ErrNotFound, respBody)
			case resp.StatusCode >= 400:
				respBody := readErrorBody(resp.Body)
				resp.Body.Close()
				cancelReq()
				if metrics != nil {
					metrics.WithLabelValues("fail").Inc()
				}
				return nil, fmt.Errorf("client error: %s, body: %s", resp.Status, respBody)
			default:
				// The request context must remain alive while the caller reads the
				// response body. Cancel it when the body is closed instead.
				resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: func() {
					cancelReq()
					cancelTotal()
				}}
				keepContextAlive = true
				if metrics != nil {
					metrics.WithLabelValues("success").Inc()
				}
				return resp, nil
			}
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("context done during backoff after %d attempts: %w (last error: %v)", attempt, ctx.Err(), lastErr)
			}
			return nil, fmt.Errorf("context done during backoff after %d attempts: %w", attempt, ctx.Err())
		case <-time.After(backoff):
		}

		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func getDeadline(ctx context.Context) time.Time {
	d, ok := ctx.Deadline()
	if !ok {
		return time.Now().Add(24 * time.Hour)
	}
	return d
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
