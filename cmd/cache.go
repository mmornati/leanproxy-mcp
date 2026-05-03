package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Inspect and manage the tool cache",
	Long:  `Inspect the persisted tool cache to see what tools have been indexed from MCP servers.`,
	Run:   runCache,
}

var cacheFlags struct {
	server   string
	list     bool
	search   string
	jsonOut  bool
	clear    bool
	location bool
}

func init() {
	cacheCmd.Flags().BoolVar(&cacheFlags.list, "list", false, "List all servers with cached tools")
	cacheCmd.Flags().StringVar(&cacheFlags.server, "server", "", "Show cached tools for a specific server")
	cacheCmd.Flags().StringVar(&cacheFlags.search, "search", "", "Search cached tools by name or description")
	cacheCmd.Flags().BoolVar(&cacheFlags.jsonOut, "json", false, "Output in JSON format")
	cacheCmd.Flags().BoolVar(&cacheFlags.clear, "clear", false, "Clear cache for specified server (use --server)")
	cacheCmd.Flags().BoolVar(&cacheFlags.location, "location", false, "Show the cache directory location")
	RootCmd.AddCommand(cacheCmd)
}

func runCache(cmd *cobra.Command, args []string) {
	if cacheFlags.location {
		showCacheLocation()
		return
	}

	if cacheFlags.clear {
		clearCache()
		return
	}

	if cacheFlags.list {
		listCachedServers()
		return
	}

	if cacheFlags.server != "" {
		showServerCache(cacheFlags.server, cacheFlags.search)
		return
	}

	showCacheLocation()
	fmt.Println("\nUse --help to see available options")
}

func showCacheLocation() {
	fc, err := toolstore.NewFileCache(nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Tool cache location: %s\n", fc.GetCacheDir())
}

func listCachedServers() {
	fc, err := toolstore.NewFileCache(nil)
	if err != nil {
		fmt.Printf("Error accessing cache: %v\n", err)
		return
	}

	servers, err := fc.ListCachedServers()
	if err != nil {
		fmt.Printf("Error listing cache: %v\n", err)
		return
	}

	if len(servers) == 0 {
		fmt.Println("No cached tool data found")
		return
	}

	fmt.Printf("Servers with cached tools (%d):\n\n", len(servers))
	for _, name := range servers {
		fmt.Printf("  - %s\n", name)
	}
	fmt.Println("\nUse --server <name> to see tools for a specific server")
}

func showServerCache(serverName string, searchQuery string) {
	fc, err := toolstore.NewFileCache(nil)
	if err != nil {
		fmt.Printf("Error accessing cache: %v\n", err)
		return
	}

	tools, err := fc.GetTools(serverName)
	if err != nil {
		fmt.Printf("Error reading cache for %s: %v\n", serverName, err)
		return
	}

	if tools == nil || len(tools) == 0 {
		fmt.Printf("No cached tools found for server: %s\n", serverName)
		return
	}

	fmt.Printf("Cached tools for %s (%d total):\n\n", serverName, len(tools))

	searchLower := strings.ToLower(searchQuery)

	for _, tool := range tools {
		if searchQuery != "" {
			combined := strings.ToLower(tool.Name + " " + tool.Description)
			if !strings.Contains(combined, searchLower) {
				continue
			}
		}

		if cacheFlags.jsonOut {
			data, _ := json.MarshalIndent(tool, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("  %s\n", tool.Name)
			if tool.Description != "" {
				fmt.Printf("    %s\n", tool.Description)
			}
			if tool.InputSchema != nil && len(tool.InputSchema) > 0 {
				var schema map[string]interface{}
				json.Unmarshal(tool.InputSchema, &schema)
				if props, ok := schema["properties"].(map[string]interface{}); ok {
					fmt.Printf("    Parameters:\n")
					for paramName, prop := range props {
						if propMap, ok := prop.(map[string]interface{}); ok {
							paramType, _ := propMap["type"].(string)
							fmt.Printf("      - %s (%s)\n", paramName, paramType)
						}
					}
				}
			}
		}
	}
}

func clearCache() {
	if cacheFlags.server == "" {
		fmt.Println("Error: --clear requires --server <name>")
		return
	}

	fc, err := toolstore.NewFileCache(nil)
	if err != nil {
		fmt.Printf("Error accessing cache: %v\n", err)
		return
	}

	if err := fc.Invalidate(cacheFlags.server); err != nil {
		fmt.Printf("Error clearing cache for %s: %v\n", cacheFlags.server, err)
		return
	}

	fmt.Printf("Cache cleared for server: %s\n", cacheFlags.server)
}
