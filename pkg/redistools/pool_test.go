package redistools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestWithConn_NoDeadlockOnRepeatedRedialFailure covers the story's
// acceptance criterion: a fake server that drops every connection and then
// stops accepting (so re-dial keeps failing) must never deadlock the pool,
// and Close() must return promptly afterwards. It reproduces the original
// bug directly: withConn used to receive from c.pool while holding c.mu, and
// on a failed re-dial it returned without ever putting the borrowed slot
// back, so the pool drained permanently and every later call — including
// Close, which needs c.mu — blocked forever.
func TestWithConn_NoDeadlockOnRepeatedRedialFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	const poolSize = 2

	var accepted int32
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			// Drop the connection immediately: the client's initial pool
			// fill (which does no I/O beyond connect, since Password/DB are
			// unset) still succeeds, but the very first command on any of
			// these connections will fail.
			conn.Close()
			if atomic.AddInt32(&accepted, 1) >= poolSize {
				// Stop accepting entirely, so every re-dial after this point
				// fails with "connection refused" — the "refuses redials"
				// half of the acceptance criterion.
				ln.Close()
				return
			}
		}
	}()

	cfg := DefaultConfig()
	cfg.Address = ln.Addr().String()
	cfg.PoolSize = poolSize
	cfg.DialTimeout = 500 * time.Millisecond
	cfg.CommandTimeout = 500 * time.Millisecond

	client, err := NewRedisClient(discardLogger(), cfg)
	require.NoError(t, err, "initial pool fill must succeed even though the server closes each connection immediately")

	// Fire far more concurrent operations than there are pool slots. Every
	// one of them must fail (broken connection, then a failed re-dial), but
	// none may block indefinitely and none may leak its slot.
	const attempts = poolSize * 10
	var wg sync.WaitGroup
	errCount := int32(0)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			args, _ := json.Marshal(map[string]string{"key": "k"})
			_, callErr := client.CallTool(context.Background(), toolGet, args)
			if callErr != nil {
				atomic.AddInt32(&errCount, 1)
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("operations did not complete within timeout: likely a pool deadlock")
	}

	assert.Equal(t, int32(attempts), errCount, "every call should fail (broken connection / failed re-dial), not hang")

	// The real assertion: every borrow above must have released its slot on
	// every path, so Close can still collect all of them and return
	// promptly instead of blocking on a channel that never drains.
	closeDone := make(chan struct{})
	go func() {
		client.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return promptly: pool slots were leaked")
	}
}

// TestWithConn_ConcurrentReleaseDuringClose exercises release() racing with
// Close() under -race: many goroutines borrow-and-release connections while
// Close() is invoked concurrently. Nothing may panic (send on closed
// channel) and Close() must still return promptly.
func TestWithConn_ConcurrentReleaseDuringClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			// Keep the connection open but never respond: commands will
			// time out via the command deadline rather than erroring
			// immediately, which better exercises concurrent release.
			go func(c net.Conn) {
				buf := make([]byte, 1)
				for {
					if _, readErr := c.Read(buf); readErr != nil {
						c.Close()
						return
					}
				}
			}(conn)
		}
	}()

	cfg := DefaultConfig()
	cfg.Address = ln.Addr().String()
	cfg.PoolSize = 4
	cfg.DialTimeout = 500 * time.Millisecond
	cfg.CommandTimeout = 100 * time.Millisecond

	client, err := NewRedisClient(discardLogger(), cfg)
	require.NoError(t, err)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				args, _ := json.Marshal(map[string]string{"key": "k"})
				_, _ = client.CallTool(context.Background(), toolGet, args)
			}
		}()
	}

	// Let a few operations get underway, then close concurrently with them.
	time.Sleep(20 * time.Millisecond)

	closeDone := make(chan struct{})
	go func() {
		client.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return promptly while racing with releases")
	}

	close(stop)
	wg.Wait()
}

// fakeConn is a minimal net.Conn that serves canned RESP bytes to a reader
// and discards writes, used to unit-test the RESP size limits without a real
// TCP round trip.
type fakeConn struct {
	net.Conn
	r io.Reader
}

func (f *fakeConn) Read(b []byte) (int, error)       { return f.r.Read(b) }
func (f *fakeConn) Write(b []byte) (int, error)      { return len(b), nil }
func (f *fakeConn) SetDeadline(time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(time.Time) error { return nil }
func (f *fakeConn) Close() error                     { return nil }

func TestReadResponse_BulkStringLengthLimit(t *testing.T) {
	client := testClient()
	client.config.MaxBulkLen = DefaultMaxBulkLen

	conn := &fakeConn{r: strings.NewReader("$999999999999\r\n")}
	_, err := client.readResponse(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limit")
}

func TestReadResponse_ArrayLengthLimit(t *testing.T) {
	client := testClient()
	client.config.MaxArrayLen = DefaultMaxArrayLen

	conn := &fakeConn{r: strings.NewReader(fmt.Sprintf("*%d\r\n", DefaultMaxArrayLen+1))}
	_, err := client.readResponse(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limit")
}

func TestReadResponse_WithinLimitsSucceeds(t *testing.T) {
	client := testClient()
	conn := &fakeConn{r: strings.NewReader("$5\r\nhello\r\n")}
	val, err := client.readResponse(conn)
	require.NoError(t, err)
	assert.Equal(t, "hello", val)
}

func TestReadLine_UnboundedLineIsRejected(t *testing.T) {
	longLine := make([]byte, maxLineLen+10)
	for i := range longLine {
		longLine[i] = 'a'
	}
	conn := &fakeConn{r: strings.NewReader(string(longLine) + "\r\n")}
	_, err := readLine(conn)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}
