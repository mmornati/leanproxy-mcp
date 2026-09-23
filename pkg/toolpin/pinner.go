package toolpin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// Options configures a Pinner.
type Options struct {
	Mode    Mode
	Store   *Store
	Scanner *Scanner
	Logger  *slog.Logger
	// ServerDomains maps a server to URL hosts that belong to it (an
	// HTTP/SSE server's own host), never reported by the scanner.
	ServerDomains map[string][]string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Pinner applies the pinning policy: Observe records what the upstreams
// serve and reports drift, Check tells whether a tool may be used. A nil
// *Pinner is valid and disabled. It is safe for concurrent use.
type Pinner struct {
	mode    Mode
	store   *Store
	scanner *Scanner
	logger  *slog.Logger
	domains map[string][]string
	now     func() time.Time

	hookMu sync.Mutex
	hooks  []func(Event)

	// announced holds the servers whose still-pending tools were already
	// logged by this process; shadowSeen the collisions already reported.
	announced  sync.Map
	shadowSeen sync.Map
	// observed holds the servers this process has compared at least once.
	observed sync.Map

	idx atomic.Pointer[pinIndex]
}

// New builds the pinner of a `security.tool_pinning` block. It returns nil
// (pinning disabled) for mode off.
func New(cfg *Config, serverDomains map[string][]string, logger *slog.Logger) (*Pinner, error) {
	mode := cfg.EffectiveMode()
	if mode == ModeOff {
		return nil, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	path, err := cfg.ResolvePath()
	if err != nil {
		return nil, err
	}
	store, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	var allowed []string
	var maxDesc int
	if cfg != nil {
		allowed = cfg.AllowedDomains
		maxDesc = cfg.MaxDescriptionChars
	}
	return NewWithOptions(Options{
		Mode:          mode,
		Store:         store,
		Scanner:       NewScanner(ScannerOptions{AllowedDomains: allowed, MaxDescriptionChars: maxDesc}),
		Logger:        logger,
		ServerDomains: serverDomains,
	}), nil
}

// NewWithOptions builds a pinner from its parts. Store is required.
func NewWithOptions(o Options) *Pinner {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Scanner == nil {
		o.Scanner = NewScanner(ScannerOptions{})
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Mode == "" {
		o.Mode = ModeWarn
	}
	return &Pinner{
		mode:    o.Mode,
		store:   o.Store,
		scanner: o.Scanner,
		logger:  o.Logger,
		domains: o.ServerDomains,
		now:     o.Now,
	}
}

// Mode is the policy mode (ModeOff for a nil pinner).
func (p *Pinner) Mode() Mode {
	if p == nil {
		return ModeOff
	}
	return p.mode
}

// Store is the pin file store (nil for a nil pinner).
func (p *Pinner) Store() *Store {
	if p == nil {
		return nil
	}
	return p.store
}

// OnEvent registers fn, called for every event Observe reports.
func (p *Pinner) OnEvent(fn func(Event)) {
	if p == nil || fn == nil {
		return
	}
	p.hookMu.Lock()
	p.hooks = append(p.hooks, fn)
	p.hookMu.Unlock()
}

// errNoChange makes Store.Update skip the write.
var errNoChange = errors.New("toolpin: no change")

type observedTool struct {
	def   Definition
	hash  string
	canon json.RawMessage
}

// Observe compares what server lists now (info may be nil when unknown)
// with its pins, updates the pin file and returns the events. A server not
// pinned yet is trusted on first use: every tool is pinned and approved,
// except tools with a high-severity scanner finding, which stay pending.
func (p *Pinner) Observe(server string, info *ServerInfo, defs []Definition) []Event {
	if p == nil || p.mode == ModeOff || server == "" {
		return nil
	}
	now := p.now().UTC()
	observed := make(map[string]observedTool, len(defs))
	order := make([]string, 0, len(defs))
	for _, d := range defs {
		if d.Name == "" {
			continue
		}
		canon, err := Canonical(d)
		if err != nil {
			p.logger.Warn("tool pinning: cannot canonicalize a tool definition, hashing it as sent", "server", server, "tool", d.Name, "error", err)
			canon, _ = json.Marshal(d)
		}
		sum := sha256.Sum256(canon)
		if _, dup := observed[d.Name]; !dup {
			order = append(order, d.Name)
		}
		observed[d.Name] = observedTool{def: d, hash: HashPrefix + hex.EncodeToString(sum[:]), canon: canon}
	}
	sort.Strings(order)

	var events []Event
	var err error
	if !p.upToDate(server, info, observed) {
		err = p.store.Update(func(f *File) error {
			events = events[:0]
			sp := f.Servers[server]
			if sp == nil {
				events = p.pinFirstUse(f, server, info, order, observed, now)
				return nil
			}
			var dirty bool
			events, dirty = p.compare(sp, server, info, order, observed, now)
			if !dirty {
				return errNoChange
			}
			sp.UpdatedAt = now
			return nil
		})
	}
	if err != nil && !errors.Is(err, errNoChange) {
		p.logger.Warn("tool pinning: cannot write the pin file; pins are kept in memory", "path", p.store.Path(), "error", err)
	}
	p.observed.Store(server, true)
	events = append(events, p.shadowing(server)...)
	for _, ev := range events {
		p.emit(ev)
	}
	p.announcePending(server, events)
	return events
}

// upToDate reports, from the current pins and without touching the file,
// whether what server lists now matches them exactly (the common case of
// a refresh), so Observe can skip the read-modify-write.
func (p *Pinner) upToDate(server string, info *ServerInfo, observed map[string]observedTool) bool {
	sp := p.store.Current().Servers[server]
	if sp == nil || len(sp.Tools) != len(observed) {
		return false
	}
	if info != nil && (sp.ServerInfo == nil || *sp.ServerInfo != *info || sp.PendingServerInfo != nil) {
		return false
	}
	for name, tp := range sp.Tools {
		o, ok := observed[name]
		if !ok || tp.RemovedAt != nil {
			return false
		}
		switch {
		case tp.Pending == nil && tp.Hash == o.hash:
		case tp.Pending != nil && tp.Pending.Hash == o.hash:
		default:
			return false
		}
	}
	return true
}

func (p *Pinner) scan(server string, d Definition) []Finding {
	return p.scanner.Scan(d, p.domains[server])
}

func (p *Pinner) pinFirstUse(f *File, server string, info *ServerInfo, order []string, observed map[string]observedTool, now time.Time) []Event {
	sp := &ServerPins{FirstSeen: now, UpdatedAt: now, Tools: make(map[string]*ToolPin, len(order))}
	if info != nil {
		cp := *info
		sp.ServerInfo = &cp
	}
	f.Servers[server] = sp
	var events []Event
	flagged := 0
	for _, name := range order {
		o := observed[name]
		findings := p.scan(server, o.def)
		tp := &ToolPin{FirstSeen: now}
		sev := MaxSeverity(findings)
		if sev == SeverityHigh {
			tp.Pending = &Observed{Hash: o.hash, Definition: o.canon, Findings: findings, SeenAt: now}
			flagged++
		} else {
			approvedAt := now
			tp.Hash, tp.Definition, tp.Findings = o.hash, o.canon, findings
			tp.Approval, tp.ApprovedAt = ApprovalTOFU, &approvedAt
		}
		sp.Tools[name] = tp
		if sev.AtLeast(SeverityMedium) {
			events = append(events, flaggedEvent(now, server, name, findings, sev == SeverityHigh))
		}
	}
	detail := fmt.Sprintf("%d tools pinned and approved on first use", len(order)-flagged)
	if flagged > 0 {
		detail += fmt.Sprintf(", %d held for review (high-severity scanner finding)", flagged)
	}
	return append([]Event{{Time: now, Kind: EventServerPinned, Server: server, Detail: detail}}, events...)
}

func flaggedEvent(now time.Time, server, tool string, findings []Finding, held bool) Event {
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		if f.Severity.AtLeast(SeverityMedium) {
			parts = append(parts, f.String())
		}
	}
	detail := strings.Join(parts, "; ")
	if held {
		detail += " (pending approval)"
	}
	return Event{Time: now, Kind: EventToolFlagged, Server: server, Tool: tool, Severity: MaxSeverity(findings), Detail: detail}
}

// compare updates sp from what the server lists now. dirty reports a change
// to write even when it has no event (a version bump, a tool listed again
// while still pending).
func (p *Pinner) compare(sp *ServerPins, server string, info *ServerInfo, order []string, observed map[string]observedTool, now time.Time) (events []Event, dirty bool) {
	if info != nil {
		switch {
		case sp.ServerInfo == nil:
			cp := *info
			sp.ServerInfo = &cp
			events = append(events, Event{Time: now, Kind: EventServerPinned, Server: server, Detail: "server identity pinned: " + info.Name})
		case info.Name != sp.ServerInfo.Name:
			if sp.PendingServerInfo == nil || sp.PendingServerInfo.Name != info.Name {
				cp := *info
				sp.PendingServerInfo = &cp
				events = append(events, Event{Time: now, Kind: EventServerIdentityChanged, Server: server, Severity: SeverityHigh,
					Detail: fmt.Sprintf("serverInfo name was %q, now %q", sp.ServerInfo.Name, info.Name)})
			}
		default:
			if sp.PendingServerInfo != nil {
				sp.PendingServerInfo = nil
				events = append(events, Event{Time: now, Kind: EventToolReverted, Server: server, Detail: "serverInfo name back to " + info.Name})
			}
			if info.Version != sp.ServerInfo.Version {
				p.logger.Info("tool pinning: server version changed", "server", server, "from", sp.ServerInfo.Version, "to", info.Version)
				sp.ServerInfo.Version = info.Version
				// Recorded without an event: tools are checked one by one.
				dirty = true
			}
		}
	}

	for _, name := range order {
		o := observed[name]
		tp := sp.Tools[name]
		if tp == nil {
			findings := p.scan(server, o.def)
			tp = &ToolPin{FirstSeen: now, Pending: &Observed{Hash: o.hash, Definition: o.canon, Findings: findings, SeenAt: now}}
			sp.Tools[name] = tp
			events = append(events, Event{Time: now, Kind: EventToolAdded, Server: server, Tool: name, Severity: MaxSeverity(findings),
				Detail: findingsDetail(findings), Diff: tp.Diff(server, name)})
			continue
		}
		wasRemoved := tp.RemovedAt != nil
		tp.RemovedAt = nil
		switch {
		case tp.Hash == o.hash:
			if tp.Pending != nil {
				tp.Pending = nil
				events = append(events, Event{Time: now, Kind: EventToolReverted, Server: server, Tool: name, Detail: "definition back to the approved one"})
			} else if wasRemoved {
				events = append(events, Event{Time: now, Kind: EventToolReverted, Server: server, Tool: name, Detail: "listed again, unchanged"})
			}
		case tp.Pending != nil && tp.Pending.Hash == o.hash:
			dirty = dirty || wasRemoved
		default:
			findings := p.scan(server, o.def)
			tp.Pending = &Observed{Hash: o.hash, Definition: o.canon, Findings: findings, SeenAt: now}
			kind := EventToolChanged
			if tp.Hash == "" {
				kind = EventToolAdded
			}
			events = append(events, Event{Time: now, Kind: kind, Server: server, Tool: name, Severity: MaxSeverity(findings),
				Detail: findingsDetail(findings), Diff: tp.Diff(server, name)})
		}
	}

	for _, name := range sp.SortedToolNames() {
		tp := sp.Tools[name]
		if _, ok := observed[name]; ok || tp.RemovedAt != nil {
			continue
		}
		if tp.Hash == "" {
			delete(sp.Tools, name) // never approved: nothing to keep
		} else {
			removedAt := now
			tp.RemovedAt = &removedAt
			tp.Pending = nil
		}
		events = append(events, Event{Time: now, Kind: EventToolRemoved, Server: server, Tool: name})
	}
	return events, dirty || len(events) > 0
}

func findingsDetail(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, f.String())
	}
	return "scanner: " + strings.Join(parts, "; ")
}

