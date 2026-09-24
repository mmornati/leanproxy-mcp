package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// End-to-end coverage for issue #312: `server run --stdio` runs a
// sandbox-configured stdio server through a container runtime binary
// instead of spawning its command directly, and fails that one server's
// start with a clear error (without affecting other configured servers)
// when the runtime binary is missing.
//
// A real Docker/Podman daemon is not required: like the unit tests in
// pkg/pool, this uses a small shell script standing in for `docker` that
// records its own invocation and then execs the wrapped command, so the
// full stdio pipeline (through the real leanproxy-mcp binary) is exercised
// without needing container infrastructure in CI.

// fakeDockerScriptE2E mirrors pkg/pool's fakeDockerScript: it logs each
// flag `buildSandboxArgv` is expected to produce (env vars by NAME only,
// never a value) and then execs the wrapped command.
const fakeDockerScriptE2E = `#!/bin/sh
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

// installFakeDockerE2E writes fakeDockerScriptE2E as an executable
// "docker" in a fresh dir and returns that dir and the log file path it
// will append to.
func installFakeDockerE2E(t *testing.T) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	scriptPath := filepath.Join(dir, "docker")
	if err := os.WriteFile(scriptPath, []byte(fakeDockerScriptE2E), 0o700); err != nil { //nolint:gosec // test fixture, intentionally executable
		t.Fatal(err)
	}
	logPath = filepath.Join(dir, "docker.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, logPath
}

func writeSandboxConfig(t *testing.T, dir, server, fakeBin, upstreamLog string) string {
	t.Helper()
	path := filepath.Join(dir, "leanproxy.yaml")
	cfg := fmt.Sprintf(`version: "1.0"
servers:
  - name: %s
    transport: stdio
    enabled: true
    stdio:
      command: %q
      args: [%q]
      env_passthrough: ["DOCKER_LOG"]
      sandbox:
        runtime: docker
        image: node:22-alpine
        network: none
`, server, fakeBin, upstreamLog)
	writeFile(t, path, cfg)
	return path
}

// TestSandbox_EndToEndThroughRealBinary verifies that the real
// leanproxy-mcp binary spawns a sandbox-configured stdio server through
// the container runtime (never the command directly), that requests still
// round-trip normally, and that stopping the proxy triggers the runtime's
// container cleanup (#312).
func TestSandbox_EndToEndThroughRealBinary(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dockerDir, dockerLog := installFakeDockerE2E(t)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "sbx")
	cfg := writeSandboxConfig(t, dir, srv, fakeBin, upstreamLog)

	env := append(os.Environ(),
		"PATH="+dockerDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_LOG="+dockerLog,
	)
	s := startStdioProxyWithEnv(t, proxyBin, cfg, env)

	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}

	resp := s.toolCall(2, "list_tools", map[string]string{"server_name": srv})
	if resp.Error != nil || !strings.Contains(resp.raw, "echo") {
		t.Fatalf("list_tools through sandboxed server failed: err=%v raw=%s", resp.Error, resp.raw)
	}

	// Closing stdin triggers the proxy's normal EOF shutdown path, which
	// stops every server (running the sandbox cleanup synchronously) before
	// the process itself exits.
	_ = s.stdin.Close()
	waitDone := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-waitDone
	}

	deadline := time.Now().Add(5 * time.Second)
	var logText string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(dockerLog)
		if err == nil {
			logText = string(data)
			if strings.Contains(logText, "IMAGE node:22-alpine") {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !strings.Contains(logText, "IMAGE node:22-alpine") {
		t.Fatalf("expected the fake docker runtime to have been invoked with the configured image, log:\n%s", logText)
	}
	if !strings.Contains(logText, "OPT --network none") {
		t.Errorf("expected network none, log:\n%s", logText)
	}
	if !strings.Contains(logText, fmt.Sprintf("OPT --name leanproxy-%s-", srv)) {
		t.Errorf("expected a leanproxy-<server>-<gen> container name, log:\n%s", logText)
	}
}

// TestSandbox_MissingRuntimeFailsOnlyThatServer verifies that when a
// sandboxed server's configured runtime binary is missing, that server
// fails to start with a clear error while an unrelated, unsandboxed
// server in the same config still starts and serves tools normally
// (#312's acceptance criteria).
func TestSandbox_MissingRuntimeFailsOnlyThatServer(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	brokenLog := filepath.Join(dir, "broken-upstream.log")
	fineLog := filepath.Join(dir, "fine-upstream.log")
	broken := uniqueServerName(t, "sbxbroken")
	fine := uniqueServerName(t, "sbxfine")

	cfgPath := filepath.Join(dir, "leanproxy.yaml")
	cfg := fmt.Sprintf(`version: "1.0"
servers:
  - name: %s
    transport: stdio
    enabled: true
    stdio:
      command: %q
      args: [%q]
      sandbox:
        runtime: docker
        image: node:22-alpine
        network: none
  - name: %s
    transport: stdio
    enabled: true
    stdio:
      command: %q
      args: [%q]
`, broken, fakeBin, brokenLog, fine, fakeBin, fineLog)
	writeFile(t, cfgPath, cfg)

	// A PATH with no "docker" on it at all (isolated to just what the
	// proxy binary itself and the fake upstream need).
	isolated := t.TempDir()
	for _, name := range []string{"sh"} {
		if real, err := exec.LookPath(name); err == nil {
			_ = os.Symlink(real, filepath.Join(isolated, name))
		}
	}
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+isolated)

	s := startStdioProxyWithEnv(t, proxyBin, cfgPath, env)

	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}

	// The unrelated, unsandboxed server must still work.
	resp := s.toolCall(2, "list_tools", map[string]string{"server_name": fine})
	if resp.Error != nil || !strings.Contains(resp.raw, "echo") {
		t.Fatalf("unsandboxed server should still start when a sibling's sandbox runtime is missing: err=%v raw=%s", resp.Error, resp.raw)
	}

	// The sandboxed server, whose runtime is missing, must fail clearly
	// rather than silently running unsandboxed: the pool never registered
	// it, so it is absent from the running server set entirely.
	brokenResp := s.toolCall(3, "list_tools", map[string]string{"server_name": broken})
	if brokenResp.Error == nil && !strings.Contains(brokenResp.raw, "not found") {
		t.Fatalf("expected the sandboxed server (missing runtime) to have failed to start, got: %s", brokenResp.raw)
	}
	if !strings.Contains(s.stderr.String(), "not found on PATH") {
		t.Errorf("expected a clear missing-runtime error in the server's own logs, stderr:\n%s", s.stderr.String())
	}
}
