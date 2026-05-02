package bouncer

import (
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
	buf := make([]byte, r.bufferSize)
	var input strings.Builder

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			input.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("bouncer redact: %w", err)
		}
	}

	data := input.String()
	slog.Debug("redacting message", "size", len(data))

	redacted := r.redactString(data)

	if _, err := writer.Write([]byte(redacted)); err != nil {
		return fmt.Errorf("bouncer redact: %w", err)
	}

	return nil
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