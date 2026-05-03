package dryrun

import (
	"encoding/json"
	"log/slog"
)

type DryRunner struct {
	enabled bool
	logger  *slog.Logger
}

func NewDryRunner(enabled bool) *DryRunner {
	return &DryRunner{
		enabled: enabled,
		logger:  slog.Default(),
	}
}

func (d *DryRunner) ShouldSkip() bool {
	return d.enabled
}

func (d *DryRunner) Preview(action string, details map[string]interface{}) {
	if !d.enabled {
		return
	}

	logData := map[string]interface{}{
		"level":  "INFO",
		"msg":    "[DRY-RUN] Would execute action",
		"action": action,
	}

	for k, v := range details {
		logData[k] = v
	}

	jsonData, _ := json.Marshal(logData)
	d.logger.Info(string(jsonData))
}

func (d *DryRunner) Enabled() bool {
	return d.enabled
}