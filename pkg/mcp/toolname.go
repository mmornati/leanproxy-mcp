package mcp

import (
	"fmt"
	"strings"
)

// toolNameSeparators are the characters accepted between a server name and a
// tool name in a namespaced tool reference ("server_tool", "server.tool").
var toolNameSeparators = [...]byte{'.', '_'}

// SplitToolName splits a namespaced tool reference such as "server_tool" or
// "server.tool" into its server and tool parts. Unlike a naive split on the
// first separator, it matches the longest name in servers that is a prefix
// of ref immediately followed by '.' or '_', so a server literally named
// "my_srv" is reachable via "my_srv_tool" and overlapping names (e.g. "git"
// and "github") resolve to the correct owner instead of the first partial
// match.
//
// It returns an error when no configured server name is a matching prefix
// of ref.
func SplitToolName(ref string, servers []string) (server, tool string, err error) {
	bestLen := -1
	for _, name := range servers {
		if name == "" || len(name) <= bestLen {
			continue
		}
		for _, sep := range toolNameSeparators {
			prefix := name + string(sep)
			if strings.HasPrefix(ref, prefix) {
				bestLen = len(name)
				server = name
				tool = ref[len(prefix):]
				break
			}
		}
	}

	if bestLen < 0 || tool == "" {
		return "", "", fmt.Errorf("invalid tool name '%s': expected format is 'serverName_toolName' or 'serverName.toolName'", ref)
	}

	return server, tool, nil
}
