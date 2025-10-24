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
)

var ErrNotFound = errors.New("404 not found")

const INITIAL_BACKOFF = 200 * time.Millisecond
const MAX_BACKOFF = 10 * time.Second

func DoRequestWithRetry(
	ctx context.Context,
	method, url string,
	body []byte,
	headers map[string]string,
	singleTimeout time.Duration,
	totalTimeout time.Duration,
	client *http.Client,
) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}

	backoff := INITIAL_BACKOFF
	var lastErr error
	attempt := 0

	log := logr.FromContextOrDiscard(ctx)

	ctx, cancelTotal := context.WithTimeout(ctx, totalTimeout)
	defer cancelTotal()

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
		cancelReq()

		if err != nil {
			lastErr = err
			log.Error(err, "http request get error", "attemp", attempt)
		} else {
			switch {
			case resp.StatusCode >= 500:
				resp.Body.Close()
				lastErr = fmt.Errorf("server error: %s", resp.Status)
				log.Info("http request status code 5xx", "attemp", attempt, "status_code", resp.Status)
			case resp.StatusCode == http.StatusNotFound:
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return nil, fmt.Errorf("url: %s, err: %w, respBody: %s", url, ErrNotFound, respBody)
			case resp.StatusCode >= 400:
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return nil, fmt.Errorf("client error: %s, body: %s", resp.Status, string(respBody))
			default:
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

		if backoff < INITIAL_BACKOFF {
			backoff *= 2
			if backoff > INITIAL_BACKOFF {
				backoff = INITIAL_BACKOFF
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
