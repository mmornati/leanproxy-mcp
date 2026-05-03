package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var helpCmd = &cobra.Command{
	Use:   "help",
	Short: "Help about any command",
	Long:  `Help provides help for any command in the application.`,
	RunE:  runHelp,
}

func init() {
	RootCmd.AddCommand(helpCmd)
}

func runHelp(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		cmd.Help()
		return nil
	}

	subCmd, _, err := RootCmd.Find(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tokengate: error: %v\n", err)
		os.Exit(ExitMisuse)
	}

	subCmd.Help()
	return nil
}