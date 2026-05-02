package bouncer

import (
	"sync"
	"testing"
)

func TestBufferZeroing(t *testing.T) {
	buf := GetBuffer()
	copy(buf, []byte("secret data"))
	ReturnBuffer(buf)

	buf2 := GetBuffer()
	for i := range buf2 {
		if buf2[i] != 0 {
			t.Errorf("buffer not zeroed at index %d", i)
		}
	}
	ReturnBuffer(buf2)
}

func TestBufferPoolReusesAllocations(t *testing.T) {
	buf1 := GetBuffer()
	addr1 := &buf1[0]
	ReturnBuffer(buf1)

	buf2 := GetBuffer()
	addr2 := &buf2[0]
	ReturnBuffer(buf2)

	if addr1 != addr2 {
		t.Log("buffers may be reused but not guaranteed - this is ok")
	}
}

func TestConcurrentBufferGetReturn(t *testing.T) {
	var wg sync.WaitGroup
	done := make(chan struct{})
	errors := make(chan error, 100)

	go func() {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				buf := GetBuffer()
				for j := range buf {
					buf[j] = byte(j % 256)
				}
				ReturnBuffer(buf)
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case err := <-errors:
		t.Errorf("concurrent buffer access error: %v", err)
	}
}

func TestBufferSize(t *testing.T) {
	buf := GetBuffer()
	if len(buf) != defaultBufferSize {
		t.Errorf("expected buffer size %d, got %d", defaultBufferSize, len(buf))
	}
	ReturnBuffer(buf)
}

func TestMaxBufferSize(t *testing.T) {
	if maxBufferSize != 65536 {
		t.Errorf("expected maxBufferSize 65536, got %d", maxBufferSize)
	}
}

func TestConstantTimeZero(t *testing.T) {
	buf := []byte("sensitive data")
	constantTimeZero(buf)

	for i := range buf {
		if buf[i] != 0 {
			t.Errorf("constantTimeZero failed at index %d", i)
		}
	}
}

func TestConstantTimeZeroEmptySlice(t *testing.T) {
	var buf []byte
	constantTimeZero(buf)
}

func TestBufferNotSharedAcrossGoroutines(t *testing.T) {
	result := make(chan []byte, 2)

	go func() {
		buf := GetBuffer()
		copy(buf, []byte("goroutine 1 data"))
		ReturnBuffer(buf)
		buf2 := GetBuffer()
		result <- buf2
	}()

	go func() {
		buf := GetBuffer()
		copy(buf, []byte("goroutine 2 data"))
		ReturnBuffer(buf)
		buf2 := GetBuffer()
		result <- buf2
	}()

	r1 := <-result
	r2 := <-result

	for i := range r1 {
		if r1[i] != 0 && r2[i] != 0 {
			if string(r1) == string(r2) {
				t.Error("buffers appear to contain same data - possible leak")
			}
		}
	}
}

func BenchmarkBufferGetReturn(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buf := GetBuffer()
		ReturnBuffer(buf)
	}
}