package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

var (
	namespaceServers string
	namespaceDesc    string
	namespaceAssign  string
)

var namespaceCmd = &cobra.Command{
	Use:   "namespace [list|add|assign]",
	Short: "Manage MCP server namespaces",
	Long: `Manage hierarchical namespaces for organizing MCP servers.

Namespaces allow multi-team organizations to manage access to MCP servers
by grouping them under logical organizational units.

Examples:
  # List all namespaces
  leanproxy namespace list

  # Add a new namespace
  leanproxy namespace add engineering --servers=github,jira --description="Engineering team"

  # Assign a server to a namespace
  leanproxy namespace assign engineering github

  # List tools in a specific namespace
  leanproxy namespace list engineering --tools
`,
	SilenceUsage: true,
}

var namespaceListCmd = &cobra.Command{
	Use:   "list [namespace]",
	Short: "List namespaces or tools in a namespace",
	Args:  cobra.RangeArgs(0, 1),
	RunE:  runNamespaceList,
}

var namespaceAddCmd = &cobra.Command{
	Use:   "add <namespace>",
	Short: "Add a new namespace",
	Args:  cobra.ExactArgs(1),
	RunE:  runNamespaceAdd,
}

var namespaceAssignCmd = &cobra.Command{
	Use:   "assign <namespace> <server>",
	Short: "Assign a server to a namespace",
	Args:  cobra.ExactArgs(2),
	RunE:  runNamespaceAssign,
}

func init() {
	RootCmd.AddCommand(namespaceCmd)

	namespaceCmd.AddCommand(namespaceListCmd)
	namespaceCmd.AddCommand(namespaceAddCmd)
	namespaceCmd.AddCommand(namespaceAssignCmd)

	namespaceListCmd.Flags().Bool("tools", false, "List tools in the namespace")
	namespaceAddCmd.Flags().StringVar(&namespaceServers, "servers", "", "Comma-separated list of servers")
	namespaceAddCmd.Flags().StringVar(&namespaceDesc, "description", "", "Namespace description")
	namespaceAssignCmd.Flags().StringVar(&namespaceAssign, "to", "", "Target namespace for assignment")
}

func runNamespaceList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	logger := slog.Default()

	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("config flag: %w", err)
	}

	mgr := registry.NewNamespaceManager(logger)

	if cfgPath != "" {
		file, err := os.Open(cfgPath)
		if err != nil {
			return fmt.Errorf("open config: %w", err)
		}
		defer file.Close()

		if err := mgr.Load(ctx, file); err != nil {
			return fmt.Errorf("load namespace config: %w", err)
		}
	}

	showTools, _ := cmd.Flags().GetBool("tools")

	if len(args) > 0 {
		nsName := args[0]

		if showTools {
			tools, err := mgr.ListToolsInNamespace(ctx, nsName)
			if err != nil {
				return fmt.Errorf("list tools: %w", err)
			}

			fmt.Printf("Tools in namespace '%s':\n", nsName)
			for _, tool := range tools {
				fmt.Printf("  - %s (server: %s)\n", tool.Name, tool.ServerID)
			}
		} else {
			ns, err := mgr.GetNamespace(ctx, nsName)
			if err != nil {
				return fmt.Errorf("get namespace: %w", err)
			}

			fmt.Printf("Namespace: %s\n", ns.Name)
			if ns.Description != "" {
				fmt.Printf("Description: %s\n", ns.Description)
			}
			fmt.Printf("Servers: %v\n", ns.Servers)
			if len(ns.Children) > 0 {
				fmt.Printf("Children: %v\n", getChildNames(ns.Children))
			}
			if len(ns.AllowedClients) > 0 {
				fmt.Printf("Allowed Clients: %v\n", ns.AllowedClients)
			}
		}
	} else {
		namespaces := mgr.GetAllNamespaces(ctx)
		if len(namespaces) == 0 {
			fmt.Println("No namespaces configured")
			return nil
		}

		fmt.Println("Configured namespaces:")
		for _, ns := range namespaces {
			fmt.Printf("  - %s", ns.Name)
			if ns.Description != "" {
				fmt.Printf(": %s", ns.Description)
			}
			fmt.Printf(" [%d servers]\n", len(ns.Servers))
		}
	}

	return nil
}

func runNamespaceAdd(cmd *cobra.Command, args []string) error {
	nsName := args[0]

	serversStr, _ := cmd.Flags().GetString("servers")
	desc, _ := cmd.Flags().GetString("description")

	fmt.Printf("Adding namespace '%s'\n", nsName)
	if serversStr != "" {
		fmt.Printf("  Servers: %s\n", serversStr)
	}
	if desc != "" {
		fmt.Printf("  Description: %s\n", desc)
	}

	fmt.Println("\nNote: Namespace configuration should be added to leanproxy.yaml")
	fmt.Println("Example configuration:")
	fmt.Println("  namespaces:")
	fmt.Println("    " + nsName + ":")
	if serversStr != "" {
		fmt.Println("      servers:")
		for _, s := range strings.Split(serversStr, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				fmt.Printf("        - %s\n", s)
			}
		}
	}
	if desc != "" {
		fmt.Printf("      description: \"%s\"\n", desc)
	}

	return nil
}

func runNamespaceAssign(cmd *cobra.Command, args []string) error {
	nsName := args[0]
	serverID := args[1]

	fmt.Printf("Assigning server '%s' to namespace '%s'\n", serverID, nsName)
	fmt.Println("\nNote: This operation requires updating leanproxy.yaml")
	fmt.Printf("Add '%s' to the '%s' namespace servers list.\n", serverID, nsName)

	return nil
}

func getChildNames(children map[string]*registry.Namespace) []string {
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	return names
}