package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Manage token registry",
	Long:  `Manage the tokengate token registry including list, add, remove, and update operations.`,
}

var registryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tokens in registry",
	Long:  `List all tokens currently registered in the tokengate registry.`,
	RunE:  runRegistryList,
}

func init() {
	RootCmd.AddCommand(registryCmd)
	registryCmd.AddCommand(registryListCmd)
}

func runRegistryList(cmd *cobra.Command, args []string) error {
	if DryRunEnabled {
		fmt.Println("Dry-run mode: would list registry entries")
		return nil
	}

	fmt.Println("Registry entries:")
	fmt.Println("  (empty)")
	return nil
}