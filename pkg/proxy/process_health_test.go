package proxy

import (
	"testing"
)

func TestProcessHealthChecker_CheckProcessHealth(t *testing.T) {
	checker := NewProcessHealthChecker()

	health := checker.CheckProcessHealth(1)

	if health.PID != 1 {
		t.Errorf("expected PID 1, got %d", health.PID)
	}

	if health.Status == "" {
		t.Error("status should not be empty")
	}
}

func TestProcessHealthChecker_NonExistentProcess(t *testing.T) {
	checker := NewProcessHealthChecker()

	health := checker.CheckProcessHealth(999999)

	if health.IsAlive {
		t.Error("process 999999 should not be alive")
	}
}

func TestProcessHealth_Structure(t *testing.T) {
	health := ProcessHealth{
		PID:        12345,
		MemoryMB:   128,
		CPUPercent: 25.5,
		Status:     "running",
		IsAlive:    true,
	}

	if health.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", health.PID)
	}
	if health.MemoryMB != 128 {
		t.Errorf("expected memory 128MB, got %d", health.MemoryMB)
	}
	if health.CPUPercent != 25.5 {
		t.Errorf("expected CPU 25.5, got %f", health.CPUPercent)
	}
	if health.Status != "running" {
		t.Errorf("expected status running, got %s", health.Status)
	}
	if !health.IsAlive {
		t.Error("expected IsAlive to be true")
	}
}

func TestSplitLines(t *testing.T) {
	input := "line1\nline2\nline3"
	result := splitLines(input)

	if len(result) != 3 {
		t.Errorf("expected 3 lines, got %d", len(result))
	}
	if result[0] != "line1" {
		t.Errorf("expected line1, got %s", result[0])
	}
	if result[1] != "line2" {
		t.Errorf("expected line2, got %s", result[1])
	}
	if result[2] != "line3" {
		t.Errorf("expected line3, got %s", result[2])
	}
}

func TestSplitWords(t *testing.T) {
	input := "VmRSS:   12345 kB"
	result := splitWords(input)

	if len(result) < 2 {
		t.Errorf("expected at least 2 words, got %d", len(result))
	}
}

func TestSplitLines_Empty(t *testing.T) {
	input := ""
	result := splitLines(input)

	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d", len(result))
	}
}

func TestSplitLines_NoNewline(t *testing.T) {
	input := "single line"
	result := splitLines(input)

	if len(result) != 1 {
		t.Errorf("expected 1 line, got %d", len(result))
	}
}

func TestSplitWords_SingleWord(t *testing.T) {
	input := "word"
	result := splitWords(input)

	if len(result) != 1 {
		t.Errorf("expected 1 word, got %d", len(result))
	}
	if result[0] != "word" {
		t.Errorf("expected word, got %s", result[0])
	}
}