package registry

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestServerConfigValidation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	tests := []struct {
		name    string
		config  ServerConfig
		wantErr bool
	}{
		{
			name:    "empty ID",
			config:  ServerConfig{ID: "", Command: []string{"sleep", "1"}},
			wantErr: true,
		},
		{
			name:    "empty command",
			config:  ServerConfig{ID: "test", Command: []string{}},
			wantErr: true,
		},
		{
			name: "valid config",
			config: ServerConfig{
				ID:      "test-server",
				Name:    "Test Server",
				Command: []string{"echo", "hello"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manager.Start(context.Background(), tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("Start() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStartDuplicateServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	config := ServerConfig{
		ID:      "dup-test",
		Name:    "Dup Test",
		Command: []string{"echo", "hello"},
	}

	_, err := manager.Start(context.Background(), config)
	if err != nil {
		t.Fatalf("First Start() failed: %v", err)
	}

	_, err = manager.Start(context.Background(), config)
	if err == nil {
		t.Error("Expected error for duplicate server, got nil")
	}
}

func TestServerStateTransitions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	config := ServerConfig{
		ID:      "state-test",
		Name:    "State Test",
		Command: []string{"sleep", "10"},
	}

	handle, err := manager.Start(context.Background(), config)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if handle.ID != config.ID {
		t.Errorf("Handle.ID = %v, want %v", handle.ID, config.ID)
	}
	if handle.PID == 0 {
		t.Error("Handle.PID should not be 0")
	}

	status, err := manager.Status(context.Background(), config.ID)
	if err != nil {
		t.Fatalf("Status() failed: %v", err)
	}
	if status.State != StateRunning {
		t.Errorf("Status.State = %v, want %v", status.State, StateRunning)
	}

	err = manager.Stop(context.Background(), config.ID)
	if err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	status, err = manager.Status(context.Background(), config.ID)
	if err != nil {
		t.Fatalf("Status() after stop failed: %v", err)
	}
}

func TestKillServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	config := ServerConfig{
		ID:      "kill-test",
		Name:    "Kill Test",
		Command: []string{"sleep", "60"},
	}

	_, err := manager.Start(context.Background(), config)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	err = manager.Kill(context.Background(), config.ID)
	if err != nil {
		t.Fatalf("Kill() failed: %v", err)
	}

	_, err = manager.Status(context.Background(), config.ID)
	if err == nil {
		t.Error("Expected error after Kill, got nil")
	}
}

func TestRestartServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	config := ServerConfig{
		ID:      "restart-test",
		Name:    "Restart Test",
		Command: []string{"sleep", "10"},
	}

	_, err := manager.Start(context.Background(), config)
	if err != nil {
		t.Fatalf("First Start() failed: %v", err)
	}

	err = manager.Restart(context.Background(), config.ID)
	if err != nil {
		t.Fatalf("Restart() failed: %v", err)
	}

	status, err := manager.Status(context.Background(), config.ID)
	if err != nil {
		t.Fatalf("Status() after restart failed: %v", err)
	}
	if status.State != StateRunning {
		t.Errorf("Status.State = %v, want %v", status.State, StateRunning)
	}
}

func TestListServers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	config1 := ServerConfig{
		ID:      "list-test-1",
		Name:    "List Test 1",
		Command: []string{"echo", "server1"},
	}
	config2 := ServerConfig{
		ID:      "list-test-2",
		Name:    "List Test 2",
		Command: []string{"echo", "server2"},
	}

	_, err := manager.Start(context.Background(), config1)
	if err != nil {
		t.Fatalf("Start() server1 failed: %v", err)
	}
	_, err = manager.Start(context.Background(), config2)
	if err != nil {
		t.Fatalf("Start() server2 failed: %v", err)
	}

	list, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(list) < 2 {
		t.Errorf("List() returned %d servers, want at least 2", len(list))
	}
}

func TestNonexistentServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	_, err := manager.Status(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent server")
	}

	err = manager.Stop(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Expected error stopping nonexistent server")
	}

	err = manager.Kill(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Expected error killing nonexistent server")
	}
}

func TestServerNotFoundForRestart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	err := manager.Restart(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Expected error for restart of nonexistent server")
	}
}

func TestExecutableNotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	config := ServerConfig{
		ID:      "notfound-test",
		Name:    "Not Found Test",
		Command: []string{"/nonexistent/path/to/binary"},
	}

	_, err := manager.Start(context.Background(), config)
	if err == nil {
		t.Error("Expected error for nonexistent executable")
	}
}

func TestProcessAlreadyExited(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	manager := NewLifecycleManager(logger).(*lifecycleManager)

	config := ServerConfig{
		ID:      "quick-exit",
		Name:    "Quick Exit",
		Command: []string{"true"},
	}

	_, err := manager.Start(context.Background(), config)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	entry := manager.servers[config.ID]
	if entry != nil {
		t.Log("Server still in registry after quick exit")
	}
}