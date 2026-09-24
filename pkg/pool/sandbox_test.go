package pool

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

func TestSandboxContainerName(t *testing.T) {
	name := sandboxContainerName("some server!", 3)
	if name != "leanproxy-some_server_-3" {
		t.Errorf("unexpected container name: %q", name)
	}
	if sandboxContainerName("", 1) != "leanproxy-server-1" {
		t.Errorf("empty name should fall back to \"server\", got %q", sandboxContainerName("", 1))
	}
}

func TestEnvNamesOf(t *testing.T) {
	env := []string{"PATH=/usr/bin", "API_KEY=super-secret", "PATH=/other", "NOEQUALS"}
	names := envNamesOf(env)
	if len(names) != 2 || names[0] != "PATH" || names[1] != "API_KEY" {
		t.Fatalf("unexpected names: %v", names)
	}
	for _, n := range names {
		if strings.Contains(n, "secret") || strings.Contains(n, "/usr") {
			t.Fatalf("envNamesOf leaked a value: %v", names)
		}
	}
}

// TestBuildSandboxArgvNeverContainsSecretValues verifies the core security
// property of #312: the generated argv passes env var *names* only (`-e
// NAME`), never `-e NAME=VALUE`, so a secret value never lands in argv (and
// therefore never in a redacted log line or in `ps` output).
func TestBuildSandboxArgvNeverContainsSecretValues(t *testing.T) {
	spec := &migrate.SandboxConfig{
		Runtime: "docker",
		Image:   "node:22-alpine",
		Network: "none",
	}
	envNames := []string{"API_KEY", "PATH"}
	argv := buildSandboxArgv("leanproxy-test-1", "npx", []string{"-y", "some-server"}, envNames, spec)

	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "super-secret") {
		t.Fatalf("argv leaked a secret value: %v", argv)
	}
	if !strings.Contains(joined, "-e API_KEY") {
		t.Errorf("expected bare -e API_KEY in argv, got: %v", argv)
	}
	if strings.Contains(joined, "API_KEY=") {
		t.Errorf("argv must never contain NAME=VALUE for env, got: %v", argv)
	}

	wantContains := []string{
		"--rm", "-i", "--init",
		"--name", "leanproxy-test-1",
		"--network", "none",
		"--read-only", "--tmpfs", "/tmp",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "256",
	}
	for _, want := range wantContains {
		if !containsArg(argv, want) {
			t.Errorf("argv missing %q: %v", want, argv)
		}
	}

	// image, command, args appear at the end in order.
	last := argv[len(argv)-4:]
	want := []string{"node:22-alpine", "npx", "-y", "some-server"}
	for i := range want {
		if last[i] != want[i] {
			t.Errorf("unexpected tail of argv: %v (full: %v)", last, argv)
			break
		}
	}
}

func TestBuildSandboxArgvDefaultsNetworkToNone(t *testing.T) {
	spec := &migrate.SandboxConfig{Runtime: "docker", Image: "node:22-alpine"}
	argv := buildSandboxArgv("c1", "npx", nil, nil, spec)
	if !containsPair(argv, "--network", "none") {
		t.Errorf("expected default network none, got: %v", argv)
	}
}

func TestBuildSandboxArgvMemoryAndCPUs(t *testing.T) {
	spec := &migrate.SandboxConfig{Runtime: "docker", Image: "node:22-alpine", Memory: "512m", CPUs: "1"}
	argv := buildSandboxArgv("c1", "npx", nil, nil, spec)
	if !containsPair(argv, "-m", "512m") {
		t.Errorf("missing -m 512m: %v", argv)
	}
	if !containsPair(argv, "--cpus", "1") {
		t.Errorf("missing --cpus 1: %v", argv)
	}
}

func TestBuildSandboxArgvMounts(t *testing.T) {
	spec := &migrate.SandboxConfig{
		Runtime: "docker",
		Image:   "node:22-alpine",
		Mounts: []migrate.SandboxMount{
			{Host: "/home/op/projects/foo", Container: "/work", ReadOnly: true},
		},
	}
	argv := buildSandboxArgv("c1", "npx", nil, nil, spec)
	if !containsPair(argv, "-v", "/home/op/projects/foo:/work:ro") {
		t.Errorf("missing read-only mount flag: %v", argv)
	}
}

