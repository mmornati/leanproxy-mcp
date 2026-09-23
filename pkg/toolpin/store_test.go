package toolpin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestStore_MissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "pins.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Existed() || len(s.Current().Servers) != 0 {
		t.Fatal("a missing file must load as an empty pin set")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("opening must not create the file")
	}
}

func TestStore_CorruptFileIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("a corrupt file must fail to open")
	}
}

func TestStore_TightensPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions")
	}
	path := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"servers":{}}`), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil { // #nosec G302 -- test fixture
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestStore_UpdateIsAtomicAndMerges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pins.json")
	a, _ := OpenStore(path)
	b, _ := OpenStore(path)
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		for j := 0; j < 10; j++ {
			wg.Add(1)
			go func(s *Store, name string) {
				defer wg.Done()
				_ = s.Update(func(f *File) error {
					f.Servers[name] = &ServerPins{Tools: map[string]*ToolPin{}}
					return nil
				})
			}(s, string(rune('a'+i))+strings.Repeat("x", j))
		}
	}
	wg.Wait()
	// Every write left a complete, parseable file and no temp files.
	if err := a.Reload(); err != nil {
		t.Fatalf("pin file corrupt after concurrent writes: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestStore_SingleStoreNeverLosesUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	s, _ := OpenStore(path)
	var wg sync.WaitGroup
	for j := 0; j < 20; j++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = s.Update(func(f *File) error {
				f.Servers[name] = &ServerPins{Tools: map[string]*ToolPin{}}
				return nil
			})
		}(strings.Repeat("x", j+1))
	}
	wg.Wait()
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Current().Servers); n != 20 {
		t.Fatalf("%d servers, want 20", n)
	}
}

func TestStore_RejectsNewerFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"servers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("a newer format must be refused")
	}
}

func TestConfig(t *testing.T) {
	var nilCfg *Config
	if nilCfg.EffectiveMode() != ModeWarn || nilCfg.Validate() != nil {
		t.Fatal("nil config must default to warn")
	}
	for _, bad := range []*Config{{Mode: "strict"}, {MaxDescriptionChars: -1}, {AllowedDomains: []string{"https://x.io"}}} {
		if bad.Validate() == nil {
			t.Errorf("%+v must be invalid", bad)
		}
	}
	if (&Config{Mode: "BLOCK"}).EffectiveMode() != ModeBlock {
		t.Fatal("mode is case-insensitive")
	}
	t.Setenv(PinsFileEnv, "/tmp/x/pins.json")
	if p, _ := (&Config{Path: "/elsewhere.json"}).ResolvePath(); p != "/tmp/x/pins.json" {
		t.Fatalf("env override ignored: %s", p)
	}
	t.Setenv(PinsFileEnv, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if p, _ := (&Config{Path: "~/pins.json"}).ResolvePath(); p != filepath.Join(home, "pins.json") {
		t.Fatalf("~ not expanded: %s", p)
	}
	if p, _ := nilCfg.ResolvePath(); p != filepath.Join(home, ".config", "leanproxy", "pins.json") {
		t.Fatalf("default path %s", p)
	}
}
