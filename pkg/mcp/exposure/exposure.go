// Package exposure decides how LeanProxy exposes the upstream tools to one
// MCP client (issue #322): through its own discovery router (search_tools,
// list_servers, list_tools, invoke_tool), or directly, every upstream tool
// listed in tools/list under a namespaced name, so a client with native
// tool search (Claude Code, the Anthropic and OpenAI APIs, Cursor, ...)
// can defer and discover the tools itself.
//
// It holds the `exposure:` config block, the per-client mode resolution
// and the namespaced tool names. It has no dependency on pkg/mcp or
// pkg/migrate, so both can import it.
//
//	exposure:
//	  mode: router             # clients no rule matches: router | passthrough | hybrid
//	  builtin_clients: true    # apply the built-in client table (BuiltinClients)
//	  clients:                 # first match wins, before the built-in table
//	    - match: "my-agent*"   # glob on clientInfo.name, case-insensitive
//	      mode: hybrid
//	  always_load: []          # "server.tool" globs listed with _meta anthropic/alwaysLoad
//	  max_name_length: 64      # cap of a namespaced tool name
package exposure

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

// Mode is how the upstream tools are exposed to a client.
type Mode string

const (
	// ModeRouter lists LeanProxy's discovery tools only (search_tools,
	// list_servers, list_tools, invoke_tool): the behavior before #322,
	// and the default for clients the built-in table does not know.
	ModeRouter Mode = "router"
	// ModePassthrough lists every upstream tool, namespaced
	// (<server>__<tool>), with its full metadata; tools/call on a
	// namespaced name routes to the upstream.
	ModePassthrough Mode = "passthrough"
	// ModeHybrid is passthrough plus search_tools (and read_result while
	// the response governor is on).
	ModeHybrid Mode = "hybrid"
)

// ParseMode parses a mode name (case-insensitive, surrounding spaces
// ignored).
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case ModeRouter, ModePassthrough, ModeHybrid:
		return m, nil
	default:
		return "", fmt.Errorf("invalid exposure mode %q (must be router, passthrough or hybrid)", s)
	}
}

// ListsUpstreamTools reports whether the mode lists the upstream tools
// themselves in tools/list (passthrough and hybrid).
func (m Mode) ListsUpstreamTools() bool { return m == ModePassthrough || m == ModeHybrid }

// Name-length bounds. Anthropic, OpenAI and most MCP clients cap a tool
// name at 64 characters of [a-zA-Z0-9_-].
const (
	DefaultMaxNameLength = 64
	// MinMaxNameLength leaves room for a few characters of the name
	// before the hash suffix.
	MinMaxNameLength = 24
)

// ClientRule maps clients, by clientInfo.name, to a mode.
type ClientRule struct {
	// Match is a glob (path.Match syntax: *, ?, [classes]) on the
	// clientInfo.name the client sends in initialize, compared
	// case-insensitively.
	Match string `yaml:"match"`
	// Mode is the mode of a matching client.
	Mode Mode `yaml:"mode"`
}

// BuiltinClients is the default client table: the clients known to handle
// a large tool list themselves (native tool search / deferred loading, or
// their own tool selection UI) get passthrough. The clientInfo.name values
// are the ones those clients send today:
//
//   - claude-code: Claude Code (MCP tool search, on by default);
//   - claude-ai: Claude Desktop and claude.ai connectors;
//   - cursor*: Cursor ("cursor-vscode"; dynamic context discovery);
//   - visual studio code*: VS Code / GitHub Copilot ("Visual Studio Code",
//     "Visual Studio Code - Insiders"; per-tool picker and virtual tools).
//
// Anything else gets exposure.mode (router unless configured).
var BuiltinClients = []ClientRule{
	{Match: "claude-code", Mode: ModePassthrough},
	{Match: "claude-ai", Mode: ModePassthrough},
	{Match: "cursor*", Mode: ModePassthrough},
	{Match: "visual studio code*", Mode: ModePassthrough},
}

