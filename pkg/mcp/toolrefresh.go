package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// DefaultToolRefreshRetryInterval is how often the background refresh
// retries servers whose tool list is unknown or whose last refresh failed.
// A server that keeps failing is retried less and less often, see
// MaxToolRefreshRetryInterval.
const DefaultToolRefreshRetryInterval = 30 * time.Second

// MaxToolRefreshRetryInterval caps the per-server backoff of the periodic
// retry: the delay doubles after every failure (30s, 1m, 2m, ...) up to
// this. A server that needs an authorization nobody gives it would
// otherwise be asked, and its failure logged, every 30 seconds forever.
const MaxToolRefreshRetryInterval = 10 * time.Minute

// ToolRefreshStatus is the outcome of a server's tool refreshes so far.
type ToolRefreshStatus struct {
	// LastError and LastErrorAt describe the most recent failed refresh.
	// They are kept after a later success: Failures tells whether the
	// server is failing now.
	LastError   string
	LastErrorAt time.Time
	// Failures counts the refreshes that failed since the last success.
	Failures int
	// LastSuccessAt is when the tool list was last fetched.
	LastSuccessAt time.Time
	// NextRetryAt is the earliest time of the next periodic retry; zero
	// when none is pending. An explicit refresh (a server event, a caller)
	// does not wait for it.
	NextRetryAt time.Time
}

// refreshState is a server's ToolRefreshStatus plus its backoff position.
// It is guarded by Handler.refreshMu.
type refreshState struct {
	ToolRefreshStatus
	// backoffStep is the number of failures since the last success or
	// explicit refresh; it selects the delay before the next periodic retry.
	backoffStep int
}

// ToolRefreshStatus returns the outcome of server's tool refreshes: the zero
// value when it was never refreshed.
func (h *Handler) ToolRefreshStatus(server string) ToolRefreshStatus {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	if st := h.refreshStatus[server]; st != nil {
		return st.ToolRefreshStatus
	}
	return ToolRefreshStatus{}
}

// refreshBackoff is the delay before the periodic retry that follows the
// step-th consecutive failure (step >= 1): base, doubled for every earlier
// failure, capped at MaxToolRefreshRetryInterval (or at base, when that is
// longer).
func refreshBackoff(base time.Duration, step int) time.Duration {
	limit := max(base, MaxToolRefreshRetryInterval)
	d := base
	for i := 1; i < step && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// refreshStateLocked returns name's state, creating it. refreshMu must be
// held.
func (h *Handler) refreshStateLocked(name string) *refreshState {
	if h.refreshStatus == nil {
		h.refreshStatus = make(map[string]*refreshState)
	}
	st := h.refreshStatus[name]
	if st == nil {
		st = &refreshState{}
		h.refreshStatus[name] = st
	}
	return st
}

// recordRefreshLocked stores the outcome of a refresh of name. refreshMu
// must be held; the returned func, to call without it, logs a failure: at
// WARN the first time and whenever the error changes, at DEBUG while the
// same error repeats.
func (h *Handler) recordRefreshLocked(name string, err error) func() {
	now := time.Now()
	st := h.refreshStateLocked(name)
	if err == nil {
		st.Failures, st.backoffStep = 0, 0
		st.LastSuccessAt = now
		st.NextRetryAt = time.Time{}
		return func() {}
	}
	interval := h.retryInterval
	if interval <= 0 {
		interval = DefaultToolRefreshRetryInterval
	}
	msg := err.Error()
	repeated := st.Failures > 0 && st.LastError == msg
	st.Failures++
	st.backoffStep++
	st.LastError, st.LastErrorAt = msg, now
	st.NextRetryAt = now.Add(refreshBackoff(interval, st.backoffStep))
	failures, next := st.Failures, st.NextRetryAt

	level := slog.LevelWarn
	if repeated {
		level = slog.LevelDebug
	}
	return func() {
		h.logger.Log(context.Background(), level, "failed to refresh tools", "server", name, "error", err,
			"failures", failures, "next_retry", next.Format(time.RFC3339))
	}
}

// retryDue reports whether the periodic retry of name may run now. The
// tolerance keeps a retry planned for "one interval from now" on the next
// tick instead of the one after: the tick comes one interval after the
// previous tick, slightly before one interval after the failure it caused.
func (h *Handler) retryDue(name string, now time.Time, interval time.Duration) bool {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	st := h.refreshStatus[name]
	return st == nil || !now.Before(st.NextRetryAt.Add(-interval/2))
}

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
	if h.exposure.Load().MayListUpstreamTools() {
		go h.watchExposure(ctx)
	}
}

