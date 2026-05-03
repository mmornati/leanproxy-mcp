package compactor

import (
	"encoding/json"
	"fmt"
	"time"
)

type RawManifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Tools       []RawTool       `json:"tools"`
	OriginalHash string         `json:"-"`
}

type RawTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func (r RawManifest) Hash() string {
	data, _ := json.Marshal(r)
	return fmt.Sprintf("%x", data)
}

type DistilledManifest struct {
	ServerName    string         `json:"server_name"`
	Tools         []DistilledTool `json:"tools"`
	OriginalHash  string         `json:"original_hash"`
	DistilledAt   time.Time      `json:"distilled_at"`
}

type DistilledTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func (d DistilledManifest) TokenReduction(originalTokenCount int) float64 {
	if originalTokenCount == 0 {
		return 0
	}
	distilledJSON, _ := json.Marshal(d)
	distilledTokens := len(distilledJSON) / 4
	return float64(originalTokenCount-distilledTokens) / float64(originalTokenCount) * 100
}
