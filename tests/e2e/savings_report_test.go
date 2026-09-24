package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// End-to-end coverage for issue #324 (auditable savings report): a real
// session with the response governor on, through each front end
// (`server run --stdio`, `server run --http`, `serve`), followed by a real
// `leanproxy-mcp report` invocation against the same isolated HOME,
// checking the report picks up the governor's real counters and that the
// three front ends behave identically.

func reportViaCLI(t *testing.T, proxyBin string, env []string, args ...string) (stdout string, exitCode int) {
	t.Helper()
	cmd := exec.Command(proxyBin, append([]string{"report"}, args...)...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("report %v failed: %v\nstderr:\n%s", args, err, ee.Stderr)
		}
		t.Fatalf("report %v failed: %v", args, err)
	}
	return string(out), 0
}

func decodeSavingsReport(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var rep map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("report --export json did not decode: %v\nraw=%s", err, raw)
	}
	return rep
}

// mechanismSaved returns the "saved_tokens" of the named mechanism row in
// a decoded SavingsReport, or -1 if the row is absent.
func mechanismSaved(t *testing.T, rep map[string]interface{}, mechanism string) float64 {
	t.Helper()
	mechs, ok := rep["mechanisms"].([]interface{})
	if !ok {
		t.Fatalf("report has no mechanisms array: %v", rep)
	}
	for _, m := range mechs {
		row, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if row["mechanism"] == mechanism {
			v, _ := row["saved_tokens"].(float64)
			return v
		}
	}
	return -1
}

func TestSavingsReport_Stdio(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, governorOn)
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home)

	s := startStdioProxyEnv(t, proxyBin, cfg, []string{"HOME=" + home})
	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
	// Two identical calls: the first truncates, the second in-session
	// dedups if dedup is on; governorOn (response_governor_test.go) only
	// sets max_tokens, so only truncation is expected here.
	resp := s.toolCall(2, "invoke_tool", map[string]interface{}{
		"server": "big", "tool": "big_text", "arguments": map[string]interface{}{},
	})
	if resp.Error != nil {
		t.Fatalf("invoke_tool big_text: %s", resp.raw)
	}
	requireNoSecrets(t, "governed result", resp.raw)

	// tools/list also exercises the schema counters.
	if listResp := s.call(3, "tools/list", nil); listResp.Error != nil {
		t.Fatalf("tools/list: %s", listResp.raw)
	}

	_ = s.stdin.Close()
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("proxy did not exit on EOF")
	}

	out, _ := reportViaCLI(t, proxyBin, env, "--export", "json")
	rep := decodeSavingsReport(t, out)
	if saved := mechanismSaved(t, rep, "response_truncation"); saved <= 0 {
		t.Errorf("response_truncation saved_tokens = %v, want > 0 (stdio front end)", saved)
	}
	requireNoSecrets(t, "report json", out)
}

func TestSavingsReport_StreamableHTTP(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, governorOn)

	p := startHTTPProxy(t, proxyBin, cfg, nil)
	_, c := newHTTPProto(t, p.url)
	protoInitialize(t, c, "2025-06-18")
	resp := govViaInvoke(c, "big", "big_text")
	if resp.Error != nil {
		t.Fatalf("invoke_tool big_text over http: %v", resp.Error)
	}
	p.stop(t)

	env := append(os.Environ(), "HOME="+p.home)
	out, _ := reportViaCLI(t, proxyBin, env, "--export", "json")
	rep := decodeSavingsReport(t, out)
	if saved := mechanismSaved(t, rep, "response_truncation"); saved <= 0 {
		t.Errorf("response_truncation saved_tokens = %v, want > 0 (http front end)", saved)
	}
}

func TestSavingsReport_Serve(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, governorOn)
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home)

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd := exec.Command(proxyBin, "serve", "--config", cfg, "--listen", addr, "--metrics-bind", "off", "--dashboard-bind", "off")
	cmd.Env = env
	logs := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	waitForPort(t, addr)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial serve: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Write(serveAuthLine()); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReaderSize(conn, 1<<20)
	c := &protoClient{t: t,
		send: func(b []byte) error { _, err := conn.Write(b); return err },
		next: func(d time.Duration) (string, bool) {
			_ = conn.SetReadDeadline(time.Now().Add(d))
			line, err := r.ReadString('\n')
			if err != nil {
				return "", false
			}
			return strings.TrimRight(line, "\n"), true
		},
	}
	protoInitialize(t, c, "2025-06-18")
	resp := govViaNamespaced(c, "big", "big_text")
	if resp.Error != nil {
		t.Fatalf("big_text over serve: %v", resp.Error)
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	if t.Failed() {
		t.Logf("serve logs:\n%s", logs.String())
	}

	out, _ := reportViaCLI(t, proxyBin, env, "--export", "json")
	rep := decodeSavingsReport(t, out)
	if saved := mechanismSaved(t, rep, "response_truncation"); saved <= 0 {
		t.Errorf("response_truncation saved_tokens = %v, want > 0 (serve front end)", saved)
	}
}

// TestSavingsReport_UsageFilePermissions checks the acceptance criterion
// "Usage files are 0600" against the real binary's output.
func TestSavingsReport_UsageFilePermissions(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, governorOn)
	home := t.TempDir()

	s := startStdioProxyEnv(t, proxyBin, cfg, []string{"HOME=" + home})
	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
	_ = s.stdin.Close()
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("proxy did not exit on EOF")
	}

	usageDir := filepath.Join(home, ".leanproxy", "usage")
	entries, err := os.ReadDir(usageDir)
	if err != nil {
		t.Fatalf("read usage dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no usage files were written")
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("usage file %s mode = %o, want 0600", e.Name(), perm)
		}
	}
}

// TestSavingsReport_OfflinePrivacy checks the report never carries a
// payload, only names/numbers/timestamps (acceptance: "no payloads,
// arguments or secrets in the report").
func TestSavingsReport_OfflinePrivacy(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, governorOn)
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home)

	s := startStdioProxyEnv(t, proxyBin, cfg, []string{"HOME=" + home})
	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
	resp := s.toolCall(2, "invoke_tool", map[string]interface{}{
		"server": "big", "tool": "big_text",
		"arguments": map[string]interface{}{"note": e2eAWSKey},
	})
	if resp.Error != nil {
		t.Fatalf("invoke_tool big_text: %s", resp.raw)
	}
	_ = s.stdin.Close()
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("proxy did not exit on EOF")
	}

	for _, format := range []string{"", "json", "md", "csv"} {
		var args []string
		if format != "" {
			args = []string{"--export", format}
		}
		out, _ := reportViaCLI(t, proxyBin, env, args...)
		requireNoSecrets(t, "report --export "+format, out)
		if strings.Contains(out, e2eAWSKey) {
			t.Fatalf("report --export %s leaked the fake secret", format)
		}
	}
}
