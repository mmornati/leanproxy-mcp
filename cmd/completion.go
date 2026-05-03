package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh]",
	Short: "Generate shell completion scripts",
	Long: `Generate and install shell completion scripts for bash or zsh.

Examples:
  # Generate bash completion and display it
  leanproxy completion bash

  # Generate zsh completion and display it
  leanproxy completion zsh

  # Install bash completion to system directory
  leanproxy completion bash > /etc/bash_completion.d/leanproxy

  # Install zsh completion to home directory
  leanproxy completion zsh > "${HOME}/.zsh/completions/_leanproxy"
`,
	Run:               runCompletion,
	Args:              cobra.ExactArgs(1),
	ValidArgs:         []string{"bash", "zsh"},
}

func init() {
	RootCmd.AddCommand(completionCmd)
}

func runCompletion(cmd *cobra.Command, args []string) {
	shell := args[0]

	switch shell {
	case "bash":
		generateBashCompletion(cmd)
	case "zsh":
		generateZshCompletion(cmd)
	default:
		fmt.Fprintf(os.Stderr, "Unsupported shell: %s\n", shell)
		os.Exit(1)
	}
}

func generateBashCompletion(cmd *cobra.Command) {
	cmd.GenBashCompletion(os.Stdout)
}

func generateZshCompletion(cmd *cobra.Command) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	zshCompletionDir := filepath.Join(homeDir, ".leanproxy", "completions")
	if err := os.MkdirAll(zshCompletionDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create completion directory: %v\n", err)
		os.Exit(1)
	}

	zshScript := `#!/bin/zsh
#compdef leanproxy

_leanproxy() {
    local -a commands
    commands=(
        'serve:Start the JSON-RPC streaming proxy server'
        'version:Print version information'
        'completion:Generate shell completion scripts'
        'config:Configuration management'
        'init:Initialize new configuration'
        'migrate:Migrate configuration from other tools'
    )

    if (( CURRENT == 2 )); then
        _describe 'command' commands
    else
        case "${words[2]}" in
            serve)
                _arguments \
                    '--listen[Address to listen on]:address:' \
                    '--upstream[Upstream JSON-RPC server URL]:url:' \
                    '--config[Path to config file]:file:_files' \
                    '--dry-run[Preview actions without making changes]' \
                    '-v[Enable verbose logging]' \
                    '--log-level[Log level]:level:(debug info warn error)'
                ;;
            version)
                _arguments '-h[Show help]'
                ;;
            completion)
                _arguments 'bash:Generate bash completion' 'zsh:Generate zsh completion'
                ;;
            config)
                _arguments 'init:Initialize configuration' 'validate:Validate configuration'
                ;;
            migrate)
                _arguments '--format[Migration format]:format:(opencode cursor claude vscode)' '--dry-run[Preview migration]'
                ;;
        esac
    fi
}

_leanproxy "$@"
`

	completionFile := filepath.Join(zshCompletionDir, "_leanproxy")
	if err := os.WriteFile(completionFile, []byte(zshScript), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write zsh completion file: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "Zsh completion installed to %s\n", completionFile)
	fmt.Fprintln(os.Stdout, "Add the following to your ~/.zshrc to enable it:")
	fmt.Fprintf(os.Stdout, "  autoload -U compinit; compinit\n")
	fmt.Fprintf(os.Stdout, "  fpath=(%s $fpath)\n", zshCompletionDir)
}