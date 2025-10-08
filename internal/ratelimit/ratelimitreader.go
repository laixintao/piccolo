package ratelimit

import (
	"context"
	"io"

	"golang.org/x/time/rate"
)

type RateLimitedReadSeeker struct {
	Rs      io.ReadSeeker
	Limiter *rate.Limiter
}

func (r *RateLimitedReadSeeker) Read(p []byte) (int, error) {
	n, err := r.Rs.Read(p)
	if n > 0 {
		// will block here
		_ = r.Limiter.WaitN(context.Background(), n)
	}
	return n, err
}


func (r *RateLimitedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return r.Rs.Seek(offset, whence)
}
