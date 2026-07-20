package filesystemtools

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func tempRoot(t *testing.T) (*FilesystemClient, string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "filesystem-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	client, err := NewFilesystemClient(slog.Default(), []string{dir})
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("failed to create filesystem client: %v", err)
	}

	cleanup := func() {
		client.Close()
		os.RemoveAll(dir)
	}

	return client, dir, cleanup
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	fpath := filepath.Join(dir, name)
	err := os.MkdirAll(filepath.Dir(fpath), 0755)
	if err != nil {
		t.Fatalf("failed to create parent dirs for %s: %v", name, err)
	}
	if err := os.WriteFile(fpath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file %s: %v", name, err)
	}
}

func TestNewFilesystemClient_NoRoots(t *testing.T) {
	_, err := NewFilesystemClient(slog.Default(), nil)
	if err == nil {
		t.Fatal("expected error when no allowed_roots provided")
	}
	if err.Error() != "filesystem.allowed_roots is required — set at least one allowed root directory in config" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestNewFilesystemClient_EmptyRoots(t *testing.T) {
	_, err := NewFilesystemClient(slog.Default(), []string{})
	if err == nil {
		t.Fatal("expected error when empty allowed_roots provided")
	}
}

func TestNewFilesystemClient_InvalidRoot(t *testing.T) {
	_, err := NewFilesystemClient(slog.Default(), []string{"/nonexistent/path/that/does/not/exist"})
	if err == nil {
		t.Fatal("expected error for non-existent root directory")
	}
}

func TestNewFilesystemClient_ValidRoot(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	if client == nil {
		t.Fatal("expected non-nil client")
	}
	roots := client.AllowedRoots()
	if len(roots) != 1 {
		t.Fatalf("expected 1 allowed root, got %d", len(roots))
	}
	if roots[0] != dir {
		t.Errorf("root = %q, want %q", roots[0], dir)
	}
}

func TestResolvePathWithinRoots(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{"empty path", "", "", true},
		{"dot", ".", ".", false},
		{"simple file", "foo.txt", "foo.txt", false},
		{"nested file", "dir/foo.txt", "dir/foo.txt", false},
		{"absolute path", "/etc/passwd", "", true},
		{"traversal direct", "../etc/passwd", "", true},
		{"traversal nested", "foo/../../etc/passwd", "", true},
		{"clean dot slash", "./foo", "foo", false},
		{"clean double slash", "foo//bar", "foo/bar", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePathWithinRoots(tt.path)
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetTools(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	tools := client.GetTools()
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		if names[tool.Name] {
			t.Errorf("duplicate tool name: %s", tool.Name)
		}
		names[tool.Name] = true

		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil || string(tool.InputSchema) == "" || string(tool.InputSchema) == "{}" {
			t.Errorf("tool %q has empty or missing input schema", tool.Name)
		}
		var schema map[string]interface{}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Errorf("tool %q has invalid input schema JSON: %v", tool.Name, err)
		}
	}

	expectedTools := []string{toolReadFile, toolWriteFile, toolListDir, toolFileInfo, toolSearchFiles, toolReadMultiple}
	for _, name := range expectedTools {
		if !names[name] {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

func TestCallTool_UnknownTool(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestReadFile(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "hello.txt", "Hello, World!")

	result, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"hello.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rr, ok := result.(ReadFileResult)
	if !ok {
		t.Fatalf("expected ReadFileResult, got %T", result)
	}
	if rr.Content != "Hello, World!" {
		t.Errorf("content = %q, want %q", rr.Content, "Hello, World!")
	}
	if rr.Size != 13 {
		t.Errorf("size = %d, want 13", rr.Size)
	}
	if rr.Path != "hello.txt" {
		t.Errorf("path = %q, want %q", rr.Path, "hello.txt")
	}
}

func TestReadFile_Nested(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "subdir/nested.txt", "nested content")

	result, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"subdir/nested.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rr := result.(ReadFileResult)
	if rr.Content != "nested content" {
		t.Errorf("content = %q, want %q", rr.Content, "nested content")
	}
}

func TestReadFile_EmptyPath(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":""}`))
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestReadFile_Traversal(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"../etc/passwd"}`))
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestReadFile_AbsolutePath(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"/etc/passwd"}`))
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestReadFile_Nonexistent(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"nonexistent.txt"}`))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadFile_Directory(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"."}`))
	if err == nil {
		t.Fatal("expected error when reading a directory")
	}
}

func TestReadFile_LargeFile(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	// Create a file larger than 1MB
	content := make([]byte, 2*1024*1024)
	for i := range content {
		content[i] = byte('A' + i%26)
	}
	writeTestFile(t, dir, "large.bin", string(content))

	result, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"large.bin"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rr := result.(ReadFileResult)
	if !rr.Truncated {
		t.Error("expected truncated=true for large file")
	}
	if rr.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", rr.Size, len(content))
	}
	if len(rr.Content) == len(content) {
		t.Error("expected truncated content, got full content")
	}
}

func TestWriteFile(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	result, err := client.CallTool(context.Background(), toolWriteFile, json.RawMessage(`{"path":"newfile.txt","content":"new content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wr := result.(WriteFileResult)
	if wr.Path != "newfile.txt" {
		t.Errorf("path = %q, want %q", wr.Path, "newfile.txt")
	}
	if wr.BytesWritten != 11 {
		t.Errorf("bytes_written = %d, want 11", wr.BytesWritten)
	}

	// Verify by reading back
	readResult, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"newfile.txt"}`))
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	rr := readResult.(ReadFileResult)
	if rr.Content != "new content" {
		t.Errorf("read back content = %q, want %q", rr.Content, "new content")
	}
}

