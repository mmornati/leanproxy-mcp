package bouncer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

const SecretRedacted = "[SECRET_REDACTED]"

type Redactor struct {
	patterns   []*regexp.Regexp
	bufferSize int
}

func NewRedactor(patterns []*regexp.Regexp) *Redactor {
	return &Redactor{
		patterns:   patterns,
		bufferSize: 4096,
	}
}

func (r *Redactor) RedactStream(reader io.Reader, writer io.Writer) error {
	readerBuf := bufio.NewReaderSize(reader, r.bufferSize)
	writerBuf := bufio.NewWriterSize(writer, r.bufferSize)
	defer writerBuf.Flush()

	var totalRead, totalWritten int64

	for {
		buf := GetBuffer()
		defer ReturnBuffer(buf)

		n, err := readerBuf.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			redacted := r.redactChunk(chunk)

			_, writeErr := writerBuf.Write(redacted)
			if writeErr != nil {
				return fmt.Errorf("bouncer redact: %w", writeErr)
			}
			totalRead += int64(n)
			totalWritten += int64(len(redacted))
			slog.Debug("processing chunk", "size", n)
		}
		if err == io.EOF {
			slog.Info("streaming redaction complete", "bytes_read", totalRead, "bytes_written", totalWritten)
			break
		}
		if err != nil {
			return fmt.Errorf("bouncer redact: %w", err)
		}
	}

	return nil
}

func (r *Redactor) redactChunk(chunk []byte) []byte {
	result := make([]byte, 0, len(chunk))
	remaining := chunk

	for len(remaining) > 0 {
		matchIndex := -1
		matchEnd := -1

		for _, pattern := range r.patterns {
			loc := pattern.FindIndex(remaining)
			if loc != nil && (matchIndex == -1 || loc[0] < matchIndex) {
				matchIndex = loc[0]
				matchEnd = loc[1]
			}
		}

		if matchIndex == -1 {
			result = append(result, remaining...)
			break
		}

		result = append(result, remaining[:matchIndex]...)
		result = append(result, SecretRedacted...)
		remaining = remaining[matchEnd:]
	}

	return result
}

func (r *Redactor) RedactJSON(data []byte) ([]byte, error) {
	slog.Debug("redacting message", "size", len(data))

	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("invalid JSON input, passing through unchanged")
		return data, nil
	}

	redacted := r.redactString(string(data))

	slog.Info("redaction complete", "secrets_found", strings.Count(string(data), SecretRedacted)-strings.Count(redacted, SecretRedacted))

	return []byte(redacted), nil
}

func (r *Redactor) redactString(data string) string {
	result := data
	for _, pattern := range r.patterns {
		result = pattern.ReplaceAllString(result, SecretRedacted)
	}
	return result
}

func NewRedactorFromLoaded(loaded *LoadedPatterns) *Redactor {
	return &Redactor{
		patterns:   loaded.All,
		bufferSize: 4096,
	}
}