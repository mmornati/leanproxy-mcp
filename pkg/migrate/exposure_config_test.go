package migrate

import (
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/exposure"
)

// The `exposure:` block (issue #322) is parsed and validated at load time.

func TestLoadConfigExposure(t *testing.T) {
	cfg, err := loadYAML(t, `
servers: []
exposure:
  mode: hybrid
  builtin_clients: false
  clients:
    - match: "claude-code"
      mode: passthrough
    - match: "Zed*"
      mode: router
  always_load: ["github.search_*"]
  max_name_length: 48
`)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Exposure
	if e == nil || e.Mode != exposure.ModeHybrid || e.BuiltinClients == nil || *e.BuiltinClients || len(e.Clients) != 2 || e.MaxNameLength != 48 || len(e.AlwaysLoad) != 1 {
		t.Fatalf("exposure block = %+v", e)
	}
	r, err := exposure.NewResolver(e, "")
	if err != nil {
		t.Fatal(err)
	}
	for client, want := range map[string]exposure.Mode{"claude-code": exposure.ModePassthrough, "zed": exposure.ModeRouter, "cursor-vscode": exposure.ModeHybrid} {
		if got, _ := r.ModeFor(client); got != want {
			t.Errorf("%s = %q, want %q", client, got, want)
		}
	}
}

func TestLoadConfigExposureAbsent(t *testing.T) {
	cfg, err := loadYAML(t, "servers: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Exposure != nil {
		t.Fatalf("exposure = %+v, want nil (defaults)", cfg.Exposure)
	}
}

func TestLoadConfigExposureRejected(t *testing.T) {
	for name, block := range map[string]string{
		"bad mode":        "exposure:\n  mode: direct\n",
		"bad client mode": "exposure:\n  clients:\n    - match: x\n      mode: all\n",
		"no match":        "exposure:\n  clients:\n    - mode: router\n",
		"bad glob":        "exposure:\n  clients:\n    - match: \"a[\"\n      mode: router\n",
		"bad always_load": "exposure:\n  always_load: [\"a[\"]\n",
		"name length":     "exposure:\n  max_name_length: 100\n",
	} {
		_, err := loadYAML(t, "servers: []\n"+block)
		if err == nil || !strings.Contains(err.Error(), "exposure.") {
			t.Errorf("%s: LoadConfig error = %v, want an exposure.* validation error", name, err)
		}
	}
}
