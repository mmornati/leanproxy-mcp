package utils

import (
	"context"
	"fmt"
	"time"
)

func FormatTimeout(ctx context.Context, d time.Duration) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(d):
		return fmt.Sprintf("operation completed after %v", d), nil
	}
}

func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return nil
}

func SanitizeString(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 32 && c < 127 {
			result = append(result, c)
		} else {
			result = append(result, '?')
		}
	}
	return string(result)
}
