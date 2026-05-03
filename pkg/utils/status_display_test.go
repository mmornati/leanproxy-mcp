package utils

import (
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

func TestStatusDisplay_RenderTable_Empty(t *testing.T) {
	display := NewStatusDisplay()

	statusList := proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers:   []proxy.ServerStatus{},
	}

	result := display.RenderTable(statusList)

	if result != "No servers configured" {
		t.Errorf("expected 'No servers configured', got %s", result)
	}
}

func TestStatusDisplay_RenderTable_SingleServer(t *testing.T) {
	display := NewStatusDisplay()

	statusList := proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers: []proxy.ServerStatus{
			{
				Name:            "server-1",
				Status:          proxy.StatusRunning,
				Uptime:          2*time.Minute + 34*time.Second,
				LastResponseTime: time.Now().Add(-120 * time.Millisecond),
				RestartCount:    0,
			},
		},
	}

	result := display.RenderTable(statusList)

	if !strings.Contains(result, "server-1") {
		t.Errorf("expected output to contain server-1, got %s", result)
	}
	if !strings.Contains(result, "running") {
		t.Errorf("expected output to contain running, got %s", result)
	}
}

func TestStatusDisplay_RenderTable_MultipleServers(t *testing.T) {
	display := NewStatusDisplay()

	statusList := proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers: []proxy.ServerStatus{
			{
				Name:            "server-1",
				Status:          proxy.StatusRunning,
				Uptime:          2 * time.Minute,
				LastResponseTime: time.Now(),
				RestartCount:    0,
			},
			{
				Name:            "server-2",
				Status:          proxy.StatusError,
				Uptime:          5 * time.Minute,
				LastResponseTime: time.Now().Add(-30 * time.Second),
				RestartCount:    2,
			},
			{
				Name:            "server-3",
				Status:          proxy.StatusStopped,
				Uptime:          0,
				LastResponseTime: time.Time{},
				RestartCount:    0,
			},
		},
	}

	result := display.RenderTable(statusList)

	if !strings.Contains(result, "server-1") {
		t.Errorf("expected output to contain server-1")
	}
	if !strings.Contains(result, "server-2") {
		t.Errorf("expected output to contain server-2")
	}
	if !strings.Contains(result, "server-3") {
		t.Errorf("expected output to contain server-3")
	}
}

func TestStatusDisplay_RenderTable_LongServerName(t *testing.T) {
	display := NewStatusDisplay()

	statusList := proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers: []proxy.ServerStatus{
			{
				Name:            "very-long-server-name-that-exceeds-limit",
				Status:          proxy.StatusRunning,
				Uptime:          2 * time.Minute,
				LastResponseTime: time.Now(),
				RestartCount:    0,
			},
		},
	}

	result := display.RenderTable(statusList)

	if strings.Contains(result, "very-long-server-name-that-exceeds-limit") {
		t.Error("expected long server name to be truncated")
	}
	if !strings.Contains(result, "very-lon...") {
		t.Errorf("expected output to contain truncated name")
	}
}

func TestStatusDisplay_RenderVerbose_Empty(t *testing.T) {
	display := NewStatusDisplay()

	statusList := proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers:   []proxy.ServerStatus{},
	}

	result := display.RenderVerbose(statusList)

	if result != "No servers configured" {
		t.Errorf("expected 'No servers configured', got %s", result)
	}
}

func TestStatusDisplay_RenderVerbose_WithMemoryAndRequests(t *testing.T) {
	display := NewStatusDisplay()

	statusList := proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers: []proxy.ServerStatus{
			{
				Name:            "server-1",
				Status:          proxy.StatusRunning,
				Uptime:          2 * time.Minute,
				LastResponseTime: time.Now(),
				RequestCount:    1234,
				ErrorRate:       0.1,
				MemoryMB:        45,
				RestartCount:    0,
			},
		},
	}

	result := display.RenderVerbose(statusList)

	if !strings.Contains(result, "server-1") {
		t.Errorf("expected output to contain server-1")
	}
	if !strings.Contains(result, "Memory: 45MB") {
		t.Errorf("expected output to contain Memory: 45MB")
	}
	if !strings.Contains(result, "Requests: 1234") {
		t.Errorf("expected output to contain Requests: 1234")
	}
	if !strings.Contains(result, "Error Rate: 0.1%") {
		t.Errorf("expected output to contain Error Rate: 0.1%%")
	}
}

func TestStatusDisplay_RenderCompact(t *testing.T) {
	display := NewStatusDisplay()

	status := proxy.ServerStatus{
		Name:   "server-1",
		Status: proxy.StatusRunning,
		Uptime: 2 * time.Minute,
	}

	result := display.RenderCompact(status)

	if !strings.Contains(result, "server-1") {
		t.Errorf("expected output to contain server-1")
	}
	if !strings.Contains(result, "running") {
		t.Errorf("expected output to contain running")
	}
}

func TestStatusDisplay_RenderJSON(t *testing.T) {
	display := NewStatusDisplay()

	statusList := proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers: []proxy.ServerStatus{
			{
				Name:            "server-1",
				Status:          proxy.StatusRunning,
				Uptime:          2 * time.Minute,
				LastResponseTime: time.Now(),
				RequestCount:    100,
			},
		},
	}

	result, err := display.RenderJSON(statusList)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "server-1") {
		t.Errorf("expected JSON to contain server-1")
	}
	if !strings.Contains(result, "running") {
		t.Errorf("expected JSON to contain running")
	}
}

func TestFormatDuration_Zero(t *testing.T) {
	result := formatDuration(0)
	if result != "0s" {
		t.Errorf("expected 0s, got %s", result)
	}
}

func TestFormatDuration_Seconds(t *testing.T) {
	result := formatDuration(30 * time.Second)
	if result != "30s" {
		t.Errorf("expected 30s, got %s", result)
	}
}

func TestFormatDuration_Minutes(t *testing.T) {
	result := formatDuration(2*time.Minute + 34*time.Second)
	if result != "2m34s" {
		t.Errorf("expected 2m34s, got %s", result)
	}
}

func TestFormatDuration_Hours(t *testing.T) {
	result := formatDuration(1*time.Hour + 30*time.Minute)
	if result != "1h30m" {
		t.Errorf("expected 1h30m, got %s", result)
	}
}

func TestFormatDuration_Days(t *testing.T) {
	result := formatDuration(2*24*time.Hour + 5*time.Hour)
	if result != "2d5h" {
		t.Errorf("expected 2d5h, got %s", result)
	}
}