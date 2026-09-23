// Package harness is the end-to-end benchmark and conformance harness for
// leanproxy-mcp (issue #301). The measurements live in the test files behind
// the `harness` build tag and run with `make harness`; they build the real
// binary, drive `server run --stdio` over pipes against the catalog mock in
// ./catalogmcp, and write bench-results/harness.md.
//
// This file (no build tag) only holds what the harness test and the catalog
// mock share: the embedded 118-tool catalog and the fake credentials.
package harness

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// catalogJSON is a realistic multi-server catalog (GitHub, Jira/Confluence,
// Slack, Garmin, Postgres; 118 tools), ported from
// docs/audit/experiments/catalog.py.
//
//go:embed testdata/catalog.json
var catalogJSON []byte

// Tool is one MCP tool definition as a server returns it in tools/list.
// InputSchema is kept raw so the mock serves it byte-for-byte.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Server is one catalog server and its tools, in catalog order.
type Server struct {
	Name  string `json:"name"`
	Tools []Tool `json:"tools"`
}

// Catalog is the whole embedded catalog.
type Catalog struct {
	Servers []Server `json:"servers"`
}

// LoadCatalog decodes the embedded catalog.
func LoadCatalog() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		return nil, fmt.Errorf("decode embedded catalog: %w", err)
	}
	return &c, nil
}

// Server returns the named server, or nil.
func (c *Catalog) Server(name string) *Server {
	for i := range c.Servers {
		if c.Servers[i].Name == name {
			return &c.Servers[i]
		}
	}
	return nil
}

// ToolCount is the number of tools across every server.
func (c *Catalog) ToolCount() int {
	n := 0
	for _, s := range c.Servers {
		n += len(s.Tools)
	}
	return n
}

// FakeSecrets returns credential-shaped strings that the built-in bouncer
// patterns must redact. They are assembled at runtime so no secret-looking
// literal is committed (GitHub push protection rejects literal keys).
func FakeSecrets() []string {
	return []string{
		"AKIA" + "IOSFODNN7EXAMPLE",
		"ghp_" + strings.Repeat("a1B2", 9),
		"sk_live_" + strings.Repeat("x9Y8", 6),
	}
}

// CountFakeSecrets reports how many of FakeSecrets occur in s.
func CountFakeSecrets(s string) int {
	n := 0
	for _, secret := range FakeSecrets() {
		if strings.Contains(s, secret) {
			n++
		}
	}
	return n
}
