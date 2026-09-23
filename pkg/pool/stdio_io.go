package pool

import (
	"bufio"
	"bytes"
	"encoding/json"
	errstd "errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

const (
	// DefaultMaxResponseBytes is the default cap on one JSON-RPC message
	// (one stdout line) read from a stdio server (max_response_bytes).
	DefaultMaxResponseBytes = 64 << 20
	// stdoutReadBufferSize is the bufio buffer of the stdout reader. Lines
	// that fit are handed over without a copy.
	stdoutReadBufferSize = 64 << 10
	// oversizedHeadBytes / oversizedTailBytes are how much of a discarded
	// oversized line is kept to find the response's "id".
	oversizedHeadBytes = 4 << 10
	oversizedTailBytes = 256
	// lineBufferKeepBytes bounds the reader's reusable line buffer: after a
	// larger line the buffer is dropped so one big response does not pin
	// its memory for the life of the process.
	lineBufferKeepBytes = 1 << 20
	// maxStderrLineBytes truncates each stderr line kept and logged.
	maxStderrLineBytes = 8 << 10
)

// oversizedLine describes a stdout line longer than the configured limit
// that was discarded up to its terminating newline.
type oversizedLine struct {
	size int64
	head []byte
	tail []byte
}

// lineReader splits a stdout stream into newline-terminated lines of at
// most max bytes. A longer line is not buffered: it is discarded up to the
// next '\n' (so the stream stays in sync) and reported as oversized with a
// small head and tail sample.
type lineReader struct {
	r   *bufio.Reader
	max int
	buf []byte
	err error
}

func newLineReader(r io.Reader, maxLine int) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, stdoutReadBufferSize), max: maxLine}
}

// next returns the next line (without its '\n'), or an oversized line
// report, or the read error that ended the stream. The returned line is
// only valid until the next call.
func (lr *lineReader) next() ([]byte, *oversizedLine, error) {
	if lr.err != nil {
		return nil, nil, lr.err
	}
	if cap(lr.buf) > lineBufferKeepBytes {
		lr.buf = nil
	}
	lr.buf = lr.buf[:0]
	for {
		chunk, err := lr.r.ReadSlice('\n')
		content := chunk
		if err == nil {
			content = chunk[:len(chunk)-1]
		}
		if len(lr.buf)+len(content) > lr.max {
			return nil, lr.discard(chunk, err), nil
		}
		switch {
		case err == nil && len(lr.buf) == 0:
			// Fast path: the whole line is in the bufio buffer.
			return content, nil, nil
		case err == nil:
			lr.buf = append(lr.buf, content...)
			return lr.buf, nil, nil
		case errstd.Is(err, bufio.ErrBufferFull):
			lr.buf = append(lr.buf, chunk...)
			continue
		default:
			// EOF or read error: deliver a final unterminated line first.
			lr.err = err
			lr.buf = append(lr.buf, chunk...)
			if len(lr.buf) > 0 {
				return lr.buf, nil, nil
			}
			return nil, nil, err
		}
	}
}

// discard skips the rest of an oversized line whose buffered part is lr.buf
// followed by chunk (read with error err).
func (lr *lineReader) discard(chunk []byte, err error) *oversizedLine {
	over := &oversizedLine{}
	take := func(b []byte) {
		over.size += int64(len(b))
		if room := oversizedHeadBytes - len(over.head); room > 0 {
			over.head = append(over.head, b[:min(room, len(b))]...)
		}
		over.tail = append(over.tail, b...)
		if len(over.tail) > oversizedTailBytes {
			over.tail = append(over.tail[:0], over.tail[len(over.tail)-oversizedTailBytes:]...)
		}
	}
	take(lr.buf)
	lr.buf = lr.buf[:0]
	for {
		if err == nil {
			take(chunk[:len(chunk)-1])
			return over
		}
		take(chunk)
		if !errstd.Is(err, bufio.ErrBufferFull) {
			lr.err = err
			return over
		}
		chunk, err = lr.r.ReadSlice('\n')
	}
}

// trailingIDPattern matches a numeric "id" that is the last member of the
// top-level object, e.g. `...,"id":42}`.
var trailingIDPattern = regexp.MustCompile(`"id"\s*:\s*(-?\d+)\s*\}\s*$`)

// oversizedResponseID makes a best-effort attempt to find the numeric
// JSON-RPC "id" of a discarded response: first as a top-level member in the
// head of the line (the usual `{"jsonrpc":"2.0","id":N,...}` order), then
// as the last member of the object at its tail.
func oversizedResponseID(over *oversizedLine) (int64, bool) {
	if id, ok := topLevelIDFromPrefix(over.head); ok {
		return id, true
	}
	if m := trailingIDPattern.FindSubmatch(over.tail); m != nil {
		if id, err := strconv.ParseInt(string(m[1]), 10, 64); err == nil {
			return id, true
		}
	}
	return 0, false
}

// topLevelIDFromPrefix tokenizes a (possibly truncated) JSON object and
// returns its top-level numeric "id" member, ignoring any "id" nested in
// the result.
func topLevelIDFromPrefix(prefix []byte) (int64, bool) {
	dec := json.NewDecoder(bytes.NewReader(prefix))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return 0, false
	}
	depth := 1
	expectKey := true
	key := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, false
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			default:
				depth--
				if depth == 0 {
					return 0, false
				}
				if depth == 1 {
					expectKey = true
				}
			}
			continue
		}
		if depth != 1 {
			continue
		}
		if expectKey {
			key, _ = tok.(string)
			expectKey = false
			continue
		}
		expectKey = true
		if key != "id" {
			continue
		}
		n, ok := tok.(json.Number)
		if !ok {
			return 0, false
		}
		id, err := strconv.ParseInt(n.String(), 10, 64)
		return id, err == nil
	}
}

// stderrLineSink receives each (truncated) stderr line.
type stderrLineSink func(line []byte, truncated int)

// drainStderr reads r until EOF or a read error, splitting it into lines
// and truncating each to maxStderrLineBytes. It never stops early: a child
// whose stderr pipe is not drained blocks on its next write.
func drainStderr(r io.Reader, sink stderrLineSink) {
	br := bufio.NewReaderSize(r, 4096)
	line := make([]byte, 0, 256)
	truncated := 0
	for {
		chunk, err := br.ReadSlice('\n')
		content := chunk
		if err == nil {
			content = chunk[:len(chunk)-1]
		}
		room := maxStderrLineBytes - len(line)
		n := min(max(room, 0), len(content))
		line = append(line, content[:n]...)
		truncated += len(content) - n
		if errstd.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == nil || len(line) > 0 || truncated > 0 {
			sink(bytes.TrimRight(line, "\r"), truncated)
		}
		line = line[:0]
		truncated = 0
		if err != nil {
			return
		}
	}
}

// formatStderrLine renders one captured stderr line, noting truncation.
func formatStderrLine(line []byte, truncated int) string {
	if truncated > 0 {
		return fmt.Sprintf("%s ...[truncated %d bytes]", line, truncated)
	}
	return string(line)
}
