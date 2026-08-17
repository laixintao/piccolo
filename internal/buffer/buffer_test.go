package buffer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBufferPool(t *testing.T) {
	t.Parallel()

	bufferPool := NewBufferPool()
	b := bufferPool.Get()
	require.Len(t, b, 32*1024)
	bufferPool.Put(b)

	// A caller returning an unexpected slice must not poison the pool.
	bufferPool.Put(make([]byte, 1))
	require.Len(t, bufferPool.Get(), size)
}
