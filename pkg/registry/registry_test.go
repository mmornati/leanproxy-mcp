package registry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

func TestRegisterUnregister(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()
	entry := ServerEntry{
		ID:          "test-server",
		Address:     "localhost:8080",
		Transport:   TransportHTTP,
		Capabilities: []string{"code-complete", "diagnostics"},
		Health:      HealthHealthy,
	}

	err := reg.Register(ctx, entry)
	if err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	retrieved, err := reg.Get(ctx, "test-server")
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if retrieved.ID != entry.ID {
		t.Errorf("Get() ID = %v, want %v", retrieved.ID, entry.ID)
	}

	err = reg.Unregister(ctx, "test-server")
	if err != nil {
		t.Fatalf("Unregister() failed: %v", err)
	}

	_, err = reg.Get(ctx, "test-server")
	if err == nil {
		t.Error("Expected error after Unregister, got nil")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()
	entry := ServerEntry{
		ID:        "dup-test",
		Address:   "localhost:8080",
		Transport: TransportHTTP,
	}

	err := reg.Register(ctx, entry)
	if err != nil {
		t.Fatalf("First Register() failed: %v", err)
	}

	err = reg.Register(ctx, entry)
	if err == nil {
		t.Error("Expected error for duplicate registration, got nil")
	}
}

func TestUnregisterNonExistent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()
	err := reg.Unregister(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for Unregister non-existent server")
	}
}

func TestList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entries := []ServerEntry{
		{ID: "server1", Address: "localhost:8080", Transport: TransportHTTP},
		{ID: "server2", Address: "localhost:8081", Transport: TransportStdio},
		{ID: "server3", Address: "localhost:8082", Transport: TransportHTTP},
	}

	for _, e := range entries {
		if err := reg.Register(ctx, e); err != nil {
			t.Fatalf("Register() failed for %s: %v", e.ID, err)
		}
	}

	list, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("List() returned %d servers, want 3", len(list))
	}
}

