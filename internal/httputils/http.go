package httputils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

func DoRequestWithRetry(
	ctx context.Context,
	method, url string,
	body []byte,
	headers map[string]string,
	singleTimeout time.Duration,
	initialBackoff, maxBackoff time.Duration,
	client *http.Client,
) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}

	backoff := initialBackoff
	var lastErr error
	attempt := 0

	ctx, cancel := LimitTimeout(ctx, 10*time.Second)
	defer cancel()

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

		reqCtx, cancel := LimitTimeout(ctx, singleTimeout)
		req, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, fmt.Errorf("create request failed: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		cancel()

		if err != nil {
			lastErr = err
			fmt.Printf("[attempt %d] request error: %v\n", attempt, err)
		} else {
			defer resp.Body.Close()

			if resp.StatusCode >= 500 {
				lastErr = fmt.Errorf("server error: %s", resp.Status)
				fmt.Printf("[attempt %d] server error: %s\n", attempt, resp.Status)
			} else if resp.StatusCode >= 400 {
				respBody, _ := io.ReadAll(resp.Body)
				return nil, fmt.Errorf("client error: %s, body: %s", resp.Status, string(respBody))
			} else {
				respBody, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("read body failed: %w", err)
				}
				return respBody, nil
			}
		}

		// backoff before next retry
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

// limit the ctx's timeout to `maxTimeout`
func LimitTimeout(parent context.Context, maxTimeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), maxTimeout)
	}

	deadline, ok := parent.Deadline()
	if !ok {
		return context.WithTimeout(parent, maxTimeout)
	}

	remaining := time.Until(deadline)
	if remaining <= maxTimeout {
		return context.WithDeadline(parent, deadline)
	}

	return context.WithTimeout(parent, maxTimeout)
}