// Config is the `exposure:` block. A nil *Config means the defaults:
// router for unknown clients, the built-in client table on, no always_load
// tool, 64-character names.
type Config struct {
	// Mode is the mode of the clients that no rule (clients, then the
	// built-in table) matches. Empty means router.
	Mode Mode `yaml:"mode,omitempty"`
	// BuiltinClients applies the BuiltinClients table after Clients.
	// Absent means true.
	BuiltinClients *bool `yaml:"builtin_clients,omitempty"`
	// Clients are evaluated top to bottom, before the built-in table; the
	// first match wins.
	Clients []ClientRule `yaml:"clients,omitempty"`
	// AlwaysLoad are "server.tool" globs (path.Match syntax) whose
	// passthrough entries carry _meta {"anthropic/alwaysLoad": true}:
	// Claude Code then loads them upfront instead of deferring them. The
	// same key sent by an upstream is dropped, so only this list decides.
	AlwaysLoad []string `yaml:"always_load,omitempty"`
	// MaxNameLength caps the namespaced tool names (default 64, at least
	// MinMaxNameLength). Lower it when the client adds its own prefix and
	// enforces 64 characters on the result.
	MaxNameLength int `yaml:"max_name_length,omitempty"`
}

// Validate checks the block. A nil receiver is valid (the defaults).
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if c.Mode != "" {
		if _, err := ParseMode(string(c.Mode)); err != nil {
			return fmt.Errorf("exposure.mode: %w", err)
		}
	}
	for i, r := range c.Clients {
		field := fmt.Sprintf("exposure.clients[%d]", i)
		if strings.TrimSpace(r.Match) == "" {
			return fmt.Errorf("%s: match is required", field)
		}
		if _, err := path.Match(strings.ToLower(r.Match), ""); err != nil {
			return fmt.Errorf("%s: invalid match glob %q: %w", field, r.Match, err)
		}
		if _, err := ParseMode(string(r.Mode)); err != nil {
			return fmt.Errorf("%s.mode: %w", field, err)
		}
	}
	for i, g := range c.AlwaysLoad {
		if strings.TrimSpace(g) == "" {
			return fmt.Errorf("exposure.always_load[%d]: empty glob", i)
		}
		if _, err := path.Match(g, ""); err != nil {
			return fmt.Errorf("exposure.always_load[%d]: invalid glob %q: %w", i, g, err)
		}
	}
	if c.MaxNameLength != 0 && (c.MaxNameLength < MinMaxNameLength || c.MaxNameLength > DefaultMaxNameLength) {
		return fmt.Errorf("exposure.max_name_length must be between %d and %d, got %d", MinMaxNameLength, DefaultMaxNameLength, c.MaxNameLength)
	}
	return nil
}

// Resolver decides the mode of each client. A nil *Resolver is valid: every
// client gets router, as before #322.
type Resolver struct {
	rules      []ClientRule
	fallback   Mode
	forced     Mode
	alwaysLoad []string
	maxLen     int
}

// NewResolver builds the resolver of cfg (nil: the defaults). forced, when
// not empty, is the mode of every client whatever the rules say (the
// `server run --exposure` flag).
func NewResolver(cfg *Config, forced Mode) (*Resolver, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	r := &Resolver{fallback: ModeRouter, maxLen: DefaultMaxNameLength}
	if forced != "" {
		m, err := ParseMode(string(forced))
		if err != nil {
			return nil, err
		}
		r.forced = m
	}
	builtin := true
	if cfg != nil {
		if cfg.Mode != "" {
			r.fallback, _ = ParseMode(string(cfg.Mode))
		}
		if cfg.BuiltinClients != nil {
			builtin = *cfg.BuiltinClients
		}
		for _, c := range cfg.Clients {
			m, _ := ParseMode(string(c.Mode))
			r.rules = append(r.rules, ClientRule{Match: strings.ToLower(c.Match), Mode: m})
		}
		r.alwaysLoad = append(r.alwaysLoad, cfg.AlwaysLoad...)
		if cfg.MaxNameLength != 0 {
			r.maxLen = cfg.MaxNameLength
		}
	}
	if builtin {
		r.rules = append(r.rules, BuiltinClients...)
	}
	return r, nil
}

