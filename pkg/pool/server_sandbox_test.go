package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// fakeDockerScript is a stand-in container runtime used to test the pool's
// sandbox wiring end to end without a real Docker/Podman daemon (#312):
//   - `run ...`: parses the exact flag set buildSandboxArgv produces, logs
//     each flag (and, for -e, only its NAME — never a value, mirroring what
//     a real `docker run -e NAME` does), then execs the wrapped command so
//     the rest of the stdio pipeline behaves exactly as an unsandboxed
//     spawn would.
//   - `rm -f <name>`: logs the cleanup call instead of doing anything, so
//     tests can assert the pool actually calls it on stop/crash (#312).
const fakeDockerScript = `#!/bin/sh
set -e
if [ "$1" = "rm" ]; then
  echo "RM $2 $3" >> "$DOCKER_LOG"
  exit 0
fi
if [ "$1" != "run" ]; then
  echo "unexpected subcommand: $1" >&2
  exit 1
fi
shift
while [ $# -gt 0 ]; do
  case "$1" in
    --rm|-i|--init|--read-only)
      echo "FLAG $1" >> "$DOCKER_LOG"
      shift
      ;;
    --name|--network|--tmpfs|--cap-drop|--security-opt|--pids-limit|-m|--cpus|-v)
      echo "OPT $1 $2" >> "$DOCKER_LOG"
      shift 2
      ;;
    -e)
      echo "ENV_NAME $2" >> "$DOCKER_LOG"
      shift 2
      ;;
    *)
      break
      ;;
  esac
done
echo "IMAGE $1" >> "$DOCKER_LOG"
shift
exec "$@"
`

// installFakeDocker writes fakeDockerScript as an executable named "docker"
// in a fresh temp dir, prepends that dir to PATH for the duration of the
// test, and returns the path to the invocation log the script appends to.
func installFakeDocker(t *testing.T) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "docker")
	if err := os.WriteFile(scriptPath, []byte(fakeDockerScript), 0o700); err != nil { //nolint:gosec // test fixture, intentionally executable
		t.Fatalf("write fake docker: %v", err)
	}
	logPath = filepath.Join(dir, "docker.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_LOG", logPath)
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
	return logPath
}

// TestSandboxedSpawnRoundTrip verifies that a server configured with
// sandbox.runtime: docker is actually spawned through the container
// runtime binary (not run directly), that the request/response pipe still
// works through the wrapper, and that stopping the server invokes the
// runtime's `rm -f` cleanup with the expected container name (#312).
func TestSandboxedSpawnRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sandbox spawn test in short mode")
	}

	logPath := installFakeDocker(t)

	config := StdioServerConfig{
		Name:           "sandboxed-echo",
		Command:        "sh",
		Args:           []string{"-c", `while read -r line; do echo '{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}'; done`},
		Env:            []string{"SANDBOX_TEST_SECRET=super-secret-value"},
		EnvPassthrough: []string{"DOCKER_LOG"},
		Sandbox: &migrate.SandboxConfig{
			Runtime: "docker",
			Image:   "node:22-alpine",
			Network: "none",
		},
	}

	server := newServerV2(config.Name, config, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.spawn(ctx); err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	result, err := server.sendRequest(ctx, Request{
		Method: "initialize",
		ID:     1,
		Params: json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}`),
	})
	if err != nil {
		t.Fatalf("request through sandboxed pipe failed: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["status"] != "ok" {
		t.Errorf("expected status=ok through sandbox wrapper, got %v", decoded["status"])
	}

	if err := server.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	logText := string(logData)

	if !strings.Contains(logText, "IMAGE node:22-alpine") {
		t.Errorf("expected fake docker to see the configured image, log:\n%s", logText)
	}
	if !strings.Contains(logText, "OPT --network none") {
		t.Errorf("expected network none, log:\n%s", logText)
	}
	if !strings.Contains(logText, "ENV_NAME SANDBOX_TEST_SECRET") {
		t.Errorf("expected -e SANDBOX_TEST_SECRET (name only), log:\n%s", logText)
	}
	if strings.Contains(logText, "super-secret-value") {
		t.Errorf("secret value leaked into container runtime argv/log:\n%s", logText)
	}
	if !strings.Contains(logText, fmt.Sprintf("RM -f leanproxy-%s-", config.Name)) {
		t.Errorf("expected docker rm -f cleanup on stop, log:\n%s", logText)
	}
}

// isolatedPathWithOnly builds a PATH containing a single directory that
// only has symlinks for the named commands (resolved from the real PATH),
// so a runtime like "docker" that is not in names is guaranteed
// unresolvable regardless of what happens to be installed on the test
// host.
func isolatedPathWithOnly(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		real, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("LookPath(%s): %v", name, err)
		}
		if err := os.Symlink(real, filepath.Join(dir, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}
	return dir
}

// TestSandboxedSpawnMissingRuntimeFailsClearly verifies that a sandboxed
// server whose configured runtime binary is not on PATH fails its own
// start with a clear error, and never silently falls back to running the
// command unsandboxed on the host (#312).
func TestSandboxedSpawnMissingRuntimeFailsClearly(t *testing.T) {
	// Ensure "docker" is not resolvable, regardless of the host running
	// this test suite, while "sh" (needed to spawn at all) still is.
	t.Setenv("PATH", isolatedPathWithOnly(t, "sh"))

	config := StdioServerConfig{
		Name:    "sandboxed-missing-runtime",
		Command: "sh",
		Args:    []string{"-c", "true"},
		Sandbox: &migrate.SandboxConfig{Runtime: "docker", Image: "node:22-alpine"},
	}
	server := newServerV2(config.Name, config, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := server.spawn(ctx)
	if err == nil {
		t.Fatal("expected spawn to fail when the sandbox runtime binary is missing")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("expected a clear \"not found on PATH\" error, got: %v", err)
	}
	if server.getState() != StateError {
		t.Errorf("expected server left in error state, got %s", server.getState())
	}
}

// TestSandboxedSpawnMissingRuntimeDoesNotAffectOtherServers verifies the
// acceptance criterion that a server whose sandbox runtime is missing
// fails on its own, while an unrelated, unsandboxed server in the same
// pool starts normally (#312).
func TestSandboxedSpawnMissingRuntimeDoesNotAffectOtherServers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	t.Setenv("PATH", isolatedPathWithOnly(t, "sh"))

	broken := newServerV2("broken-sandbox", StdioServerConfig{
		Name:    "broken-sandbox",
		Command: "sh",
		Args:    []string{"-c", "true"},
		Sandbox: &migrate.SandboxConfig{Runtime: "docker", Image: "node:22-alpine"},
	}, slog.Default())

	fine := newServerV2("fine", StdioServerConfig{
		Name:    "fine",
		Command: "sh",
		Args:    []string{"-c", `while read -r line; do echo '{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}'; done`},
	}, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := broken.spawn(ctx); err == nil {
		t.Fatal("expected the sandboxed server to fail to spawn")
	}
	if err := fine.spawn(ctx); err != nil {
		t.Fatalf("unrelated unsandboxed server should still start: %v", err)
	}
	defer fine.stop()
}
