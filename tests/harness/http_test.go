//go:build harness

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// Streamable HTTP front end (`server run --http`, issue #309): conformance
// with the mcp-go Streamable HTTP client and latency through the real
// binary.

// httpToken is the bearer token of every harness HTTP front end (built at
// runtime: never a literal credential in the tree).
var httpToken = "harness-" + strings.Repeat("http", 6)

const (
	httpBurstCalls   = 500
	httpBurstWorkers = 32
)

// startHTTPFrontend starts `server run --http` in e on a free loopback
// port and returns its endpoint URL.
func startHTTPFrontend(t testing.TB, e *env, bins binaries, logName string) (string, *exec.Cmd) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cmd := exec.Command(bins.proxy, "server", "run", "--http", addr, "--http-token", httpToken,
		"--config", e.cfg, "--log-file", filepath.Join(e.dir, logName))
	cmd.Env = e.vars
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTP front end %s never came up", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "http://" + addr + "/mcp", cmd
}

// httpSession is a minimal Streamable HTTP client for measurements: one
// keep-alive connection pool, JSON answers.
type httpSession struct {
	url    string
	sid    string
	client *http.Client
	nextID atomic.Int64
}

func newHTTPSession(t testing.TB, url string) *httpSession {
	t.Helper()
	s := &httpSession{url: url, client: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: httpBurstWorkers}}}
	resp, body, err := s.post(map[string]interface{}{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]interface{}{
		"protocolVersion": "2025-11-25", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "leanproxy-harness-http", "version": "1"},
	}})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize over HTTP: %v %s", err, body)
	}
	s.sid = resp.Header.Get("Mcp-Session-Id")
	if _, _, err := s.post(map[string]string{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *httpSession) post(msg interface{}) (*http.Response, []byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+httpToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.sid != "" {
		req.Header.Set("Mcp-Session-Id", s.sid)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp, body, err
}

// call sends one request and returns its answer's line and round trip.
func (s *httpSession) call(method string, params interface{}) (rpcMsg, []byte, time.Duration, error) {
	id := s.nextID.Add(1)
	start := time.Now()
	resp, body, err := s.post(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	d := time.Since(start)
	if err != nil {
		return rpcMsg{}, nil, d, err
	}
	if resp.StatusCode != http.StatusOK {
		return rpcMsg{}, body, d, fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, body)
	}
	var m rpcMsg
	if err := json.Unmarshal(body, &m); err != nil {
		return rpcMsg{}, body, d, err
	}
	return m, body, d, nil
}

// httpLatency is the HTTP front end's share of latencyResults.
type httpLatency struct {
	ms          []float64
	burstWall   time.Duration
	burstErrors int
}

// measureHTTPLatency runs the paced sequential calls of measureLatency and
// a concurrent burst through the HTTP front end.
func measureHTTPLatency(t *testing.T, bins binaries, cat *Catalog) httpLatency {
	e := newEnv(t, bins, catalogServers(cat), "")
	url, _ := startHTTPFrontend(t, e, bins, "leanproxy-http.log")
	s := newHTTPSession(t, url)

	paced := func(n, offset int) []float64 {
		out := make([]float64, 0, n)
		for k := 0; k < n; k++ {
			time.Sleep(sequentialPace)
			text := fmt.Sprintf("msg %d", offset+k)
			m, body, d, err := s.call("tools/call", invokeParams("slack", "slack_post_message", map[string]interface{}{"channel_id": "c", "text": text}))
			if err != nil || m.Error != nil || !strings.Contains(toolText(reply{msg: m}), text) {
				t.Fatalf("HTTP paced call %d: %v %.300s", k, err, body)
			}
			out = append(out, float64(d.Microseconds())/1000)
		}
		return out
	}
	paced(sequentialWarmup, 0)
	var res httpLatency
	res.ms = paced(sequentialCalls, sequentialWarmup)

	targets := []prompt{{"github", "create_issue", ""}, {"jira", "jira_search", ""}, {"slack", "slack_post_message", ""}, {"garmin", "get_activity", ""}, {"postgres", "pg_query", ""}}
	for _, tg := range targets {
		if _, _, _, err := s.call("tools/call", invokeParams(tg.server, tg.tool, map[string]interface{}{})); err != nil {
			t.Fatalf("warm %s: %v", tg.server, err)
		}
	}
	var next atomic.Int64
	var errs atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < httpBurstWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				k := int(next.Add(1)) - 1
				if k >= httpBurstCalls {
					return
				}
				tg := targets[k%len(targets)]
				want := fmt.Sprintf("burst %d\"", k)
				m, body, _, err := s.call("tools/call", invokeParams(tg.server, tg.tool, map[string]interface{}{"q": fmt.Sprintf("burst %d", k)}))
				if err != nil || m.Error != nil || !strings.Contains(toolText(reply{msg: m}), want) {
					if errs.Add(1) <= 3 {
						t.Logf("HTTP burst call %d: %v %.200s", k, err, body)
					}
				}
			}
		}()
	}
	wg.Wait()
	res.burstWall = time.Since(start)
	res.burstErrors = int(errs.Load())
	return res
}

