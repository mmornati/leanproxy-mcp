package bouncer

import (
	"crypto/subtle"
	"sync"
)

const (
	defaultBufferSize = 4096
	maxBufferSize     = 65536
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, defaultBufferSize)
		return &buf
	},
}

func GetBuffer() []byte {
	bufPtr := bufferPool.Get().(*[]byte)
	return (*bufPtr)[:defaultBufferSize]
}

func ReturnBuffer(buf []byte) {
	constantTimeZero(buf)
	bufferPool.Put(&buf)
}

func constantTimeZero(buf []byte) {
	if len(buf) > 0 {
		subtle.XORBytes(buf, buf, buf)
	}
}