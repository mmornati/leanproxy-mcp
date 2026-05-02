package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionString = "0.1.0"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Print the version and build information for leanproxy.`,
	Run:   runVersion,
}

func init() {
	RootCmd.AddCommand(versionCmd)
}

func runVersion(cmd *cobra.Command, args []string) {
	fmt.Printf("leanproxy version %s\n", versionString)
}