// TestHarness_StreamableHTTPConformance is the #309 conformance check: the
// mcp-go Streamable HTTP client drives `server run --http` at every
// revision with a Streamable HTTP transport (initialize, tools/list, a
// gateway call, resources and prompts), and at the latest one gets an
// upstream's elicitation relayed mid-call and its progress notifications.
func TestHarness_StreamableHTTPConformance(t *testing.T) {
	bins := sharedBinaries(t)
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	servers := catalogServers(cat, "--resources")[:2]
	e := newEnv(t, bins, servers, "")
	url, _ := startHTTPFrontend(t, e, bins, "conformance-http.log")

	connect := func(t *testing.T, version string, opts ...client.ClientOption) (*client.Client, context.Context) {
		t.Helper()
		tr, err := transport.NewStreamableHTTP(url,
			transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + httpToken}),
			transport.WithContinuousListening())
		if err != nil {
			t.Fatal(err)
		}
		c := client.NewClient(tr, opts...)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		if err := c.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		init, err := c.Initialize(ctx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{
			ProtocolVersion: version,
			ClientInfo:      mcpgo.Implementation{Name: "harness-http", Version: "1"},
		}})
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}
		if init.ProtocolVersion != version {
			t.Fatalf("negotiated %s, want %s", init.ProtocolVersion, version)
		}
		return c, ctx
	}

	for _, version := range []string{"2025-03-26", "2025-06-18", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			c, ctx := connect(t, version)
			tools, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
			if err != nil || len(tools.Tools) != 4 {
				t.Fatalf("tools/list: %v %+v", err, tools)
			}
			res, err := c.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{
				Name: "list_tools", Arguments: map[string]interface{}{"server_name": servers[0].name},
			}})
			if err != nil || res.IsError || len(res.Content) == 0 {
				t.Fatalf("list_tools: %v %+v", err, res)
			}
			resources, err := c.ListResources(ctx, mcpgo.ListResourcesRequest{})
			if err != nil || len(resources.Resources) != 2 {
				t.Fatalf("resources/list: %v %+v", err, resources)
			}
			read, err := c.ReadResource(ctx, mcpgo.ReadResourceRequest{Params: mcpgo.ReadResourceParams{URI: resources.Resources[0].URI}})
			if err != nil || len(read.Contents) != 1 {
				t.Fatalf("resources/read: %v %+v", err, read)
			}
			prompts, err := c.ListPrompts(ctx, mcpgo.ListPromptsRequest{})
			if err != nil || len(prompts.Prompts) != 2 {
				t.Fatalf("prompts/list: %v %+v", err, prompts)
			}
			if _, err := c.GetPrompt(ctx, mcpgo.GetPromptRequest{Params: mcpgo.GetPromptParams{Name: servers[1].name + ".brief"}}); err != nil {
				t.Fatalf("prompts/get: %v", err)
			}
		})
	}

	t.Run("server-to-client", func(t *testing.T) {
		elicit := &elicitationAnswer{}
		c, ctx := connect(t, "2025-11-25", client.WithElicitationHandler(elicit))
		var mu sync.Mutex
		var progress []mcpgo.JSONRPCNotification
		c.OnNotification(func(n mcpgo.JSONRPCNotification) {
			if n.Method == "notifications/progress" {
				mu.Lock()
				progress = append(progress, n)
				mu.Unlock()
			}
		})
		server := servers[0].name
		invoke := func(tool string, meta *mcpgo.Meta) *mcpgo.CallToolResult {
			t.Helper()
			res, err := c.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{
				Name: "invoke_tool", Arguments: map[string]interface{}{"server": server, "tool": tool}, Meta: meta,
			}})
			if err != nil || res.IsError || len(res.Content) == 0 {
				t.Fatalf("invoke_tool %s: %v %+v", tool, err, res)
			}
			return res
		}
		res := invoke("ask_elicitation", nil)
		text, _ := res.Content[0].(mcpgo.TextContent)
		if !strings.Contains(text.Text, "client replied: true") || !strings.Contains(text.Text, `"staging"`) {
			t.Fatalf("elicitation answer did not reach the upstream: %+v", res.Content)
		}
		elicit.mu.Lock()
		shown := append([]string(nil), elicit.messages...)
		elicit.mu.Unlock()
		if len(shown) != 1 || shown[0] != "["+server+"] Which environment?" {
			t.Fatalf("elicitation shown to the user = %q", shown)
		}
		invoke("progress", &mcpgo.Meta{ProgressToken: "harness-http-token"})
		mu.Lock()
		defer mu.Unlock()
		if len(progress) < 2 {
			t.Fatalf("got %d progress notifications, want >= 2", len(progress))
		}
		for _, n := range progress {
			if tok := n.Params.AdditionalFields["progressToken"]; tok != "harness-http-token" {
				t.Fatalf("progress with token %v, want the client's own", tok)
			}
		}
	})
}
