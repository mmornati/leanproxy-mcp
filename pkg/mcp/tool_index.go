package mcp

import "encoding/json"

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Examples    []ToolExample   `json:"examples,omitempty"`
	Returns     ReturnSchema    `json:"returns,omitempty"`
	Categories  []string        `json:"categories,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolExample struct {
	Input       map[string]interface{} `json:"input"`
	Description string                 `json:"description"`
}

type ReturnSchema struct {
	Type        string             `json:"type"`
	Description string             `json:"description"`
	Fields      []FieldDescription `json:"fields,omitempty"`
}

type FieldDescription struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Req  bool   `json:"required,omitempty"`
	Desc string `json:"description"`
}

// LeanproxyTools are the gateway tools the proxy exposes in tools/list. The
// recommended flow is search_tools -> invoke_tool; list_servers and
// list_tools stay for browsing.
var LeanproxyTools = []ToolDefinition{
	{
		Name:        "search_tools",
		Description: "Find the best tools for a task across all servers. Returns the top matches with their input schema; call them with invoke_tool.",
		Categories:  []string{"discovery", "meta"},
		Returns: ReturnSchema{
			Type:        "object",
			Description: "Returns a content block with one line per matching tool, best first",
			Fields: []FieldDescription{
				{Name: "content", Type: "array", Desc: "Array of text content blocks"},
			},
		},
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string"},
				"k": {"type": "integer", "default": 5, "maximum": 20},
				"server": {"type": "string", "description": "optional filter"}
			},
			"required": ["query"]
		}`),
	},
	{
		Name:        "list_servers",
		Description: "List configured MCP servers: transport, state, tool count.",
		Categories:  []string{"discovery", "meta"},
		Returns: ReturnSchema{
			Type:        "object",
			Description: "Returns a content block with one line per server",
			Fields: []FieldDescription{
				{Name: "content", Type: "array", Desc: "Array of text content blocks"},
			},
		},
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	},
	{
		Name:        "list_tools",
		Description: "Browse all tools of one server. Prefer search_tools.",
		Categories:  []string{"discovery", "meta"},
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
					"description": "From list_servers."
				},
				"max_description_chars": {
					"type": "number",
					"description": "Max desc length (default 200)",
					"default": 200
				}
			},
			"required": ["server_name"]
		}`),
	},
	{
		Name:        "invoke_tool",
		Description: "Invoke a tool found by search_tools.",
		Categories:  []string{"execution", "meta"},
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
					"description": "Server of the tool."
				},
				"tool": {
					"type": "string",
					"description": "Tool name, no server prefix."
				},
				"arguments": {
					"type": "object",
					"description": "Tool arguments."
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
