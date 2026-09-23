package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// DefaultToolRefreshRetryInterval is how often the background refresh
// retries servers whose tool list is unknown or whose last refresh failed.
const DefaultToolRefreshRetryInterval = 30 * time.Second

// refreshCall is one in-progress tools/list refresh of a server. Every
// caller that asks for a refresh while it runs shares it (singleflight).
type refreshCall struct {
	done chan struct{}
	err  error
	// again is set when a refresh was requested (tools/list_changed, new
	// session) while this one was running: the answer it is waiting for
	// may predate the change, so it runs once more before finishing.
	again bool
}

// SetToolRefreshRetryInterval changes how often the background refresh
// retries servers without a known tool list. It must be called before
// StartBackgroundRefresh; a non-positive value is ignored.
func (h *Handler) SetToolRefreshRetryInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	h.refreshMu.Lock()
	h.retryInterval = d
	h.refreshMu.Unlock()
}

// OnToolsChanged registers fn to be called (on the refresh goroutine) with a
// server's new tool list every time a refresh changes it. `serve` uses it to
// re-register the server's tools with its router.
func (h *Handler) OnToolsChanged(fn func(server string, tools []Tool)) {
	h.refreshMu.Lock()
	h.toolsListeners = append(h.toolsListeners, fn)
	h.refreshMu.Unlock()
}

// LoadPersistentToolCache seeds the in-memory tool cache from the
// persistent toolstore cache. It does no network I/O, so the proxy can
// serve immediately at startup.
func (h *Handler) LoadPersistentToolCache() {
	if h.toolStore == nil {
		return
	}
	h.loadFromPersistentCache()
}

// StartBackgroundRefresh starts keeping the tool cache current without ever
// blocking a request: it refreshes every server's tools/list in parallel
// (one goroutine per server), refreshes a server again whenever the pool
// reports a new session (a restart) or a tools/list_changed notification,
// and periodically retries servers whose tool list is still unknown. It
// returns immediately; everything stops when ctx ends.
func (h *Handler) StartBackgroundRefresh(ctx context.Context) {
	h.refreshMu.Lock()
	h.bgCtx = ctx
	interval := h.retryInterval
	h.refreshMu.Unlock()
	if interval <= 0 {
		interval = DefaultToolRefreshRetryInterval
	}

	if src, ok := h.pool.(pool.ServerEventSource); ok {
		src.SetServerEventHandler(h.handleServerEvent)
		context.AfterFunc(ctx, func() { src.SetServerEventHandler(nil) })
	}
	for _, name := range h.pool.ListServers() {
		h.startRefresh(name, false)
	}
	go h.retryLoop(ctx, interval)
}

// handleServerEvent refreshes a server's tools when the pool reports that
// they may have changed.
func (h *Handler) handleServerEvent(ev pool.ServerEvent) {
	if h.backgroundContext().Err() != nil {
		return
	}
	h.logger.Debug("server event, refreshing its tools", "server", ev.Server, "event", ev.Kind.String(), "generation", ev.Generation)
	h.startRefresh(ev.Server, true)
}

// retryLoop periodically refreshes servers without a known tool list. A
// stdio server in the error state is skipped: its restart (by the pool's
// reconnect logic) reports a new session, which triggers the refresh, and
// a request here would restart it outside that backoff.
func (h *Handler) retryLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, name := range h.pool.ListServers() {
			if !h.needsRetry(name) {
				continue
			}
			transport, _ := h.pool.GetServerTransport(name)
			state, _ := h.pool.GetServerState(name)
			if transport == "stdio" && healthLabel(state) != "healthy" {
				continue
			}
			h.startRefresh(name, false)
		}
	}
}

// needsRetry reports whether a server's tool list is unknown or its last
// refresh failed.
func (h *Handler) needsRetry(name string) bool {
	h.refreshMu.Lock()
	failed := h.refreshErrs[name] != nil
	h.refreshMu.Unlock()
	return failed || h.toolCountFor(name) == 0
}

func (h *Handler) backgroundContext() context.Context {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	return h.bgContextLocked()
}

// bgContextLocked returns the background refresh context. refreshMu must be
// held.
func (h *Handler) bgContextLocked() context.Context {
	if h.bgCtx == nil {
		return context.Background()
	}
	return h.bgCtx
}

// RefreshServerTools refreshes the tool list of one server and waits for
// the result, bounded by ctx and the server's own timeout. A refresh
// already in progress for that server is joined rather than duplicated.
func (h *Handler) RefreshServerTools(ctx context.Context, name string) error {
	call := h.startRefresh(name, false)
	// The refresh itself is bounded by the server's timeout; the margin lets
	// it report its own (more useful) error when it hits that bound.
	timer := time.NewTimer(h.timeoutFor(name) + refreshWaitMargin)
	defer timer.Stop()
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("listing the tools of %s did not finish within %v", name, h.timeoutFor(name))
	}
}

// refreshWaitMargin is added to a server's timeout when waiting for its
// refresh.
const refreshWaitMargin = 500 * time.Millisecond

