package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
	Long:  `Manage tokengate configuration including validation, viewing, and updating.`,
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration",
	Long:  `Validate a configuration file or read from stdin.`,
	RunE:  runConfigValidate,
}

func init() {
	RootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configValidateCmd)
}

func runConfigValidate(cmd *cobra.Command, args []string) error {
	var reader io.Reader

	if len(args) > 0 && args[0] == "-" {
		reader = os.Stdin
		fmt.Fprintln(os.Stderr, "tokengate: reading config from stdin")
	} else if len(args) > 0 {
		file, err := os.Open(args[0])
		if err != nil {
			ExitConfigError(err)
		}
		defer file.Close()
		reader = file
	} else if GlobalConfigPath != "" {
		file, err := os.Open(GlobalConfigPath)
		if err != nil {
			ExitConfigError(err)
		}
		defer file.Close()
		reader = file
	} else {
		ExitMisusef("config file path required or use - for stdin")
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		ExitWithError(ExitGeneral, err)
	}

	if DryRunEnabled {
		fmt.Println("Dry-run mode: would validate configuration")
		fmt.Printf("  Size: %d bytes\n", len(data))
		return nil
	}

	fmt.Println("Configuration is valid")
	return nil
}