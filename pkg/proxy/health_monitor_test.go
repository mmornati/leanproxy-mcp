package proxy

import (
	"testing"
	"time"
)

func TestServerHealthStatus_Values(t *testing.T) {
	statuses := []ServerHealthStatus{
		StatusRunning,
		StatusError,
		StatusStopped,
		StatusStarting,
		StatusUnresponsive,
	}

	expectedValues := []string{"running", "error", "stopped", "starting", "unresponsive"}

	for i, status := range statuses {
		if string(status) != expectedValues[i] {
			t.Errorf("expected status %s, got %s", expectedValues[i], status)
		}
	}
}

func TestServerStatus_Structure(t *testing.T) {
	status := ServerStatus{
		Name:             "test-server",
		Status:           StatusRunning,
		Uptime:           5 * time.Minute,
		LastResponseTime: time.Now(),
		RequestCount:     100,
		ErrorRate:        0.5,
		MemoryMB:         128,
		CPUPercent:       25.5,
	}

	if status.Name != "test-server" {
		t.Errorf("expected name test-server, got %s", status.Name)
	}
	if status.Status != StatusRunning {
		t.Errorf("expected status running, got %s", status.Status)
	}
	if status.RequestCount != 100 {
		t.Errorf("expected request count 100, got %d", status.RequestCount)
	}
}

func TestServerStatusList_Structure(t *testing.T) {
	now := time.Now()
	list := ServerStatusList{
		Timestamp: now,
		Servers: []ServerStatus{
			{Name: "server-1", Status: StatusRunning},
			{Name: "server-2", Status: StatusError},
		},
	}

	if len(list.Servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(list.Servers))
	}
	if list.Timestamp != now {
		t.Errorf("timestamp mismatch")
	}
}
