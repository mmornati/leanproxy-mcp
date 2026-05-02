package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage MCP server configurations",
	Long:  `Add, remove, list, enable, or disable MCP servers in leanproxy_servers.yaml`,
}

func init() {
	RootCmd.AddCommand(serverCmd)
}

func userConfigPath() string {
	if path := os.Getenv("LEANPROXY_CONFIG"); path != "" {
		return path
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return filepath.Join(home, ".config", "leanproxy_servers.yaml")
}

var addCmd = &cobra.Command{
	Use:   "add <name> <command> [args...]",
	Short: "Add a new MCP server",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runServerAdd,
}

var addFlags struct {
	env        []string
	cwd        string
	transport  string
}

func init() {
	addCmd.Flags().StringArrayVar(&addFlags.env, "env", []string{}, "Environment variables (KEY=value)")
	addCmd.Flags().StringVar(&addFlags.cwd, "cwd", "", "Working directory for the command")
	addCmd.Flags().StringVar(&addFlags.transport, "transport", "stdio", "Transport type (stdio, http, sse)")
	serverCmd.AddCommand(addCmd)
}

func runServerAdd(cmd *cobra.Command, args []string) error {
	name := args[0]
	command := args[1]
	commandArgs := args[2:]

	_, err := exec.LookPath(command)
	if err != nil {
		return fmt.Errorf("command not found in PATH: %s", command)
	}

	transport := registry.TransportType(addFlags.transport)
	switch transport {
	case registry.TransportStdio, registry.TransportHTTP, registry.TransportSSE:
	default:
		return fmt.Errorf("invalid transport type: %s (must be stdio, http, or sse)", addFlags.transport)
	}

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		cfg = &migrate.Config{
			Version: "1.0",
			Servers: []*migrate.ServerConfig{},
		}
	}

	for _, srv := range cfg.Servers {
		if srv.Name == name {
			return fmt.Errorf("server %q already exists", name)
		}
	}

	stdio := &migrate.StdioConfig{
		Command: command,
		Args:    commandArgs,
		CWD:     addFlags.cwd,
		Env:     addFlags.env,
	}
	if stdio.CWD == "" {
		stdio.CWD = filepath.Dir(command)
	}

	enabled := true
	newServer := &migrate.ServerConfig{
		Name:      name,
		Transport: transport,
		Stdio:     stdio,
		Enabled:   &enabled,
		Timeout:   "30s",
		ConnectTimeout: "10s",
	}

	if transport != registry.TransportStdio {
		newServer.Stdio = nil
	}

	cfg.Servers = append(cfg.Servers, newServer)

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q added successfully\n", name)
	return nil
}

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runServerRemove,
}

func init() {
	serverCmd.AddCommand(removeCmd)
}

func runServerRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return fmt.Errorf("no servers configured")
	}

	found := -1
	for i, srv := range cfg.Servers {
		if srv.Name == name {
			found = i
			break
		}
	}

	if found == -1 {
		return fmt.Errorf("server %q not found", name)
	}

	fmt.Printf("Remove server %q? [y/N]: ", name)
	var response string
	fmt.Scanln(&response)
	if response != "y" && response != "Y" {
		fmt.Println("Cancelled.")
		return nil
	}

	cfg.Servers = append(cfg.Servers[:found], cfg.Servers[found+1:]...)

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q removed successfully\n", name)
	return nil
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured MCP servers",
	RunE:  runServerList,
}

var listFlags struct {
	source string
}

func init() {
	listCmd.Flags().StringVar(&listFlags.source, "source", "", "Filter by source (opencode, claude, vscode, cursor, generic)")
	serverCmd.AddCommand(listCmd)
}

func runServerList(cmd *cobra.Command, args []string) error {
	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		fmt.Println("No servers configured.")
		return nil
	}

	fmt.Printf("%-20s %-10s %-15s %s\n", "NAME", "STATUS", "TRANSPORT", "COMMAND")
	fmt.Println("--------------------------------------------------------------")

	for _, srv := range cfg.Servers {
		status := "enabled"
		if srv.Enabled != nil && !*srv.Enabled {
			status = "disabled"
		}

		cmdStr := ""
		if srv.Stdio != nil {
			cmdStr = srv.Stdio.Command
			if len(srv.Stdio.Args) > 0 {
				cmdStr += " " + joinStrings(srv.Stdio.Args)
			}
		} else if srv.HTTP != nil {
			cmdStr = srv.HTTP.URL
		}

		fmt.Printf("%-20s %-10s %-15s %s\n", srv.Name, status, srv.Transport, cmdStr)
	}

	fmt.Printf("\n%d server(s)\n", len(cfg.Servers))
	return nil
}

var enableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable a disabled MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runServerEnable,
}

func init() {
	serverCmd.AddCommand(enableCmd)
}

func runServerEnable(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no servers configured")
	}

	found := false
	for _, srv := range cfg.Servers {
		if srv.Name == name {
			enabled := true
			srv.Enabled = &enabled
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("server %q not found", name)
	}

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q enabled\n", name)
	return nil
}

var disableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runServerDisable,
}

func init() {
	serverCmd.AddCommand(disableCmd)
}

func runServerDisable(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no servers configured")
	}

	found := false
	for _, srv := range cfg.Servers {
		if srv.Name == name {
			enabled := false
			srv.Enabled = &enabled
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("server %q not found", name)
	}

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q disabled\n", name)
	return nil
}

func saveConfig(path string, cfg *migrate.Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := migrate.MarshalConfig(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func joinStrings(strs []string) string {
	result := ""
	for _, s := range strs {
		result += s + " "
	}
	return result
}
