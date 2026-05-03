package bouncer

import (
	"bytes"
	"testing"
)

func TestGoImportPruning(t *testing.T) {
	content := `package main

import (
	"fmt"
	"os"
	"context"
)

func main() {
	fmt.Println("hello")
}
`
	pruner := NewBoilerplatePruner(true, true, false, nil)
	result, report, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.ImportsRedacted == 0 {
		t.Error("expected imports to be redacted")
	}

	if bytes.Contains(result, []byte("import (")) {
		t.Error("import block should be redacted")
	}

	if !bytes.Contains(result, []byte("[IMPORTS_REDACTED:")) {
		t.Error("result should contain IMPORTS_REDACTED marker")
	}

	if !bytes.Contains(result, []byte("func main()")) {
		t.Error("actual code content should be preserved")
	}
}

func TestPythonImportPruning(t *testing.T) {
	content := `from os import getcwd
import sys
import json

def main():
    print("hello")
`
	pruner := NewBoilerplatePruner(true, true, false, nil)
	result, report, err := pruner.Process([]byte(content), "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.ImportsRedacted == 0 {
		t.Error("expected imports to be redacted")
	}

	if bytes.Contains(result, []byte("from os")) {
		t.Error("python import should be redacted")
	}

	if !bytes.Contains(result, []byte("def main()")) {
		t.Error("actual code content should be preserved")
	}
}

func TestJavaScriptImportPruning(t *testing.T) {
	content := `import express from 'express';
import { Router } from 'express';
import fs from 'fs';

const app = express();
`
	pruner := NewBoilerplatePruner(true, true, false, nil)
	result, report, err := pruner.Process([]byte(content), "javascript")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.ImportsRedacted == 0 {
		t.Error("expected imports to be redacted")
	}

	if bytes.Contains(result, []byte("import express")) {
		t.Error("js import should be redacted")
	}

	if !bytes.Contains(result, []byte("const app")) {
		t.Error("actual code content should be preserved")
	}
}

func TestCopyrightPruning(t *testing.T) {
	content := `/*
 * Copyright 2024 Example Corp
 * Licensed under the Apache License, Version 2.0
 */

package main

func main() {}
`
	pruner := NewBoilerplatePruner(true, false, true, nil)
	result, report, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.LicensesRedacted == 0 {
		t.Error("expected license/copyright to be redacted")
	}

	if bytes.Contains(result, []byte("Copyright")) {
		t.Error("copyright should be redacted")
	}

	if !bytes.Contains(result, []byte("[LICENSE_REDACTED]")) {
		t.Error("result should contain LICENSE_REDACTED marker")
	}
}

func TestNoOpForCleanFiles(t *testing.T) {
	content := `func main() {
	println("hello world")
}
`
	pruner := NewBoilerplatePruner(true, true, true, nil)
	result, report, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.ImportsRedacted != 0 {
		t.Error("no imports should be redacted for clean file")
	}

	if report.LicensesRedacted != 0 {
		t.Error("no licenses should be redacted for clean file")
	}

	if !bytes.Equal(result, []byte(content)) {
		t.Error("clean file should pass through unchanged")
	}
}

func TestDisabledBoilerplate(t *testing.T) {
	content := `import "fmt"

func main() {
	fmt.Println("hello")
}
`
	pruner := NewBoilerplatePruner(false, true, true, nil)
	result, _, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(result, []byte(content)) {
		t.Error("disabled boilerplate should pass through unchanged")
	}
}

func TestRedactImportsOnly(t *testing.T) {
	content := `/*
 * Copyright 2024 Test Corp
 */

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	pruner := NewBoilerplatePruner(true, true, false, nil)
	result, report, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.ImportsRedacted == 0 {
		t.Error("expected imports to be redacted")
	}

	if report.LicensesRedacted != 0 {
		t.Error("licenses should NOT be redacted when redactLicenses is false")
	}

	if !bytes.Contains(result, []byte("Copyright")) {
		t.Error("copyright should be preserved")
	}
}

func TestRedactLicensesOnly(t *testing.T) {
	content := `/*
 * Copyright 2024 Test Corp
 */

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	pruner := NewBoilerplatePruner(true, false, true, nil)
	result, report, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.LicensesRedacted == 0 {
		t.Error("expected licenses to be redacted")
	}

	if report.ImportsRedacted != 0 {
		t.Errorf("imports should NOT be redacted when redactImports is false, got %d", report.ImportsRedacted)
	}

	if !bytes.Contains(result, []byte("import \"fmt\"")) {
		t.Error("import should be preserved when redactImports is false")
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"test.go", "go"},
		{"test.py", "python"},
		{"test.js", "javascript"},
		{"test.ts", "typescript"},
		{"test.jsx", "javascript"},
		{"test.tsx", "typescript"},
		{"test.txt", "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			result := DetectLanguage(tc.filename)
			if result != tc.expected {
				t.Errorf("DetectLanguage(%s) = %s, want %s", tc.filename, result, tc.expected)
			}
		})
	}
}

func TestTokenSavingsCalculation(t *testing.T) {
	content := `/*
 * Copyright 2024 Test Corp
 */

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	pruner := NewBoilerplatePruner(true, true, true, nil)
	_, report, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.OriginalSize <= report.ProcessedSize {
		t.Error("processed size should be smaller than original")
	}

	if report.TokenSavings <= 0 {
		t.Error("token savings should be positive")
	}

	expectedSavings := (report.OriginalSize - report.ProcessedSize) * 4 / 3
	if report.TokenSavings != expectedSavings {
		t.Errorf("token savings = %d, want %d", report.TokenSavings, expectedSavings)
	}
}

func TestShebangRemoval(t *testing.T) {
	content := `#!/usr/bin/env python3

print("hello")
`
	pruner := NewBoilerplatePruner(true, true, true, nil)
	result, _, err := pruner.Process([]byte(content), "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bytes.HasPrefix(result, []byte("#!")) {
		t.Error("shebang should be removed")
	}

	if !bytes.Contains(result, []byte("print(\"hello\")")) {
		t.Error("actual code content should be preserved")
	}
}

func TestEmptyFile(t *testing.T) {
	content := ""
	pruner := NewBoilerplatePruner(true, true, true, nil)
	result, report, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(result, []byte(content)) {
		t.Error("empty file should pass through unchanged")
	}

	if report.OriginalSize != 0 {
		t.Error("original size should be 0 for empty file")
	}
}

func TestUnknownLanguageFallsBackToGo(t *testing.T) {
	content := `import "fmt"
import "os"

func main() {}
`
	pruner := NewBoilerplatePruner(true, true, false, nil)
	result, report, err := pruner.Process([]byte(content), "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.ImportsRedacted == 0 {
		t.Error("expected imports to be redacted with go fallback")
	}

	if !bytes.Contains(result, []byte("[IMPORTS_REDACTED:")) {
		t.Error("result should contain IMPORTS_REDACTED marker")
	}
}

func TestCustomPatterns(t *testing.T) {
	content := `My Company Inc. 2024
All rights reserved.

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	customPatterns := []PatternDef{
		{Name: "my-company", Pattern: "My Company Inc\\..*?\\n"},
	}
	pruner := NewBoilerplatePruner(true, true, false, customPatterns)
	result, _, err := pruner.Process([]byte(content), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(result, []byte("[BOILERPLATE_REDACTED:my-company]")) {
		t.Error("result should contain custom BOILERPLATE_REDACTED marker")
	}
}
