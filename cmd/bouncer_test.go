package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

func TestBouncerCmd_HelpOutput(t *testing.T) {
	cmd := bouncerCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestValidatePatternsCmd_HelpOutput(t *testing.T) {
	cmd := validatePatternsCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestListPatternsCmd_HelpOutput(t *testing.T) {
	cmd := listPatternsCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestBouncerCmd_ListPatterns(t *testing.T) {
	cmd := listPatternsCmd
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("list patterns should not error: %v", err)
	}
}

func TestBouncerCmd_PersistentFlags(t *testing.T) {
	if err := bouncerCmd.PersistentFlags().Set("config", "/tmp/test.yaml"); err != nil {
		t.Fatalf("set config flag: %v", err)
	}

	got, err := bouncerCmd.PersistentFlags().GetString("config")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got != "/tmp/test.yaml" {
		t.Errorf("config = %v, want /tmp/test.yaml", got)
	}
}

// TestValidatePatternsCmd_MissingConfig exercises the bug fixed in #278: when
// the operator points --config at a path that does not exist, the validator
// must surface the missing-file signal rather than silently compiling
// built-in patterns and exiting 0.
func TestValidatePatternsCmd_MissingConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	_, err := runRootForTest(t, []string{"bouncer", "validate-patterns", "--config", missing})
	if err == nil {
		t.Fatal("validate-patterns should error when --config points at a missing file")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error should mention the missing path %q, got %v", missing, err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should signal a not-found condition, got %v", err)
	}
}

// TestValidatePatternsCmd_ValidCustomPattern covers the happy path: a present
// config file with a safe custom pattern must compile and exit 0.
func TestValidatePatternsCmd_ValidCustomPattern(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "leanproxy.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
bouncer:
  enabled: true
  patterns:
    - name: fake-token
      pattern: "fake-[A-Za-z0-9]{8,}"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out, err := runRootForTest(t, []string{"bouncer", "validate-patterns", "--config", cfgPath})
	if err != nil {
		t.Fatalf("validate-patterns should succeed with safe custom pattern, got %v (output: %q)", err, out)
	}
	if !strings.Contains(out, "custom: 1") {
		t.Errorf("output should report 1 custom pattern compiled, got %q", out)
	}
}

// TestValidatePatternsCmd_DangerousCustomPattern covers the second half of
// #278: LoadConfig + Config.Validate must reject a custom pattern that would
// otherwise be silently dropped by SafeCompile, so the operator sees the
// failure at validation time instead of discovering it via a silently
// downgraded redactor in production.
func TestValidatePatternsCmd_DangerousCustomPattern(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "leanproxy.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
bouncer:
  enabled: true
  patterns:
    - name: redos
      pattern: "(.+)+secret"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := runRootForTest(t, []string{"bouncer", "validate-patterns", "--config", cfgPath})
	if err == nil {
		t.Fatal("validate-patterns should reject a dangerous nested-quantifier pattern")
	}
	if !strings.Contains(err.Error(), "redos") {
		t.Errorf("error should mention the offending pattern name, got %v", err)
	}
}

// runRootForTest runs RootCmd with the provided args, capturing stdout and
// stderr so the test can assert on both. Cobra's ExecuteC re-routes child
// commands through the root, so tests that exercise a subcommand must drive
// RootCmd directly.
func runRootForTest(t *testing.T, args []string) (string, error) {
	t.Helper()

	root := RootCmd
	prevOut := root.OutOrStderr()
	prevErr := root.ErrOrStderr()
	prevSilenceUsage := root.SilenceUsage
	prevSilenceErrors := root.SilenceErrors
	prevArgs := root.Flags().Args()
	t.Cleanup(func() {
		root.SetOut(prevOut)
		root.SetErr(prevErr)
		root.SilenceUsage = prevSilenceUsage
		root.SilenceErrors = prevSilenceErrors
		root.SetArgs(prevArgs)
	})

	buf := &strings.Builder{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetArgs(args)

	err := root.Execute()
	return buf.String(), err
}

// TestAllPatternDefs_ExportedSmoke verifies the helper exposed for external
// validation walks both Patterns and CustomPatterns in order.
func TestAllPatternDefs_ExportedSmoke(t *testing.T) {
	cfg := &bouncer.Config{
		Patterns:       []bouncer.PatternDef{{Name: "a", Pattern: "x"}},
		CustomPatterns: []bouncer.PatternDef{{Name: "b", Pattern: "y"}},
	}
	all := cfg.AllPatternDefs()
	if len(all) != 2 {
		t.Fatalf("AllPatternDefs() = %d entries, want 2", len(all))
	}
	if all[0].Name != "a" || all[1].Name != "b" {
		t.Errorf("AllPatternDefs() order = [%s, %s], want [a, b]", all[0].Name, all[1].Name)
	}

	var nilCfg *bouncer.Config
	if got := nilCfg.AllPatternDefs(); got != nil {
		t.Errorf("(*Config)(nil).AllPatternDefs() = %v, want nil", got)
	}
}

// Compile-time sanity: the sentinel error must be importable from outside
// the package so callers (cmd/bouncer, cmd/serve, ...) can branch on it.
var _ = migrate.ErrConfigNotFound
