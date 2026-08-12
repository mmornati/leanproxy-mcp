// Package live_snapshot provides a tool that queries configured MCP servers
// to capture a ground-truth snapshot of their tool counts and total schema
// payload size. The snapshot is consumed by:
//
//   - tests/bench/token_economy_bench_test.go for the README's "schema tax"
//     comparisons (native `tools/list` vs LeanProxy router)
//   - go generate directives that re-render the README mermaid diagram and
//     docs/index.md tables so the public numbers never drift from reality.
//
// Re-run with: `go run ./tests/bench/live_snapshot -config fixtures/live-snapshot.yaml`
// The output is written to fixtures/live-snapshot.json.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshot is the committed artifact; README and docs/index.md are
// (re)generated from this struct.
type Snapshot struct {
	QueriedAt   time.Time    `json:"queried_at"`
	Source      string       `json:"source"`
	Servers     []ServerSpec `json:"servers"`
	Totals      Totals       `json:"totals"`
	EstimatorRT EstimatorRT  `json:"estimator"`
}

type ServerSpec struct {
	Name        string `json:"name"`
	Transport   string `json:"transport"`
	ToolCount   int    `json:"tool_count"`
	SchemaBytes int    `json:"schema_bytes"`
	Reachable   bool   `json:"reachable"`
	Error       string `json:"error,omitempty"`
}

type Totals struct {
	Servers     int `json:"servers"`
	Tools       int `json:"tools"`
	SchemaBytes int `json:"schema_bytes"`
}

type EstimatorRT struct {
	CharsPerToken float64 `json:"chars_per_token"`
	ReadmeTokens  int     `json:"router_tokens"` // tokens for the router list_tools JSON
}

type serverConfig struct {
	Name      string   `yaml:"name"`
	Transport string   `yaml:"transport"`
	Command   string   `yaml:"command,omitempty"`
	Args      []string `yaml:"args,omitempty"`
	URL       string   `yaml:"url,omitempty"`
	Env       []string `yaml:"env,omitempty"`
}

// mcToolsList mirrors the subset of the JSON-RPC response we care about.
// We intentionally do not import the project types here — this tool is
// meant to be runnable standalone on a fresh checkout.
type mcToolsList struct {
	Result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description,omitempty"`
			InputSchema json.RawMessage `json:"inputSchema,omitempty"`
		} `json:"tools"`
	} `json:"result"`
}

func main() {
	var (
		configPath  = flag.String("config", "tests/bench/fixtures/live-snapshot.yaml", "path to MCP server config YAML")
		outPath     = flag.String("out", "tests/bench/fixtures/live-snapshot.json", "path to write snapshot JSON")
		routerPath  = flag.String("router", "tests/bench/fixtures/router-tools.json", "path to a pre-computed LeanProxy router tools/list response (since the router itself is not exposed at runtime)")
		quiet       = flag.Bool("quiet", false, "suppress progress output")
		timeoutSecs = flag.Int("timeout", 15, "per-server connect timeout in seconds")
	)
	flag.Parse()

	servers, err := loadConfig(*configPath)
	if err != nil {
		fatal("load config %s: %v", *configPath, err)
	}

	snapshot := Snapshot{
		QueriedAt: time.Now().UTC(),
		Source:    *configPath,
	}

	for _, s := range servers {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "→ querying %s (%s)...\n", s.Name, s.Transport)
		}
		spec := ServerSpec{Name: s.Name, Transport: s.Transport}
		resp, err := queryToolsList(s, *timeoutSecs)
		if err != nil {
			spec.Reachable = false
			spec.Error = err.Error()
		} else {
			spec.Reachable = true
			spec.ToolCount = len(resp.Result.Tools)
			for _, t := range resp.Result.Tools {
				b, _ := json.Marshal(t)
				spec.SchemaBytes += len(b)
			}
		}
		snapshot.Servers = append(snapshot.Servers, spec)
	}

	for _, s := range snapshot.Servers {
		if !s.Reachable {
			continue
		}
		snapshot.Totals.Servers++
		snapshot.Totals.Tools += s.ToolCount
		snapshot.Totals.SchemaBytes += s.SchemaBytes
	}
	sort.Slice(snapshot.Servers, func(i, j int) bool {
		return snapshot.Servers[i].Name < snapshot.Servers[j].Name
	})

	// The router tools/list response is computed in `pkg/gateway/tools.go`
	// (3 tools: list_servers, invoke_tool, list_tools). We snapshot it here
	// so the README can render the router payload token count without
	// importing the proxy package.
	if *routerPath != "" {
		if routerBytes, err := os.ReadFile(*routerPath); err == nil {
			snapshot.EstimatorRT.ReadmeTokens = estimateTokens(string(routerBytes))
		}
	}
	snapshot.EstimatorRT.CharsPerToken = 4.0

	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fatal("mkdir: %v", err)
	}
	out, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		fatal("marshal: %v", err)
	}
	if err := os.WriteFile(*outPath, append(out, '\n'), 0o644); err != nil {
		fatal("write: %v", err)
	}

	if !*quiet {
		fmt.Fprintf(os.Stderr, "✓ snapshot written to %s (%d servers, %d tools, %d bytes)\n",
			*outPath, snapshot.Totals.Servers, snapshot.Totals.Tools, snapshot.Totals.SchemaBytes)
	}
}

