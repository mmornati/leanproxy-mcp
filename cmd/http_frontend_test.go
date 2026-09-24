package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
)

func TestCheckRunModeFlags(t *testing.T) {
	tests := []struct {
		name      string
		stdio     bool
		http      string
		token     string
		noAuth    bool
		lists     bool
		wantError string
	}{
		{name: "stdio", stdio: true},
		{name: "http", http: "127.0.0.1:8765", token: "x", noAuth: false, lists: true},
		{name: "neither", wantError: "either --stdio or --http"},
		{name: "both", stdio: true, http: "127.0.0.1:8765", wantError: "mutually exclusive"},
		{name: "http flags with stdio", stdio: true, noAuth: true, wantError: "only apply to --http"},
		{name: "http lists with stdio", stdio: true, lists: true, wantError: "only apply to --http"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkRunModeFlags(tt.stdio, tt.http, tt.token, tt.noAuth, tt.lists)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestHTTPAuthSettings(t *testing.T) {
	home := t.TempDir()
	flagToken := "flag-" + strings.Repeat("a", 20)

	// --no-auth: loopback only, never with a token.
	if tok, _, err := httpAuthSettings("127.0.0.1:8765", "", true, "", home); err != nil || tok != "" {
		t.Fatalf("--no-auth on loopback = %q, %v", tok, err)
	}
	if _, _, err := httpAuthSettings("0.0.0.0:8765", "", true, "", home); err == nil || !strings.Contains(err.Error(), "only allowed on a loopback") {
		t.Fatalf("--no-auth on 0.0.0.0 error = %v", err)
	}
	if _, _, err := httpAuthSettings("127.0.0.1:8765", flagToken, true, "", home); err == nil {
		t.Fatal("--no-auth with --http-token must be refused")
	}

	// Explicit token, then env, then the serve token file.
	if tok, src, err := httpAuthSettings("0.0.0.0:8765", flagToken, false, "", home); err != nil || tok != flagToken || src != "--http-token flag" {
		t.Fatalf("flag token = %q %q %v", tok, src, err)
	}
	if _, _, err := httpAuthSettings("127.0.0.1:8765", "short", false, "", home); err == nil || !strings.Contains(err.Error(), "--http-token") {
		t.Fatalf("short flag token error = %v", err)
	}
	envToken := "env-" + strings.Repeat("b", 20)
	if tok, src, err := httpAuthSettings("127.0.0.1:8765", "", false, envToken, home); err != nil || tok != envToken || src != serveTokenEnv {
		t.Fatalf("env token = %q %q %v", tok, src, err)
	}
	tok, src, err := httpAuthSettings("127.0.0.1:8765", "", false, "", home)
	if err != nil || len(tok) != 64 || src != serveTokenPath(home) {
		t.Fatalf("token file = %d chars, %q, %v", len(tok), src, err)
	}
	// The same file as serve: a second resolution reads it back.
	again, _, err := resolveServeToken("", "", home)
	if err != nil || again != tok {
		t.Fatalf("serve does not share the token file: %v", err)
	}
}

func TestHTTPFrontendOptions(t *testing.T) {
	cfg := &migrate.Config{Server: &migrate.FrontendConfig{
		MaxConcurrentRequests: 7,
		HTTP: &migrate.HTTPFrontendConfig{
			AllowedHosts:       []string{"gw.lan"},
			AllowedOrigins:     []string{"https://a.example"},
			MaxSessions:        3,
			SessionIdleTimeout: "90s",
		},
	}}
	opts := httpFrontendOptions(cfg, "127.0.0.1:0", "tok", []string{"extra.lan"}, []string{"https://b.example"})
	if opts.MaxConcurrent != 7 || opts.MaxSessions != 3 || opts.SessionIdleTimeout != 90*time.Second ||
		opts.MaxBodyBytes != migrate.DefaultHTTPMaxBodyBytes {
		t.Fatalf("options = %+v", opts)
	}
	if strings.Join(opts.AllowedHosts, ",") != "gw.lan,extra.lan" || strings.Join(opts.AllowedOrigins, ",") != "https://a.example,https://b.example" {
		t.Fatalf("allowlists = %v %v", opts.AllowedHosts, opts.AllowedOrigins)
	}
	if err := checkHTTPOrigins([]string{"https://ok.example", "*"}); err == nil {
		t.Fatal("a wildcard origin must be refused")
	}
}

func TestPrintHTTPFrontendStatus(t *testing.T) {
	home := t.TempDir()
	cfg := &migrate.Config{}

	var b bytes.Buffer
	printHTTPFrontendStatusFor(&b, cfg, nil, home)
	out := b.String()
	for _, want := range []string{"## HTTP Front End", "Not running", "Max sessions: 64", "not created yet"} {
		if !strings.Contains(out, want) {
			t.Fatalf("idle report lacks %q:\n%s", want, out)
		}
	}

	path := serveTokenPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("c", 64)), 0o644); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	printHTTPFrontendStatusFor(&b, cfg, &statusfile.StatusInfo{PID: 42, HTTP: &statusfile.HTTPFrontendStatus{
		URL: "http://0.0.0.0:8765/mcp", Loopback: false, Auth: false, AllowedOrigins: []string{"https://a.example"}, MaxSessions: 5,
	}}, home)
	out = b.String()
	for _, want := range []string{"Running: http://0.0.0.0:8765/mcp (pid 42)", "NON-LOOPBACK", "DISABLED (--no-auth)", "https://a.example", "readable by other users"} {
		if !strings.Contains(out, want) {
			t.Fatalf("running report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, strings.Repeat("c", 64)) {
		t.Fatal("doctor must never print the token")
	}
}
