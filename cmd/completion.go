package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	noDesc         bool
	completionDesc string
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate and install shell completion scripts for bash, zsh, fish, or PowerShell.

This command outputs the completion script to stdout. Redirect to a file to save it.

Examples:
  # Generate bash completion and display it
  leanproxy completion bash

  # Generate zsh completion and display it
  leanproxy completion zsh

  # Generate fish completion
  leanproxy completion fish

  # Generate PowerShell completion
  leanproxy completion powershell

  # Install bash completion to system directory
  leanproxy completion bash > /etc/bash_completion.d/leanproxy

  # Install zsh completion to home directory
  leanproxy completion zsh > "${HOME}/.zsh/completions/_leanproxy"

  # Install fish completion
  leanproxy completion fish > ~/.config/fish/completions/leanproxy.fish
`,
	DisableFlagParsing: true,
	RunE:               runCompletion,
	ValidArgs:          []string{"bash", "zsh", "fish", "powershell"},
}

func init() {
	RootCmd.AddCommand(completionCmd)
	completionCmd.Flags().BoolVar(&noDesc, "no-desc", false, "Suppress command descriptions")
	completionCmd.Flags().StringVar(&completionDesc, "description", "", "Custom completion description")
}

func runCompletion(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		if err := cmd.Usage(); err != nil {
			return fmt.Errorf("completion: %w", err)
		}
		return nil
	}

	shell := args[0]

	switch shell {
	case "bash":
		generateBashCompletion(cmd)
	case "zsh":
		generateZshCompletion(cmd)
	case "fish":
		generateFishCompletion(cmd)
	case "powershell":
		generatePowerShellCompletion(cmd)
	default:
		return fmt.Errorf("completion: unsupported shell %q (supported: bash, zsh, fish, powershell)", shell)
	}
	return nil
}

func generateBashCompletion(cmd *cobra.Command) {
	cmd.GenBashCompletion(os.Stdout)
}

func generateZshCompletion(cmd *cobra.Command) {
	if err := cmd.GenZshCompletion(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "completion: failed to generate zsh completion: %v\n", err)
		os.Exit(1)
	}
}

func generateFishCompletion(cmd *cobra.Command) {
	if err := cmd.GenFishCompletion(os.Stdout, !noDesc); err != nil {
		fmt.Fprintf(os.Stderr, "completion: failed to generate fish completion: %v\n", err)
		os.Exit(1)
	}
}

func generatePowerShellCompletion(cmd *cobra.Command) {
	if err := cmd.GenPowerShellCompletion(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "completion: failed to generate powershell completion: %v\n", err)
		os.Exit(1)
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

func registerCustomCompletions(cmd *cobra.Command) {
	cmd.RegisterFlagCompletionFunc("config", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeConfigPath(toComplete), cobra.ShellCompDirectiveDefault
	})

	cmd.RegisterFlagCompletionFunc("log-level", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeLogLevel(toComplete), cobra.ShellCompDirectiveDefault
	})

	cmd.RegisterFlagCompletionFunc("socket-path", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeSocketPath(toComplete), cobra.ShellCompDirectiveDefault
	})

	cmd.RegisterFlagCompletionFunc("registry-url", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeRegistryURL(toComplete), cobra.ShellCompDirectiveDefault
	})

	cmd.RegisterFlagCompletionFunc("token-uri", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeTokenURI(toComplete), cobra.ShellCompDirectiveDefault
	})
}

func init() {
	registerCustomCompletions(RootCmd)
}
