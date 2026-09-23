// Package toolpin pins upstream tool definitions and detects tool
// poisoning and rug pulls (issue #310, OWASP MCP03).
//
// Every tool an upstream server lists is hashed (see Hash for exactly what
// is hashed) and compared with the hash recorded in the pin file
// (~/.config/leanproxy/pins.json, mode 0600, written atomically). A server
// seen for the first time is trusted on first use: its tools are pinned and
// approved, except tools whose metadata the scanner flags with a
// high-severity finding. From then on a tool that is added or whose
// definition changes stays pending until `leanproxy-mcp tools pins
// approve` accepts it; the policy mode decides what a pending tool means:
//
//   - off:   nothing is pinned or checked;
//   - warn:  (default) events are logged and reported by `doctor security`,
//     and list_tools / search_tools carry a one-line warning;
//   - block: pending tools are hidden from discovery and calls to them are
//     refused until approved.
//
// The scanner (Scanner) looks for hidden instructions, sensitive paths,
// invisible or bidi unicode, external URLs, base64 blobs and overlong
// descriptions in every new or changed tool, reusing the prompt-injection
// classifier's pattern engine (pkg/bouncer/injection) with its own pattern
// set plus the injection guard's default set.
package toolpin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mode is the tool pinning policy (security.tool_pinning.mode).
type Mode string

const (
	// ModeOff disables pinning and drift detection.
	ModeOff Mode = "off"
	// ModeWarn logs drift and adds a warning to discovery output (default).
	ModeWarn Mode = "warn"
	// ModeBlock hides new or changed tools and refuses calls to them until
	// they are approved.
	ModeBlock Mode = "block"
)

// DefaultMaxDescriptionChars is the description length (in characters)
// above which the scanner reports a long-description finding.
const DefaultMaxDescriptionChars = 2000

// PinsFileEnv overrides the pin file location (tests, sandboxes).
const PinsFileEnv = "LEANPROXY_PINS_FILE"

// Config is the `security.tool_pinning:` block. A nil *Config means the
// defaults: mode warn, the default pin file, no extra allowed domains.
type Config struct {
	// Mode is off, warn (default) or block.
	Mode Mode `yaml:"mode,omitempty"`
	// Path overrides the pin file (default ~/.config/leanproxy/pins.json;
	// the LEANPROXY_PINS_FILE environment variable wins over both).
	Path string `yaml:"path,omitempty"`
	// AllowedDomains are URL hosts the scanner does not report in tool
	// descriptions (a host matches itself and its subdomains). The host of
	// an HTTP/SSE server's own URL is always allowed for that server.
	AllowedDomains []string `yaml:"allowed_domains,omitempty"`
	// MaxDescriptionChars is the description length above which a
	// long-description finding is reported (default 2000).
	MaxDescriptionChars int `yaml:"max_description_chars,omitempty"`
}

// EffectiveMode returns the configured mode, or ModeWarn when unset.
func (c *Config) EffectiveMode() Mode {
	if c == nil || c.Mode == "" {
		return ModeWarn
	}
	return Mode(strings.ToLower(string(c.Mode)))
}

// EffectiveMaxDescriptionChars returns max_description_chars or the
// default.
func (c *Config) EffectiveMaxDescriptionChars() int {
	if c == nil || c.MaxDescriptionChars <= 0 {
		return DefaultMaxDescriptionChars
	}
	return c.MaxDescriptionChars
}

// Validate rejects an unknown mode and a negative length. A nil receiver is
// valid.
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	switch c.EffectiveMode() {
	case ModeOff, ModeWarn, ModeBlock:
	default:
		return fmt.Errorf("security.tool_pinning.mode: %q is not one of off, warn, block", c.Mode)
	}
	if c.MaxDescriptionChars < 0 {
		return fmt.Errorf("security.tool_pinning.max_description_chars must be >= 0, got %d", c.MaxDescriptionChars)
	}
	for _, d := range c.AllowedDomains {
		if strings.TrimSpace(d) == "" || strings.ContainsAny(d, "/: ") {
			return fmt.Errorf("security.tool_pinning.allowed_domains: %q is not a host name", d)
		}
	}
	return nil
}

// ResolvePath returns the pin file path: LEANPROXY_PINS_FILE when set,
// then the configured path, then DefaultPath.
func (c *Config) ResolvePath() (string, error) {
	if p := os.Getenv(PinsFileEnv); p != "" {
		return filepath.Clean(p), nil
	}
	if c != nil && c.Path != "" {
		return expandHome(c.Path)
	}
	return DefaultPath()
}

// DefaultPath is $HOME/.config/leanproxy/pins.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("toolpin: cannot determine the home directory: %w", err)
	}
	return filepath.Join(home, ".config", "leanproxy", "pins.json"), nil
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("toolpin: expand %q: %w", p, err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Clean(p), nil
}
