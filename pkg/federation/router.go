package federation

import (
	"context"
	"fmt"
)

type FederationRouter struct {
	peerManager *PeerManager
	logger      Logger
}

func NewFederationRouter(pm *PeerManager, logger Logger) *FederationRouter {
	return &FederationRouter{
		peerManager: pm,
		logger:      logger,
	}
}

func (fr *FederationRouter) IsEnabled() bool {
	return fr.peerManager != nil && fr.peerManager.IsEnabled()
}

func (fr *FederationRouter) Route(ctx context.Context, toolName string) (peerName string, err error) {
	if !fr.IsEnabled() {
		return "", nil
	}

	peerName = fr.peerManager.GetToolPeer(toolName)
	if peerName != "" {
		fr.logger.Debug("found tool in local index", "tool", toolName, "peer", peerName)
		return peerName, nil
	}

	peers := fr.peerManager.ListPeers()
	for _, p := range peers {
		status := fr.peerManager.GetPeerStatus(p)
		if status != PeerStatusOnline {
			continue
		}

		tools, err := fr.peerManager.DiscoverTools(ctx, p)
		if err != nil {
			fr.logger.Error("failed to discover tools from peer", "peer", p, "error", err)
			continue
		}

		for _, t := range tools {
			if t.Name == toolName {
				fr.logger.Debug("found tool via discovery", "tool", toolName, "peer", p)
				return p, nil
			}
		}
	}

	return "", fmt.Errorf("tool %q not found in any federated peer", toolName)
}

func (fr *FederationRouter) InvokeWithFailover(ctx context.Context, server, tool string, params map[string]interface{}) ([]byte, error) {
	if !fr.IsEnabled() {
		return nil, fmt.Errorf("federation not enabled")
	}

	peerName, err := fr.Route(ctx, tool)
	if err != nil {
		return nil, err
	}

	peers := fr.peerManager.ListPeers()
	startIdx := 0
	for i, p := range peers {
		if p == peerName {
			startIdx = i
			break
		}
	}

	for i := 0; i < len(peers); i++ {
		idx := (startIdx + i) % len(peers)
		p := peers[idx]

		status := fr.peerManager.GetPeerStatus(p)
		if status != PeerStatusOnline {
			continue
		}

		result, err := fr.peerManager.Invoke(ctx, p, server, tool, params)
		if err != nil {
			fr.logger.Error("invoke failed on peer, trying next", "peer", p, "error", err)
			fr.markPeerOffline(p)
			continue
		}

		return result, nil
	}

	return nil, fmt.Errorf("all federated peers failed for tool %q", tool)
}

func (fr *FederationRouter) markPeerOffline(peerName string) {
	if fr.peerManager != nil {
		fr.peerManager.MarkPeerOffline(peerName)
	}
}

func (pm *PeerManager) MarkPeerOffline(peerName string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if peer, ok := pm.peers[peerName]; ok {
		peer.Status = PeerStatusOffline
	}
}