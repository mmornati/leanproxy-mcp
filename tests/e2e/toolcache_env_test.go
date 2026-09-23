package e2e

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the persistent tool cache (pkg/toolstore) of every proxy
// binary these tests start at a scratch directory: the children inherit
// LEANPROXY_TOOLCACHE_DIR through os.Environ(), so no e2e run ever writes
// to the real ~/.config/leanproxy/toolcache.
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
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