// handleServerEvent refreshes a server's tools when the pool reports that
// they may have changed.
//
// Resource and prompt list changes are not cached: the aggregated lists are
// fetched on demand, so the clients are only told to fetch them again
// (notifications/resources/list_changed, notifications/prompts/list_changed).
// A restarted or reconnected server may serve different lists, so a new
// session after the first one notifies too; so does a first session, once
// a client was answered initialize before every upstream was known (it may
// have listed without this server).
func (h *Handler) handleServerEvent(ev pool.ServerEvent) {
	if h.backgroundContext().Err() != nil {
		return
	}
	switch ev.Kind {
	case pool.EventResourcesListChanged:
		h.logger.Debug("server resource list changed", "server", ev.Server)
		h.notifySessions(NotificationResourcesListChanged)
		return
	case pool.EventPromptsListChanged:
		h.logger.Debug("server prompt list changed", "server", ev.Server)
		h.notifySessions(NotificationPromptsListChanged)
		return
	case pool.EventSessionStarted:
		if ev.Generation > 1 || h.capsGuessed.Load() {
			h.notifyListChangedFor(ev.Server)
		}
	}
	h.logger.Debug("server event, refreshing its tools", "server", ev.Server, "event", ev.Kind.String(), "generation", ev.Generation)
	h.startRefresh(ev.Server, true)
}

// retryLoop periodically refreshes servers without a known tool list, each
// with its own backoff (see refreshBackoff). A stdio server in the error
// state is skipped: its restart (by the pool's reconnect logic) reports a
// new session, which triggers the refresh, and a request here would restart
// it outside that backoff.
func (h *Handler) retryLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		for _, name := range h.pool.ListServers() {
			if !h.needsRetry(name) || !h.retryDue(name, now, interval) {
				continue
			}
			transport, _ := h.pool.GetServerTransport(name)
			state, _ := h.pool.GetServerState(name)
			if transport == "stdio" && healthLabel(state) != "healthy" {
				continue
			}
			h.startRefreshWith(name, false, false)
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
// may predate the change that triggered this call). The refresh is an
// explicit one (a server event, a caller): it restarts the backoff of the
// periodic retry.
func (h *Handler) startRefresh(name string, force bool) *refreshCall {
	return h.startRefreshWith(name, force, true)
}

// startRefreshWith is startRefresh; explicit is false for the periodic
// retry, which keeps its backoff.
func (h *Handler) startRefreshWith(name string, force, explicit bool) *refreshCall {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	if explicit {
		if st := h.refreshStatus[name]; st != nil {
			st.backoffStep = 0
		}
	}
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
			if err != nil {
				h.logger.Debug("failed to refresh tools, refreshing again", "server", name, "error", err)
			}
			continue
		}
		// Recorded before the call is released, so whoever waited for it
		// reads a status that includes it.
		logFailure := h.recordRefreshLocked(name, err)
		delete(h.refreshing, name)
		call.err = err
		if err != nil {
			h.refreshErrs[name] = err
		} else {
			delete(h.refreshErrs, name)
		}
		h.refreshMu.Unlock()
		logFailure()
		close(call.done)
		return
	}
}

// fetchServerTools sends tools/list to one server (every page, following
// nextCursor, bounded by its timeout) and stores the result. The request is
// detached from any caller: a list_tools caller that gives up does not
// abort the shared refresh.
func (h *Handler) fetchServerTools(name string) error {
	h.cacheRefreshes.Add(1)
	h.logger.Debug("refreshing tools", "server", name)
	items, err := h.listUpstream(h.backgroundContext(), name, MethodToolsList, "tools")
	tools := make([]Tool, 0, len(items))
	if err == nil {
		for _, item := range items {
			var t Tool
			if uerr := json.Unmarshal(item, &t); uerr != nil {
				err = fmt.Errorf("invalid tools/list result: %w", uerr)
				break
			}
			tools = append(tools, t)
		}
	}
	if err != nil {
		// Logged by recordRefresh, which knows whether the error is new.
		h.cacheFailures.Add(1)
		return err
	}

	// Tool pinning (#310) sees the definitions exactly as the upstream sent
	// them; clients only ever see them with invisible characters stripped.
	h.observePins(name, tools)
	h.setServerTools(name, sanitizeTools(tools))
	return nil
}

// setServerTools stores a server's tool list, persists it and notifies the
// OnToolsChanged listeners when it changed.
func (h *Handler) setServerTools(name string, tools []Tool) {
	if tools == nil {
		tools = []Tool{}
	}
	h.toolCache.mu.RLock()
	old, had := h.toolCache.tools[name]
	h.toolCache.mu.RUnlock()
	changed := !had || !reflect.DeepEqual(old, tools)
	// Index before publishing to the cache: search_tools treats a server
	// whose tools are cached as searchable.
	if changed {
		h.searchIndex().SetServerTools(name, toSearchTools(tools))
	}
	h.toolCache.mu.Lock()
	h.toolCache.tools[name] = tools
	h.toolCache.mu.Unlock()

	if h.toolStore != nil {
		if err := h.toolStore.SetTools(name, toolsToCachedTools(tools)); err != nil {
			h.logger.Warn("failed to persist tools to cache", "name", name, "error", err)
		}
	}
	h.logger.Info("tools refreshed", "server", name, "count", len(tools))

	if !changed {
		return
	}
	// Passthrough and hybrid clients list these tools themselves (#322).
	h.exposureChanged(true)
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

		tools := sanitizeTools(cachedToolsToTools(cachedTools))

		h.searchIndex().SetServerTools(serverName, toSearchTools(tools))
		h.toolCache.mu.Lock()
		h.toolCache.tools[serverName] = tools
		h.toolCache.mu.Unlock()

		h.logger.Debug("loaded tools from persistent cache", "server", serverName, "count", len(tools))
	}
}
