package compactor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

type ManifestProcessor struct {
	logger *slog.Logger
}

func NewManifestProcessor(logger *slog.Logger) *ManifestProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &ManifestProcessor{logger: logger}
}

func (m *ManifestProcessor) Process(ctx context.Context, raw RawManifest) (*DistilledManifest, error) {
	if len(raw.Tools) == 0 {
		return nil, fmt.Errorf("compactor: no tools in manifest")
	}

	result := &DistilledManifest{
		ServerName:   raw.Name,
		Tools:        make([]DistilledTool, 0, len(raw.Tools)),
		OriginalHash: raw.Hash(),
	}

	for _, tool := range raw.Tools {
		distilledTool, err := m.processTool(ctx, tool)
		if err != nil {
			m.logger.Warn("failed to process tool", "tool", tool.Name, "error", err)
			continue
		}
		result.Tools = append(result.Tools, *distilledTool)
	}

	if len(result.Tools) == 0 {
		return nil, fmt.Errorf("compactor: no tools successfully processed")
	}

	return result, nil
}

func (m *ManifestProcessor) processTool(ctx context.Context, tool RawTool) (*DistilledTool, error) {
	return &DistilledTool{
		Name:        tool.Name,
		Description: compactDescription(tool.Description),
		Parameters:  tool.Parameters,
	}, nil
}

func compactDescription(description string) string {
	if len(description) <= 50 {
		return description
	}
	return description[:47] + "..."
}

func CompactDescriptionForTest(description string) string {
	if len(description) <= 50 {
		return description
	}
	return description[:47] + "..."
}

func validateDistilledManifest(distilled *DistilledManifest) error {
	if distilled == nil {
		return fmt.Errorf("compactor: distilled manifest is nil")
	}
	if distilled.ServerName == "" {
		return fmt.Errorf("compactor: server name is required")
	}
	if len(distilled.Tools) == 0 {
		return fmt.Errorf("compactor: at least one tool is required")
	}
	for _, tool := range distilled.Tools {
		if tool.Name == "" {
			return fmt.Errorf("compactor: tool name is required")
		}
		if len(tool.Description) > 50 {
			return fmt.Errorf("compactor: tool description exceeds 50 characters")
		}
		if tool.Parameters == nil {
			tool.Parameters = json.RawMessage("{}")
		}
	}
	return nil
}

func calculateTokenReduction(original, distilled []byte) float64 {
	if len(original) == 0 {
		return 0
	}
	originalTokens := len(original) / 4
	distilledTokens := len(distilled) / 4
	return float64(originalTokens-distilledTokens) / float64(originalTokens) * 100
}
