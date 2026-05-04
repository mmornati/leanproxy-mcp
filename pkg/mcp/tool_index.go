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
		Name:        "search_tools",
		Description: "Search for tools across all configured MCP servers. **IMPORTANT:** Always call this first to discover available server_name and tool_name before invoking any tool. Returns tool names, descriptions, and parameters.",
		Categories:  []string{"discovery", "meta"},
		Examples: []ToolExample{
			{
				Input: map[string]interface{}{
					"query": "github",
				},
				Description: "Find all tools related to GitHub operations",
			},
			{
				Input: map[string]interface{}{
					"query":        "activity",
					"max_description_chars": 150,
				},
				Description: "Search for 'activity' tools with shorter descriptions",
			},
			{
				Input: map[string]interface{}{
					"query": "issues",
				},
				Description: "Find all issue-tracking tools across servers",
			},
		},
		Returns: ReturnSchema{
			Type:        "object",
			Description: "Returns a content block with formatted tool results",
			Fields: []FieldDescription{
				{Name: "content", Type: "array", Desc: "Array of text content blocks"},
			},
		},
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {
					"type": "string",
					"description": "Search query (e.g., 'github issues', 'garmin activities', 'filesystem'). Supports fuzzy matching."
				},
				"max_description_chars": {
					"type": "number",
					"description": "Maximum characters for tool descriptions (default: 200, min: 50, max: 500)",
					"default": 200
				}
			},
			"required": ["query"]
		}`),
	},
	{
		Name:        "invoke_tool",
		Description: "Invoke a tool on a configured MCP server. **Required:** First use search_tools to discover server_name and tool_name, then pass them as arguments.",
		Categories:  []string{"execution", "meta"},
		Examples: []ToolExample{
			{
				Input: map[string]interface{}{
					"server": "github",
					"tool":   "github_list_issues",
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
					"tool":   "github_search_issues",
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
					"description": "Server name from search_tools (e.g., 'github', 'garmin', 'filesystem'). Must be a configured MCP server."
				},
				"tool": {
					"type": "string",
					"description": "Tool name from search_tools (e.g., 'github_list_issues', 'garmin_get_activities'). Do NOT prefix with server name."
				},
				"arguments": {
					"type": "object",
					"description": "Tool arguments as key-value pairs. Refer to search_tools output for available parameters."
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