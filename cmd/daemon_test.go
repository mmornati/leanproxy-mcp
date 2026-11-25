package cmd

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
