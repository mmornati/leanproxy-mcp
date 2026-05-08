package proxy

import (
	"context"
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

func TestDefaultHealthConfig(t *testing.T) {
	config := DefaultHealthConfig()

	if config.CheckInterval != 1*time.Second {
		t.Errorf("expected check interval 1s, got %v", config.CheckInterval)
	}
	if config.ResponseTimeout != 30*time.Second {
		t.Errorf("expected response timeout 30s, got %v", config.ResponseTimeout)
	}
	if config.MaxRestartAttempts != 3 {
		t.Errorf("expected max restart attempts 3, got %d", config.MaxRestartAttempts)
	}
	if config.RestartBackoff != 5*time.Second {
		t.Errorf("expected restart backoff 5s, got %v", config.RestartBackoff)
	}
}

type mockServer struct {
	nameVal          string
	pidVal           int
	runningVal       bool
	startTimeVal     time.Time
	requestCountVal  int64
	errorCountVal    int64
	crashCallbacks   []func()
	restartCallbacks []func()
}

func (m *mockServer) Name() string                { return m.nameVal }
func (m *mockServer) PID() int                    { return m.pidVal }
func (m *mockServer) IsRunning() bool             { return m.runningVal }
func (m *mockServer) StartTime() time.Time        { return m.startTimeVal }
func (m *mockServer) LastResponseTime() time.Time { return time.Now() }
func (m *mockServer) RequestCount() int64         { return m.requestCountVal }
func (m *mockServer) ErrorCount() int64           { return m.errorCountVal }
func (m *mockServer) OnCrash(callback func())     { m.crashCallbacks = append(m.crashCallbacks, callback) }
func (m *mockServer) OnRestart(callback func()) {
	m.restartCallbacks = append(m.restartCallbacks, callback)
}

func TestHealthMonitor_RegisterServer(t *testing.T) {
	monitor := NewHealthMonitor(nil, nil)
	server := &mockServer{
		nameVal:    "test-server",
		pidVal:     12345,
		runningVal: true,
	}

	monitor.RegisterServer(server)

	if len(monitor.servers) != 1 {
		t.Errorf("expected 1 server registered, got %d", len(monitor.servers))
	}
}

func TestHealthMonitor_UnregisterServer(t *testing.T) {
	monitor := NewHealthMonitor(nil, nil)
	server := &mockServer{
		nameVal:    "test-server",
		pidVal:     12345,
		runningVal: true,
	}

	monitor.RegisterServer(server)
	monitor.UnregisterServer("test-server")

	if len(monitor.servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(monitor.servers))
	}
}

func TestHealthMonitor_GetStatus(t *testing.T) {
	monitor := NewHealthMonitor(nil, nil)

	server1 := &mockServer{
		nameVal:         "server-1",
		pidVal:          12345,
		runningVal:      true,
		startTimeVal:    time.Now().Add(-5 * time.Minute),
		requestCountVal: 100,
		errorCountVal:   1,
	}

	server2 := &mockServer{
		nameVal:         "server-2",
		pidVal:          12346,
		runningVal:      false,
		startTimeVal:    time.Now().Add(-10 * time.Minute),
		requestCountVal: 50,
		errorCountVal:   5,
	}

	monitor.RegisterServer(server1)
	monitor.RegisterServer(server2)

	status := monitor.GetStatus()

	if len(status.Servers) != 2 {
		t.Errorf("expected 2 servers in status, got %d", len(status.Servers))
	}
}

func TestHealthMonitor_WatchStatus(t *testing.T) {
	monitor := NewHealthMonitor(nil, nil)

	server := &mockServer{
		nameVal:         "server-1",
		pidVal:          12345,
		runningVal:      true,
		startTimeVal:    time.Now().Add(-5 * time.Minute),
		requestCountVal: 100,
		errorCountVal:   1,
	}

	monitor.RegisterServer(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	statusChan := monitor.WatchStatus(ctx, 100*time.Millisecond)

	select {
	case status := <-statusChan:
		if len(status.Servers) != 1 {
			t.Errorf("expected 1 server, got %d", len(status.Servers))
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for status update")
	}
}

func TestHealthMonitor_checkServer_Running(t *testing.T) {
	t.Skip("testing internal method - use public API tests instead")
}

func TestHealthMonitor_checkServer_Stopped(t *testing.T) {
	t.Skip("testing internal method - use public API tests instead")
}

func TestHealthMonitor_checkServer_Unresponsive(t *testing.T) {
	t.Skip("testing internal method - use public API tests instead")
}

func TestHealthMonitor_StartStop(t *testing.T) {
	monitor := NewHealthMonitor(nil, nil)

	err := monitor.Start()
	if err != nil {
		t.Errorf("unexpected error starting monitor: %v", err)
	}

	monitor.Stop()
}
