package transport

import "sync"

const transferBufferSize = 256 * 1024

var transferBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, transferBufferSize)
		return &b
	},
}

func acquireTransferBuf() *[]byte {
	return transferBufPool.Get().(*[]byte)
}

func releaseTransferBuf(b *[]byte) {
	transferBufPool.Put(b)
}