func TestFindByCapability(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entries := []ServerEntry{
		{ID: "s1", Capabilities: []string{"code-complete", "diagnostics"}},
		{ID: "s2", Capabilities: []string{"code-complete"}},
		{ID: "s3", Capabilities: []string{"diagnostics"}},
	}

	for _, e := range entries {
		if err := reg.Register(ctx, e); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
	}

	results, err := reg.FindByCapability(ctx, "code-complete")
	if err != nil {
		t.Fatalf("FindByCapability() failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("FindByCapability() returned %d, want 2", len(results))
	}

	results, err = reg.FindByCapability(ctx, "diagnostics")
	if err != nil {
		t.Fatalf("FindByCapability() failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("FindByCapability() returned %d, want 2", len(results))
	}

	results, err = reg.FindByCapability(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("FindByCapability() failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("FindByCapability() returned %d, want 0", len(results))
	}
}

func TestFindByTransport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entries := []ServerEntry{
		{ID: "s1", Transport: TransportHTTP},
		{ID: "s2", Transport: TransportHTTP},
		{ID: "s3", Transport: TransportStdio},
		{ID: "s4", Transport: TransportSSE},
	}

	for _, e := range entries {
		if err := reg.Register(ctx, e); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
	}

	results, err := reg.FindByTransport(ctx, TransportHTTP)
	if err != nil {
		t.Fatalf("FindByTransport() failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("FindByTransport(HTTP) returned %d, want 2", len(results))
	}

	results, err = reg.FindByTransport(ctx, TransportStdio)
	if err != nil {
		t.Fatalf("FindByTransport() failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("FindByTransport(Stdio) returned %d, want 1", len(results))
	}

	results, err = reg.FindByTransport(ctx, TransportSSE)
	if err != nil {
		t.Fatalf("FindByTransport() failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("FindByTransport(SSE) returned %d, want 1", len(results))
	}
}

func TestFindBest(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entries := []ServerEntry{
		{
			ID:           "s1",
			Capabilities: []string{"code-complete"},
			Transport:    TransportHTTP,
			Health:       HealthUnhealthy,
			Stats:        ServerStats{Load: 0.9, RequestCount: 100},
		},
		{
			ID:           "s2",
			Capabilities: []string{"code-complete"},
			Transport:    TransportHTTP,
			Health:       HealthHealthy,
			Stats:        ServerStats{Load: 0.3, RequestCount: 50},
		},
		{
			ID:           "s3",
			Capabilities: []string{"code-complete"},
			Transport:    TransportHTTP,
			Health:       HealthHealthy,
			Stats:        ServerStats{Load: 0.1, RequestCount: 200},
		},
	}

	for _, e := range entries {
		if err := reg.Register(ctx, e); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
	}

	result, err := reg.FindBest(ctx, MatchCriteria{
		Capabilities: []string{"code-complete"},
		Transport:    TransportHTTP,
		MinHealth:    HealthHealthy,
		MaxLoad:      0.5,
	})
	if err != nil {
		t.Fatalf("FindBest() failed: %v", err)
	}
	if result.ID != "s3" {
		t.Errorf("FindBest() ID = %v, want s3 (lowest load)", result.ID)
	}

	_, err = reg.FindBest(ctx, MatchCriteria{Capabilities: []string{"nonexistent"}})
	if err == nil {
		t.Error("Expected error for no matching servers")
	}
}

func TestUpdateHealth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entry := ServerEntry{
		ID:     "health-test",
		Health: HealthHealthy,
	}

	if err := reg.Register(ctx, entry); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	if err := reg.UpdateHealth(ctx, "health-test", HealthUnhealthy); err != nil {
		t.Fatalf("UpdateHealth() failed: %v", err)
	}

	updated, _ := reg.Get(ctx, "health-test")
	if updated.Health != HealthUnhealthy {
		t.Errorf("Health = %v, want %v", updated.Health, HealthUnhealthy)
	}
}

func TestListUnhealthy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entries := []ServerEntry{
		{ID: "s1", Health: HealthHealthy},
		{ID: "s2", Health: HealthUnhealthy},
		{ID: "s3", Health: HealthUnknown},
		{ID: "s4", Health: HealthHealthy},
	}

	for _, e := range entries {
		if err := reg.Register(ctx, e); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
	}

	unhealthy, err := reg.ListUnhealthy(ctx)
	if err != nil {
		t.Fatalf("ListUnhealthy() failed: %v", err)
	}
	if len(unhealthy) != 2 {
		t.Errorf("ListUnhealthy() returned %d, want 2", len(unhealthy))
	}
}

func TestEventSubscription(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	ch := make(chan RegistryEvent, 50)
	unsubscribe := reg.Subscribe(ch)
	defer unsubscribe()

	entry := ServerEntry{ID: "event-test", Address: "localhost:8080", Transport: TransportHTTP}

	if err := reg.Register(ctx, entry); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	if err := reg.UpdateHealth(ctx, "event-test", HealthUnhealthy); err != nil {
		t.Fatalf("UpdateHealth() failed: %v", err)
	}

	if err := reg.Unregister(ctx, "event-test"); err != nil {
		t.Fatalf("Unregister() failed: %v", err)
	}

	eventCount := 0
	timeout := time.After(2 * time.Second)
	for eventCount < 3 {
		select {
		case event := <-ch:
			eventCount++
			t.Logf("Received event %d: type=%d, id=%s", eventCount, event.Type, event.Server.ID)
		case <-timeout:
			t.Fatalf("Timeout waiting for events, received %d of 3", eventCount)
		}
	}

	if eventCount != 3 {
		t.Errorf("Received %d events, want 3", eventCount)
	}
}

func TestPersistence(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "registry-test-*.json")
	if err != nil {
		t.Fatalf("CreateTemp() failed: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	entries := []ServerEntry{
		{ID: "persist-s1", Capabilities: []string{"cap1"}, Transport: TransportHTTP, Health: HealthHealthy},
		{ID: "persist-s2", Capabilities: []string{"cap2"}, Transport: TransportStdio, Health: HealthHealthy},
	}

	ctx := context.Background()

	{
		reg := NewRegistry(logger, tmpPath).(*inMemoryRegistry)
		for _, e := range entries {
			if err := reg.Register(ctx, e); err != nil {
				t.Fatalf("Register() failed: %v", err)
			}
		}
		if err := reg.Save(ctx); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}
	}

	{
		reg := NewRegistry(logger, tmpPath).(*inMemoryRegistry)
		if err := reg.Load(ctx); err != nil {
			t.Fatalf("Load() failed: %v", err)
		}

		list, err := reg.List(ctx)
		if err != nil {
			t.Fatalf("List() failed: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("Loaded %d servers, want 2", len(list))
		}

		s1, _ := reg.Get(ctx, "persist-s1")
		if s1 == nil || s1.Capabilities[0] != "cap1" {
			t.Error("Server persist-s1 not loaded correctly")
		}
	}
}

func TestConcurrency(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			entry := ServerEntry{
				ID:        fmt.Sprintf("concurrent-%d", id),
				Transport: TransportHTTP,
			}
			reg.Register(ctx, entry)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reg.Get(ctx, fmt.Sprintf("concurrent-%d", id))
		}(i)
	}

	for i := 5; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reg.Unregister(ctx, fmt.Sprintf("concurrent-%d", id))
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		reg.List(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		reg.FindByCapability(ctx, "any")
	}()

	wg.Wait()
}

func TestUpdate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entry := ServerEntry{
		ID:           "update-test",
		Address:      "localhost:8080",
		Capabilities: []string{"cap1"},
		Transport:    TransportHTTP,
	}

	if err := reg.Register(ctx, entry); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	updated := ServerEntry{
		ID:           "update-test",
		Address:      "localhost:9090",
		Capabilities: []string{"cap1", "cap2"},
		Transport:    TransportHTTP,
	}

	if err := reg.Update(ctx, updated); err != nil {
		t.Fatalf("Update() failed: %v", err)
	}

	result, _ := reg.Get(ctx, "update-test")
	if result.Address != "localhost:9090" {
		t.Errorf("Address = %v, want localhost:9090", result.Address)
	}
	if len(result.Capabilities) != 2 {
		t.Errorf("Capabilities len = %d, want 2", len(result.Capabilities))
	}

	caps, _ := reg.FindByCapability(ctx, "cap2")
	if len(caps) != 1 {
		t.Errorf("FindByCapability(cap2) returned %d, want 1", len(caps))
	}
}

