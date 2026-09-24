package migrate

import "testing"

func TestInferSandboxImage(t *testing.T) {
	cases := []struct {
		command string
		want    string
		ok      bool
	}{
		{"npx", "node:22-alpine", true},
		{"npm", "node:22-alpine", true},
		{"node", "node:22-alpine", true},
		{"/usr/local/bin/npx", "node:22-alpine", true},
		{"uvx", "ghcr.io/astral-sh/uv:python3.12-alpine", true},
		{"uv", "ghcr.io/astral-sh/uv:python3.12-alpine", true},
		{"python3", "ghcr.io/astral-sh/uv:python3.12-alpine", true},
		{"some-custom-binary", "", false},
	}
	for _, c := range cases {
		got, ok := InferSandboxImage(c.command)
		if ok != c.ok || got != c.want {
			t.Errorf("InferSandboxImage(%q) = (%q, %v), want (%q, %v)", c.command, got, ok, c.want, c.ok)
		}
	}
}

func TestSandboxConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		sandbox *SandboxConfig
		command string
		wantErr bool
		errMsg  string
	}{
		{name: "nil is valid", sandbox: nil, command: "npx"},
		{name: "empty runtime is valid (unsandboxed)", sandbox: &SandboxConfig{}, command: "npx"},
		{name: "runtime none is valid", sandbox: &SandboxConfig{Runtime: "none"}, command: "npx"},
		{
			name:    "unsupported runtime",
			sandbox: &SandboxConfig{Runtime: "firejail"},
			command: "npx",
			wantErr: true,
			errMsg:  "sandbox.runtime must be docker, podman or none",
		},
		{
			name:    "docker with inferable image is valid",
			sandbox: &SandboxConfig{Runtime: "docker"},
			command: "npx",
		},
		{
			name:    "docker with explicit image is valid",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "custom:latest"},
			command: "some-custom-binary",
		},
		{
			name:    "docker with no image and no inference fails",
			sandbox: &SandboxConfig{Runtime: "docker"},
			command: "some-custom-binary",
			wantErr: true,
			errMsg:  "sandbox.image is required",
		},
		{
			name:    "invalid network",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", Network: "vpn"},
			command: "npx",
			wantErr: true,
			errMsg:  "sandbox.network must be none, bridge or host",
		},
		{
			name:    "bridge network is valid",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", Network: "bridge"},
			command: "npx",
		},
		{
			name:    "host network is valid",
			sandbox: &SandboxConfig{Runtime: "podman", Image: "x", Network: "host"},
			command: "npx",
		},
		{
			name: "mount missing host",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", Mounts: []SandboxMount{
				{Container: "/work"},
			}},
			command: "npx",
			wantErr: true,
			errMsg:  "requires both host and container",
		},
		{
			name: "mount missing container",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", Mounts: []SandboxMount{
				{Host: "/home/op/foo"},
			}},
			command: "npx",
			wantErr: true,
			errMsg:  "requires both host and container",
		},
		{
			name: "valid mount",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", Mounts: []SandboxMount{
				{Host: "/home/op/foo", Container: "/work", ReadOnly: true},
			}},
			command: "npx",
		},
		{
			name:    "valid memory",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", Memory: "512m"},
			command: "npx",
		},
		{
			name:    "invalid memory",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", Memory: "lots"},
			command: "npx",
			wantErr: true,
			errMsg:  "not a valid memory value",
		},
		{
			name:    "valid cpus",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", CPUs: "0.5"},
			command: "npx",
		},
		{
			name:    "invalid cpus",
			sandbox: &SandboxConfig{Runtime: "docker", Image: "x", CPUs: "lots"},
			command: "npx",
			wantErr: true,
			errMsg:  "must be a number",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.sandbox.Validate("myserver", tc.command)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if tc.errMsg != "" && !contains(err.Error(), tc.errMsg) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestServerConfigValidateSandboxIntegration(t *testing.T) {
	sc := &ServerConfig{
		Name:      "test",
		Transport: TransportStdio,
		Stdio: &StdioConfig{
			Command: "some-custom-binary",
			Sandbox: &SandboxConfig{Runtime: "docker"},
		},
	}
	if err := sc.Validate(); err == nil {
		t.Fatal("expected error: sandboxed server with no image and an uninferable command")
	}

	sc.Stdio.Sandbox.Image = "custom:latest"
	if err := sc.Validate(); err != nil {
		t.Fatalf("unexpected error after setting an explicit image: %v", err)
	}
}