func TestWriteFile_Nested(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolWriteFile, json.RawMessage(`{"path":"a/b/c/nested.txt","content":"nested"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	readResult, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"a/b/c/nested.txt"}`))
	if err != nil {
		t.Fatalf("failed to read back: %v", err)
	}
	rr := readResult.(ReadFileResult)
	if rr.Content != "nested" {
		t.Errorf("content = %q, want %q", rr.Content, "nested")
	}
}

func TestWriteFile_Traversal(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolWriteFile, json.RawMessage(`{"path":"../outside.txt","content":"escape"}`))
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestListDir_Root(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "a.txt", "a")
	writeTestFile(t, dir, "b.txt", "b")
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)

	result, err := client.CallTool(context.Background(), toolListDir, json.RawMessage(`{"path":"."}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ld := result.(ListDirResult)
	if ld.Path != "." {
		t.Errorf("path = %q, want %q", ld.Path, ".")
	}

	found := make(map[string]bool)
	for _, e := range ld.Entries {
		found[e.Name] = true
	}

	if !found["a.txt"] {
		t.Error("expected a.txt in directory listing")
	}
	if !found["b.txt"] {
		t.Error("expected b.txt in directory listing")
	}
	if !found["sub"] {
		t.Error("expected sub directory in listing")
	}
}

func TestListDir_EmptyPath(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolListDir, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("listing root with empty path should work: %v", err)
	}
}

func TestFileInfo(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "info_test.txt", "test")

	result, err := client.CallTool(context.Background(), toolFileInfo, json.RawMessage(`{"path":"info_test.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fi := result.(FileInfoResult)
	if fi.Name != "info_test.txt" {
		t.Errorf("name = %q, want %q", fi.Name, "info_test.txt")
	}
	if fi.Size != 4 {
		t.Errorf("size = %d, want 4", fi.Size)
	}
	if fi.IsDir {
		t.Error("expected IsDir=false")
	}
	if fi.Path != "info_test.txt" {
		t.Errorf("path = %q, want %q", fi.Path, "info_test.txt")
	}
}

func TestFileInfo_Directory(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	result, err := client.CallTool(context.Background(), toolFileInfo, json.RawMessage(`{"path":"."}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fi := result.(FileInfoResult)
	if !fi.IsDir {
		t.Error("expected IsDir=true for root")
	}
}

func TestSearchFiles(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "main.go", "package main")
	writeTestFile(t, dir, "util.go", "package util")
	writeTestFile(t, dir, "README.md", "# readme")
	writeTestFile(t, dir, "sub/handler.go", "package sub")

	result, err := client.CallTool(context.Background(), toolSearchFiles, json.RawMessage(`{"pattern":"*.go","root":"."}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sr := result.(SearchFilesResult)
	if sr.Pattern != "*.go" {
		t.Errorf("pattern = %q, want %q", sr.Pattern, "*.go")
	}

	goFiles := make(map[string]bool)
	for _, f := range sr.Files {
		goFiles[f] = true
	}

	if !goFiles["main.go"] {
		t.Error("expected main.go in search results")
	}
	if !goFiles["util.go"] {
		t.Error("expected util.go in search results")
	}
	if goFiles["README.md"] {
		t.Error("did not expect README.md in search results")
	}
}

func TestSearchFiles_EmptyPattern(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolSearchFiles, json.RawMessage(`{"pattern":""}`))
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestReadMultiple(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "f1.txt", "file1")
	writeTestFile(t, dir, "f2.txt", "file2")

	result, err := client.CallTool(context.Background(), toolReadMultiple, json.RawMessage(`{"paths":["f1.txt","f2.txt"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mr := result.(ReadMultipleResult)
	if len(mr.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(mr.Files))
	}

	contentMap := make(map[string]string)
	for _, f := range mr.Files {
		contentMap[f.Path] = f.Content
	}

	if contentMap["f1.txt"] != "file1" {
		t.Errorf("f1.txt content = %q, want %q", contentMap["f1.txt"], "file1")
	}
	if contentMap["f2.txt"] != "file2" {
		t.Errorf("f2.txt content = %q, want %q", contentMap["f2.txt"], "file2")
	}
}

func TestReadMultiple_WithErrors(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "exists.txt", "exists")

	result, err := client.CallTool(context.Background(), toolReadMultiple, json.RawMessage(`{"paths":["exists.txt","nonexistent.txt"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mr := result.(ReadMultipleResult)
	if len(mr.Files) != 1 {
		t.Fatalf("expected 1 successful file read, got %d", len(mr.Files))
	}
	if len(mr.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(mr.Errors))
	}
	if mr.Errors[0].Path != "nonexistent.txt" {
		t.Errorf("error path = %q, want %q", mr.Errors[0].Path, "nonexistent.txt")
	}
}

func TestReadMultiple_EmptyPaths(t *testing.T) {
	client, _, cleanup := tempRoot(t)
	defer cleanup()

	_, err := client.CallTool(context.Background(), toolReadMultiple, json.RawMessage(`{"paths":[]}`))
	if err == nil {
		t.Fatal("expected error for empty paths array")
	}
}

func TestCallTool_JSONSerialization(t *testing.T) {
	client, dir, cleanup := tempRoot(t)
	defer cleanup()

	writeTestFile(t, dir, "serialize.txt", "serialization test")

	result, err := client.CallTool(context.Background(), toolReadFile, json.RawMessage(`{"path":"serialize.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var unmarshaled map[string]interface{}
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if unmarshaled["content"] != "serialization test" {
		t.Errorf("content = %v, want %q", unmarshaled["content"], "serialization test")
	}
}
