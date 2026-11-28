package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGatewayHost stands for the machine: whether a gateway listens, and
// how many were started.
type fakeGatewayHost struct {
	up     atomic.Bool
	spawns atomic.Int32
	// bootTime is how long a spawned gateway takes to listen; dies makes
	// it exit instead.
	bootTime time.Duration
	dies     bool
}

func (h *fakeGatewayHost) starter(t *testing.T, lock string) *gatewayStarter {
	t.Helper()
	return &gatewayStarter{
		addr:      "127.0.0.1:0",
		lockPath:  lock,
		logFile:   "/log/daemon.log",
		timeout:   2 * time.Second,
		poll:      2 * time.Millisecond,
		reachable: func(string) bool { return h.up.Load() },
		spawn: func() (<-chan error, error) {
			h.spawns.Add(1)
			exited := make(chan error, 1)
			if h.dies {
				exited <- errors.New("exit status 1")
				return exited, nil
			}
			time.AfterFunc(h.bootTime, func() { h.up.Store(true) })
			return exited, nil
		},
	}
}

func TestGatewayStarterLeavesARunningGatewayAlone(t *testing.T) {
	h := &fakeGatewayHost{}
	h.up.Store(true)
	lock := filepath.Join(t.TempDir(), "lock")
	if err := h.starter(t, lock).ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := h.spawns.Load(); n != 0 {
		t.Fatalf("a running gateway was started again (%d spawns)", n)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Fatal("the lock was taken although nothing had to start")
	}
}

func TestGatewayStarterStartsOneGatewayForManyClients(t *testing.T) {
	h := &fakeGatewayHost{bootTime: 30 * time.Millisecond}
	lock := filepath.Join(t.TempDir(), "sub", "lock")
	// Each starter is one `connect` process: they share only the lock.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		s := h.starter(t, lock)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.ensure(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := h.spawns.Load(); n != 1 {
		t.Fatalf("%d gateways were started for 8 clients, want 1", n)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Fatal("the start lock was left behind")
	}
}

func TestGatewayStarterReportsAGatewayThatDiesWhileStarting(t *testing.T) {
	h := &fakeGatewayHost{dies: true}
	lock := filepath.Join(t.TempDir(), "lock")
	err := h.starter(t, lock).ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stopped while starting") || !strings.Contains(err.Error(), "/log/daemon.log") {
		t.Fatalf("want an error naming the log, got %v", err)
	}
	if _, statErr := os.Stat(lock); statErr == nil {
		t.Fatal("the start lock was left behind after a failed start")
	}
}

func TestGatewayStarterTimesOutBehindAStuckStarter(t *testing.T) {
	h := &fakeGatewayHost{}
	lock := filepath.Join(t.TempDir(), "lock")
	// A live lock held by another connect that never finishes.
	if err := os.WriteFile(lock, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := h.starter(t, lock)
	s.timeout = 50 * time.Millisecond
	err := s.ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not come up") {
		t.Fatalf("want a timeout, got %v", err)
	}
	if n := h.spawns.Load(); n != 0 {
		t.Fatalf("a gateway was started while another connect held the lock (%d spawns)", n)
	}
}

func TestGatewayStarterTakesOverAStaleLock(t *testing.T) {
	h := &fakeGatewayHost{}
	lock := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(lock, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * gatewayLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := h.starter(t, lock).ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := h.spawns.Load(); n != 1 {
		t.Fatalf("%d spawns behind a stale lock, want 1", n)
	}
}

func TestGatewayStarterStopsWithItsContext(t *testing.T) {
	h := &fakeGatewayHost{bootTime: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	err := h.starter(t, filepath.Join(t.TempDir(), "lock")).ensure(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestConnectAutostart(t *testing.T) {
	const local = "http://127.0.0.1:8765/mcp"
	tests := []struct {
		name        string
		noAutostart bool
		endpoint    string
		service     bool
		want        bool
	}{
		{name: "default", endpoint: local, want: true},
		{name: "another local port", endpoint: "http://localhost:9000/mcp", want: true},
		{name: "--no-autostart", endpoint: local, noAutostart: true},
		{name: "a gateway on another machine", endpoint: "http://192.0.2.1:8765/mcp"},
		{name: "not a URL", endpoint: "127.0.0.1:8765"},
		{name: "the service owns the gateway", endpoint: local, service: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectAutostart(tt.noAutostart, tt.endpoint, tt.service); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewConnectStarter(t *testing.T) {
	home := t.TempDir()
	s, err := newConnectStarter("http://127.0.0.1:8765/mcp", home, time.Minute, "off", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.addr != "127.0.0.1:8765" || !strings.HasPrefix(s.lockPath, home) {
		t.Fatalf("unexpected starter: addr %q, lock %q", s.addr, s.lockPath)
	}
	for name, tt := range map[string]struct {
		endpoint, dashboard string
		idle                time.Duration
	}{
		"a gateway on another machine": {endpoint: "http://192.0.2.1:8765/mcp", dashboard: "off"},
		"a public dashboard":           {endpoint: "http://127.0.0.1:8765/mcp", dashboard: "0.0.0.0:9090"},
		"a negative idle time":         {endpoint: "http://127.0.0.1:8765/mcp", dashboard: "off", idle: -time.Second},
		"not a URL":                    {endpoint: "127.0.0.1:8765", dashboard: "off"},
	} {
		if _, err := newConnectStarter(tt.endpoint, home, tt.idle, tt.dashboard, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestWaitIdle(t *testing.T) {
	// Clients until `busy` drops: the idle period only starts then.
	var busy atomic.Bool
	busy.Store(true)
	count := func() int {
		if busy.Load() {
			return 1
		}
		return 0
	}
	done := make(chan bool, 1)
	go func() { done <- waitIdle(context.Background(), count, 40*time.Millisecond, 5*time.Millisecond) }()
	select {
	case <-done:
		t.Fatal("stopped while a client was connected")
	case <-time.After(120 * time.Millisecond):
	}
	busy.Store(false)
	left := time.Now()
	select {
	case idle := <-done:
		if !idle {
			t.Fatal("waitIdle reported a canceled context")
		}
		if waited := time.Since(left); waited < 30*time.Millisecond {
			t.Fatalf("stopped %s after the last client left, before the idle period", waited)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not stop once idle")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitIdle(ctx, func() int { return 0 }, time.Hour, time.Millisecond) {
		t.Fatal("a canceled wait reported idle")
	}
}

func TestCheckIdleExit(t *testing.T) {
	if err := checkIdleExit(0, ""); err != nil {
		t.Fatal(err)
	}
	if err := checkIdleExit(time.Minute, "127.0.0.1:8765"); err != nil {
		t.Fatal(err)
	}
	if checkIdleExit(time.Minute, "") == nil {
		t.Fatal("--idle-exit was accepted without --http")
	}
	if checkIdleExit(-time.Second, "127.0.0.1:8765") == nil {
		t.Fatal("a negative --idle-exit was accepted")
	}
}
