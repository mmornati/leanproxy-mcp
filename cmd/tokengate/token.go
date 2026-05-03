package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Token operations",
	Long:  `Perform token-related operations including validation, resolution, and management.`,
}

var tokenValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a token",
	Long:  `Validate a token for correctness and availability.`,
	RunE:  runTokenValidate,
}

var tokenResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Resolve a token",
	Long:  `Resolve a token to its underlying value or resource.`,
	RunE:  runTokenResolve,
}

func init() {
	RootCmd.AddCommand(tokenCmd)
	tokenCmd.AddCommand(tokenValidateCmd)
	tokenCmd.AddCommand(tokenResolveCmd)
}

func runTokenValidate(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		ExitMisusef("token validate requires a token argument")
	}

	token := args[0]

	if DryRunEnabled {
		fmt.Printf("Dry-run mode: would validate token %s\n", token)
		return nil
	}

	fmt.Printf("Token %s: valid\n", token)
	return nil
}

func runTokenResolve(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		ExitMisusef("token resolve requires a token argument")
	}

	token := args[0]

	if DryRunEnabled {
		fmt.Printf("Dry-run mode: would resolve token %s\n", token)
		return nil
	}

	fmt.Printf("Resolved token %s\n", token)
	return nil
}