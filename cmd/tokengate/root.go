package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	GlobalConfigPath string
	VerboseEnabled   bool
	LogLevel         string
	DryRunEnabled    bool
	ShowVersion      bool
	versionString    = "0.1.0"
)

var RootCmd = &cobra.Command{
	Use:   "tokengate",
	Short: "tokengate - A POSIX-compliant CLI for token management",
	Long: `tokengate provides a command-line interface for managing tokens,
servers, and proxy configuration with full POSIX compliance.

Usage: tokengate [OPTIONS] COMMAND [ARGUMENTS]

Options:
  -h, --help        Show help
  -v, --verbose     Enable verbose output
  -c, --config=FILE Configuration file path
  -n, --dry-run     Preview actions without making changes

Commands:
  proxy    Manage proxy server
  registry Manage token registry
  token    Token operations
  config   Configuration management

Exit Status:
  0      Success
  1      General error
  2      Misuse
  3      Configuration error
  4      Token resolution failure
`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if ShowVersion {
			fmt.Printf("tokengate version %s\n", versionString)
			return nil
		}
		return nil
	},
}

func Execute() error {
	return RootCmd.Execute()
}

func init() {
	pflag.CommandLine.SortFlags = true

	RootCmd.PersistentFlags().BoolVar(&ShowVersion, "version", false, "Print version information")
	RootCmd.PersistentFlags().BoolP("help", "h", false, "Show help")
	RootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")
	RootCmd.PersistentFlags().StringVar(&GlobalConfigPath, "config", "", "Path to config file")
	RootCmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")
	RootCmd.PersistentFlags().BoolP("dry-run", "n", false, "Preview actions without making changes")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  `Print the version and build information for tokengate.`,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Root().Printf("tokengate version %s\n", versionString)
		},
	}
	RootCmd.AddCommand(versionCmd)

	RootCmd.SetHelpFunc(customHelp)
	RootCmd.SetUsageFunc(usageFunc)

	cobra.EnableCommandSorting = false
}

func verboseEnabled(cmd *cobra.Command) bool {
	v, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return false
	}
	return v || VerboseEnabled
}

func customHelp(cmd *cobra.Command, args []string) {
	fmt.Printf("Usage: tokengate [OPTIONS] COMMAND [ARGUMENTS]\n\n")
	fmt.Printf("tokengate - A POSIX-compliant CLI for token management\n\n")
	fmt.Printf("Options:\n")
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		format := "  -%s, --%s\t%s\n"
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			format = "  -%s, --%s=%s\t%s\n"
			fmt.Printf(format, f.Shorthand, f.Name, f.DefValue, f.Usage)
		} else if f.Shorthand != "" {
			fmt.Printf(format, f.Shorthand, f.Name, f.Usage)
		} else {
			fmt.Printf("  --%s\t%s\n", f.Name, f.Usage)
		}
	})
	fmt.Printf("\nCommands:\n")
	for _, sub := range cmd.Commands() {
		fmt.Printf("  %s\t%s\n", sub.Name(), sub.Short)
	}
	fmt.Printf("\nExit Status:\n")
	fmt.Printf("  0\tSuccess\n")
	fmt.Printf("  1\tGeneral error\n")
	fmt.Printf("  2\tMisuse\n")
	fmt.Printf("  3\tConfiguration error\n")
	fmt.Printf("  4\tToken resolution failure\n")
}

func usageFunc(cmd *cobra.Command) error {
	fmt.Fprintf(os.Stderr, "Usage: tokengate [OPTIONS] COMMAND [ARGUMENTS]\n")
	fmt.Fprintf(os.Stderr, "\nOptions:\n")
	cmd.Flags().PrintDefaults()
	fmt.Fprintf(os.Stderr, "\nCommands:\n")
	for _, sub := range cmd.Commands() {
		fmt.Fprintf(os.Stderr, "  %s\t%s\n", sub.Name(), sub.Short)
	}
	return nil
}

func logError(format string, args ...interface{}) {
	slog.Error(fmt.Sprintf(format, args...))
}