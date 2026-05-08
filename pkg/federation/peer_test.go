package federation

import (
	"context"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

type testLogger struct {
	debugCalls []string
	infoCalls  []string
	errorCalls []string
}

func (l *testLogger) Debug(msg string, args ...any) {
	l.debugCalls = append(l.debugCalls, msg)
}

func (l *testLogger) Info(msg string, args ...any) {
	l.infoCalls = append(l.infoCalls, msg)
}

func (l *testLogger) Error(msg string, args ...any) {
	l.errorCalls = append(l.errorCalls, msg)
}

func TestNewPeerManager_Disabled(t *testing.T) {
	logger := &testLogger{}
	pm, err := NewPeerManager(nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pm != nil {
		t.Error("expected nil peer manager when federation is disabled")
	}
}

func TestNewPeerManager_NoPeers(t *testing.T) {
	logger := &testLogger{}
	cfg := &migrate.FederationConfig{
		Enabled: true,
		Peers:   []*migrate.PeerConfig{},
	}
	pm, err := NewPeerManager(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pm == nil {
		t.Error("expected peer manager when enabled but no peers")
	}
	if len(pm.ListPeers()) != 0 {
		t.Errorf("expected 0 peers, got %d", len(pm.ListPeers()))
	}
}

func TestNewPeerManager_WithPeers(t *testing.T) {
	logger := &testLogger{}
	cfg := &migrate.FederationConfig{
		Enabled: true,
		Peers: []*migrate.PeerConfig{
			{Name: "company-a", URL: "https://proxy.company-a.internal:8080", AuthToken: "token-a"},
			{Name: "company-b", URL: "https://proxy.company-b.internal:8080"},
		},
	}
	pm, err := NewPeerManager(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pm == nil {
		t.Fatal("expected peer manager")
	}

	peers := pm.ListPeers()
	if len(peers) != 2 {
		t.Errorf("expected 2 peers, got %d", len(peers))
	}

	if pm.GetPeerStatus("company-a") != PeerStatusUnknown {
		t.Error("expected initial status to be unknown")
	}
}

func TestPeerManager_GetToolPeer_Empty(t *testing.T) {
	logger := &testLogger{}
	cfg := &migrate.FederationConfig{
		Enabled: true,
		Peers:   []*migrate.PeerConfig{},
	}
	pm, _ := NewPeerManager(cfg, logger)

	peer := pm.GetToolPeer("nonexistent-tool")
	if peer != "" {
		t.Errorf("expected empty string for unknown tool, got %q", peer)
	}
}

func TestPeerManager_ListPeers(t *testing.T) {
	logger := &testLogger{}
	cfg := &migrate.FederationConfig{
		Enabled: true,
		Peers: []*migrate.PeerConfig{
			{Name: "peer1", URL: "http://peer1:8080"},
			{Name: "peer2", URL: "http://peer2:8080"},
			{Name: "peer3", URL: "http://peer3:8080"},
		},
	}
	pm, _ := NewPeerManager(cfg, logger)

	peers := pm.ListPeers()
	if len(peers) != 3 {
		t.Errorf("expected 3 peers, got %d", len(peers))
	}
}

func TestPeerManager_IsEnabled(t *testing.T) {
	logger := &testLogger{}

	cfg := &migrate.FederationConfig{Enabled: false}
	pm, _ := NewPeerManager(cfg, logger)
	if pm != nil && pm.IsEnabled() {
		t.Error("expected disabled when config enabled is false")
	}

	cfg = &migrate.FederationConfig{Enabled: true, Peers: []*migrate.PeerConfig{}}
	pm, _ = NewPeerManager(cfg, logger)
	if pm == nil || !pm.IsEnabled() {
		t.Error("expected enabled when config enabled is true with no peers")
	}

	pm, _ = NewPeerManager(nil, logger)
	if pm != nil && pm.IsEnabled() {
		t.Error("expected nil manager when config is nil")
	}
}

func TestSplitToolRef(t *testing.T) {
	tests := []struct {
		input    string
		wantNs   string
		wantName string
	}{
		{"github@create_issue", "github", "create_issue"},
		{"namespace@toolname", "namespace", "toolname"},
		{"toolname", "", "toolname"},
		{"@toolname", "", "toolname"},
		{"ns@", "ns", ""},
	}

	for _, tt := range tests {
		ns, name := splitToolRef(tt.input)
		if ns != tt.wantNs || name != tt.wantName {
			t.Errorf("splitToolRef(%q) = (%q, %q), want (%q, %q)",
				tt.input, ns, name, tt.wantNs, tt.wantName)
		}
	}
}

func TestFederationRouter_NotEnabled(t *testing.T) {
	logger := &testLogger{}
	fr := NewFederationRouter(nil, logger)

	if fr.IsEnabled() {
		t.Error("expected router to not be enabled when peer manager is nil")
	}

	result, err := fr.Route(context.Background(), "tool")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result when not enabled, got %q", result)
	}
}