// NormalizeToolName folds a tool name for the shadowing check: lower case,
// letters and digits only ("Create-Issue" and "create_issue" collide).
func NormalizeToolName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Collision is one cross-server tool name collision.
type Collision struct {
	Server, Tool           string
	OtherServer, OtherTool string
}

// Collisions returns the tool name collisions between server and the other
// pinned servers (every server when server is "").
func Collisions(f *File, server string) []Collision {
	byKey := map[string][][2]string{}
	for _, sname := range f.SortedServerNames() {
		sp := f.Servers[sname]
		for _, tname := range sp.SortedToolNames() {
			if sp.Tools[tname].RemovedAt != nil {
				continue
			}
			key := NormalizeToolName(tname)
			if key == "" {
				continue
			}
			byKey[key] = append(byKey[key], [2]string{sname, tname})
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Collision
	for _, k := range keys {
		list := byKey[k]
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				a, b := list[i], list[j]
				if a[0] == b[0] {
					continue
				}
				if server != "" && a[0] != server && b[0] != server {
					continue
				}
				out = append(out, Collision{Server: a[0], Tool: a[1], OtherServer: b[0], OtherTool: b[1]})
			}
		}
	}
	return out
}

// maxShadowDetail caps the collisions listed in one tool_shadowed event.
const maxShadowDetail = 10

