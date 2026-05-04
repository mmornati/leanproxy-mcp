package mcp

import (
	"fmt"
	"strings"
)

type ErrorHint struct {
	Original   string
	Suggestion string
	Action    string
	Reference string
}

var ErrorHintRegistry = map[string][]ErrorHint{
	"repository": {
		{
			Original:   "Could not resolve to a Repository",
			Suggestion: "Check the owner/repo spelling. Ensure the repository exists and you have access. Tip: Use the exact format 'owner/repo' (e.g., 'mmornati/leanproxy-mcp').",
			Action:    "check_repo",
			Reference: "https://docs.github.com/en/github/finding-open-source-projects-on-github",
		},
	},
	"not running": {
		{
			Original:   "is not running",
			Suggestion: "The MCP server is not running. Run 'leanproxy server start <server_name>' or check server status with 'leanproxy status'.",
			Action:    "restart_server",
		},
	},
	"failed": {
		{
			Original:   "server error",
			Suggestion: "The remote server encountered an error. Try again in a few moments, or check the server logs for more details.",
			Action:    "retry",
		},
	},
	"not authenticated": {
		{
			Original:   "not authenticated",
			Suggestion: "Authentication required. Check your credentials configuration for this server.",
			Action:    "check_auth",
		},
		{
			Original:   "401",
			Suggestion: "Authentication failed. Verify your API token or credentials are correct.",
			Action:    "check_auth",
		},
	},
	"forbidden": {
		{
			Original:   "403",
			Suggestion: "Access denied. You may not have permission for this operation. Check your API token scopes.",
			Action:    "check_permissions",
		},
		{
			Original:   "permission denied",
			Suggestion: "Insufficient permissions. Verify your account has the required access rights.",
			Action:    "check_permissions",
		},
	},
	"not found": {
		{
			Original:   "404",
			Suggestion: "The requested resource was not found. Check if the resource exists and the URL is correct.",
			Action:    "verify_resource",
		},
	},
	"rate limited": {
		{
			Original:   "429",
			Suggestion: "Rate limit exceeded. Wait a moment before retrying, or check if you need to configure rate limiting.",
			Action:    "wait_retry",
		},
		{
			Original:   "rate limit",
			Suggestion: "Too many requests. Consider adding a delay between requests or reducing request frequency.",
			Action:    "wait_retry",
		},
	},
	"timeout": {
		{
			Original:   "timeout",
			Suggestion: "The request timed out. The server may be slow or unavailable. Try again or increase timeout settings.",
			Action:    "retry",
		},
		{
			Original:   "context deadline exceeded",
			Suggestion: "The request took too long. Try again or check if the server is experiencing issues.",
			Action:    "retry",
		},
	},
	"invalid params": {
		{
			Original:   "invalid params",
			Suggestion: "Check the parameter names and types. Refer to search_tools output for available parameters.",
			Action:    "check_params",
		},
		{
			Original:   "missing",
			Suggestion: "A required parameter is missing. Check search_tools output for required parameters.",
			Action:    "check_params",
		},
	},
	"connection refused": {
		{
			Original:   "connection refused",
			Suggestion: "Cannot connect to the server. Verify the server is running and the connection settings are correct.",
			Action:    "check_server",
		},
	},
	"tool not found": {
		{
			Original:   "tool not found",
			Suggestion: "The tool doesn't exist on this server. Use search_tools to discover available tools.",
			Action:    "search_tools",
		},
	},
}

func EnrichError(originalMessage string) string {
	originalLower := strings.ToLower(originalMessage)

	for pattern, hints := range ErrorHintRegistry {
		patternLower := strings.ToLower(pattern)
		if strings.Contains(originalLower, patternLower) {
			hint := hints[0]
			return fmt.Sprintf("%s\n\n💡 %s", originalMessage, hint.Suggestion)
		}
	}

	return originalMessage
}

func GetErrorHint(originalMessage string) *ErrorHint {
	originalLower := strings.ToLower(originalMessage)

	for pattern, hints := range ErrorHintRegistry {
		patternLower := strings.ToLower(pattern)
		if strings.Contains(originalLower, patternLower) {
			hint := hints[0]
			return &hint
		}
	}

	return nil
}

func GetAllHintsForError(originalMessage string) []ErrorHint {
	originalLower := strings.ToLower(originalMessage)

	for pattern, hints := range ErrorHintRegistry {
		patternLower := strings.ToLower(pattern)
		if strings.Contains(originalLower, patternLower) {
			return hints
		}
	}

	return nil
}

func AddErrorContext(originalMessage, serverName, toolName string) string {
	if serverName != "" || toolName != "" {
		context := "\n\n📋 Context:"
		if serverName != "" {
			context += fmt.Sprintf("\n  Server: %s", serverName)
		}
		if toolName != "" {
			context += fmt.Sprintf("\n  Tool: %s", toolName)
		}
		return originalMessage + context
	}

	return originalMessage
}

func FormatErrorWithHint(originalMessage, serverName, toolName string) string {
	enriched := EnrichError(originalMessage)
	return AddErrorContext(enriched, serverName, toolName)
}