// PopulateToolCache loads the persistent cache, then refreshes every server
// in parallel and waits (bounded by ctx) for all of them. Unlike
// StartBackgroundRefresh it blocks; it is meant for tools and tests that
// need a complete cache before continuing.
func (h *Handler) PopulateToolCache(ctx context.Context) {
	h.logger.Info("populating tool cache from backend servers")
	if h.toolStore != nil {
		h.loadFromPersistentCache()
	}
	servers := h.pool.ListServers()
	calls := make([]*refreshCall, 0, len(servers))
	for _, name := range servers {
		calls = append(calls, h.startRefresh(name, false))
	}
	for _, call := range calls {
		select {
		case <-call.done:
		case <-ctx.Done():
			return
		}
	}
	h.logger.Info("tool cache population complete")
}

// startRefresh starts a refresh of name, or joins the one in progress. With
// force, a refresh in progress runs once more before finishing (its answer
// may predate the change that triggered this call).
func (h *Handler) startRefresh(name string, force bool) *refreshCall {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	if call, ok := h.refreshing[name]; ok {
		if force {
			call.again = true
		}
		return call
	}
	if h.refreshing == nil {
		h.refreshing = make(map[string]*refreshCall)
		h.refreshErrs = make(map[string]error)
	}
	call := &refreshCall{done: make(chan struct{})}
	h.refreshing[name] = call
	go h.runRefresh(name, call)
	return call
}

func (h *Handler) runRefresh(name string, call *refreshCall) {
	for {
		err := h.fetchServerTools(name)

		h.refreshMu.Lock()
		if call.again && h.bgContextLocked().Err() == nil {
			call.again = false
			h.refreshMu.Unlock()
			continue
		}
		delete(h.refreshing, name)
		call.err = err
		if err != nil {
			h.refreshErrs[name] = err
		} else {
			delete(h.refreshErrs, name)
		}
		h.refreshMu.Unlock()
		close(call.done)
		return
	}
}

// fetchServerTools sends tools/list to one server (bounded by its timeout)
// and stores the result. The request is detached from any caller: a
// list_tools caller that gives up does not abort the shared refresh.
func (h *Handler) fetchServerTools(name string) error {
	h.cacheRefreshes.Add(1)
	timeout := h.timeoutFor(name)
	ctx, cancel := context.WithTimeout(h.backgroundContext(), timeout)
	defer cancel()

	h.logger.Debug("refreshing tools", "server", name)
	resp, err := h.pool.SendRequestToServer(ctx, name, MethodToolsList, nil, timeout)
	if err == nil && resp == nil {
		err = fmt.Errorf("no response")
	}
	if err == nil && resp.Error != nil {
		err = fmt.Errorf("server error: %s", resp.Error.Message)
	}
	var result ToolsListResult
	if err == nil {
		if len(resp.Result) == 0 || string(resp.Result) == "null" {
			err = fmt.Errorf("empty tools/list result")
		} else if uerr := json.Unmarshal(resp.Result, &result); uerr != nil {
			err = fmt.Errorf("invalid tools/list result: %w", uerr)
		}
	}
	if err != nil {
		h.cacheFailures.Add(1)
		h.logger.Warn("failed to refresh tools", "server", name, "error", err)
		return err
	}

	h.setServerTools(name, result.Tools)
	return nil
}

// setServerTools stores a server's tool list, persists it and notifies the
// OnToolsChanged listeners when it changed.
func (h *Handler) setServerTools(name string, tools []Tool) {
	if tools == nil {
		tools = []Tool{}
	}
	h.toolCache.mu.Lock()
	old, had := h.toolCache.tools[name]
	h.toolCache.tools[name] = tools
	h.toolCache.mu.Unlock()

	if h.toolStore != nil {
		if err := h.toolStore.SetTools(name, toolsToCachedTools(tools)); err != nil {
			h.logger.Warn("failed to persist tools to cache", "name", name, "error", err)
		}
	}
	h.logger.Info("tools refreshed", "server", name, "count", len(tools))

	if had && reflect.DeepEqual(old, tools) {
		return
	}
	h.refreshMu.Lock()
	listeners := append([]func(string, []Tool){}, h.toolsListeners...)
	h.refreshMu.Unlock()
	for _, fn := range listeners {
		snapshot := make([]Tool, len(tools))
		copy(snapshot, tools)
		fn(name, snapshot)
	}
}

func (h *Handler) loadFromPersistentCache() {
	for _, serverName := range h.pool.ListServers() {
		cachedTools, err := h.toolStore.GetTools(serverName)
		if err != nil {
			h.logger.Warn("failed to load tools from persistent cache", "server", serverName, "error", err)
			continue
		}
		if cachedTools == nil {
			continue
		}

		tools := make([]Tool, len(cachedTools))
		for i, ct := range cachedTools {
			tools[i] = Tool{
				Name:        ct.Name,
				Description: ct.Description,
				InputSchema: ct.InputSchema,
			}
		}

		h.toolCache.mu.Lock()
		h.toolCache.tools[serverName] = tools
		h.toolCache.mu.Unlock()

		h.logger.Debug("loaded tools from persistent cache", "server", serverName, "count", len(tools))
	}
}
