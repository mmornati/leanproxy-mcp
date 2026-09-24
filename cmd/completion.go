package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var noDesc bool

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for bash, zsh, fish, or PowerShell.

This command outputs the completion script to stdout. Redirect to a file to save it.

Examples:
  # Bash (requires the bash-completion package)
  leanproxy-mcp completion bash > /etc/bash_completion.d/leanproxy-mcp

  # Zsh (the directory must be on your $fpath)
  leanproxy-mcp completion zsh > "${HOME}/.zsh/completions/_leanproxy-mcp"

  # Fish
  leanproxy-mcp completion fish > ~/.config/fish/completions/leanproxy-mcp.fish

  # PowerShell
  leanproxy-mcp completion powershell | Out-String | Invoke-Expression

  # Omit command descriptions from the completion candidates
  leanproxy-mcp completion zsh --no-desc
`,
	Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
	RunE:      runCompletion,
	ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
}

func init() {
	RootCmd.AddCommand(completionCmd)
	completionCmd.Flags().BoolVar(&noDesc, "no-desc", false, "Omit command descriptions from completion candidates")
}

func runCompletion(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		if err := cmd.Usage(); err != nil {
			return fmt.Errorf("completion: %w", err)
		}
		return nil
	}
	if err := generateCompletion(cmd.Root(), args[0], !noDesc, cmd.OutOrStdout()); err != nil {
		return fmt.Errorf("completion: failed to generate %s completion: %w", args[0], err)
	}
	return nil
}

// generateCompletion writes the completion script for root (the whole CLI,
// not the completion subcommand itself) in the given shell's syntax.
func generateCompletion(root *cobra.Command, shell string, includeDesc bool, w io.Writer) error {
	switch shell {
	case "bash":
		return root.GenBashCompletionV2(w, includeDesc)
	case "zsh":
		if includeDesc {
			return root.GenZshCompletion(w)
		}
		return root.GenZshCompletionNoDesc(w)
	case "fish":
		return root.GenFishCompletion(w, includeDesc)
	case "powershell":
		if includeDesc {
			return root.GenPowerShellCompletionWithDesc(w)
		}
		return root.GenPowerShellCompletion(w)
	default:
		return fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish, powershell)", shell)
	}
}

func completeConfigPath(prefix string) []string {
	var completions []string
	candidates := []string{
		"/etc/leanproxy/leanproxy_servers.yaml",
		filepath.Join(os.Getenv("HOME"), ".leanproxy", "leanproxy_servers.yaml"),
		"./leanproxy_servers.yaml",
		"./config.yaml",
		"./config.yml",
	}

	for _, path := range candidates {
		if len(prefix) == 0 || (len(path) >= len(prefix) && path[:len(prefix)] == prefix) {
			completions = append(completions, path)
		}
	}

	return completions
}

func completeLogLevel(prefix string) []string {
	levels := []string{"debug", "info", "warn", "error"}
	var matches []string
	for _, level := range levels {
		if len(prefix) == 0 || (len(level) >= len(prefix) && level[:len(prefix)] == prefix) {
			matches = append(matches, level)
		}
	}
	return matches
}

func completeTokenURI(prefix string) []string {
	schemes := []string{"api://", "oidc://", "oauth://"}
	var matches []string
	for _, scheme := range schemes {
		if len(prefix) == 0 || (len(scheme) >= len(prefix) && scheme[:len(prefix)] == prefix) {
			matches = append(matches, scheme)
		}
	}
	return matches
}

func completeSocketPath(prefix string) []string {
	matches := []string{}
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return matches
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sock" {
			socketPath := filepath.Join("/tmp", entry.Name())
			if len(prefix) == 0 || (len(socketPath) >= len(prefix) && socketPath[:len(prefix)] == prefix) {
				matches = append(matches, socketPath)
			}
		}
	}
	return matches
}

func completeRegistryURL(prefix string) []string {
	schemes := []string{"http://", "https://", "unix://"}
	var matches []string
	for _, scheme := range schemes {
		if len(prefix) == 0 || (len(scheme) >= len(prefix) && scheme[:len(prefix)] == prefix) {
			matches = append(matches, scheme)
		}
	}
	return matches
}

// registerFlagCompletion registers fn as the completion function for the
// named flag on cmd, logging (rather than ignoring) the rare error case
// where the flag does not exist or already has a completion function.
func registerFlagCompletion(cmd *cobra.Command, flag string, fn func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)) {
	if err := cmd.RegisterFlagCompletionFunc(flag, fn); err != nil {
		fmt.Fprintf(os.Stderr, "completion: failed to register completion for --%s: %v\n", flag, err)
	}
}

// registerCustomCompletions wires up completion functions for RootCmd's own
// persistent flags. It must run only after those flags have been defined
// (see the call in root.go's Execute, not an init() here): Go runs init()
// functions across files in a package in filename order, and
// "completion.go" sorts before "root.go", so calling this from an init()
// in this file registered completions before the "config"/"log-level"
// flags existed on RootCmd, which cobra reports as the flag "not existing".
//
// completeSocketPath, completeRegistryURL and completeTokenURI are kept as
// standalone helpers (see completion_test.go) but are intentionally not
// wired up here: no command in this CLI defines "socket-path",
// "registry-url" or "token-uri" flags, so registering completions for them
// would always fail the same way.
func registerCustomCompletions(cmd *cobra.Command) {
	registerFlagCompletion(cmd, "config", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeConfigPath(toComplete), cobra.ShellCompDirectiveDefault
	})

	registerFlagCompletion(cmd, "log-level", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeLogLevel(toComplete), cobra.ShellCompDirectiveDefault
	})
}
