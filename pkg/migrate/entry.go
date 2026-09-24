package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// mcpServerEntry is one server entry in the "mcpServers"-style maps shared by
// Claude Code, Claude Desktop, Cursor, VS Code ("servers") and the generic
// ~/.config/mcp.json. A local entry has a command; a remote one has a url and
// an optional type ("http", "sse", ...).
type mcpServerEntry struct {
	Type     string            `json:"type"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      envList           `json:"env"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Disabled bool              `json:"disabled"`
}

// envList is an "env" field that clients write either as an object
// ({"KEY": "value"}, the common form) or as an array of "KEY=value"
// strings. Both decode to sorted "KEY=value" entries.
type envList []string

func (e *envList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*e = nil
		return nil
	}
	switch data[0] {
	case '{':
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		out := make([]string, 0, len(m))
		for k, v := range m {
			var s string
			switch tv := v.(type) {
			case string:
				s = tv
			case nil:
				s = ""
			default:
				s = fmt.Sprint(tv)
			}
			out = append(out, k+"="+s)
		}
		sort.Strings(out)
		*e = out
		return nil
	case '[':
		var list []string
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*e = list
		return nil
	default:
		return fmt.Errorf("env must be an object or an array of KEY=value strings")
	}
}

// clientEnvVarRef matches the "${env:NAME}" syntax of Cursor and VS Code.
// LeanProxy expands "${NAME}" from its own environment instead.
var clientEnvVarRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

func convertEnv(env []string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, len(env))
	for i, kv := range env {
		out[i] = clientEnvVarRef.ReplaceAllString(kv, "$${$1}")
	}
	return out
}

// toDiscovered converts a client entry to a DiscoveredServer. Local entries
// become stdio servers; remote ones become http or sse servers.
func (e mcpServerEntry) toDiscovered(name, source string) (DiscoveredServer, error) {
	srv := DiscoveredServer{Name: name, Source: source}
	if e.Disabled {
		enabled := false
		srv.Enabled = &enabled
	}

	transport, err := entryTransport(e.Type, e.Command, e.URL)
	if err != nil {
		return srv, fmt.Errorf("server %q: %w", name, err)
	}
	srv.Transport = transport

	if transport == TransportStdio {
		srv.Stdio = &StdioConfig{
			Command: e.Command,
			Args:    e.Args,
			Env:     convertEnv(e.Env),
			CWD:     filepath.Dir(e.Command),
		}
		return srv, nil
	}

	var headers map[string]string
	if len(e.Headers) > 0 {
		headers = make(map[string]string, len(e.Headers))
		for k, v := range e.Headers {
			headers[k] = v
		}
	}
	srv.HTTP = &HTTPConfig{URL: e.URL, Headers: headers}
	return srv, nil
}

// entryTransport maps a client's "type" (plus whether the entry has a command
// or a url) to a LeanProxy transport.
func entryTransport(typ, command, rawURL string) (TransportType, error) {
	switch strings.ToLower(typ) {
	case "stdio", "local":
		if command == "" {
			return "", fmt.Errorf("stdio entry has no command")
		}
		return TransportStdio, nil
	case "http", "streamable-http", "streamablehttp", "remote":
		if rawURL == "" {
			return "", fmt.Errorf("%s entry has no url", typ)
		}
		return TransportHTTP, nil
	case "sse":
		if rawURL == "" {
			return "", fmt.Errorf("sse entry has no url")
		}
		return TransportSSE, nil
	case "":
		switch {
		case command != "":
			return TransportStdio, nil
		case rawURL != "":
			return remoteTransportFromURL(rawURL), nil
		default:
			return "", fmt.Errorf("entry has neither a command nor a url")
		}
	default:
		return "", fmt.Errorf("unsupported type %q", typ)
	}
}

// remoteTransportFromURL guesses the transport of a url entry that has no
// type: an endpoint path ending in /sse is the legacy SSE transport,
// anything else Streamable HTTP.
func remoteTransportFromURL(rawURL string) TransportType {
	path := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		path = u.Path
	}
	if strings.HasSuffix(strings.TrimSuffix(path, "/"), "/sse") {
		return TransportSSE
	}
	return TransportHTTP
}

// readConfigFile reads a client config file. A missing file is not an error:
// it returns found=false. Comments and trailing commas (JSONC, as written by
// VS Code) are removed so the result can be passed to encoding/json.
func readConfigFile(path string) (data []byte, found bool, err error) {
	data, err = os.ReadFile(path) // #nosec G304 -- reading from known client config paths
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return stripJSONC(data), true, nil
}

