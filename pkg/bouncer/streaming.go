package bouncer

import (
	"io"
	"regexp"
)

const defaultBufferSizeStreaming = 4096

// StreamingRedactor is a thin wrapper kept for API compatibility. All
// streaming redaction is implemented by Redactor.RedactStream, which carries
// state across read boundaries; this type used to redact each read
// independently and leaked any secret that straddled a boundary.
type StreamingRedactor struct {
	inner *Redactor
}

func NewStreamingRedactor(patterns []*regexp.Regexp) *StreamingRedactor {
	r := NewRedactor(patterns)
	r.bufferSize = defaultBufferSizeStreaming
	return &StreamingRedactor{inner: r}
}

func NewStreamingRedactorWithAlerts(patterns []*regexp.Regexp, am *AlertManager) *StreamingRedactor {
	r := NewRedactorWithAlerts(patterns, am)
	r.bufferSize = defaultBufferSizeStreaming
	return &StreamingRedactor{inner: r}
}

func (sr *StreamingRedactor) RedactStream(r io.Reader, w io.Writer, meta ...*RedactionMeta) error {
	return sr.inner.RedactStream(r, w, meta...)
}

func (sr *StreamingRedactor) redactChunkWithCount(chunk []byte) ([]byte, int) {
	return sr.inner.redactChunkWithCount(chunk)
}

func (sr *StreamingRedactor) redactChunk(chunk []byte) []byte {
	return sr.inner.redactChunk(chunk)
}

// RedactToWriter is an alias for RedactStream kept for backwards
// compatibility with callers that pre-date the RedactStream rename. Both
// methods share the same code path — fixing one fixes both — so any future
// change to streaming redaction must update the single implementation in
// Redactor.RedactStream. See issue #277 for the rationale.
func (sr *StreamingRedactor) RedactToWriter(r io.Reader, w io.Writer) error {
	return sr.RedactStream(r, w)
}
