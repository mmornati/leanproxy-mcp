---
id: 2-4
key: 2-4-in-memory-only
epic: epic-2
title: Implement In-Memory Only Processing
---

# Story 2-4: Implement In-Memory Only Processing

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 2-4 |
| **Key** | `2-4-in-memory-only` |
| **Epic** | `epic-2` (Security & Data Governance - The Bouncer) |
| **Title** | Implement In-Memory Only Processing |

## Story Requirements

### User Story

**As a** developer,
**I want to** ensure all redaction and optimization happens in-memory only,
**So that** no sensitive data is ever written to disk.

### Acceptance Criteria (BDD Format)

```gherkin
Feature: In-Memory Only Processing
  All sensitive data processing happens in memory with no persistence
  to disk or external network calls.

  Scenario: No unredacted data is written to disk
    Given intercepted JSON-RPC traffic with secrets
    When the Bouncer processes it
    Then no unredacted data is written to disk
    And no network requests are made to external services
    And all processing happens in memory

  Scenario: Default audit logging does not expose secrets
    Given audit logging is disabled (default)
    When redaction events occur
    Then only the fact that redaction occurred is logged (not the content)
    And no sensitive data appears in any log file

  Scenario: Large payloads are processed via streaming
    Given the proxy receives a large file read result (up to 50MB)
    When the Bouncer processes it
    Then it streams through without loading the entire payload into memory
    And memory usage stays bounded

  Scenario: Memory is released after processing
    Given a large message has been processed
    When processing completes
    Then buffers are released for garbage collection
    And no sensitive data remains in memory longer than necessary
```

## Developer Context

### Technical Requirements

1. **Streaming Processing**
   - All redaction MUST use streaming `io.Reader`/`io.Writer` interfaces
   - NEVER load full message into memory as a string or byte slice
   - Use fixed-size buffers (4KB or 8KB) for chunk processing
   - Support payloads up to 50MB without memory spikes (NFR2)

2. **No Disk Persistence**
   - No temporary files for message processing
   - No caching of unredacted messages
   - No writing of secrets to log files (even temporary)
   - If audit logging is enabled, only write redaction metadata (count, type, timestamp)

3. **No External Network Calls**
   - Bouncer MUST NOT make any HTTP or network requests
   - All pattern matching happens locally in-process
   - No telemetry or phone-home features

4. **Buffer Management**
   - Use `sync.Pool` for buffer reuse to reduce allocations
   - Fixed maximum buffer size: 64KB per chunk
   - Automatic buffer return to pool after use

5. **Memory Safety**
   - Sensitive data in byte buffers MUST be zeroed before buffer return
   - Use `crypto/subtle` constant-time operations where possible
   - Clear sensitive data from stack frames when function returns

### Architecture Compliance

| Requirement | Implementation |
|-------------|----------------|
| Go with cobra CLI | CLI has no disk-based secret storage commands |
| `pkg/bouncer/` for redaction | Streaming implementation in `pkg/bouncer/streaming.go` |
| camelCase for Go symbols | Use `camelCase` for functions/variables |
| `fmt.Errorf("context: %w", err)` | Use for error wrapping |
| `log/slog` for logging | Only redaction metadata logged, no content |
| In-memory only | `sync.Pool` for buffer management, no disk I/O |
| Streaming regex redaction | Chunk-based processing with buffer pools |

### File Structure

```
pkg/bouncer/
├── redactor.go          # Core streaming redaction engine
├── streaming.go         # Streaming buffer management
├── buffer_pool.go       # sync.Pool buffer implementation
├── buffer_pool_test.go  # Buffer pool tests
├── patterns.go          # Pattern types and helpers
└── redactor_test.go     # Redaction engine tests

cmd/leanproxy/
└── main.go              # CLI entry point (no changes needed)
```

### Package Implementation

**`pkg/bouncer/buffer_pool.go`**:
```go
package bouncer

import (
    "crypto/subtle"
    "sync"
)

const (
    defaultBufferSize = 4096
    maxBufferSize     = 65536 // 64KB max chunk
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
```

