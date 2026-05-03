package utils

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

type StatusDisplay struct {
	terminalSupportsANSI bool
}

func NewStatusDisplay() *StatusDisplay {
	return &StatusDisplay{
		terminalSupportsANSI: true,
	}
}

func (sd *StatusDisplay) RenderTable(statusList proxy.ServerStatusList) string {
	if len(statusList.Servers) == 0 {
		return "No servers configured"
	}

	var builder strings.Builder

	builder.WriteString("\n")
	builder.WriteString("NAME           STATUS      UPTIME     LAST RESPONSE   RESTARTS\n")
	builder.WriteString("──────────────────────────────────────────────────────────────\n")

	for _, server := range statusList.Servers {
		name := server.Name
		if len(name) > 11 {
			name = name[:8] + "..."
		}
		if len(name) < 11 {
			name = name + strings.Repeat(" ", 11-len(name))
		}

		status := string(server.Status)
		if len(status) < 10 {
			status = status + strings.Repeat(" ", 10-len(status))
		}

		uptime := formatDuration(server.Uptime)
		if len(uptime) < 10 {
			uptime = uptime + strings.Repeat(" ", 10-len(uptime))
		}

		lastResponse := formatDuration(time.Since(server.LastResponseTime))
		if server.Status == proxy.StatusStopped || server.LastResponseTime.IsZero() {
			lastResponse = "-"
		}
		if len(lastResponse) < 10 {
			lastResponse = lastResponse + strings.Repeat(" ", 10-len(lastResponse))
		}

		restartStr := fmt.Sprintf("%d", server.RestartCount)
		if len(restartStr) < 8 {
			restartStr = restartStr + strings.Repeat(" ", 8-len(restartStr))
		}

		builder.WriteString(fmt.Sprintf("%s %s %s %s %s\n", name, status, uptime, lastResponse, restartStr))
	}

	return builder.String()
}

func (sd *StatusDisplay) RenderVerbose(statusList proxy.ServerStatusList) string {
	if len(statusList.Servers) == 0 {
		return "No servers configured"
	}

	var builder strings.Builder

	for _, server := range statusList.Servers {
		builder.WriteString(fmt.Sprintf("Server: %s\n", server.Name))
		builder.WriteString(fmt.Sprintf("  Status: %s\n", server.Status))
		builder.WriteString(fmt.Sprintf("  Uptime: %s\n", formatDuration(server.Uptime)))

		if server.Status == proxy.StatusStopped {
			builder.WriteString("  Last Error: -\n")
		} else if !server.LastResponseTime.IsZero() {
			builder.WriteString(fmt.Sprintf("  Last Response: %s\n", formatDuration(time.Since(server.LastResponseTime))))
		} else {
			builder.WriteString("  Last Response: -\n")
		}

		if server.MemoryMB > 0 {
			builder.WriteString(fmt.Sprintf("  Memory: %dMB\n", server.MemoryMB))
		}

		if server.RequestCount > 0 {
			builder.WriteString(fmt.Sprintf("  Requests: %d\n", server.RequestCount))
		}

		if server.ErrorRate > 0 {
			builder.WriteString(fmt.Sprintf("  Error Rate: %.1f%%\n", server.ErrorRate))
		}

		if server.RestartCount > 0 {
			builder.WriteString(fmt.Sprintf("  Restarts: %d\n", server.RestartCount))
		}

		if server.LastError != "" {
			builder.WriteString(fmt.Sprintf("  Last Error: %s\n", server.LastError))
		}

		builder.WriteString("\n")
	}

	return builder.String()
}

func (sd *StatusDisplay) RenderCompact(status proxy.ServerStatus) string {
	return fmt.Sprintf("[%s] %s (%s)",
		status.Status,
		status.Name,
		formatDuration(status.Uptime))
}

func (sd *StatusDisplay) RenderJSON(statusList proxy.ServerStatusList) (string, error) {
	output, err := json.MarshalIndent(statusList, "", "  ")
	if err != nil {
		return "", fmt.Errorf("status display: marshal json: %w", err)
	}
	return string(output), nil
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}