// scanServerMapFile reads path and converts the server maps found under each
// of the top-level keys (e.g. "mcpServers"). Errors name the file.
func scanServerMapFile(path, source string, keys ...string) ([]DiscoveredServer, []error) {
	doc, found, err := readConfigDoc(path)
	if err != nil || !found {
		return nil, errSlice(err)
	}
	var servers []DiscoveredServer
	var errs []error
	for _, key := range keys {
		found, keyErrs := convertServerMap(doc[key], source, path, key)
		servers = append(servers, found...)
		errs = append(errs, keyErrs...)
	}
	return servers, errs
}

// readConfigDoc reads a client config file as a JSON object.
func readConfigDoc(path string) (map[string]json.RawMessage, bool, error) {
	data, found, err := readConfigFile(path)
	if err != nil || !found {
		if err != nil {
			err = fmt.Errorf("%s: %w", path, err)
		}
		return nil, false, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return doc, true, nil
}

// convertServerMap decodes one server map (absent: nothing) and converts its
// entries, sorted by name. An entry that cannot be decoded or converted is
// reported as an error and left out; the others are still returned.
func convertServerMap(raw json.RawMessage, source, path, key string) ([]DiscoveredServer, []error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, []error{fmt.Errorf("%s: %s: %w", path, key, err)}
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	var servers []DiscoveredServer
	var errs []error
	for _, name := range names {
		var entry mcpServerEntry
		if err := json.Unmarshal(entries[name], &entry); err != nil {
			errs = append(errs, fmt.Errorf("%s: server %q: %w", path, name, err))
			continue
		}
		srv, err := entry.toDiscovered(name, source)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		servers = append(servers, srv)
	}
	return servers, errs
}

func errSlice(err error) []error {
	if err == nil {
		return nil
	}
	return []error{err}
}

// stripJSONC removes // and /* */ comments and trailing commas outside of
// strings. Plain JSON is returned unchanged.
func stripJSONC(data []byte) []byte {
	return stripTrailingCommas(stripComments(data))
}

// scanJSON calls visit for each byte outside of JSON strings; bytes inside
// strings are copied as-is. visit returns the index of the last byte it
// consumed.
func scanJSON(data []byte, visit func(out []byte, i int) ([]byte, int)) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		out, i = visit(out, i)
	}
	return out
}

func stripComments(data []byte) []byte {
	return scanJSON(data, func(out []byte, i int) ([]byte, int) {
		switch {
		case data[i] == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
			return out, i
		case data[i] == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			return out, i + 1
		default:
			return append(out, data[i]), i
		}
	})
}

func stripTrailingCommas(data []byte) []byte {
	return scanJSON(data, func(out []byte, i int) ([]byte, int) {
		if data[i] == ',' {
			j := i + 1
			for j < len(data) && isJSONSpace(data[j]) {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				return out, i
			}
		}
		return append(out, data[i]), i
	})
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// IsLeanProxyServer reports whether a discovered entry runs LeanProxy itself
// (e.g. the entry a client uses to start `leanproxy-mcp server run --stdio`,
// or the HTTP gateway entry). Importing it would make the proxy start itself
// as an upstream.
func IsLeanProxyServer(srv DiscoveredServer) bool {
	switch strings.ToLower(srv.Name) {
	case "leanproxy", "leanproxy-mcp":
		return true
	}
	if srv.Stdio == nil {
		return false
	}
	if isLeanProxyExecutable(srv.Stdio.Command) {
		return true
	}
	// A launcher (npx leanproxy-mcp, go run .../leanproxy-mcp@latest,
	// env leanproxy-mcp) names the program in its first positional
	// argument. Later arguments are the program's own (e.g. a directory
	// that happens to be called leanproxy-mcp) and are not checked.
	if !launcherCommands[commandBase(srv.Stdio.Command)] {
		return false
	}
	for _, arg := range srv.Stdio.Args {
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") || launcherSubcommands[arg] {
			continue
		}
		return isLeanProxyExecutable(arg)
	}
	return false
}

var launcherCommands = map[string]bool{
	"npx": true, "bunx": true, "pnpx": true, "pnpm": true, "yarn": true,
	"uvx": true, "env": true, "go": true,
}

var launcherSubcommands = map[string]bool{
	"run": true, "exec": true, "dlx": true, "x": true,
}

func commandBase(s string) string {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(s, `\`, "/")))
	return strings.TrimSuffix(base, ".exe")
}

func isLeanProxyExecutable(s string) bool {
	base := commandBase(s)
	if at := strings.Index(base, "@"); at > 0 {
		base = base[:at] // npx leanproxy-mcp@latest, go run .../leanproxy-mcp@v1
	}
	return base == "leanproxy-mcp" || base == "leanproxy"
}