**`pkg/bouncer/streaming.go`**:
```go
package bouncer

import (
    "bufio"
    "fmt"
    "io"
    "log/slog"
    "regexp"
)

type StreamingRedactor struct {
    patterns   []*regexp.Regexp
    bufferSize int
    pool       *sync.Pool
}

func NewStreamingRedactor(patterns []*regexp.Regexp) *StreamingRedactor {
    return &StreamingRedactor{
        patterns:   patterns,
        bufferSize: defaultBufferSize,
    }
}

func (sr *StreamingRedactor) RedactStream(r io.Reader, w io.Writer) error {
    reader := bufio.NewReaderSize(r, sr.bufferSize)
    writer := bufio.NewWriterSize(w, sr.bufferSize)
    defer writer.Flush()

    for {
        buf := GetBuffer()
        defer ReturnBuffer(buf)

        n, readErr := reader.Read(buf)
        if n > 0 {
            chunk := buf[:n]
            redacted := sr.redactChunk(chunk)

            _, writeErr := writer.Write(redacted)
            if writeErr != nil {
                return fmt.Errorf("streaming redact write: %w", writeErr)
            }
        }

        if readErr != nil {
            if readErr == io.EOF {
                return nil
            }
            return fmt.Errorf("streaming redact read: %w", readErr)
        }
    }
}

func (sr *StreamingRedactor) redactChunk(chunk []byte) []byte {
    result := make([]byte, 0, len(chunk))
    remaining := chunk

    for len(remaining) > 0 {
        matchIndex := -1
        matchedPattern := -1

        for i, pattern := range sr.patterns {
            loc := pattern.FindIndex(remaining)
            if loc != nil && (matchIndex == -1 || loc[0] < matchIndex) {
                matchIndex = loc[0]
                matchedPattern = i
            }
        }

        if matchIndex == -1 {
            result = append(result, remaining...)
            break
        }

        result = append(result, remaining[:matchIndex]...)
        result = append(result, "[SECRET_REDACTED]"...)
        remaining = remaining[matchIndex:]
    }

    return result
}
```

### Testing Requirements

1. **Memory Safety Tests** (`pkg/bouncer/buffer_pool_test.go`):
   - Test buffer is zeroed on return
   - Test buffer pool reuses allocations
   - Test concurrent buffer get/return doesn't race

2. **Streaming Tests** (`pkg/bouncer/redactor_test.go`):
   - Test large payload (10MB+) processes without full memory load
   - Test streaming handles partial reads/writes
   - Test streaming completes correctly on EOF

3. **Security Tests**:
   - Test no sensitive data in memory after processing completes
   - Test buffer overflow protection (max buffer size enforced)
   - Test concurrent processing doesn't leak data between goroutines

4. **Test Implementation**:
```go
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
}

func TestLargePayloadStreaming(t *testing.T) {
    largeData := make([]byte, 10*1024*1024) // 10MB
    fillWithSecrets(largeData)

    r := bytes.NewReader(largeData)
    var w bytes.Buffer

    redactor := NewStreamingRedactor(BuiltInPatterns)
    err := redactor.RedactStream(r, &w)

    require.NoError(t, err)
    assert.Less(t, w.Len(), len(largeData)*2)
}
```

### Error Handling

- Read errors: return wrapped error, do not continue processing
- Write errors: return wrapped error, flush if possible
- Pattern errors: log and skip (already compiled patterns should be valid)
- Memory allocation failures: return error with context
- Use `fmt.Errorf("streaming redact: %w", err)` for error wrapping

### Logging Requirements

- `slog.Debug("processing chunk", "size", n)` - chunk processing
- `slog.Info("streaming redaction complete", "bytes_read", totalRead, "bytes_written", totalWritten)` - completion
- `slog.Warn("memory pressure detected", "allocated", m.Alloc)` - if runtime reports pressure
- NEVER log: secret values, unredacted content, full payloads

### Security Considerations

1. **Defense in Depth**
   - Streaming processing prevents memory dumps of full secrets
   - Buffer zeroing ensures secrets don't persist in freed memory
   - No persistence layer means no file-based data recovery attacks

2. **Runtime Verification**
   - Consider adding `runtime.GC()` calls after processing sensitive data
   - Consider `runtime.mallocgc` statistics monitoring for anomaly detection
