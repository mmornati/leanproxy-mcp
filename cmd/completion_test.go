package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestGenerateBashCompletion(t *testing.T) {
	cmd := &cobra.Command{
		Use: "leanproxy",
		Run: func(cmd *cobra.Command, args []string) {},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Start the proxy server",
		Run:   func(cmd *cobra.Command, args []string) {},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) {},
	})

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.GenBashCompletion(buf)

	output := buf.String()
	if output == "" {
		t.Error("Bash completion generation produced no output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("leanproxy")) {
		t.Error("Bash completion should contain 'leanproxy'")
	}
}

func TestGenerateZshCompletion(t *testing.T) {
	cmd := &cobra.Command{
		Use: "leanproxy",
		Run: func(cmd *cobra.Command, args []string) {},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Start the proxy server",
		Run:   func(cmd *cobra.Command, args []string) {},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) {},
	})

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.GenZshCompletion(buf)

	output := buf.String()
	if output == "" {
		t.Error("Zsh completion generation produced no output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("leanproxy")) {
		t.Error("Zsh completion should contain 'leanproxy'")
	}
}

func TestGenerateFishCompletion(t *testing.T) {
	cmd := &cobra.Command{
		Use: "leanproxy",
		Run: func(cmd *cobra.Command, args []string) {},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Start the proxy server",
		Run:   func(cmd *cobra.Command, args []string) {},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) {},
	})

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.GenFishCompletion(buf, true)

	output := buf.String()
	if output == "" {
		t.Error("Fish completion generation produced no output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("leanproxy")) {
		t.Error("Fish completion should contain 'leanproxy'")
	}
}

func TestGeneratePowerShellCompletion(t *testing.T) {
	cmd := &cobra.Command{
		Use: "leanproxy",
		Run: func(cmd *cobra.Command, args []string) {},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Start the proxy server",
		Run:   func(cmd *cobra.Command, args []string) {},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) {},
	})

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.GenPowerShellCompletion(buf)

	output := buf.String()
	if output == "" {
		t.Error("PowerShell completion generation produced no output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("leanproxy")) {
		t.Error("PowerShell completion should contain 'leanproxy'")
	}
}

func TestCustomCompleters(t *testing.T) {
	tests := []struct {
		name     string
		complete func(string) []string
		prefix   string
		wantLen  int
	}{
		{
			name:     "config file path completer",
			complete: completeConfigPath,
			prefix:   "",
			wantLen:  5,
		},
		{
			name:     "log level completer",
			complete: completeLogLevel,
			prefix:   "d",
			wantLen:  1,
		},
		{
			name:     "log level error partial",
			complete: completeLogLevel,
			prefix:   "e",
			wantLen:  1,
		},
		{
			name:     "token URI completer empty prefix",
			complete: completeTokenURI,
			prefix:   "",
			wantLen:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.complete(tt.prefix)
			if len(got) != tt.wantLen {
				t.Errorf("expected %d completions for prefix %q, got %d", tt.wantLen, tt.prefix, len(got))
			}
		})
	}
}

func TestConfigPathCompletion(t *testing.T) {
	completions := completeConfigPath("")
	if len(completions) == 0 {
		t.Error("config path completer should return at least empty list")
	}
}

func TestLogLevelCompletion(t *testing.T) {
	completions := completeLogLevel("")
	expected := []string{"debug", "info", "warn", "error"}
	for _, exp := range expected {
		found := false
		for _, c := range completions {
			if c == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected log level %q in completions", exp)
		}
	}
}

func TestTokenURICompletion(t *testing.T) {
	completions := completeTokenURI("")
	expectedSchemes := []string{"api://", "oidc://", "oauth://"}
	for _, scheme := range expectedSchemes {
		found := false
		for _, c := range completions {
			if c == scheme {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected token URI scheme %q in completions", scheme)
		}
	}
}

func TestSocketPathCompletion(t *testing.T) {
	completions := completeSocketPath("")
	if completions == nil {
		t.Error("socket path completer should return a list, not nil")
	}
}

func TestRegistryURLCompletion(t *testing.T) {
	completions := completeRegistryURL("")
	if completions == nil {
		t.Error("registry URL completer should return a list (may be empty)")
	}
}

func runCompletionCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	noDesc = false
	var out bytes.Buffer
	RootCmd.SetOut(&out)
	RootCmd.SetErr(&out)
	RootCmd.SetArgs(append([]string{"completion"}, args...))
	defer func() {
		noDesc = false
		RootCmd.SetOut(nil)
		RootCmd.SetErr(nil)
		RootCmd.SetArgs(nil)
	}()
	err := RootCmd.Execute()
	return out.String(), err
}

// The generated scripts must complete the whole CLI (leanproxy-mcp), not the
// "completion" subcommand they are generated from.
func TestCompletionCmdTargetsRootCommand(t *testing.T) {
	for _, tc := range []struct{ shell, want string }{
		{"bash", "complete -o default -F __start_leanproxy-mcp leanproxy-mcp"},
		{"zsh", "#compdef leanproxy-mcp"},
		{"fish", "complete -c leanproxy-mcp"},
		{"powershell", "Register-ArgumentCompleter -CommandName 'leanproxy-mcp'"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			out, err := runCompletionCmd(t, tc.shell)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("%s completion missing %q", tc.shell, tc.want)
			}
			if strings.Contains(out, "#compdef completion") || strings.Contains(out, "__start_completion") {
				t.Errorf("%s completion was generated for the completion subcommand, not the root", tc.shell)
			}
		})
	}
}

func TestCompletionCmdNoDesc(t *testing.T) {
	withDesc, err := runCompletionCmd(t, "zsh")
	if err != nil {
		t.Fatal(err)
	}
	withoutDesc, err := runCompletionCmd(t, "zsh", "--no-desc")
	if err != nil {
		t.Fatalf("--no-desc must be parsed as a flag: %v", err)
	}
	if withDesc == withoutDesc {
		t.Error("--no-desc had no effect on the zsh completion script")
	}
	if !strings.Contains(withoutDesc, "__completeNoDesc") {
		t.Error("zsh --no-desc script should request completions without descriptions")
	}
}

func TestCompletionCmdRejectsUnknownShell(t *testing.T) {
	if _, err := runCompletionCmd(t, "tcsh"); err == nil {
		t.Error("expected an error for an unsupported shell")
	}
}
