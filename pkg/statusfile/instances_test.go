package statusfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeStatus(t *testing.T, dir, name string, info StatusInfo) string {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// deadPID is a pid no process has: the largest a 32-bit pid can be.
const deadPID = 1<<31 - 1

func TestReadAllStatusListsLiveProcessesGatewayFirst(t *testing.T) {
	dir := t.TempDir()
	me := os.Getpid()
	parent := os.Getppid()
	now := time.Now()

	writeStatus(t, dir, instanceFileName(me), StatusInfo{PID: me, StartedAt: now, ListenAddr: "stdio"})
	writeStatus(t, dir, instanceFileName(parent), StatusInfo{PID: parent, StartedAt: now.Add(-time.Hour),
		ListenAddr: "http://127.0.0.1:8765/mcp", HTTP: &HTTPFrontendStatus{URL: "http://127.0.0.1:8765/mcp"}})
	stale := writeStatus(t, dir, instanceFileName(deadPID), StatusInfo{PID: deadPID, StartedAt: now})
	// A legacy file describing a process that also has an instance file.
	writeStatus(t, dir, legacyFile, StatusInfo{PID: me, StartedAt: now, ListenAddr: "stdio"})
	// Files that are not status files are ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, instancePrefix+"junk.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	all, err := readAllStatusIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d instances, want 2: %+v", len(all), all)
	}
	if all[0].PID != parent || all[0].HTTP == nil || all[1].PID != me {
		t.Fatalf("order = %d, %d; want the HTTP gateway first", all[0].PID, all[1].PID)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the file of a dead process must be removed")
	}
}

func TestReadAllStatusMissingDir(t *testing.T) {
	all, err := readAllStatusIn(filepath.Join(t.TempDir(), "nope"))
	if err != nil || all != nil {
		t.Fatalf("missing dir = %v, %v", all, err)
	}
}

func TestNewFileStatusStoreWritesOneFilePerProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := NewFileStatusStore("stdio", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(store.GetFilePath()), instanceFileName(os.Getpid()); got != want {
		t.Fatalf("status file = %s, want %s", got, want)
	}
	store.SetClients(2)
	store.UpdateServers([]ServerStatus{{Name: "a", Status: "running", Transport: "stdio"}})
	info, err := ReadCurrentStatus()
	if err != nil || info == nil {
		t.Fatalf("ReadCurrentStatus = %v, %v", info, err)
	}
	if info.PID != os.Getpid() || info.Clients != 2 || len(info.Servers) != 1 || info.Servers[0].Transport != "stdio" {
		t.Fatalf("info = %+v", info)
	}
	store.RemoveFile()
	if info, _ := ReadCurrentStatus(); info != nil {
		t.Fatalf("after RemoveFile: %+v", info)
	}
}
