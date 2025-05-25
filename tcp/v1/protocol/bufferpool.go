package protocol

import "sync"

const (
	DefaultReadBufferSize  = 32 * 1024 // 32KB
	DefaultWriteBufferSize = 4 * 1024  // 4KB
)

var readBufPool = sync.Pool{
	New: func() any {
		return make([]byte, DefaultReadBufferSize)
	},
}

var writeBufPool = sync.Pool{
	New: func() any {
		return make([]byte, DefaultWriteBufferSize)
	},
}

// ------------------- Read Buffer --------------------
func GetReadBuffer(size int) []byte {
	buf := readBufPool.Get().([]byte)
	if cap(buf) < size {
		return make([]byte, size)
	}
	return buf[:size]
}

func PutReadBuffer(buf []byte) {
	readBufPool.Put(buf[:cap(buf)])
}

// ------------------- Write Buffer --------------------
func GetWriteBuffer(size int) []byte {
	buf := writeBufPool.Get().([]byte)
	if cap(buf) < size {
		return make([]byte, size)
	}
	return buf[:size]
}

func PutWriteBuffer(buf []byte) {
	writeBufPool.Put(buf[:cap(buf)])
}
