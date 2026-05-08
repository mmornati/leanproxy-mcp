package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

type PeerStatus string

const (
	PeerStatusUnknown PeerStatus = "unknown"
	PeerStatusOnline  PeerStatus = "online"
	PeerStatusOffline PeerStatus = "offline"
	PeerStatusError   PeerStatus = "error"
)

type ToolInfo struct {
	Name      string
	Namespace string
	Server    string
}

type ListToolsResponse struct {
	Tools []string `json:"tools"`
}

type InvokeRequest struct {
	Server string                 `json:"server"`
	Tool   string                 `json:"tool"`
	Params map[string]interface{} `json:"params"`
}

type InvokeResponse struct {
	Result json.RawMessage `json:"result"`
}

type Peer struct {
	Name       string
	URL        string
	AuthToken  string
	Status     PeerStatus
	LastCheck  time.Time
	httpClient *http.Client
}

type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

type PeerManager struct {
	mu       sync.RWMutex
	peers    map[string]*Peer
	client   *http.Client
	logger   Logger
	toolIdx  map[string]string
}

func NewPeerManager(cfg *migrate.FederationConfig, logger Logger) (*PeerManager, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	pm := &PeerManager{
		peers:   make(map[string]*Peer),
		client:  &http.Client{Timeout: 10 * time.Second},
		logger:  logger,
		toolIdx: make(map[string]string),
	}

	for _, peerCfg := range cfg.Peers {
		peer := &Peer{
			Name:       peerCfg.Name,
			URL:        peerCfg.URL,
			AuthToken:  peerCfg.AuthToken,
			Status:     PeerStatusUnknown,
			httpClient: &http.Client{Timeout: 10 * time.Second},
		}
		pm.peers[peerCfg.Name] = peer
	}

	logger.Info("federation peer manager initialized", "peer_count", len(pm.peers))
	return pm, nil
}

func (pm *PeerManager) Connect(ctx context.Context, peerName string) error {
	pm.mu.RLock()
	peer, ok := pm.peers[peerName]
	pm.mu.RUnlock()

	if !ok {
		return fmt.Errorf("peer %q not found", peerName)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", peer.URL+"/health", nil)
	if err != nil {
		pm.updatePeerStatus(peerName, PeerStatusOffline)
		return fmt.Errorf("failed to create request: %w", err)
	}

	if peer.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+peer.AuthToken)
	}

	resp, err := peer.httpClient.Do(req)
	if err != nil {
		pm.updatePeerStatus(peerName, PeerStatusOffline)
		return fmt.Errorf("failed to connect to peer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		pm.updatePeerStatus(peerName, PeerStatusOnline)
		pm.logger.Info("peer connected", "peer", peerName)
		return nil
	}

	pm.updatePeerStatus(peerName, PeerStatusError)
	return fmt.Errorf("peer health check failed with status %d", resp.StatusCode)
}

func (pm *PeerManager) updatePeerStatus(peerName string, status PeerStatus) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if peer, ok := pm.peers[peerName]; ok {
		peer.Status = status
		peer.LastCheck = time.Now()
	}
}

func (pm *PeerManager) DiscoverTools(ctx context.Context, peerName string) ([]ToolInfo, error) {
	pm.mu.RLock()
	peer, ok := pm.peers[peerName]
	pm.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %q not found", peerName)
	}

	if peer.Status != PeerStatusOnline {
		if err := pm.Connect(ctx, peerName); err != nil {
			return nil, fmt.Errorf("peer offline: %w", err)
		}
	}

	body := []byte("{}")
	req, err := http.NewRequestWithContext(ctx, "POST", peer.URL+"/federation/list-tools", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if peer.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+peer.AuthToken)
	}

	resp, err := peer.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to discover tools: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list-tools failed: %s", string(respBody))
	}

	var listResp ListToolsResponse
	if err := json.Unmarshal(respBody, &listResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	tools := make([]ToolInfo, 0, len(listResp.Tools))
	for _, tool := range listResp.Tools {
		namespace, name := splitToolRef(tool)
		tools = append(tools, ToolInfo{
			Name:      name,
			Namespace: namespace,
			Server:    peerName,
		})
		pm.updateToolIndex(name, peerName)
	}

	pm.logger.Debug("discovered tools from peer", "peer", peerName, "count", len(tools))
	return tools, nil
}

func splitToolRef(tool string) (namespace, name string) {
	for i := len(tool) - 1; i >= 0; i-- {
		if tool[i] == '@' {
			return tool[:i], tool[i+1:]
		}
	}
	return "", tool
}

func (pm *PeerManager) updateToolIndex(toolName, peerName string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.toolIdx[toolName] = peerName
}

func (pm *PeerManager) Invoke(ctx context.Context, peerName string, server, tool string, params map[string]interface{}) ([]byte, error) {
	pm.mu.RLock()
	peer, ok := pm.peers[peerName]
	pm.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("peer %q not found", peerName)
	}

	if peer.Status != PeerStatusOnline {
		if err := pm.Connect(ctx, peerName); err != nil {
			return nil, fmt.Errorf("peer offline: %w", err)
		}
	}

	invokeReq := InvokeRequest{
		Server: server,
		Tool:   tool,
		Params: params,
	}

	body, _ := json.Marshal(invokeReq)
	req, err := http.NewRequestWithContext(ctx, "POST", peer.URL+"/federation/invoke", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if peer.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+peer.AuthToken)
	}

	resp, err := peer.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("invoke failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invoke failed: %s", string(respBody))
	}

	var invokeResp InvokeResponse
	if err := json.Unmarshal(respBody, &invokeResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return invokeResp.Result, nil
}

func (pm *PeerManager) GetPeerStatus(peerName string) PeerStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if peer, ok := pm.peers[peerName]; ok {
		return peer.Status
	}
	return PeerStatusUnknown
}

func (pm *PeerManager) GetToolPeer(toolName string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.toolIdx[toolName]
}

func (pm *PeerManager) ListPeers() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	names := make([]string, 0, len(pm.peers))
	for name := range pm.peers {
		names = append(names, name)
	}
	return names
}

func (pm *PeerManager) IsEnabled() bool {
	return pm != nil
}