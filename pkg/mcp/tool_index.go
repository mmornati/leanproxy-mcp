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

var LeanproxyTools = []ToolDefinition{
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
		Description: "List tools on one server. Call list_servers first for names.",
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
		Description: "Invoke a server tool. Call list_tools first for tool names.",
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
					"description": "From list_servers."
				},
				"tool": {
					"type": "string",
					"description": "From list_tools. No server prefix."
				},
				"arguments": {
					"type": "object",
					"description": "Arguments per list_tools."
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
