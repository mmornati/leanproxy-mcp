package mcp

type ParameterMeta struct {
	Required        bool
	Default        interface{}
	Min            interface{}
	Max            interface{}
	Description   string
	SuggestedValues []string
}

var SearchToolsParamDefaults = map[string]interface{}{
	"max_description_chars": 200,
}

var SearchToolsParamMeta = map[string]ParameterMeta{
	"query": {
		Required:      true,
		Default:      nil,
		Description:  "Search query (e.g., 'github issues', 'garmin activities', 'filesystem'). Supports fuzzy matching across tool names and descriptions.",
		SuggestedValues: []string{"github", "filesystem", "garmin", "intervals", "productivity"},
	},
	"max_description_chars": {
		Required:    false,
		Default:     200,
		Min:         50,
		Max:         500,
		Description: "Maximum characters for tool descriptions. Default: 200. Range: 50-500.",
	},
}

var InvokeToolParamDefaults = map[string]interface{}{}

var InvokeToolParamMeta = map[string]ParameterMeta{
	"server": {
		Required:    true,
		Description: "Server name from search_tools (e.g., 'github', 'garmin', 'filesystem'). Must be a configured and running MCP server.",
	},
	"tool": {
		Required:    true,
		Description: "Tool name from search_tools (e.g., 'github_list_issues', 'garmin_get_activities'). Do NOT prefix with server name.",
	},
	"arguments": {
		Required:    false,
		Description: "Tool arguments as key-value pairs. Refer to search_tools output for available parameters. Pass empty object {} if no arguments needed.",
	},
}

func GetParamDefault(toolName, paramName string) interface{} {
	switch toolName {
	case "search_tools":
		return SearchToolsParamDefaults[paramName]
	case "invoke_tool":
		return InvokeToolParamDefaults[paramName]
	}
	return nil
}

func GetParamMeta(toolName, paramName string) *ParameterMeta {
	switch toolName {
	case "search_tools":
		if meta, ok := SearchToolsParamMeta[paramName]; ok {
			return &meta
		}
	case "invoke_tool":
		if meta, ok := InvokeToolParamMeta[paramName]; ok {
			return &meta
		}
	}
	return nil
}

func ApplyDefaults(toolName string, args map[string]interface{}) map[string]interface{} {
	if args == nil {
		args = make(map[string]interface{})
	}

	switch toolName {
	case "search_tools":
		if _, hasKey := args["max_description_chars"]; !hasKey {
			args["max_description_chars"] = SearchToolsParamDefaults["max_description_chars"]
		}
	case "invoke_tool":
		// No defaults for invoke_tool - all params required or user-dependent
	}

	return args
}

func GetAllParamDefaults(toolName string) map[string]interface{} {
	switch toolName {
	case "search_tools":
		return SearchToolsParamDefaults
	case "invoke_tool":
		return InvokeToolParamDefaults
	}
	return nil
}

func ValidateParam(toolName, paramName string, value interface{}) (bool, string) {
	meta := GetParamMeta(toolName, paramName)
	if meta == nil {
		return true, ""
	}

	if meta.Required && value == nil {
		return false, "parameter is required"
	}

	if value == nil {
		return true, ""
	}

	switch paramName {
	case "max_description_chars":
		if fval, ok := value.(float64); ok {
			if fval == 0 {
				return true, ""
			}
			if fval < 50 || fval > 500 {
				return false, "must be between 50 and 500"
			}
		}
	}

	return true, ""
}