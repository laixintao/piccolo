package ratelimit

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestRateLimitedReadSeekerRespectsBurst(t *testing.T) {
	t.Parallel()

	reader := &RateLimitedReadSeeker{
		Ctx:     context.Background(),
		Rs:      bytes.NewReader([]byte("abcdefgh")),
		Limiter: rate.NewLimiter(rate.Inf, 3),
	}
	buf := make([]byte, 8)
	n, err := reader.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, "abc", string(buf[:n]))
}

func TestRateLimitedReadSeekerWithoutLimiter(t *testing.T) {
	t.Parallel()

	reader := &RateLimitedReadSeeker{Rs: bytes.NewReader([]byte("abc"))}
	buf := make([]byte, 3)
	n, err := reader.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 3, n)
}
