package cmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/dashboard"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

func newTestManager(t *testing.T, config string) (*serverManager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if config != "" {
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.DiscardHandler)
	p := pool.NewUnifiedPool(pool.NewStdioPool(5, time.Minute, logger), pool.NewHTTPClientPool(logger), pool.NewSSEPool(logger), logger)
	t.Cleanup(func() { _ = p.Close() })
	return newServerManager(path, p, logger), path
}

// Remote servers on a closed port: they join the pool without needing a
// real upstream (the connection fails in the background).
const twoRemoteServers = `version: "1.0"
servers:
  - name: alpha
    enabled: true
    transport: http
    http:
      url: http://127.0.0.1:1/alpha
  - name: beta
    enabled: false
    transport: http
    http:
      url: http://127.0.0.1:1/beta
`

func serverNames(m *serverManager) map[string]dashboard.ServerStatus {
	out := make(map[string]dashboard.ServerStatus)
	for _, s := range m.Servers() {
		out[s.Name] = s
	}
	return out
}

func inPool(m *serverManager, name string) bool {
	_, ok := m.pool.Snapshot(name)
	return ok
}

func TestServerManagerAppliesAndReloadsTheServerList(t *testing.T) {
	m, path := newTestManager(t, twoRemoteServers)
	ctx := context.Background()
	cfg, err := migrate.LoadConfig(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if started := m.start(ctx, cfg); started != 1 {
		t.Fatalf("started %d servers, want 1 (beta is disabled)", started)
	}
	if !inPool(m, "alpha") || inPool(m, "beta") {
		t.Fatal("only the enabled server must be in the pool")
	}
	got := serverNames(m)
	if got["beta"].Health != dashboard.HealthDisabled || got["alpha"].Target != "http://127.0.0.1:1/alpha" {
		t.Fatalf("servers = %+v", got)
	}

	// Enabling beta writes the config and starts it, without touching alpha.
	if err := m.SetServerEnabled(ctx, "beta", true); err != nil {
		t.Fatal(err)
	}
	if !inPool(m, "alpha") || !inPool(m, "beta") {
		t.Fatal("beta must be started by enabling it")
	}
	data, _ := os.ReadFile(path)
	if strings.Count(string(data), "enabled: true") != 2 {
		t.Fatalf("config not updated:\n%s", data)
	}

	// A changed entry is restarted; a removed one is stopped.
	changed := strings.Replace(string(data), "127.0.0.1:1/alpha", "127.0.0.1:1/alpha-v2", 1)
	changed = changed[:strings.Index(changed, "    - name: beta")]
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if inPool(m, "beta") {
		t.Fatal("a server removed from the config must leave the pool")
	}
	if got := serverNames(m)["alpha"].Target; got != "http://127.0.0.1:1/alpha-v2" {
		t.Fatalf("alpha target = %q, want the new URL", got)
	}

	// A broken file keeps the running servers and reports why.
	if err := os.WriteFile(path, []byte("servers: [this is: not: yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(ctx); err == nil {
		t.Fatal("a broken config must fail the reload")
	}
	if !inPool(m, "alpha") || m.Overview().LastReloadError == "" {
		t.Fatal("a failed reload must keep the servers and record the error")
	}
}

func TestServerManagerAddAndRemove(t *testing.T) {
	m, path := newTestManager(t, "")
	ctx := context.Background()
	spec := dashboard.ServerSpec{Name: "remote", Transport: "http", URL: "http://127.0.0.1:1/mcp",
		Headers: map[string]string{"Authorization": "Bearer abc"}}
	if err := m.AddServer(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if !inPool(m, "remote") {
		t.Fatal("an added server must be started")
	}
	if err := m.AddServer(ctx, spec); err == nil {
		t.Fatal("adding the same name twice must fail")
	}
	// The header is stored in the config but never shown as the target.
	if got := serverNames(m)["remote"].Target; strings.Contains(got, "abc") {
		t.Fatalf("target leaks a credential: %q", got)
	}
	if err := m.RemoveServer(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	if inPool(m, "remote") {
		t.Fatal("a removed server must be stopped")
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "remote") {
		t.Fatalf("config still lists the server:\n%s", data)
	}
	if err := m.RemoveServer(ctx, "remote"); err == nil {
		t.Fatal("removing an unknown server must fail")
	}
}

func TestServerManagerWatchPicksUpFileChanges(t *testing.T) {
	m, path := newTestManager(t, `version: "1.0"`+"\nservers: []\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg, err := migrate.LoadConfig(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	m.start(ctx, cfg)

	// What `server add` in another terminal does: rewrite the file.
	if err := os.WriteFile(path, []byte(twoRemoteServers), 0o600); err != nil {
		t.Fatal(err)
	}
	// One watcher pass, without waiting for its ticker.
	m.mu.Lock()
	known := m.stamp
	m.mu.Unlock()
	if stampOf(path) == known {
		t.Fatal("a rewritten file must change its stamp")
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if !inPool(m, "alpha") {
		t.Fatal("the reload must start the new server")
	}
	m.mu.Lock()
	settled := m.stamp == stampOf(path)
	m.mu.Unlock()
	if !settled {
		t.Fatal("a reload must record the file's stamp, or the watcher reloads forever")
	}
}

func TestServerConfigFromSpec(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := serverConfigFromSpec(dashboard.ServerSpec{Name: "local", Command: exe, Args: []string{"-x"}, Env: []string{"A=1"}})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Transport != registry.TransportStdio || srv.Stdio.Command != exe || srv.Stdio.CWD != filepath.Dir(exe) || srv.HTTP != nil {
		t.Fatalf("stdio config = %+v / %+v", srv, srv.Stdio)
	}
	srv, err = serverConfigFromSpec(dashboard.ServerSpec{Name: "r", Transport: "sse", URL: "https://example.com/sse"})
	if err != nil || srv.Transport != registry.TransportSSE || srv.HTTP.URL != "https://example.com/sse" || srv.Stdio != nil {
		t.Fatalf("sse config = %+v, %v", srv, err)
	}

	bad := map[string]dashboard.ServerSpec{
		"empty name":       {Command: exe},
		"name with slash":  {Name: "a/b", Command: exe},
		"name with spaces": {Name: "a b", Command: exe},
		"no command":       {Name: "x"},
		"unknown command":  {Name: "x", Command: "leanproxy-no-such-command-xyz"},
		"no url":           {Name: "x", Transport: "http"},
		"file url":         {Name: "x", Transport: "http", URL: "file:///etc/passwd"},
		"bad transport":    {Name: "x", Transport: "smoke-signal", Command: exe},
	}
	for name, spec := range bad {
		if _, err := serverConfigFromSpec(spec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestClassifyServer(t *testing.T) {
	cases := []struct {
		name   string
		in     dashboard.ServerStatus
		health string
		want   string
	}{
		{"auth", dashboard.ServerStatus{State: "error", LastError: "initialize: transport error: authorization required"}, dashboard.HealthDown, "Authentication refused"},
		{"crash", dashboard.ServerStatus{State: "error", LastError: "exit status 1\nstack"}, dashboard.HealthDown, "exit status 1"},
		{"idle stop", dashboard.ServerStatus{State: "stopped"}, dashboard.HealthIdle, "starts on the next call"},
		{"starting", dashboard.ServerStatus{State: "idle"}, dashboard.HealthWarn, "handshake"},
		{"refresh", dashboard.ServerStatus{State: "idle", Initialized: true, RefreshFailures: 2}, dashboard.HealthWarn, "tool list"},
		{"pins", dashboard.ServerStatus{State: "idle", Initialized: true, PendingPins: 3}, dashboard.HealthWarn, "3 tool definitions"},
		{"busy", dashboard.ServerStatus{State: "busy", Initialized: true, InFlight: 2}, dashboard.HealthOK, "2 calls in flight"},
		{"up", dashboard.ServerStatus{State: "running", Initialized: true}, dashboard.HealthOK, "Up"},
	}
	for _, c := range cases {
		st := c.in
		classifyServer(&st)
		if st.Health != c.health || !strings.Contains(st.Summary, c.want) {
			t.Errorf("%s: %s %q, want %s containing %q", c.name, st.Health, st.Summary, c.health, c.want)
		}
	}
}

func TestServerTargetHidesCredentials(t *testing.T) {
	got := serverTarget(&migrate.ServerConfig{HTTP: &migrate.HTTPConfig{URL: "https://user:pass@example.com/mcp?key=secret#frag"}})
	if got != "https://example.com/mcp" {
		t.Fatalf("target = %q", got)
	}
	got = serverTarget(&migrate.ServerConfig{Stdio: &migrate.StdioConfig{Command: "npx", Args: []string{"--token", "secret"}}})
	if got != "npx" {
		t.Fatalf("target = %q", got)
	}
}
