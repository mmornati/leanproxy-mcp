package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// helper.go — utilities shared across story-specific *_test.go files.

func writeSimpleConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "leanproxy_servers.yaml")
	content := `version: "1.0"
servers:
  - name: echo
    transport: stdio
    enabled: true
    stdio:
      command: /bin/echo
      args: ["hello"]
      env: []
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return path
}

// freePort asks the OS for a free TCP port. Used by HTTP-endpoint tests so we
// don't fight for a fixed port across parallel test runs.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// writeFile is a tiny helper to materialize arbitrary files in a temp dir.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// startServe launches the binary as a background `serve` process, captures
// the PID into pidFile, and redirects stdout/stderr to logFile. The caller is
// responsible for invoking stopServe via t.Cleanup / defer.
func startServe(t *testing.T, args []string, pidFile, logFile string) error {
	t.Helper()
	wd, _ := os.Getwd()
	binaryPath := filepath.Join(wd, "leanproxy-mcp")
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("binary not found at %s", binaryPath)
	}

	logFh, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	fullArgs := append([]string{"serve"}, args...)
	cmd := exec.Command(binaryPath, fullArgs...)
	cmd.Stdout = logFh
	cmd.Stderr = logFh
	cmd.Dir = wd
	cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+findFirstArg(args, "--config"))

	if err := cmd.Start(); err != nil {
		logFh.Close()
		return fmt.Errorf("failed to start serve: %w", err)
	}

	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		logFh.Close()
		return fmt.Errorf("failed to write pidfile: %w", err)
	}

	go func() {
		_ = cmd.Wait()
		logFh.Close()
	}()

	return nil
}

// runBinaryWithTimeout runs the binary with a hard timeout. If the timeout
// elapses before the process exits, the process is killed. Used for tests that
// need to assert CLI flag acceptance (e.g. serve --cache-strategy=X) without
// waiting for the serve process to start its long-running HTTP listeners.
func runBinaryWithTimeout(args []string, timeout time.Duration) (string, string, int) {
	wd, _ := os.Getwd()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, filepath.Join(wd, "leanproxy-mcp"), args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = wd

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return stdout.String(), stderr.String(), exitCode
}

// findFirstArg returns the value following --flag in args, or "" if absent.
func findFirstArg(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// waitForHTTP polls url until it returns 2xx or timeout elapses, returning
// the response and body. This is more resilient than a single GET because
// serve takes a moment to bind its dashboard/metrics listener.
func waitForHTTP(t *testing.T, url string, timeout time.Duration) (*http.Response, string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return resp, string(body)
			}
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s after %s: %v", url, timeout, lastErr)
	return nil, ""
}
