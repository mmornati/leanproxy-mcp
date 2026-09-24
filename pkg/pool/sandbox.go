package pool

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// sandboxCleanupTimeout bounds how long a container-runtime cleanup (`rm
// -f`) is allowed to run for, so a wedged docker/podman daemon can never
// hang a stop or crash-restart cycle indefinitely.
const sandboxCleanupTimeout = 5 * time.Second

// resolveSandboxRuntime validates spec.Runtime and locates its executable
// on PATH. A missing runtime binary fails clearly instead of silently
// falling back to running the command unsandboxed on the host (#312).
func resolveSandboxRuntime(runtime string) (string, error) {
	switch runtime {
	case "docker", "podman":
	default:
		return "", fmt.Errorf("sandbox: unsupported runtime %q (must be docker or podman)", runtime)
	}
	path, err := exec.LookPath(runtime)
	if err != nil {
		return "", fmt.Errorf("sandbox: runtime %q not found on PATH; install it, or set sandbox.runtime: none to run this server unsandboxed", runtime)
	}
	return path, nil
}

// CheckSandboxRuntime reports whether runtime ("docker" or "podman") is
// available on PATH, for `leanproxy doctor sandbox` (#312) to show without
// needing a live server config.
func CheckSandboxRuntime(runtime string) error {
	_, err := resolveSandboxRuntime(runtime)
	return err
}

// sandboxEnabled reports whether spec asks for actual container isolation
// (as opposed to being absent, or explicitly "none").
func sandboxEnabled(spec *migrate.SandboxConfig) bool {
	return spec != nil && spec.Runtime != "" && spec.Runtime != "none"
}

// sandboxContainerName builds the container name for one process
// generation: unique per server and generation, and stable/predictable so
// `docker ps -a --filter name=leanproxy-` finds every container this proxy
// ever started.
func sandboxContainerName(serverName string, gen uint64) string {
	return fmt.Sprintf("leanproxy-%s-%d", sanitizeContainerNamePart(serverName), gen)
}

// sanitizeContainerNamePart restricts name to the character set Docker and
// Podman accept in a container name ([a-zA-Z0-9_.-]).
func sanitizeContainerNamePart(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}

// envNamesOf extracts the ordered, de-duplicated variable names from a
// "KEY=VALUE" environment list. It never returns or logs values.
func envNamesOf(env []string) []string {
	seen := make(map[string]bool, len(env))
	names := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, found := strings.Cut(kv, "=")
		if !found || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// sandboxCacheVolume returns the named volume and its mount point inside
// the container for a well-known interpreter command's package cache, and
// whether one is known for command. Unrecognized commands get no cache
// volume even when sandbox.cache_volume is set.
func sandboxCacheVolume(command string) (volume, mountPoint string, ok bool) {
	switch filepath.Base(command) {
	case "npx", "npm", "node":
		return "leanproxy-sandbox-cache-npm", "/root/.npm", true
	case "uvx", "uv":
		return "leanproxy-sandbox-cache-uv", "/root/.cache/uv", true
	default:
		return "", "", false
	}
}

// buildSandboxArgv builds the `docker run`/`podman run` argv that wraps
// command/args inside spec's isolation (#312):
//
//	run --rm -i --init --name <containerName> --network <net> --read-only
//	  --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges
//	  --pids-limit 256 [-m <mem>] [--cpus <cpus>] [-v host:container[:ro]]...
//	  [-v cache-volume:mount] [-e NAME]... <image> <command> <args>...
//
// envNames are variable *names* only (never values): each is passed as a
// bare `-e NAME`, which tells docker/podman to take the value from the
// runtime CLI's own process environment. The caller must set that same
// KEY=VALUE list as the spawned runtime process's cmd.Env so the value
// never appears in argv, in a redacted log line, or in `ps` output.
func buildSandboxArgv(containerName, command string, args, envNames []string, spec *migrate.SandboxConfig) []string {
	argv := []string{
		"run", "--rm", "-i", "--init",
		"--name", containerName,
	}

	network := spec.Network
	if network == "" {
		network = "none"
	}
	argv = append(argv, "--network", network)

	argv = append(argv,
		"--read-only", "--tmpfs", "/tmp",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "256",
	)

	if spec.Memory != "" {
		argv = append(argv, "-m", spec.Memory)
	}
	if spec.CPUs != "" {
		argv = append(argv, "--cpus", spec.CPUs)
	}

	for _, m := range spec.Mounts {
		flag := m.Host + ":" + m.Container
		if m.ReadOnly {
			flag += ":ro"
		}
		argv = append(argv, "-v", flag)
	}

	if spec.CacheVolume {
		if volume, mountPoint, ok := sandboxCacheVolume(command); ok {
			argv = append(argv, "-v", volume+":"+mountPoint)
		}
	}

	for _, name := range envNames {
		argv = append(argv, "-e", name)
	}

	image := spec.Image
	if image == "" {
		image, _ = migrate.InferSandboxImage(command)
	}
	argv = append(argv, image, command)
	argv = append(argv, args...)
	return argv
}

// sandboxCleanup force-removes containerName via `<runtimePath> rm -f`,
// bounded by sandboxCleanupTimeout. The pool's own process-group kill
// signals the runtime CLI process, not the container it manages (the
// daemon/podman keeps it running independently), so this is the only
// reliable way to guarantee no `leanproxy-*` container is left behind
// after a stop, restart or crash (#312). "No such container" (the
// container already went away, e.g. via --rm on a clean exit) is expected
// and not logged as an error.
func sandboxCleanup(runtimePath, containerName string, logger *slog.Logger) {
	if runtimePath == "" || containerName == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxCleanupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, runtimePath, "rm", "-f", containerName) // #nosec G204 -- runtimePath is resolved via LookPath and containerName is our own sanitized generated name
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if strings.Contains(string(out), "No such container") {
		return
	}
	if logger != nil {
		logger.Debug("sandbox: container cleanup", "container", containerName, "error", err, "output", strings.TrimSpace(string(out)))
	}
}
