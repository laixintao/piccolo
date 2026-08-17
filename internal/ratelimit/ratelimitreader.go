package ratelimit

import (
	"context"
	"io"

	"golang.org/x/time/rate"
)

type RateLimitedReadSeeker struct {
	Ctx     context.Context
	Rs      io.ReadSeeker
	Limiter *rate.Limiter
}

func (r *RateLimitedReadSeeker) Read(p []byte) (int, error) {
	if r.Limiter == nil {
		return r.Rs.Read(p)
	}
	if burst := r.Limiter.Burst(); burst > 0 && len(p) > burst {
		p = p[:burst]
	}
	n, err := r.Rs.Read(p)
	if n > 0 {
		ctx := r.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if waitErr := r.Limiter.WaitN(ctx, n); waitErr != nil && err == nil {
			err = waitErr
		}
	}
	return n, err
}

func (r *RateLimitedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return r.Rs.Seek(offset, whence)
}