func TestBuildSandboxArgvCacheVolumeKnownCommand(t *testing.T) {
	spec := &migrate.SandboxConfig{Runtime: "docker", Image: "node:22-alpine", CacheVolume: true}
	argv := buildSandboxArgv("c1", "npx", nil, nil, spec)
	if !containsPair(argv, "-v", "leanproxy-sandbox-cache-npm:/root/.npm") {
		t.Errorf("expected npm cache volume, got: %v", argv)
	}
}

func TestBuildSandboxArgvCacheVolumeUnknownCommand(t *testing.T) {
	spec := &migrate.SandboxConfig{Runtime: "docker", Image: "node:22-alpine", CacheVolume: true}
	argv := buildSandboxArgv("c1", "some-custom-binary", nil, nil, spec)
	for i, a := range argv {
		if a == "-v" && i+1 < len(argv) && strings.HasPrefix(argv[i+1], "leanproxy-sandbox-cache-") {
			t.Errorf("unexpected cache volume for unknown command: %v", argv)
		}
	}
}

func TestBuildSandboxArgvInfersImageWhenUnset(t *testing.T) {
	spec := &migrate.SandboxConfig{Runtime: "docker"}
	argv := buildSandboxArgv("c1", "npx", []string{"-y", "pkg"}, nil, spec)
	if !containsArg(argv, "node:22-alpine") {
		t.Errorf("expected inferred image node:22-alpine, got: %v", argv)
	}
}

func TestResolveSandboxRuntimeRejectsUnsupported(t *testing.T) {
	if _, err := resolveSandboxRuntime("bwrap"); err == nil {
		t.Error("expected error for unsupported runtime")
	}
}

func TestResolveSandboxRuntimeMissingBinary(t *testing.T) {
	// docker/podman are supported names, but this test environment may or
	// may not have them installed; either branch is exercised depending on
	// availability, and this only checks the function does not panic and
	// returns a clear error type when missing.
	if _, err := exec.LookPath("docker"); err != nil {
		if _, sErr := resolveSandboxRuntime("docker"); sErr == nil {
			t.Error("expected an error when docker is not on PATH")
		} else if !strings.Contains(sErr.Error(), "not found on PATH") {
			t.Errorf("unexpected error message: %v", sErr)
		}
	}
}

func TestCheckSandboxRuntimeRejectsUnsupported(t *testing.T) {
	if err := CheckSandboxRuntime("bwrap"); err == nil {
		t.Error("expected error for unsupported runtime")
	}
}

func TestSandboxEnabled(t *testing.T) {
	cases := []struct {
		spec *migrate.SandboxConfig
		want bool
	}{
		{nil, false},
		{&migrate.SandboxConfig{}, false},
		{&migrate.SandboxConfig{Runtime: "none"}, false},
		{&migrate.SandboxConfig{Runtime: "docker"}, true},
		{&migrate.SandboxConfig{Runtime: "podman"}, true},
	}
	for _, c := range cases {
		if got := sandboxEnabled(c.spec); got != c.want {
			t.Errorf("sandboxEnabled(%+v) = %v, want %v", c.spec, got, c.want)
		}
	}
}

// TestSandboxCleanupNoRuntimeOrContainer verifies the no-op guard: an empty
// runtimePath or containerName must never attempt to exec anything.
func TestSandboxCleanupNoRuntimeOrContainer(t *testing.T) {
	// Should return immediately without panicking or hanging.
	sandboxCleanup("", "leanproxy-test-1", nil)
	sandboxCleanup("/bin/true", "", nil)
}

// TestSandboxCleanupSuppressesNoSuchContainer verifies that an already-gone
// container (the common case: --rm already removed it) is not treated as an
// error worth surfacing.
func TestSandboxCleanupSuppressesNoSuchContainer(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	// A container name that (almost certainly) never existed.
	sandboxCleanup(mustLookPath(t, "docker"), "leanproxy-never-existed-xyz123", nil)
}

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("LookPath(%s): %v", name, err)
	}
	return p
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}
