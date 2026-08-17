package buffer

import "sync"

const size = 32 * 1024

type BufferPool struct {
	pool sync.Pool
}

func NewBufferPool() *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				return make([]byte, size)
			},
		},
	}
}

func (p *BufferPool) Get() []byte {
	//nolint: errcheck // Ignore
	return p.pool.Get().([]byte)
}

func (p *BufferPool) Put(b []byte) {
	if cap(b) < size {
		return
	}
	p.pool.Put(b[:size])
}
