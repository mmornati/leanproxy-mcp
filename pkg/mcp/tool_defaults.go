package mcp

type ParameterMeta struct {
	Required        bool
	Default        interface{}
	Min            interface{}
	Max            interface{}
	Description   string
	SuggestedValues []string
}

var ListToolsParamDefaults = map[string]interface{}{
	"max_description_chars": 200,
}

var ListToolsParamMeta = map[string]ParameterMeta{
	"server_name": {
		Required:      true,
		Default:      nil,
		Description:  "MCP server name (from list_servers). Required - identifies which server's tools to list.",
		SuggestedValues: []string{"github", "garmin", "filesystem", "intervals"},
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
		Description: "Server name from list_servers (e.g., 'github', 'garmin', 'filesystem'). Must be a configured and running MCP server.",
	},
	"tool": {
		Required:    true,
		Description: "Tool name from list_tools (e.g., 'list_issues', 'get_activities'). Do NOT prefix with server name.",
	},
	"arguments": {
		Required:    false,
		Description: "Tool arguments as key-value pairs. Refer to list_tools output for available parameters. Pass empty object {} if no arguments needed.",
	},
}

func GetParamDefault(toolName, paramName string) interface{} {
	switch toolName {
	case "list_tools":
		return ListToolsParamDefaults[paramName]
	case "invoke_tool":
		return InvokeToolParamDefaults[paramName]
	}
	return nil
}

func GetParamMeta(toolName, paramName string) *ParameterMeta {
	switch toolName {
	case "list_tools":
		if meta, ok := ListToolsParamMeta[paramName]; ok {
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
	case "list_tools":
		if _, hasKey := args["max_description_chars"]; !hasKey {
			args["max_description_chars"] = ListToolsParamDefaults["max_description_chars"]
		}
	case "invoke_tool":
		// No defaults for invoke_tool - all params required or user-dependent
	}

	return args
}

func GetAllParamDefaults(toolName string) map[string]interface{} {
	switch toolName {
	case "list_tools":
		return ListToolsParamDefaults
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