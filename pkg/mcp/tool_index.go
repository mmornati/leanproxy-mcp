package mcp

import "encoding/json"

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Examples    []ToolExample  `json:"examples,omitempty"`
	Returns     ReturnSchema   `json:"returns,omitempty"`
	Categories  []string      `json:"categories,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolExample struct {
	Input       map[string]interface{} `json:"input"`
	Description string                `json:"description"`
}

type ReturnSchema struct {
	Type         string            `json:"type"`
	Description string            `json:"description"`
	Fields       []FieldDescription `json:"fields,omitempty"`
}

type FieldDescription struct {
	Name     string `json:"name"`
	Type    string `json:"type"`
	Req     bool   `json:"required,omitempty"`
	Desc    string `json:"description"`
}

var LeanproxyTools = []ToolDefinition{
	{
		Name:        "list_tools",
		Description: "List all tools available on a specific MCP server. **IMPORTANT:** Always call list_servers first to get available server names, then use this tool to see tools on a specific server.",
		Categories:  []string{"discovery", "meta"},
		Examples: []ToolExample{
			{
				Input: map[string]interface{}{
					"server_name": "github",
				},
				Description: "List all tools available on the github server",
			},
			{
				Input: map[string]interface{}{
					"server_name": "garmin",
				},
				Description: "List all tools available on the garmin server",
			},
			{
				Input: map[string]interface{}{
					"server_name":        "github",
					"max_description_chars": 150,
				},
				Description: "List github tools with shorter descriptions",
			},
		},
		Returns: ReturnSchema{
			Type:        "object",
			Description: "Returns a content block with formatted tool list for the specified server",
			Fields: []FieldDescription{
				{Name: "content", Type: "array", Desc: "Array of text content blocks"},
			},
		},
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"server_name": {
					"type": "string",
					"description": "MCP server name (from list_servers). Required - identifies which server's tools to list."
				},
				"max_description_chars": {
					"type": "number",
					"description": "Maximum characters for tool descriptions (default: 200, min: 50, max: 500)",
					"default": 200
				}
			},
			"required": ["server_name"]
		}`),
	},
	{
		Name:        "invoke_tool",
		Description: "Invoke a tool on a configured MCP server. **Required:** First use list_servers to get server names, then use list_tools to discover available tools on that server.",
		Categories:  []string{"execution", "meta"},
		Examples: []ToolExample{
			{
				Input: map[string]interface{}{
					"server": "github",
					"tool":   "list_issues",
					"arguments": map[string]interface{}{
						"owner":  "mmornati",
						"repo":   "leanproxy-mcp",
						"state":  "open",
						"perPage": 10,
					},
				},
				Description: "List open issues on leanproxy-mcp repository",
			},
			{
				Input: map[string]interface{}{
					"server": "github",
					"tool":   "search_issues",
					"arguments": map[string]interface{}{
						"query":  "is:issue is:open label:bug",
						"owner":  "mmornati",
						"repo":   "leanproxy-mcp",
						"perPage": 5,
					},
				},
				Description: "Search for open bug issues",
			},
		},
		Returns: ReturnSchema{
			Type:        "object",
			Description: "Returns the result from the remote MCP tool invocation",
			Fields: []FieldDescription{
				{Name: "content", Type: "array", Desc: "Array of content blocks from tool"},
			},
		},
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"server": {
					"type": "string",
					"description": "Server name from list_servers (e.g., 'github', 'garmin', 'filesystem'). Must be a configured MCP server."
				},
				"tool": {
					"type": "string",
					"description": "Tool name from list_tools (e.g., 'list_issues', 'get_activities'). Do NOT prefix with server name."
				},
				"arguments": {
					"type": "object",
					"description": "Tool arguments as key-value pairs. Refer to list_tools output for available parameters."
				}
			},
			"required": ["server", "tool"],
			"additionalProperties": false
		}`),
	},
}

func GetToolDefinition(name string) *ToolDefinition {
	for i := range LeanproxyTools {
		if LeanproxyTools[i].Name == name {
			return &LeanproxyTools[i]
		}
	}
	return nil
}

func GetAllToolDefinitions() []ToolDefinition {
	return LeanproxyTools
}