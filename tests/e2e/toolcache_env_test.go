package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the persistent tool cache (pkg/toolstore) of every proxy
// binary these tests start at a scratch directory: the children inherit
// LEANPROXY_TOOLCACHE_DIR through os.Environ(), so no e2e run ever writes
// to the real ~/.config/leanproxy/toolcache.
//
// It also sets LEANPROXY_SERVE_TOKEN so every `serve` child requires the
// e2eServeToken handshake (see serveAuthLine) and never writes a token file
// under $HOME.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "leanproxy-e2e-toolcache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create tool cache dir:", err)
		os.Exit(1)
	}
	if err := os.Setenv("LEANPROXY_TOOLCACHE_DIR", dir); err != nil {
		fmt.Fprintln(os.Stderr, "set LEANPROXY_TOOLCACHE_DIR:", err)
		os.Exit(1)
	}
	// Tool pinning (#310) is on (warn) by default: keep every proxy these
	// tests start away from ~/.config/leanproxy/pins.json.
	pinsDir, err := os.MkdirTemp("", "leanproxy-e2e-pins-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create pins dir:", err)
		os.Exit(1)
	}
	if err := os.Setenv("LEANPROXY_PINS_FILE", filepath.Join(pinsDir, "pins.json")); err != nil {
		fmt.Fprintln(os.Stderr, "set LEANPROXY_PINS_FILE:", err)
		os.Exit(1)
	}
	// Every `serve` these tests start authenticates clients with this
	// token (#298) instead of creating ~/.config/leanproxy/serve.token.
	if err := os.Setenv("LEANPROXY_SERVE_TOKEN", e2eServeToken); err != nil {
		fmt.Fprintln(os.Stderr, "set LEANPROXY_SERVE_TOKEN:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(pinsDir)
	os.Exit(code)
}

// e2eServeToken is the `serve` auth token of every e2e run.
const e2eServeToken = "e2e-serve-token-0123456789abcdef"

// serveAuthLine is the handshake a `serve` client must send first (#298).
func serveAuthLine() []byte {
	return []byte(`{"jsonrpc":"2.0","method":"auth","params":{"token":"` + e2eServeToken + `"}}` + "\n")
}
