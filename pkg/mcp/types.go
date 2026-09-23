package mcp

import (
	"encoding/json"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

const (
	MethodInitialize             = "initialize"
	MethodInitialized            = "notifications/initialized"
	MethodToolsList              = "tools/list"
	MethodToolsCall              = "tools/call"
	MethodResourcesList          = "resources/list"
	MethodResourcesTemplatesList = "resources/templates/list"
	MethodResourcesRead          = "resources/read"
	MethodResourcesSubscribe     = "resources/subscribe"
	MethodResourcesUnsubscribe   = "resources/unsubscribe"
	MethodPromptsList            = "prompts/list"
	MethodPromptsGet             = "prompts/get"
	MethodPing                   = "ping"
	MethodShutdown               = "shutdown"
)

const JSONRPCVersion = "2.0"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      interface{}     `json:"id"`
}

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func NewError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string {
	return e.Message
}

type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
}

type ClientCapabilities struct {
	Roots    *RootsCapability          `json:"roots,omitempty"`
	Sampling *ClientSamplingCapability `json:"sampling,omitempty"`
}

type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ClientSamplingCapability struct{}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type ServerCapabilities struct {
	Tools     *ToolsCapability          `json:"tools,omitempty"`
	Resources *ResourcesCapability      `json:"resources,omitempty"`
	Prompts   *PromptsCapability        `json:"prompts,omitempty"`
	Sampling  *ServerSamplingCapability `json:"sampling,omitempty"`
	Health    *HealthcheckCapability    `json:"healthcheck,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ServerSamplingCapability struct{}

type HealthcheckCapability struct{}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Title is the human-readable name (MCP 2025-06-18).
	Title string `json:"title,omitempty"`
}

type ToolsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type ToolsListResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// Tool is an MCP tool definition as an upstream server lists it. Every
// field of the current MCP revision is kept so list_tools / search_tools can
// hand the full object to clients that negotiated 2025-06-18 or newer.
type Tool struct {
	Name string `json:"name"`
	// Title is the human-readable display name (2025-06-18).
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// OutputSchema describes the tool's structuredContent (2025-06-18).
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	// Annotations are the behavior hints (2025-03-26).
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	// Icons are the tool's visual identifiers (2025-11-25).
	Icons []Icon `json:"icons,omitempty"`
	// Meta is the reserved _meta object (for example MCP Apps UI
	// resource references), kept verbatim.
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// ToolAnnotations are a tool's behavior hints (title, readOnlyHint,
// destructiveHint, idempotentHint, openWorldHint). mcp-go's type fits the
// spec exactly, so it is reused.
type ToolAnnotations = mcpgo.ToolAnnotation

// Icon is a visual identifier of a tool, resource or prompt (2025-11-25).
// mcp-go's Icon lacks the theme field, hence a local type.
type Icon struct {
	Src      string   `json:"src"`
	MimeType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    string   `json:"theme,omitempty"`
}

// ReadOnly reports whether the tool declares readOnlyHint: true.
func (t Tool) ReadOnly() bool {
	return t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
}

// Destructive reports whether the tool explicitly declares
// destructiveHint: true (and is not read-only, in which case the hint is
// meaningless per the spec).
func (t Tool) Destructive() bool {
	return !t.ReadOnly() && t.Annotations != nil && t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint
}

type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolsCallResult is a tools/call result built by LeanProxy itself (text
// blocks only). Upstream results are relayed as raw JSON and decoded, when
// needed, with CallToolResult.
type ToolsCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Content item types of MCP tool results and prompt messages.
const (
	ContentTypeText         = "text"
	ContentTypeImage        = "image"
	ContentTypeAudio        = "audio"
	ContentTypeResource     = "resource"
	ContentTypeResourceLink = "resource_link"
)

// CallToolResult is a tools/call result as an upstream server returns it.
// Content items are kept raw (every item type — text, image, audio,
// resource, resource_link, and any future one — survives unchanged);
// ContentItemType tells them apart.
type CallToolResult struct {
	Content []json.RawMessage `json:"content"`
	// StructuredContent is the tool's structured output (2025-06-18).
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
	Meta              json.RawMessage `json:"_meta,omitempty"`
}

// ContentItemType returns the "type" of one raw content item, or "" when
// it has none.
func ContentItemType(item json.RawMessage) string {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(item, &probe) != nil {
		return ""
	}
	return probe.Type
}

type ResourcesListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type ResourcesListResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type PromptsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type PromptsListResult struct {
	Prompts    []Prompt `json:"prompts"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
	ErrCodeServerError    = -32000
)

func (r *Request) IsNotification() bool {
	return r.ID == nil
}
