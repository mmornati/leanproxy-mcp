package utils

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

func ValidatePath(path string, baseDir string) error {
	if err := validatePathTraversalPatterns(path); err != nil {
		return err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("invalid base dir: %w", err)
	}

	if !strings.HasPrefix(absPath, absBase+string(filepath.Separator)) {
		return fmt.Errorf("path traversal detected: %s", path)
	}

	return nil
}

func validatePathTraversalPatterns(path string) error {
	decoded := path
	for {
		old := decoded
		decoded, _ = url.QueryUnescape(decoded)
		if decoded == old {
			break
		}
	}

	if strings.Contains(decoded, "..") {
		return fmt.Errorf("path traversal pattern detected: %s", path)
	}

	if strings.Contains(path, "\x00") {
		return fmt.Errorf("null byte in path: %s", path)
	}

	return nil
}