// ModeFor returns the mode of a client by the clientInfo.name it sent in
// initialize ("" when it sent none), and what decided it (for the logs).
func (r *Resolver) ModeFor(clientName string) (Mode, string) {
	if r == nil {
		return ModeRouter, "default"
	}
	if r.forced != "" {
		return r.forced, "--exposure"
	}
	name := strings.ToLower(strings.TrimSpace(clientName))
	if name != "" {
		for _, rule := range r.rules {
			if ok, err := path.Match(rule.Match, name); err == nil && ok {
				return rule.Mode, fmt.Sprintf("client rule %q", rule.Match)
			}
		}
	}
	return r.fallback, "exposure.mode"
}

// MayListUpstreamTools reports whether any client can get passthrough or
// hybrid (so the handler must watch for changes of the exposed list).
func (r *Resolver) MayListUpstreamTools() bool {
	if r == nil {
		return false
	}
	if r.forced != "" {
		return r.forced.ListsUpstreamTools()
	}
	if r.fallback.ListsUpstreamTools() {
		return true
	}
	for _, rule := range r.rules {
		if rule.Mode.ListsUpstreamTools() {
			return true
		}
	}
	return false
}

// AlwaysLoad reports whether server.tool matches exposure.always_load.
func (r *Resolver) AlwaysLoad(server, tool string) bool {
	if r == nil || len(r.alwaysLoad) == 0 {
		return false
	}
	id := server + "." + tool
	for _, g := range r.alwaysLoad {
		if ok, err := path.Match(g, id); err == nil && ok {
			return true
		}
	}
	return false
}

// MaxNameLength is the cap of the namespaced names.
func (r *Resolver) MaxNameLength() int {
	if r == nil || r.maxLen == 0 {
		return DefaultMaxNameLength
	}
	return r.maxLen
}

// Separator joins the server and the tool in a namespaced name.
const Separator = "__"

// hashLen is the number of hex digits of the hash suffix (40 bits).
const hashLen = 10

// ToolName returns the namespaced name of an upstream tool:
// "<server>__<tool>" when that is at most maxLen characters of
// [a-zA-Z0-9_-] and neither part contains "__" (so the first "__" always
// splits it back); otherwise the name with every other character replaced
// by '_', cut to fit, and a deterministic suffix "_<10 hex digits of
// SHA-256(server NUL tool)>". A maxLen outside [MinMaxNameLength, 64] is
// taken as 64. The result always matches ^[a-zA-Z0-9_-]{1,maxLen}$.
func ToolName(server, tool string, maxLen int) string {
	if maxLen < MinMaxNameLength || maxLen > DefaultMaxNameLength {
		maxLen = DefaultMaxNameLength
	}
	plain := server + Separator + tool
	if server != "" && tool != "" && len(plain) <= maxLen && validChars(plain) &&
		!strings.Contains(server, Separator) && !strings.Contains(tool, Separator) {
		return plain
	}
	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	suffix := "_" + hex.EncodeToString(sum[:])[:hashLen]
	base := sanitize(plain)
	if room := maxLen - len(suffix); len(base) > room {
		base = base[:room]
	}
	return base + suffix
}

// Hashed reports whether name has the shape of a shortened name (the
// "_<10 hex digits>" suffix), i.e. whether resolving it needs the tool list.
func Hashed(name string) bool {
	if len(name) <= hashLen+1 || name[len(name)-hashLen-1] != '_' {
		return false
	}
	for _, c := range name[len(name)-hashLen:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// SplitPlain splits an unshortened namespaced name at its first "__".
// ok is false when name has no separator or an empty part.
func SplitPlain(name string) (server, tool string, ok bool) {
	server, tool, ok = strings.Cut(name, Separator)
	return server, tool, ok && server != "" && tool != ""
}

// ValidName reports whether name is a valid client-side tool name of at
// most maxLen characters (^[a-zA-Z0-9_-]{1,maxLen}$).
func ValidName(name string, maxLen int) bool {
	return name != "" && len(name) <= maxLen && validChars(name)
}

func validChars(s string) bool {
	for i := 0; i < len(s); i++ {
		if !nameChar(s[i]) {
			return false
		}
	}
	return true
}

func nameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// sanitize replaces every byte outside [a-zA-Z0-9_-] with '_' (a
// multi-byte character becomes several '_', which the hash disambiguates).
func sanitize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !nameChar(c) {
			b[i] = '_'
		}
	}
	return string(b)
}
