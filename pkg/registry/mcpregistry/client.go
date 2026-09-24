// Package mcpregistry is a client for the official MCP Registry API
// (https://registry.modelcontextprotocol.io/, API version v0), used as the
// default marketplace source (issue #313). It maps the registry's
// server.json schema (packages[] and remotes[]) into a small, LeanProxy-only
// Server model that the rest of pkg/registry converts into installable
// entries.
//
// Every request has a timeout and a bounded response size; nothing here
// shells out or executes anything, it only fetches and parses JSON.
package mcpregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the official MCP Registry API root. The maintainer
// confirmed this domain (modelcontextprotocol.io) is the project's, unlike
// the previous default (registry.mcp.io), which LeanProxy does not own.
const DefaultBaseURL = "https://registry.modelcontextprotocol.io"

// APIVersion is the registry API version path segment this client speaks.
const APIVersion = "v0"

const (
	// defaultTimeout bounds one HTTP round trip.
	defaultTimeout = 15 * time.Second
	// maxResponseBytes bounds how much of a response body is read, so a
	// malicious or misbehaving registry cannot exhaust memory.
	maxResponseBytes = 10 * 1024 * 1024
	// defaultPageLimit is the page size requested when listing/searching.
	defaultPageLimit = 100
	// maxPages bounds how many pages SearchServers will walk, so a
	// registry that never stops paginating cannot hang the caller.
	maxPages = 50
)

// Client talks to one MCP Registry instance.
type Client struct {
	baseURL string
	client  *http.Client
}

// New returns a Client for the official MCP Registry. Use WithBaseURL to
// point it at a different instance (a sub-registry, or a test server).
func New() *Client {
	return &Client{
		baseURL: DefaultBaseURL,
		client:  &http.Client{Timeout: defaultTimeout},
	}
}

// WithBaseURL overrides the registry root (e.g. a sub-registry, or a test
// httptest.Server URL). Trailing slashes are trimmed.
func (c *Client) WithBaseURL(base string) *Client {
	c.baseURL = strings.TrimRight(base, "/")
	return c
}

// WithHTTPClient overrides the HTTP client (e.g. to set a shorter timeout
// or inject a transport in tests).
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	c.client = hc
	return c
}

// Meta is the registry-managed `_meta` block of a server record. Only the
// official sub-object LeanProxy relies on for trust signals is modeled;
// unknown keys are ignored.
type Meta struct {
	Official *OfficialMeta `json:"io.modelcontextprotocol.registry/official,omitempty"`
}

