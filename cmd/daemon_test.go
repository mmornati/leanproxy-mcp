package cmd

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleDaemonSpec() daemonSpec {
	return daemonSpec{
		Executable:    "/opt/lean proxy/bin/leanproxy-mcp",
		HTTPAddr:      "127.0.0.1:8765",
		DashAddr:      "127.0.0.1:9090",
		TokenFile:     "/home/u/.config/leanproxy/serve.token",
		DashTokenFile: "/home/u/.config/leanproxy/dashboard.token",
		LogFile:       "/home/u/.leanproxy/logs/daemon.log",
		LogLevel:      "info",
		Path:          "/usr/bin:/opt/a&b/bin",
		Home:          "/home/u",
	}
}

func TestDaemonArgs(t *testing.T) {
	d := sampleDaemonSpec()
	got := strings.Join(d.args(), " ")
	want := "server run --http 127.0.0.1:8765 --log-file /home/u/.leanproxy/logs/daemon.log --log-level info" +
		" --dashboard-bind 127.0.0.1:9090 --dashboard-token-file /home/u/.config/leanproxy/dashboard.token"
	if got != want {
		t.Fatalf("args =\n%s\nwant\n%s", got, want)
	}
	// The dashboard has its own token, not the one MCP clients carry.
	if d.DashTokenFile == d.TokenFile {
		t.Fatal("the dashboard reuses the MCP token")
	}
	// The token itself never appears on the command line.
	if strings.Contains(got, "--dashboard-token ") || strings.Contains(got, "--http-token") {
		t.Fatal("the service command line carries a token")
	}
	d.DashAddr, d.ConfigPath = "", "/etc/lp.yaml"
	got = strings.Join(d.args(), " ")
	if strings.Contains(got, "dashboard") || !strings.HasSuffix(got, "--config /etc/lp.yaml") {
		t.Fatalf("args without dashboard = %s", got)
	}
}

func TestLaunchdPlistIsWellFormed(t *testing.T) {
	plist := launchdPlist(sampleDaemonSpec())
	dec := xml.NewDecoder(strings.NewReader(plist))
	dec.Strict = true
	for {
		if _, err := dec.Token(); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("plist is not well-formed XML: %v\n%s", err, plist)
		}
	}
	for _, want := range []string{
		"<string>io.leanproxy.mcp</string>",
		"<string>/opt/lean proxy/bin/leanproxy-mcp</string>",
		"<string>/usr/bin:/opt/a&amp;b/bin</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %s", want)
		}
	}
}

func TestSystemdUnitQuotesItsWords(t *testing.T) {
	unit := systemdUnit(sampleDaemonSpec())
	if !strings.Contains(unit, `ExecStart="/opt/lean proxy/bin/leanproxy-mcp" server run --http 127.0.0.1:8765`) {
		t.Fatalf("ExecStart not quoted:\n%s", unit)
	}
	if !strings.Contains(unit, "Restart=on-failure") || !strings.Contains(unit, "WantedBy=default.target") {
		t.Fatalf("unit:\n%s", unit)
	}
	if got := systemdQuote(`a "b" $c 100%`); got != `"a \"b\" $$c 100%%"` {
		t.Fatalf("systemdQuote = %s", got)
	}
}

func TestCheckLoopbackAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:8765", "localhost:1", "[::1]:9090"} {
		if err := checkLoopbackAddr("http", ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:8765", "192.168.1.4:80", "8765", "127.0.0.1", "example.com:80"} {
		if err := checkLoopbackAddr("http", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestClientSnippets(t *testing.T) {
	out := clientSnippets("http://127.0.0.1:8765/mcp", "<token>")
	for _, want := range []string{
		"claude mcp add --transport http --scope user leanproxy http://127.0.0.1:8765/mcp",
		`"Authorization: Bearer <token>"`,
		"[mcp_servers.leanproxy]",
		`"type": "http"`,
		`"command": "leanproxy-mcp", "args": ["connect"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("snippets lack %q", want)
		}
	}
}

func TestConnectToken(t *testing.T) {
	home := t.TempDir()
	// The environment wins; a missing file means no authentication and is
	// never created.
	if tok, err := connectToken("from-env", "", home); err != nil || tok != "from-env" {
		t.Fatalf("env token = %q, %v", tok, err)
	}
	if tok, err := connectToken("", "", home); err != nil || tok != "" {
		t.Fatalf("missing file = %q, %v", tok, err)
	}
	if _, err := os.Stat(serveTokenPath(home)); !os.IsNotExist(err) {
		t.Fatal("connect must not create the token file")
	}
	path := filepath.Join(home, "tok")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := connectToken("", path, home); err != nil || tok != "from-file" {
		t.Fatalf("file token = %q, %v", tok, err)
	}
	if got := connectURL("http://example.test/mcp"); got != "http://example.test/mcp" {
		t.Fatalf("--url ignored: %s", got)
	}
}

// A binary reached through a symlink (how Homebrew exposes a versioned
// install) is recorded by that symlink, which survives upgrades.
func TestStableExecutable(t *testing.T) {
	dir := t.TempDir()
	cellar := filepath.Join(dir, "Cellar", "leanproxy-mcp", "1.1", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(cellar, "leanproxy-mcp")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "leanproxy-mcp")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	other := filepath.Join(bin, "other")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherLink := filepath.Join(bin, "other-link")
	if err := os.Symlink(other, otherLink); err != nil {
		t.Fatal(err)
	}

	if got := stableExecutable(real, link); got != link {
		t.Errorf("invoked through the symlink: got %s, want the symlink %s", got, link)
	}
	if got := stableExecutable(real, real); got != real {
		t.Errorf("invoked directly: got %s, want %s", got, real)
	}
	// A symlink to some other binary, or a name that cannot be found, is
	// never used.
	if got := stableExecutable(real, otherLink); got != real {
		t.Errorf("a symlink to another binary was used: %s", got)
	}
	if got := stableExecutable(real, "leanproxy-no-such-command-xyz"); got != real {
		t.Errorf("an unknown name was used: %s", got)
	}
	// A bare name is resolved through PATH.
	t.Setenv("PATH", bin)
	if got := stableExecutable(real, "leanproxy-mcp"); got != link {
		t.Errorf("bare name: got %s, want %s", got, link)
	}
}

func TestRetryAndWaitUntil(t *testing.T) {
	calls := 0
	err := retry(5, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("retry: err=%v calls=%d, want success on the third call", err, calls)
	}
	calls = 0
	if err := retry(2, time.Millisecond, func() error { calls++; return errors.New("always") }); err == nil || calls != 2 {
		t.Fatalf("retry must give up after its attempts: err=%v calls=%d", err, calls)
	}

	n := 0
	if !waitUntil(time.Second, time.Millisecond, func() bool { n++; return n >= 4 }) {
		t.Fatal("waitUntil must return true once the condition holds")
	}
	if waitUntil(10*time.Millisecond, time.Millisecond, func() bool { return false }) {
		t.Fatal("waitUntil must time out")
	}
}
