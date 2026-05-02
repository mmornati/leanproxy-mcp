package bouncer

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"time"
)

const defaultBufferSizeStreaming = 4096

type StreamingRedactor struct {
	patterns      []*regexp.Regexp
	alertManager *AlertManager
	bufferSize    int
}

func NewStreamingRedactor(patterns []*regexp.Regexp) *StreamingRedactor {
	return &StreamingRedactor{
		patterns:   patterns,
		bufferSize: defaultBufferSizeStreaming,
	}
}

func NewStreamingRedactorWithAlerts(patterns []*regexp.Regexp, am *AlertManager) *StreamingRedactor {
	return &StreamingRedactor{
		patterns:     patterns,
		alertManager: am,
		bufferSize:   defaultBufferSizeStreaming,
	}
}

func (sr *StreamingRedactor) RedactStream(r io.Reader, w io.Writer, meta ...*RedactionMeta) error {
	reader := bufio.NewReaderSize(r, sr.bufferSize)
	writer := bufio.NewWriterSize(w, sr.bufferSize)
	defer writer.Flush()

	var totalRead, totalWritten int64
	matchCount := 0

	for {
		buf := GetBuffer()
		defer ReturnBuffer(buf)

		n, readErr := reader.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			redacted, count := sr.redactChunkWithCount(chunk)

			_, writeErr := writer.Write(redacted)
			if writeErr != nil {
				return fmt.Errorf("streaming redact write: %w", writeErr)
			}
			totalRead += int64(n)
			totalWritten += int64(len(redacted))
			matchCount += count
			slog.Debug("processing chunk", "size", n)
		}

		if readErr != nil {
			if readErr == io.EOF {
				slog.Info("streaming redaction complete", "bytes_read", totalRead, "bytes_written", totalWritten)
				if sr.alertManager != nil && matchCount > 0 && len(meta) > 0 && meta[0] != nil {
					sr.alertManager.RecordRedaction(RedactionEvent{
						PatternName: "streaming_redaction",
						Count:       matchCount,
						Timestamp:   time.Now(),
						MessageID:   meta[0].MessageID,
						Method:      meta[0].Method,
					})
					sr.alertManager.EmitSummary(meta[0].MessageID, meta[0].Method)
				}
				return nil
			}
			return fmt.Errorf("streaming redact read: %w", readErr)
		}
	}
}

func (sr *StreamingRedactor) redactChunkWithCount(chunk []byte) ([]byte, int) {
	result := make([]byte, 0, len(chunk))
	remaining := chunk
	matchCount := 0

	for len(remaining) > 0 {
		matchIndex := -1
		matchEnd := -1

		for _, pattern := range sr.patterns {
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
		matchCount++
	}

	return result, matchCount
}

func (sr *StreamingRedactor) redactChunk(chunk []byte) []byte {
	result, _ := sr.redactChunkWithCount(chunk)
	return result
}

func (sr *StreamingRedactor) RedactToWriter(r io.Reader, w io.Writer) error {
	return sr.RedactStream(r, w)
}