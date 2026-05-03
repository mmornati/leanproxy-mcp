package main

import (
	"testing"
)

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		expected int
	}{
		{"Success is 0", ExitSuccess, 0},
		{"General error is 1", ExitGeneral, 1},
		{"Misuse is 2", ExitMisuse, 2},
		{"Configuration error is 3", ExitConfigurationError, 3},
		{"Token resolution failure is 4", ExitTokenResolutionFailure, 4},
		{"Upstream error is 125", ExitUpstream, 125},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.expected {
				t.Errorf("exit code %s: got %d, want %d", tt.name, tt.code, tt.expected)
			}
		})
	}
}

func TestPosixError(t *testing.T) {
	err := &PosixError{
		Code:    ExitMisuse,
		Message: "test error",
		Cause:   nil,
	}

	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got '%s'", err.Error())
	}

	if err.Code != ExitMisuse {
		t.Errorf("expected code %d, got %d", ExitMisuse, err.Code)
	}
}

func TestExitWithError(t *testing.T) {
	err := &PosixError{
		Code:    ExitConfigurationError,
		Message: "config error",
		Cause:   nil,
	}

	code := GetExitCode(err)
	if code != ExitConfigurationError {
		t.Errorf("expected %d, got %d", ExitConfigurationError, code)
	}

	code = GetExitCode(nil)
	if code != ExitSuccess {
		t.Errorf("expected %d for nil, got %d", ExitSuccess, code)
	}
}

func TestExitMisusefFormat(t *testing.T) {
	posixErr := &PosixError{
		Code:    ExitMisuse,
		Message: "usage error",
		Cause:   nil,
	}

	if posixErr.Code != ExitMisuse {
		t.Errorf("expected code %d, got %d", ExitMisuse, posixErr.Code)
	}

	if posixErr.Error() != "usage error" {
		t.Errorf("expected 'usage error', got '%s'", posixErr.Error())
	}
}