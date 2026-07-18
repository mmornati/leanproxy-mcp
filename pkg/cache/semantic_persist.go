package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/utils"
)

const semanticStatsVersion = 1

// SemanticStatsSnapshot is the on-disk representation of semantic cache
// statistics, written periodically by the running server so that separate
// CLI processes can render the dashboard.
type SemanticStatsSnapshot struct {
	Version   int                `json:"version"`
	UpdatedAt time.Time          `json:"updated_at"`
	Stats     SemanticCacheStats `json:"stats"`
}

// DefaultSemanticStatsPath returns the stats file location used by both the
// server (writer) and the CLI (reader): ~/.leanproxy/cache/semantic-stats.json
func DefaultSemanticStatsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "leanproxy", "cache", "semantic-stats.json")
	}
	return filepath.Join(home, ".leanproxy", "cache", "semantic-stats.json")
}

// persistStats writes the current stats snapshot atomically (tmp + rename).
// Failures are logged but never propagated: persistence is best-effort.
func (sc *SemanticCache) persistStats() {
	if sc.persistPath == "" {
		return
	}
	snap := SemanticStatsSnapshot{
		Version:   semanticStatsVersion,
		UpdatedAt: time.Now(),
		Stats:     sc.Stats(),
	}
	if err := writeSemanticStatsSnapshot(sc.persistPath, snap); err != nil {
		sc.logger.Warn("semantic cache: stats persist failed", "path", sc.persistPath, "error", err)
	}
}

func writeSemanticStatsSnapshot(path string, snap SemanticStatsSnapshot) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create stats dir: %w", err)
	}
	if err := utils.ValidatePath(path, filepath.Dir(filepath.Clean(path))); err != nil {
		return fmt.Errorf("invalid stats path: %w", err)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temp stats: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}
	return nil
}

// LoadSemanticStatsSnapshot reads a stats snapshot written by the server.
// A missing file is reported as an error so callers can render an
// "unavailable" message.
func LoadSemanticStatsSnapshot(path string) (*SemanticStatsSnapshot, error) {
	if err := utils.ValidatePath(path, filepath.Dir(filepath.Clean(path))); err != nil {
		return nil, fmt.Errorf("invalid stats path: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read stats: %w", err)
	}
	var snap SemanticStatsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse stats: %w", err)
	}
	return &snap, nil
}