func TestEmptyCapabilityListQuery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entry := ServerEntry{
		ID:          "empty-cap-test",
		Capabilities: []string{},
		Transport:    TransportHTTP,
	}

	if err := reg.Register(ctx, entry); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	results, err := reg.FindByCapability(ctx, "any")
	if err != nil {
		t.Fatalf("FindByCapability() failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("FindByCapability() returned %d, want 0", len(results))
	}
}

func TestMatchCriteriaTransport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entries := []ServerEntry{
		{ID: "s1", Transport: TransportHTTP, Capabilities: []string{"cap1"}},
		{ID: "s2", Transport: TransportStdio, Capabilities: []string{"cap1"}},
	}

	for _, e := range entries {
		if err := reg.Register(ctx, e); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
	}

	result, err := reg.FindBest(ctx, MatchCriteria{
		Capabilities: []string{"cap1"},
		Transport:    TransportHTTP,
	})
	if err != nil {
		t.Fatalf("FindBest() failed: %v", err)
	}
	if result.ID != "s1" {
		t.Errorf("FindBest() ID = %v, want s1", result.ID)
	}

	_, err = reg.FindBest(ctx, MatchCriteria{
		Capabilities: []string{"cap1"},
		Transport:    TransportSSE,
	})
	if err == nil {
		t.Error("Expected error for no matching servers with SSE transport")
	}
}

func TestMatchCriteriaMaxLoad(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg := NewRegistry(logger, "").(*inMemoryRegistry)

	ctx := context.Background()

	entries := []ServerEntry{
		{ID: "s1", Transport: TransportHTTP, Stats: ServerStats{Load: 0.1}},
		{ID: "s2", Transport: TransportHTTP, Stats: ServerStats{Load: 0.9}},
	}

	for _, e := range entries {
		if err := reg.Register(ctx, e); err != nil {
			t.Fatalf("Register() failed: %v", err)
		}
	}

	result, err := reg.FindBest(ctx, MatchCriteria{
		Transport: TransportHTTP,
		MaxLoad:    0.5,
	})
	if err != nil {
		t.Fatalf("FindBest() failed: %v", err)
	}
	if result.ID != "s1" {
		t.Errorf("FindBest() ID = %v, want s1 (lower load)", result.ID)
	}
}

