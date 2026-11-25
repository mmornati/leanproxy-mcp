package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingFileRotatesAtMaxBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	r, err := OpenRotating(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	line := bytes.Repeat([]byte("x"), 64<<10)
	for i := 0; i < 40; i++ { // 2.5 MiB in all
		if _, err := r.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	cur, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("no rotated file: %v", err)
	}
	// Each file stays within maxBytes plus one check interval.
	limit := int64(1<<20 + rotateCheckBytes + len(line))
	if cur.Size() > limit || old.Size() > limit {
		t.Fatalf("files too big: current %d, rotated %d, limit %d", cur.Size(), old.Size(), limit)
	}
}

func TestRotatingFileRotatesOversizedFileOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("y"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRotating(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("current file not restarted: %v %v", info, err)
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != 4096 {
		t.Fatalf("old content not kept in .1: %v %v", info, err)
	}
}

func TestRotatingFileFollowsAnotherProcessRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	a, err := OpenRotating(path, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	// Another process rotates the file.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// After the next check, a writes to the new file, not to .1.
	a.mu.Lock()
	a.sinceCheck = rotateCheckBytes
	a.mu.Unlock()
	if _, err := a.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "after\n" {
		t.Fatalf("new file holds %q, want %q", got, "after\n")
	}
}