// shadowing reports, in one event, the collisions between server's tool
// names and other servers' that this process has not reported yet.
func (p *Pinner) shadowing(server string) []Event {
	var fresh []string
	for _, c := range Collisions(p.store.Current(), server) {
		pair := fmt.Sprintf("%s/%s <-> %s/%s", c.Server, c.Tool, c.OtherServer, c.OtherTool)
		if _, seen := p.shadowSeen.LoadOrStore(pair, true); !seen {
			fresh = append(fresh, pair)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	n := len(fresh)
	if n > maxShadowDetail {
		fresh = append(fresh[:maxShadowDetail], fmt.Sprintf("and %d more", n-maxShadowDetail))
	}
	return []Event{{Time: p.now().UTC(), Kind: EventToolShadowed, Server: server, Severity: SeverityLow,
		Detail: fmt.Sprintf("%d tool name collision(s) with other servers: %s (discovery is namespaced, so both stay reachable)", n, strings.Join(fresh, ", "))}}
}

// emit logs ev, keeps it in the recent history and calls the hooks.
func (p *Pinner) emit(ev Event) {
	attrs := []any{"event", string(ev.Kind), "server", ev.Server}
	if ev.Tool != "" {
		attrs = append(attrs, "tool", ev.Tool)
	}
	if ev.Severity != "" {
		attrs = append(attrs, "severity", string(ev.Severity))
	}
	if ev.Detail != "" {
		attrs = append(attrs, "detail", ev.Detail)
	}
	switch ev.Kind {
	case EventToolAdded, EventToolChanged:
		attrs = append(attrs, "review", "leanproxy-mcp tools pins diff "+ev.Server, "approve", ApproveCommand(ev.Server, ev.Tool))
		if ev.Diff != "" {
			attrs = append(attrs, "diff", truncate(ev.Diff, 4000))
		}
		p.logger.Warn("tool pinning: "+eventSummary(ev, p.mode), attrs...)
	case EventServerIdentityChanged:
		attrs = append(attrs, "approve", "leanproxy-mcp tools pins approve "+ev.Server)
		p.logger.Warn("tool pinning: "+eventSummary(ev, p.mode), attrs...)
	case EventToolRemoved, EventToolShadowed:
		p.logger.Warn("tool pinning: "+eventSummary(ev, p.mode), attrs...)
	case EventToolFlagged:
		p.logger.Warn("tool pinning: "+eventSummary(ev, p.mode), attrs...)
	default:
		p.logger.Info("tool pinning: "+eventSummary(ev, p.mode), attrs...)
	}
	recordRecent(ev)
	p.hookMu.Lock()
	hooks := append([]func(Event){}, p.hooks...)
	p.hookMu.Unlock()
	for _, fn := range hooks {
		fn(ev)
	}
}

func eventSummary(ev Event, mode Mode) string {
	held := ""
	if mode == ModeBlock {
		held = "; blocked until approved"
	}
	switch ev.Kind {
	case EventServerPinned:
		return "server pinned (trust on first use)"
	case EventToolAdded:
		return "new tool" + held
	case EventToolChanged:
		return "tool definition changed since it was approved" + held
	case EventToolRemoved:
		return "pinned tool no longer listed"
	case EventToolReverted:
		return "back to the approved definition"
	case EventServerIdentityChanged:
		return "server identity changed" + held
	case EventToolFlagged:
		return "scanner finding in tool metadata"
	case EventToolShadowed:
		return "tool names collide with other servers' tools"
	}
	return string(ev.Kind)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated; see `leanproxy-mcp tools pins diff`)"
}

// announcePending logs, once per process and server, the tools still
// awaiting approval that this Observe call did not report again.
func (p *Pinner) announcePending(server string, events []Event) {
	if _, done := p.announced.LoadOrStore(server, true); done {
		return
	}
	reported := map[string]bool{}
	for _, ev := range events {
		reported[ev.Tool] = true
	}
	pending, identity := p.Unapproved(server)
	var names []string
	for _, u := range pending {
		if !reported[u.Tool] {
			names = append(names, u.Tool+" ("+string(u.Status)+")")
		}
	}
	if len(names) == 0 && !identity {
		return
	}
	p.logger.Warn("tool pinning: tools still awaiting approval", "server", server, "mode", string(p.mode),
		"tools", strings.Join(names, ", "), "identity_changed", identity,
		"review", "leanproxy-mcp tools pins diff "+server)
}

// ApproveCommand is the CLI command that approves one tool.
func ApproveCommand(server, tool string) string {
	if tool == "" {
		return "leanproxy-mcp tools pins approve " + server + " --all"
	}
	return "leanproxy-mcp tools pins approve " + server + " " + tool
}

// pinIndex is the lookup form of one File snapshot.
type pinIndex struct {
	file       *File
	servers    map[string]*serverIndex
	anyPending bool
}

type serverIndex struct {
	identityPending bool
	tools           map[string]Unapproved
}

// Unapproved is a tool awaiting approval.
type Unapproved struct {
	Tool     string
	Status   Status
	Severity Severity
}

func (p *Pinner) index() *pinIndex {
	f := p.store.Current()
	if ix := p.idx.Load(); ix != nil && ix.file == f {
		return ix
	}
	ix := &pinIndex{file: f, servers: make(map[string]*serverIndex, len(f.Servers))}
	for name, sp := range f.Servers {
		si := &serverIndex{
			identityPending: sp.PendingServerInfo != nil,
			tools:           make(map[string]Unapproved, len(sp.Tools)),
		}
		for tname, tp := range sp.Tools {
			u := Unapproved{Tool: tname, Status: tp.Status()}
			if tp.Pending != nil {
				u.Severity = MaxSeverity(tp.Pending.Findings)
			}
			si.tools[tname] = u
			if u.Status == StatusNew || u.Status == StatusChanged {
				ix.anyPending = true
			}
		}
		if si.identityPending {
			ix.anyPending = true
		}
		ix.servers[name] = si
	}
	p.idx.Store(ix)
	return ix
}

// Observed reports whether this process has compared server's tools with
// its pins yet. Until then a tool's status comes from the pin file alone,
// and the upstream may already serve something else.
func (p *Pinner) Observed(server string) bool {
	if p == nil {
		return false
	}
	_, ok := p.observed.Load(server)
	return ok
}

// Check returns a tool's status and whether the policy refuses it (block
// mode, and the tool is new, changed, or its server's identity changed).
func (p *Pinner) Check(server, tool string) (Status, bool) {
	if p == nil || p.mode == ModeOff {
		return StatusUnknown, false
	}
	si := p.index().servers[server]
	if si == nil {
		return StatusUnknown, false
	}
	status := si.tools[tool].Status
	blocked := p.mode == ModeBlock && (si.identityPending || status == StatusNew || status == StatusChanged)
	return status, blocked
}

// IdentityPending reports whether server's serverInfo name changed and
// awaits approval.
func (p *Pinner) IdentityPending(server string) bool {
	if p == nil || p.mode == ModeOff {
		return false
	}
	si := p.index().servers[server]
	return si != nil && si.identityPending
}

// Unapproved returns server's tools awaiting approval (sorted) and whether
// its identity change awaits approval.
func (p *Pinner) Unapproved(server string) ([]Unapproved, bool) {
	if p == nil || p.mode == ModeOff {
		return nil, false
	}
	si := p.index().servers[server]
	if si == nil {
		return nil, false
	}
	var out []Unapproved
	for _, u := range si.tools {
		if u.Status == StatusNew || u.Status == StatusChanged {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out, si.identityPending
}

// HasPending reports whether any server has a tool or identity change
// awaiting approval.
func (p *Pinner) HasPending() bool {
	if p == nil || p.mode == ModeOff {
		return false
	}
	return p.index().anyPending
}

// Servers returns the pinned server names.
func (p *Pinner) Servers() []string {
	if p == nil || p.mode == ModeOff {
		return nil
	}
	ix := p.index()
	out := make([]string, 0, len(ix.servers))
	for name := range ix.servers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// BlockedMessage is the error a refused tool call gets.
func BlockedMessage(server, tool string, status Status, identity bool) string {
	reason := "its definition changed since it was approved"
	switch {
	case identity:
		reason = "its server's identity (serverInfo name) changed since it was approved"
	case status == StatusNew:
		reason = "it is new and has not been approved"
	}
	return fmt.Sprintf("tool %s/%s is blocked by tool pinning: %s. Review it with `leanproxy-mcp tools pins diff %s`, then approve it with `%s`.",
		server, tool, reason, server, approveHint(server, tool, identity))
}

func approveHint(server, tool string, identity bool) string {
	if identity {
		return ApproveCommand(server, "")
	}
	return ApproveCommand(server, tool)
}

// Approve accepts pending changes of server in f: the named tools, or
// every pending tool, identity change and removal with all. With neither
// tools nor all it accepts only a pending identity change. It returns what
// was approved.
func Approve(f *File, server string, tools []string, all bool, now time.Time) ([]string, error) {
	sp := f.Servers[server]
	if sp == nil {
		return nil, fmt.Errorf("server %q has no pins", server)
	}
	now = now.UTC()
	var done []string
	approveIdentity := func() {
		if sp.PendingServerInfo != nil {
			sp.ServerInfo = sp.PendingServerInfo
			sp.PendingServerInfo = nil
			done = append(done, "server identity ("+sp.ServerInfo.Name+")")
		}
	}
	approveTool := func(name string, tp *ToolPin) bool {
		switch {
		case tp.RemovedAt != nil:
			delete(sp.Tools, name)
			done = append(done, name+" (removal)")
		case tp.Pending != nil:
			at := now
			tp.Hash, tp.Definition, tp.Findings = tp.Pending.Hash, tp.Pending.Definition, tp.Pending.Findings
			tp.Pending, tp.Approval, tp.ApprovedAt = nil, ApprovalManual, &at
			done = append(done, name)
		default:
			return false
		}
		return true
	}
	switch {
	case all:
		approveIdentity()
		for _, name := range sp.SortedToolNames() {
			approveTool(name, sp.Tools[name])
		}
	case len(tools) == 0:
		approveIdentity()
		if len(done) == 0 {
			return nil, fmt.Errorf("server %q has no pending identity change; name the tools to approve or pass --all", server)
		}
	default:
		for _, name := range tools {
			tp := sp.Tools[name]
			if tp == nil {
				return nil, fmt.Errorf("server %q has no pinned tool %q", server, name)
			}
			if !approveTool(name, tp) {
				return nil, fmt.Errorf("tool %s/%s has nothing to approve", server, name)
			}
		}
	}
	if len(done) > 0 {
		sp.UpdatedAt = now
	}
	return done, nil
}

// Reset forgets every pin of server; the next refresh pins it again on
// first use.
func Reset(f *File, server string) error {
	if _, ok := f.Servers[server]; !ok {
		return fmt.Errorf("server %q has no pins", server)
	}
	delete(f.Servers, server)
	return nil
}