func loadConfig(path string) ([]serverConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Tiny inline YAML-ish parser. We only need a flat list of servers
	// with the fields we care about, so we lean on the common YAML
	// "servers:" key followed by `- name: ...` blocks. We avoid pulling
	// in gopkg.in/yaml.v3 as a dependency for a single CLI tool.
	lines := strings.Split(string(raw), "\n")
	var servers []serverConfig
	var cur *serverConfig
	inServers := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "servers:") {
			inServers = true
			continue
		}
		if !inServers {
			continue
		}
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "- ") {
			if cur != nil {
				servers = append(servers, *cur)
			}
			cur = &serverConfig{}
			rest := strings.TrimPrefix(trim, "- ")
			kv := strings.SplitN(rest, ":", 2)
			if len(kv) == 2 {
				cur.Name = strings.TrimSpace(kv[1])
			}
			continue
		}
		if cur == nil {
			continue
		}
		kv := strings.SplitN(trim, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"'`)
		switch key {
		case "name":
			cur.Name = val
		case "transport":
			cur.Transport = val
		case "command":
			cur.Command = val
		case "url":
			cur.URL = val
		}
	}
	if cur != nil {
		servers = append(servers, *cur)
	}
	// Re-derive transports from defaults if missing.
	for i := range servers {
		if servers[i].Transport == "" {
			if servers[i].Command != "" {
				servers[i].Transport = "stdio"
			} else if servers[i].URL != "" {
				servers[i].Transport = "http"
			}
		}
	}
	return servers, nil
}

// queryToolsList spawns the MCP server and exchanges initialize + tools/list.
// It returns the raw JSON-RPC response.
func queryToolsList(s serverConfig, timeoutSecs int) (*mcToolsList, error) {
	if s.Transport != "stdio" {
		return nil, fmt.Errorf("transport %q not supported by this snapshot tool yet (stdio only)", s.Transport)
	}
	if s.Command == "" {
		return nil, fmt.Errorf("empty command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = append(os.Environ(), s.Env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// initialize
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"leanproxy-snapshot","version":"0.0.1"}}}`
	if _, err := io.WriteString(stdin, initReq+"\n"); err != nil {
		return nil, fmt.Errorf("write init: %w", err)
	}
	// Read init response (and any `notifications/initialized` line).
	reader := bufio.NewReader(stdout)
	for {
		line, err := readLine(reader)
		if err != nil {
			return nil, fmt.Errorf("read init: %w", err)
		}
		if strings.Contains(line, `"result"`) {
			break
		}
	}

	// initialized notification
	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		return nil, fmt.Errorf("write initialized: %w", err)
	}

	// tools/list
	toolsReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	if _, err := io.WriteString(stdin, toolsReq+"\n"); err != nil {
		return nil, fmt.Errorf("write tools/list: %w", err)
	}
	for {
		line, err := readLine(reader)
		if err != nil {
			return nil, fmt.Errorf("read tools/list: %w", err)
		}
		if !strings.Contains(line, `"id":2`) {
			continue
		}
		var resp mcToolsList
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			return nil, fmt.Errorf("unmarshal tools/list: %w", err)
		}
		return &resp, nil
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// estimateTokens uses the same 1 token ≈ 4 chars heuristic as the runtime
// Estimator. Keeping it inline avoids a circular import (the snapshot tool
// is a standalone main package).
func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "live_snapshot: "+format+"\n", args...)
	os.Exit(1)
}
