package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"io"

	"golang.org/x/time/rate"
)

type RateLimitedReadSeeker struct {
	Rs      io.ReadSeeker
	Limiter *rate.Limiter
}

func (r *RateLimitedReadSeeker) Read(p []byte) (int, error) {
	if len(p) > 0 && r.Limiter.Limit() != rate.Inf {
		burst := r.Limiter.Burst()
		if burst <= 0 {
			return 0, fmt.Errorf("ratelimit: cannot read with nonpositive burst %d", burst)
		}
		// WaitN rejects requests larger than the burst instead of waiting.
		if len(p) > burst {
			p = p[:burst]
		}
	}

	n, err := r.Rs.Read(p)
	if n > 0 {
		if waitErr := r.Limiter.WaitN(context.Background(), n); waitErr != nil {
			return n, errors.Join(err, waitErr)
		}
	}
	return n, err
}

func (r *RateLimitedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return r.Rs.Seek(offset, whence)
}