// OfficialMeta is the well-known official-registry metadata sub-object.
type OfficialMeta struct {
	IsLatest bool `json:"isLatest,omitempty"`
	// IsVerified reports whether the registry verified the publisher owns
	// the server's namespace (DNS or GitHub verification). Used as a trust
	// signal — never trusted at face value for anything else.
	IsVerified  bool   `json:"isVerified,omitempty"`
	Status      string `json:"status,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

// EnvVar describes one environment variable a package needs.
type EnvVar struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsRequired  bool   `json:"isRequired,omitempty"`
	IsSecret    bool   `json:"isSecret,omitempty"`
}

// Package is one installable package of a server (packages[]).
type Package struct {
	// RegistryType is the package registry: "npm", "pypi", "oci", ...
	RegistryType string `json:"registryType"`
	// Identifier is the package name or image reference, without a
	// version suffix.
	Identifier string `json:"identifier"`
	// Version is the exact version this package entry publishes. The
	// registry always pins one version per package record.
	Version              string   `json:"version"`
	RuntimeHint          string   `json:"runtimeHint,omitempty"`
	RuntimeArguments     []string `json:"runtimeArguments,omitempty"`
	PackageArguments     []string `json:"packageArguments,omitempty"`
	EnvironmentVariables []EnvVar `json:"environmentVariables,omitempty"`
}

// Remote is a remote (non-stdio) transport endpoint (remotes[]).
type Remote struct {
	TransportType string `json:"transportType"` // "streamable-http" | "sse"
	URL           string `json:"url"`
}

// Repository describes the server's source repository, when published.
type Repository struct {
	URL    string `json:"url,omitempty"`
	Source string `json:"source,omitempty"`
}

// Server is one server.json record as returned by the registry.
type Server struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Version     string     `json:"version,omitempty"`
	Repository  Repository `json:"repository,omitempty"`
	License     string     `json:"license,omitempty"`
	Packages    []Package  `json:"packages,omitempty"`
	Remotes     []Remote   `json:"remotes,omitempty"`
	Meta        Meta       `json:"_meta,omitempty"`
}

// serverListResponse is the GET /v0/servers response envelope.
type serverListResponse struct {
	Servers  []Server `json:"servers"`
	Metadata struct {
		NextCursor string `json:"next_cursor,omitempty"`
		Count      int    `json:"count,omitempty"`
	} `json:"metadata"`
}

// ServerPage is one page of a listing.
type ServerPage struct {
	Servers    []Server
	NextCursor string
}

// ListServers fetches one page of the server list, starting at cursor (""
// for the first page).
func (c *Client) ListServers(ctx context.Context, cursor string, limit int) (*ServerPage, error) {
	if limit <= 0 {
		limit = defaultPageLimit
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var resp serverListResponse
	if err := c.get(ctx, "/servers", q, &resp); err != nil {
		return nil, err
	}
	return &ServerPage{Servers: resp.Servers, NextCursor: resp.Metadata.NextCursor}, nil
}

// SearchServers lists servers across pages (bounded by maxPages) and
// returns those whose name or description contains query
// (case-insensitive). It does client-side filtering rather than relying on
// a registry-side `search` query parameter, since the official API does not
// guarantee one; that keeps this client correct against any v0-compliant
// registry.
func (c *Client) SearchServers(ctx context.Context, query string, limit int) ([]Server, error) {
	if limit <= 0 {
		limit = defaultPageLimit
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var matches []Server
	cursor := ""
	for page := 0; page < maxPages; page++ {
		sp, err := c.ListServers(ctx, cursor, defaultPageLimit)
		if err != nil {
			return nil, err
		}
		for _, s := range sp.Servers {
			if q == "" || strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.Description), q) {
				matches = append(matches, s)
				if len(matches) >= limit {
					return matches, nil
				}
			}
		}
		if sp.NextCursor == "" {
			break
		}
		cursor = sp.NextCursor
	}
	return matches, nil
}

// ListAll walks every page (bounded by maxPages) and returns every server.
// Used by the marketplace sync to build the local cache.
func (c *Client) ListAll(ctx context.Context) ([]Server, error) {
	var all []Server
	cursor := ""
	for page := 0; page < maxPages; page++ {
		sp, err := c.ListServers(ctx, cursor, defaultPageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, sp.Servers...)
		if sp.NextCursor == "" {
			break
		}
		cursor = sp.NextCursor
	}
	return all, nil
}

// GetServer fetches one server by name. When version is empty, the
// registry's latest version is returned.
func (c *Client) GetServer(ctx context.Context, name, version string) (*Server, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("mcpregistry: server name is required")
	}
	q := url.Values{}
	if version != "" {
		q.Set("version", version)
	}
	var s Server
	path := "/servers/" + url.PathEscape(name)
	if err := c.get(ctx, path, q, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// get performs a GET against c.baseURL+"/v0"+path, decoding a JSON response
// into out. The response body is capped at maxResponseBytes.
func (c *Client) get(ctx context.Context, path string, q url.Values, out interface{}) error {
	u := c.baseURL + "/" + APIVersion + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("mcpregistry: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcpregistry: request %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mcpregistry: %s returned HTTP %d: %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	limited := io.LimitReader(resp.Body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("mcpregistry: read response from %s: %w", u, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("mcpregistry: response from %s exceeds %d bytes", u, maxResponseBytes)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("mcpregistry: parse response from %s: %w", u, err)
	}
	return nil
}
