package compactor

import "encoding/json"
import "fmt"

const SystemPrompt = `You are a token optimization assistant. Reduce tool descriptions to minimum necessary characters while preserving all technical accuracy. Output valid JSON only. Preserve parameter names, types, and required flags exactly. Keep descriptions under 50 characters when possible.`

func BuildDistillationPrompt(manifest RawManifest) string {
	return fmt.Sprintf("Optimize this MCP tool manifest for token efficiency:\n%s", manifestJSON(manifest))
}

func manifestJSON(manifest RawManifest) string {
	data, err := json.Marshal(manifest)
	if err != nil {
		return "{}"
	}
	return string(data)
}
