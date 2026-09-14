package ratelimit

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestReadHonorsBurst(t *testing.T) {
	t.Parallel()

	for _, limit := range []rate.Limit{0, 1e-9} {
		limiter := rate.NewLimiter(limit, 4)
		source := bytes.NewReader([]byte("abcdefgh"))
		reader := &RateLimitedReadSeeker{Rs: source, Limiter: limiter}
		buf := make([]byte, 8)

		n, err := reader.Read(buf)

		require.NoError(t, err)
		require.Equal(t, 4, n, "limit %v", limit)
		require.Equal(t, "abcd", string(buf[:n]))
		require.Less(t, limiter.Tokens(), 0.01, "read bytes must consume tokens")
		require.Equal(t, 4, source.Len(), "only charged bytes should be read")
	}
}

func TestReadAllAndSeek(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("abcdefgh"), 10)
	reader := &RateLimitedReadSeeker{
		Rs:      bytes.NewReader(data),
		Limiter: rate.NewLimiter(1e9, 3),
	}

	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, data, got)

	n, err := reader.Read(make([]byte, 8))
	require.Zero(t, n)
	require.ErrorIs(t, err, io.EOF)

	pos, err := reader.Seek(5, io.SeekStart)
	require.NoError(t, err)
	require.EqualValues(t, 5, pos)
	got, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, data[5:], got)

	_, err = reader.Seek(-1, io.SeekStart)
	require.Error(t, err)
}

func TestReadWithInfiniteLimitIgnoresBurst(t *testing.T) {
	t.Parallel()

	for _, burst := range []int{-1, 0, 2} {
		reader := &RateLimitedReadSeeker{
			Rs:      bytes.NewReader([]byte("abcdefgh")),
			Limiter: rate.NewLimiter(rate.Inf, burst),
		}
		buf := make([]byte, 8)

		n, err := reader.Read(buf)

		require.NoError(t, err)
		require.Equal(t, len(buf), n, "burst %d", burst)
		require.Equal(t, "abcdefgh", string(buf[:n]))
	}
}

func TestReadRejectsNonpositiveFiniteBurst(t *testing.T) {
	t.Parallel()

	for _, limit := range []rate.Limit{0, 1} {
		for _, burst := range []int{-1, 0} {
			source := bytes.NewReader([]byte("abcdefgh"))
			reader := &RateLimitedReadSeeker{Rs: source, Limiter: rate.NewLimiter(limit, burst)}

			n, err := reader.Read(make([]byte, 8))

			require.Error(t, err, "limit %v, burst %d", limit, burst)
			require.Zero(t, n)
			require.Equal(t, 8, source.Len(), "rejected reads must not advance the source")
		}
	}
}

func TestReadEmptyBuffer(t *testing.T) {
	t.Parallel()

	for _, burst := range []int{-1, 0, 2} {
		source := bytes.NewReader([]byte("abcdefgh"))
		limiter := rate.NewLimiter(0, burst)
		reader := &RateLimitedReadSeeker{Rs: source, Limiter: limiter}
		tokens := limiter.Tokens()

		n, err := reader.Read(nil)

		require.NoError(t, err)
		require.Zero(t, n)
		require.Equal(t, 8, source.Len())
		require.Equal(t, tokens, limiter.Tokens())
	}
}

func TestReadPreservesSourceErrors(t *testing.T) {
	t.Parallel()

	for _, sourceErr := range []error{io.EOF, errors.New("read failed")} {
		limiter := rate.NewLimiter(0, 4)
		reader := &RateLimitedReadSeeker{
			Rs:      &readResultSeeker{ReadSeeker: bytes.NewReader([]byte("abcd")), err: sourceErr},
			Limiter: limiter,
		}

		n, err := reader.Read(make([]byte, 8))

		require.Equal(t, 4, n)
		require.ErrorIs(t, err, sourceErr)
		require.Zero(t, limiter.Tokens())
	}
}

func TestReadReportsWaitError(t *testing.T) {
	t.Parallel()

	for _, sourceErr := range []error{nil, io.EOF, errors.New("read failed")} {
		limiter := rate.NewLimiter(1, 4)
		reader := &RateLimitedReadSeeker{
			Rs: &readResultSeeker{
				ReadSeeker: bytes.NewReader([]byte("abcd")),
				err:        sourceErr,
				afterRead:  func() { limiter.SetBurst(0) },
			},
			Limiter: limiter,
		}

		n, err := reader.Read(make([]byte, 4))

		require.Equal(t, 4, n)
		require.ErrorContains(t, err, "exceeds limiter's burst")
		if sourceErr != nil {
			require.ErrorIs(t, err, sourceErr)
		}
	}
}

type readResultSeeker struct {
	io.ReadSeeker
	err       error
	afterRead func()
}

func (r *readResultSeeker) Read(p []byte) (int, error) {
	n, _ := r.ReadSeeker.Read(p)
	if r.afterRead != nil {
		r.afterRead()
	}
	return n, r.err
}
