package gateway

import (
	"context"
	"strings"
)

func (g *gatewayTools) SearchTools(ctx context.Context, query string) ([]ToolSearchResult, error) {
	tools, err := g.toolReg.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	queryLower := strings.ToLower(query)
	result := make([]ToolSearchResult, 0)

	for _, tool := range tools {
		if query == "" ||
			strings.Contains(strings.ToLower(tool.Name), queryLower) ||
			strings.Contains(strings.ToLower(tool.Namespace), queryLower) {
			result = append(result, ToolSearchResult{
				ToolName:    tool.Name,
				ServerName:  tool.ServerID,
				Description: tool.Namespace + " namespace",
			})
		}
	}

	return result, nil
}