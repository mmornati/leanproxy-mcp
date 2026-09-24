package migrate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fixture copies testdata/clients/<src> to <home>/<dst>.
type fixture struct {
	src, dst string
}

// wantServer is the part of a DiscoveredServer a scanner test checks.
type wantServer struct {
	name      string
	transport TransportType
	command   string
	args      []string
	env       []string
	url       string
	headers   map[string]string
	disabled  bool
}

func installFixtures(t *testing.T, home string, fixtures []fixture) {
	t.Helper()
	for _, f := range fixtures {
		data, err := os.ReadFile(filepath.Join("testdata", "clients", f.src))
		if err != nil {
			t.Fatalf("read fixture %s: %v", f.src, err)
		}
		dst := filepath.Join(home, filepath.FromSlash(f.dst))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func toWant(srv DiscoveredServer) wantServer {
	w := wantServer{name: srv.Name, transport: srv.Transport}
	if srv.Stdio != nil {
		w.command, w.args, w.env = srv.Stdio.Command, srv.Stdio.Args, srv.Stdio.Env
	}
	if srv.HTTP != nil {
		w.url, w.headers = srv.HTTP.URL, srv.HTTP.Headers
	}
	w.disabled = srv.Enabled != nil && !*srv.Enabled
	return w
}

func TestScanners_ClientFormats(t *testing.T) {
	tests := []struct {
		name     string
		scanner  func(home string) Scanner
		fixtures []fixture
		source   string
		want     []wantServer
		// wantErrs are substrings that must each appear in the scan error;
		// none means the scan must not fail.
		wantErrs []string
	}{
		{
			name:     "claude code ~/.claude.json with object env and remote entries",
			scanner:  func(string) Scanner { return &ClaudeScanner{} },
			fixtures: []fixture{{"claude/claude.json", ".claude.json"}},
			source:   "claude",
			want: []wantServer{
				{name: "filesystem", transport: TransportStdio, command: "npx",
					args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/Users/me/Projects/leanproxy-mcp"},
					env:  []string{"API_KEY=${API_KEY}", "LOG_LEVEL=debug"}},
				{name: "leanproxy", transport: TransportStdio, command: "leanproxy-mcp", args: []string{"server", "run", "--stdio"}},
				{name: "legacy-sse", transport: TransportSSE, url: "https://example.com/sse"},
				{name: "linear", transport: TransportHTTP, url: "https://mcp.linear.app/mcp",
					headers: map[string]string{"Authorization": "Bearer ${LINEAR_TOKEN}"}},
			},
		},
		{
			name:     "claude code legacy mcp_config.json with array env",
			scanner:  func(string) Scanner { return &ClaudeScanner{} },
			fixtures: []fixture{{"claude/mcp_config.json", ".config/claude/mcp_config.json"}},
			source:   "claude",
			want: []wantServer{
				{name: "memory", transport: TransportStdio, command: "/usr/local/bin/mcp-memory",
					args: []string{}, env: []string{"MEMORY_DIR=/tmp/memory"}},
			},
		},
		{
			name:    "claude desktop on macOS",
			scanner: func(string) Scanner { return &ClaudeDesktopScanner{goos: "darwin"} },
			fixtures: []fixture{{"claude-desktop/claude_desktop_config.json",
				"Library/Application Support/Claude/claude_desktop_config.json"}},
			source: "claude-desktop",
			want:   claudeDesktopWant,
		},
		{
			name:     "claude desktop on Linux",
			scanner:  func(string) Scanner { return &ClaudeDesktopScanner{goos: "linux"} },
			fixtures: []fixture{{"claude-desktop/claude_desktop_config.json", ".config/Claude/claude_desktop_config.json"}},
			source:   "claude-desktop",
			want:     claudeDesktopWant,
		},
		{
			name: "claude desktop on Windows (APPDATA)",
			scanner: func(home string) Scanner {
				t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
				return &ClaudeDesktopScanner{goos: "windows"}
			},
			fixtures: []fixture{{"claude-desktop/claude_desktop_config.json", "AppData/Roaming/Claude/claude_desktop_config.json"}},
			source:   "claude-desktop",
			want:     claudeDesktopWant,
		},
		{
			name:     "cursor mcpServers with url entries and ${env:} references",
			scanner:  func(string) Scanner { return &CursorScanner{} },
			fixtures: []fixture{{"cursor/mcp.json", ".cursor/mcp.json"}},
			source:   "cursor",
			want: []wantServer{
				{name: "Home Assistant", transport: TransportStdio, command: "uvx", args: []string{"mcp-proxy"},
					env: []string{"API_ACCESS_TOKEN=${HA_TOKEN}", "SSE_URL=http://homeassistant.local:8123/mcp_server/sse"}},
				{name: "events", transport: TransportSSE, url: "http://localhost:9000/sse"},
				{name: "gateway", transport: TransportStdio, command: "npx",
					args: []string{"-y", "leanproxy-mcp@latest", "server", "run", "--stdio"}},
				{name: "github", transport: TransportHTTP, url: "https://api.githubcopilot.com/mcp/",
					headers: map[string]string{"Authorization": "Bearer ${env:GITHUB_TOKEN}"}},
			},
		},
		{
			name: "vscode user mcp.json (JSONC, inputs)",
			scanner: func(home string) Scanner {
				return &VSCodeScanner{goos: "linux", workspaceDir: filepath.Join(home, "ws")}
			},
			fixtures: []fixture{{"vscode/mcp.json", ".config/Code/User/mcp.json"}},
			source:   "vscode",
			want: []wantServer{
				{name: "fetch", transport: TransportHTTP, url: "https://example.com/mcp", headers: map[string]string{"X-Api-Key": "abc"}},
				{name: "perplexity", transport: TransportStdio, command: "npx", args: []string{"-y", "server-perplexity-ask"},
					env: []string{"PERPLEXITY_API_KEY=${input:perplexity-key}"}},
			},
		},
		{
			name: "vscode settings.json nested mcp.servers on macOS",
			scanner: func(home string) Scanner {
				return &VSCodeScanner{goos: "darwin", workspaceDir: filepath.Join(home, "ws")}
			},
			fixtures: []fixture{{"vscode/settings.json", "Library/Application Support/Code/User/settings.json"}},
			source:   "vscode",
			want: []wantServer{
				{name: "time", transport: TransportStdio, command: "uvx", args: []string{"mcp-server-time"}},
			},
		},
		{
			name: "vscodium settings.json flat mcp.servers key",
			scanner: func(home string) Scanner {
				return &VSCodeScanner{goos: "linux", workspaceDir: filepath.Join(home, "ws")}
			},
			fixtures: []fixture{{"vscode/settings_flat.json", ".config/VSCodium/User/settings.json"}},
			source:   "vscode",
			want: []wantServer{
				{name: "sqlite", transport: TransportStdio, command: "uvx", args: []string{"mcp-server-sqlite", "--db-path", "/tmp/test.db"}},
			},
		},
		{
			name: "vscode workspace .vscode/mcp.json",
			scanner: func(home string) Scanner {
				return &VSCodeScanner{goos: "linux", workspaceDir: filepath.Join(home, "ws")}
			},
			fixtures: []fixture{{"vscode/workspace_mcp.json", "ws/.vscode/mcp.json"}},
			source:   "vscode",
			want: []wantServer{
				{name: "playwright", transport: TransportStdio, command: "npx", args: []string{"@playwright/mcp@latest"}},
			},
		},
		{
			name:     "opencode local and remote entries",
			scanner:  func(string) Scanner { return &OpenCodeScanner{} },
			fixtures: []fixture{{"opencode/opencode.json", ".config/opencode/opencode.json"}},
			source:   "opencode",
			want: []wantServer{
				{name: "context7", transport: TransportHTTP, url: "https://mcp.context7.com/mcp",
					headers: map[string]string{"CONTEXT7_API_KEY": "key"}, disabled: true},
				{name: "garmin", transport: TransportStdio, command: "uvx", args: []string{"garmin-mcp"},
					env: []string{"GARMIN_EMAIL=me@example.com"}},
			},
		},
		{
			name:     "generic ~/.config/mcp.json",
			scanner:  func(string) Scanner { return &GenericScanner{} },
			fixtures: []fixture{{"generic/mcp.json", ".config/mcp.json"}},
			source:   "generic",
			want: []wantServer{
				{name: "echo", transport: TransportStdio, command: "/usr/bin/echo-mcp", args: []string{"--verbose"}},
			},
		},
		{
			name:     "bad entries are reported and skipped, good ones kept",
			scanner:  func(string) Scanner { return &CursorScanner{} },
			fixtures: []fixture{{"malformed/mcp.json", ".cursor/mcp.json"}},
			source:   "cursor",
			want: []wantServer{
				{name: "good", transport: TransportStdio, command: "node", args: []string{"server.js"}},
			},
			wantErrs: []string{`"bad-env"`, `"empty"`, `"weird-type"`},
		},
		{
			name:    "a malformed file is reported, other files still scanned",
			scanner: func(string) Scanner { return &ClaudeScanner{} },
			fixtures: []fixture{
				{"malformed/truncated.json", ".claude.json"},
				{"claude/mcp_config.json", ".config/claude/mcp_config.json"},
			},
			source: "claude",
			want: []wantServer{
				{name: "memory", transport: TransportStdio, command: "/usr/local/bin/mcp-memory",
					args: []string{}, env: []string{"MEMORY_DIR=/tmp/memory"}},
			},
			wantErrs: []string{".claude.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			installFixtures(t, home, tt.fixtures)

			servers, err := tt.scanner(home).Scan(context.Background())
			if len(tt.wantErrs) == 0 && err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			for _, want := range tt.wantErrs {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("Scan() error = %v, want it to mention %s", err, want)
				}
			}

			got := make([]wantServer, len(servers))
			for i, srv := range servers {
				if srv.Source != tt.source {
					t.Errorf("server %s: source = %q, want %q", srv.Name, srv.Source, tt.source)
				}
				got[i] = toWant(srv)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Scan() servers =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

var claudeDesktopWant = []wantServer{
	{name: "github", transport: TransportStdio, command: "docker",
		args: []string{"run", "-i", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "ghcr.io/github/github-mcp-server"},
		env:  []string{"GITHUB_PERSONAL_ACCESS_TOKEN=ghp_example"}},
	{name: "leanproxy", transport: TransportStdio, command: "/opt/homebrew/bin/leanproxy-mcp", args: []string{"server", "run", "--stdio"}},
}

func TestIsLeanProxyServer(t *testing.T) {
	stdio := func(name, cmd string, args ...string) DiscoveredServer {
		return DiscoveredServer{Name: name, Transport: TransportStdio, Stdio: &StdioConfig{Command: cmd, Args: args}}
	}
	tests := []struct {
		name string
		srv  DiscoveredServer
		want bool
	}{
		{"binary on PATH", stdio("proxy", "leanproxy-mcp", "server", "run", "--stdio"), true},
		{"absolute path", stdio("proxy", "/opt/homebrew/bin/leanproxy-mcp", "server", "run"), true},
		{"windows exe", stdio("proxy", `C:\Tools\leanproxy-mcp.exe`, "server", "run"), true},
		{"npx with version", stdio("proxy", "npx", "-y", "leanproxy-mcp@latest", "server", "run"), true},
		{"go run module", stdio("proxy", "go", "run", "github.com/mmornati/leanproxy-mcp@latest", "server", "run"), true},
		{"env launcher", stdio("proxy", "/usr/bin/env", "PATH=/x", "leanproxy-mcp", "server", "run"), true},
		{"named leanproxy (http gateway)", DiscoveredServer{Name: "leanproxy", Transport: TransportHTTP,
			HTTP: &HTTPConfig{URL: "http://127.0.0.1:8765/mcp"}}, true},
		{"directory argument named like the repo", stdio("filesystem", "npx", "-y",
			"@modelcontextprotocol/server-filesystem", "/Users/me/Projects/leanproxy-mcp"), false},
		{"other server", stdio("github", "docker", "run", "-i", "ghcr.io/github/github-mcp-server"), false},
		{"remote server", DiscoveredServer{Name: "linear", Transport: TransportHTTP,
			HTTP: &HTTPConfig{URL: "https://mcp.linear.app/mcp"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLeanProxyServer(tt.srv); got != tt.want {
				t.Errorf("IsLeanProxyServer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStripJSONC(t *testing.T) {
	tests := []struct{ in, want string }{
		{`{"a": 1}`, `{"a": 1}`},
		{"{\"a\": 1, // c\n}", "{\"a\": 1 \n}"},
		{`{"a": [1, 2,], /* x, */ "b": "http://x//y"}`, `{"a": [1, 2],  "b": "http://x//y"}`},
		{`{"a": "quote \" // not a comment",}`, `{"a": "quote \" // not a comment"}`},
	}
	for _, tt := range tests {
		if got := string(stripJSONC([]byte(tt.in))); got != tt.want {
			t.Errorf("stripJSONC(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestMigrator_ScanAndImport_AllClients runs the whole scan with every
// client present under a fake HOME, then imports the result.
func TestMigrator_ScanAndImport_AllClients(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFixtures(t, home, []fixture{
		{"claude/claude.json", ".claude.json"},
		{"claude-desktop/claude_desktop_config.json", ".config/Claude/claude_desktop_config.json"},
		{"cursor/mcp.json", ".cursor/mcp.json"},
		{"vscode/mcp.json", ".config/Code/User/mcp.json"},
		{"opencode/opencode.json", ".config/opencode/opencode.json"},
	})

	m := &Migrator{scanners: []Scanner{
		&OpenCodeScanner{},
		&ClaudeScanner{},
		&ClaudeDesktopScanner{goos: "linux"},
		&VSCodeScanner{goos: "linux", workspaceDir: filepath.Join(home, "ws")},
		&CursorScanner{},
		&GenericScanner{},
	}}
	result, err := m.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("Scan() warnings = %v, want none", result.Warnings)
	}

	var skipped []string
	for _, srv := range result.Skipped {
		skipped = append(skipped, srv.Source+"/"+srv.Name)
	}
	wantSkipped := []string{"claude/leanproxy", "claude-desktop/leanproxy", "cursor/gateway"}
	if !reflect.DeepEqual(skipped, wantSkipped) {
		t.Errorf("Skipped = %v, want %v", skipped, wantSkipped)
	}

	summary := m.Summarize(result.Servers)
	if summary.OpenCodeCount != 2 || summary.ClaudeCount != 3 || summary.ClaudeDesktopCount != 1 ||
		summary.VSCodeCount != 2 || summary.CursorCount != 3 || summary.GenericCount != 0 {
		t.Errorf("Summarize() = %+v", summary)
	}

	target := filepath.Join(home, "leanproxy_servers.yaml")
	if _, err := m.Import(context.Background(), result.Servers, target, true); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("imported YAML does not parse: %v", err)
	}
	byName := map[string]*ServerConfig{}
	for _, srv := range cfg.Servers {
		byName[srv.Name] = srv
	}
	if linear := byName["linear"]; linear == nil || linear.Transport != TransportHTTP || linear.HTTP == nil ||
		linear.HTTP.URL != "https://mcp.linear.app/mcp" || linear.Stdio != nil {
		t.Errorf("linear = %+v, want an http server with http.url", linear)
	}
	if sse := byName["events"]; sse == nil || sse.Transport != TransportSSE || sse.HTTP == nil ||
		sse.HTTP.URL != "http://localhost:9000/sse" {
		t.Errorf("events = %+v, want an sse server with http.url", sse)
	}
	for _, name := range []string{"leanproxy", "gateway"} {
		if byName[name] != nil {
			t.Errorf("%s was imported; it runs leanproxy-mcp itself", name)
		}
	}
}
