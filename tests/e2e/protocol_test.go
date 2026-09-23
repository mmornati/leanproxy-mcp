package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// End-to-end coverage for issue #307 (MCP protocol upgrade, part 1),
// driving the real binary through both front ends with two protomcp
// upstreams (tests/e2e/testdata/protomcp):
//
//   - version negotiation per client, and no new fields for 2024-11-05;
//   - tool annotations in list_tools text, full tool objects in
//     structuredContent for >= 2025-06-18;
//   - invoke_tool relays structuredContent and resource_link items (after
//     redaction);
//   - resources, templates and prompts of both upstreams merged and
//     namespaced, resources/read and prompts/get round-trip, secrets
//     redacted, upstream pagination followed;
//   - an upstream notifications/resources/list_changed reaches the client.

var protoAWSKey = "AKIA" + "IOSFODNN7EXAMPLE"

func buildProtoBinaries(t *testing.T) (proxyBin, fakeBin string) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..")
	dir := t.TempDir()
	proxyBin = filepath.Join(dir, "leanproxy-mcp")
	fakeBin = filepath.Join(dir, "protomcp")
	for _, b := range []struct{ out, pkg string }{
		{proxyBin, "."},
		{fakeBin, "./tests/e2e/testdata/protomcp"},
	} {
		cmd := exec.Command(goBin, "build", "-o", b.out, b.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", b.pkg, err, out)
		}
	}
	return proxyBin, fakeBin
}

// writeProtoConfig configures two protomcp servers, <prefix>a and <prefix>b.
func writeProtoConfig(t *testing.T, prefix, fakeBin string) (cfg, a, b string) {
	t.Helper()
	a, b = prefix+"a", prefix+"b"
	cfg = filepath.Join(t.TempDir(), "leanproxy.yaml")
	writeFile(t, cfg, fmt.Sprintf(`version: "1.0"
servers:
  - name: %[1]s
    transport: stdio
    enabled: true
    stdio:
      command: %[3]q
      args: [%[1]q]
  - name: %[2]s
    transport: stdio
    enabled: true
    stdio:
      command: %[3]q
      args: [%[2]q]
`, a, b, fakeBin))
	return cfg, a, b
}

// protoMsg is any message the proxy writes: a response or a notification.
type protoMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	raw string
}

// protoClient speaks newline-delimited JSON-RPC and sets notifications
// aside while it waits for a response. Server-to-client requests (#308)
// are recorded and answered by onRequest (or left unanswered when it is
// nil or returns nil).
type protoClient struct {
	t      *testing.T
	send   func([]byte) error
	next   func(time.Duration) (string, bool)
	nextID int
	notes  []string
	// noteMsgs holds every notification received, in order.
	noteMsgs []protoMsg
	// requests holds every server-to-client request received, in order.
	requests []protoMsg
	// onRequest returns the answer to a server-to-client request: a
	// map with "result" or "error".
	onRequest func(m protoMsg) map[string]interface{}
}

// handleIncoming records a message that is not the awaited response. It
// reports whether it was a notification or a server-to-client request.
func (c *protoClient) handleIncoming(m protoMsg) bool {
	c.t.Helper()
	if m.Method == "" {
		return false
	}
	if len(m.ID) == 0 {
		c.notes = append(c.notes, m.Method)
		c.noteMsgs = append(c.noteMsgs, m)
		return true
	}
	c.requests = append(c.requests, m)
	if c.onRequest == nil {
		return true
	}
	answer := c.onRequest(m)
	if answer == nil {
		return true
	}
	answer["jsonrpc"] = "2.0"
	answer["id"] = m.ID
	data, err := json.Marshal(answer)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.send(append(data, '\n')); err != nil {
		c.t.Fatalf("answer %s: %v", m.Method, err)
	}
	return true
}

func (c *protoClient) call(method string, params interface{}) protoMsg {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	req := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.send(append(data, '\n')); err != nil {
		c.t.Fatalf("write %s: %v", method, err)
	}
	for {
		line, ok := c.next(30 * time.Second)
		if !ok {
			c.t.Fatalf("no response to %s (id %d)", method, id)
		}
		var m protoMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			c.t.Fatalf("invalid line %q: %v", line, err)
		}
		m.raw = line
		if c.handleIncoming(m) {
			continue
		}
		if string(m.ID) != fmt.Sprint(id) {
			c.t.Fatalf("response id %s, want %d: %s", m.ID, id, line)
		}
		return m
	}
}

func (c *protoClient) notify(method string) {
	c.t.Helper()
	if err := c.send([]byte(`{"jsonrpc":"2.0","method":"` + method + `"}` + "\n")); err != nil {
		c.t.Fatal(err)
	}
}

// waitNote waits for a notification (already received or still to come).
func (c *protoClient) waitNote(method string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		for _, n := range c.notes {
			if n == method {
				return true
			}
		}
		left := time.Until(deadline)
		if left <= 0 {
			return false
		}
		line, ok := c.next(left)
		if !ok {
			return false
		}
		var m protoMsg
		if json.Unmarshal([]byte(line), &m) == nil {
			m.raw = line
			c.handleIncoming(m)
		}
	}
}

func startProtoStdio(t *testing.T, proxyBin, cfg string) *protoClient {
	t.Helper()
	s := startStdioProxy(t, proxyBin, cfg)
	return &protoClient{
		t: t,
		send: func(b []byte) error {
			_, err := s.stdin.Write(b)
			return err
		},
		next: func(d time.Duration) (string, bool) {
			select {
			case line, ok := <-s.lines:
				return strings.TrimRight(string(line), "\n"), ok
			case <-time.After(d):
				return "", false
			}
		},
	}
}

func protoInitialize(t *testing.T, c *protoClient, version string) map[string]json.RawMessage {
	t.Helper()
	resp := c.call("initialize", map[string]interface{}{
		"protocolVersion": version, "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if resp.Error != nil {
		t.Fatalf("initialize: %s", resp.raw)
	}
	var res map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	c.notify("notifications/initialized")
	return res
}

func toolText(t *testing.T, resp protoMsg) string {
	t.Helper()
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil || len(r.Content) == 0 {
		t.Fatalf("no text content in %s", resp.raw)
	}
	return r.Content[0].Text
}

func TestProtocolUpgrade_Stdio(t *testing.T) {
	proxyBin, fakeBin := buildProtoBinaries(t)
	cfg, a, b := writeProtoConfig(t, fmt.Sprintf("pv%d", os.Getpid()), fakeBin)

	t.Run("2024-11-05 client gets no new fields", func(t *testing.T) {
		c := startProtoStdio(t, proxyBin, cfg)
		res := protoInitialize(t, c, "2024-11-05")
		if string(res["protocolVersion"]) != `"2024-11-05"` {
			t.Fatalf("negotiated %s", res["protocolVersion"])
		}
		if strings.Contains(string(res["serverInfo"]), "title") {
			t.Fatalf("serverInfo.title sent to a 2024-11-05 client: %s", res["serverInfo"])
		}
		if tl := c.call("tools/list", nil); strings.Contains(tl.raw, "annotations") {
			t.Fatalf("tools/list has annotations for 2024-11-05: %s", tl.raw)
		}
		lt := c.call("tools/call", map[string]interface{}{"name": "list_tools", "arguments": map[string]string{"server_name": a}})
		if !strings.Contains(toolText(t, lt), a+"_delete_repo [destructive]:") || strings.Contains(lt.raw, "structuredContent") {
			t.Fatalf("list_tools for 2024-11-05: %s", lt.raw)
		}
	})

	t.Run("unknown version gets the latest", func(t *testing.T) {
		c := startProtoStdio(t, proxyBin, cfg)
		if res := protoInitialize(t, c, "2099-01-01"); string(res["protocolVersion"]) != `"2025-11-25"` {
			t.Fatalf("negotiated %s", res["protocolVersion"])
		}
	})

	t.Run("2025-06-18 client", func(t *testing.T) {
		c := startProtoStdio(t, proxyBin, cfg)
		res := protoInitialize(t, c, "2025-06-18")
		if string(res["protocolVersion"]) != `"2025-06-18"` {
			t.Fatalf("negotiated %s", res["protocolVersion"])
		}
		caps := string(res["capabilities"])
		// protomcp supports resources/subscribe, relayed since #308.
		if !strings.Contains(caps, `"resources":{"subscribe":true,"listChanged":true}`) || !strings.Contains(caps, `"prompts":{"listChanged":true}`) {
			t.Fatalf("capabilities = %s", caps)
		}

		// list_tools: compact annotation markers, full objects in
		// structuredContent (the upstream paginates one tool per page).
		lt := c.call("tools/call", map[string]interface{}{"name": "list_tools", "arguments": map[string]string{"server_name": a}})
		text := toolText(t, lt)
		for _, want := range []string{a + " tools (5)", a + "_delete_repo [destructive]: Delete a repository [repo: string]", a + "_get_repo [read-only]:"} {
			if !strings.Contains(text, want) {
				t.Fatalf("list_tools text lacks %q:\n%s", want, text)
			}
		}
		var structured struct {
			StructuredContent struct {
				Tools []struct {
					Server string          `json:"server"`
					Tool   json.RawMessage `json:"tool"`
				} `json:"tools"`
			} `json:"structuredContent"`
		}
		if err := json.Unmarshal(lt.Result, &structured); err != nil || len(structured.StructuredContent.Tools) != 5 {
			t.Fatalf("structuredContent = %s (%v)", lt.raw, err)
		}
		del := string(structured.StructuredContent.Tools[0].Tool)
		for _, want := range []string{`"outputSchema":{`, `"destructiveHint":true`, `"title":"Delete repository"`, `"_meta":{"ui/resourceUri":"ui://` + a + `/delete"}`} {
			if !strings.Contains(del, want) {
				t.Fatalf("full tool object lacks %s: %s", want, del)
			}
		}

		// search_tools carries the same structured objects.
		st := c.call("tools/call", map[string]interface{}{"name": "search_tools", "arguments": map[string]string{"query": "delete repository"}})
		if !strings.Contains(toolText(t, st), "_delete_repo [destructive]:") || !strings.Contains(st.raw, `"structuredContent":{"tools":[`) {
			t.Fatalf("search_tools = %s", st.raw)
		}

		// invoke_tool relays structuredContent and resource_link unchanged
		// (the secret in the text block is redacted).
		rich := c.call("tools/call", map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{"server": b, "tool": "rich"}})
		for _, want := range []string{`"structuredContent":{"id":9007199254740993,"server":"` + b + `"}`, `{"type":"resource_link","uri":"file:///` + b + `/notes.txt","name":"notes","mimeType":"text/plain"}`} {
			if !strings.Contains(rich.raw, want) {
				t.Fatalf("invoke_tool result lacks %s: %s", want, rich.raw)
			}
		}
		if strings.Contains(rich.raw, protoAWSKey) {
			t.Fatalf("invoke_tool leaked a secret: %s", rich.raw)
		}
		echo := c.call("tools/call", map[string]interface{}{"name": "invoke_tool", "arguments": json.RawMessage(`{"server":"` + a + `","tool":"echo","arguments":{"n":9007199254740993}}`)})
		if !strings.Contains(toolText(t, echo), `{"n":9007199254740993}`) {
			t.Fatalf("big integer not relayed: %s", echo.raw)
		}

		// Resources: both upstreams merged and namespaced (each serves one
		// per page).
		rl := c.call("resources/list", nil)
		for _, srv := range []string{a, b} {
			for _, f := range []string{"notes.txt", "big.txt"} {
				if want := `"uri":"leanproxy://` + srv + `/file:///` + srv + `/` + f + `"`; !strings.Contains(rl.raw, want) {
					t.Fatalf("resources/list lacks %s: %s", want, rl.raw)
				}
			}
		}
		if !strings.Contains(rl.raw, `"size":12345678901234567`) {
			t.Fatalf("resource members not kept verbatim: %s", rl.raw)
		}
		rt := c.call("resources/templates/list", nil)
		if !strings.Contains(rt.raw, `"uriTemplate":"leanproxy://`+a+`/file:///`+a+`/{path}"`) || !strings.Contains(rt.raw, `leanproxy://`+b+`/`) {
			t.Fatalf("templates = %s", rt.raw)
		}
		rr := c.call("resources/read", map[string]string{"uri": "leanproxy://" + b + "/file:///" + b + "/notes.txt"})
		if rr.Error != nil || !strings.Contains(rr.raw, `"uri":"leanproxy://`+b+`/file:///`+b+`/notes.txt"`) || !strings.Contains(rr.raw, "from "+b) {
			t.Fatalf("resources/read = %s", rr.raw)
		}
		if strings.Contains(rr.raw, protoAWSKey) {
			t.Fatalf("resources/read leaked a secret: %s", rr.raw)
		}
		// A template expansion and a resource_link's own URI route too.
		if rr = c.call("resources/read", map[string]string{"uri": "leanproxy://" + a + "/file:///" + a + "/deep/x.md"}); rr.Error != nil || !strings.Contains(rr.raw, "from "+a) {
			t.Fatalf("template read = %s", rr.raw)
		}
		if rr = c.call("resources/read", map[string]string{"uri": "file:///" + b + "/notes.txt"}); rr.Error != nil || !strings.Contains(rr.raw, "from "+b) {
			t.Fatalf("raw URI read = %s", rr.raw)
		}
		if rr = c.call("resources/read", map[string]string{"uri": "leanproxy://nosuch/file:///x"}); rr.Error == nil || rr.Error.Code != -32002 {
			t.Fatalf("unknown resource = %s", rr.raw)
		}

		// Prompts: merged, prefixed, get round-trips.
		pl := c.call("prompts/list", nil)
		if !strings.Contains(pl.raw, `"name":"`+a+`.greet"`) || !strings.Contains(pl.raw, `"name":"`+b+`.greet"`) {
			t.Fatalf("prompts/list = %s", pl.raw)
		}
		pg := c.call("prompts/get", map[string]interface{}{"name": b + ".greet", "arguments": map[string]string{"who": "Ada"}})
		if pg.Error != nil || !strings.Contains(pg.raw, "Hello Ada from "+b) {
			t.Fatalf("prompts/get = %s", pg.raw)
		}

		// An upstream resource list change reaches the client.
		c.call("tools/call", map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{"server": a, "tool": "notify_resources"}})
		if !c.waitNote("notifications/resources/list_changed", 10*time.Second) {
			t.Fatalf("no notifications/resources/list_changed; got %v", c.notes)
		}
	})
}

// TestProtocolUpgrade_Serve drives the same features through `serve`, plus
// its simple gateway mode's byte-faithful invoke_tool arguments.
func TestProtocolUpgrade_Serve(t *testing.T) {
	proxyBin, fakeBin := buildProtoBinaries(t)
	cfg, a, b := writeProtoConfig(t, fmt.Sprintf("ps%d", os.Getpid()), fakeBin)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	logs := &syncBuffer{}
	cmd := exec.Command(proxyBin, "serve", "--config", cfg, "--listen", addr, "--metrics-bind", "off", "--dashboard-bind", "off")
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("serve logs:\n%s", logs.String())
		}
	})
	waitForPort(t, addr)

	dial := func() *protoClient {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if _, err := conn.Write(serveAuthLine()); err != nil {
			t.Fatal(err)
		}
		// The auth line is a notification: no answer.
		r := bufio.NewReader(conn)
		return &protoClient{
			t: t,
			send: func(b []byte) error {
				_, err := conn.Write(b)
				return err
			},
			next: func(d time.Duration) (string, bool) {
				_ = conn.SetReadDeadline(time.Now().Add(d))
				line, err := r.ReadString('\n')
				if err != nil {
					return "", false
				}
				return strings.TrimRight(line, "\n"), true
			},
		}
	}

	modern, legacy := dial(), dial()
	if res := protoInitialize(t, modern, "2025-06-18"); string(res["protocolVersion"]) != `"2025-06-18"` || !strings.Contains(string(res["capabilities"]), `"resources":{"subscribe":true,"listChanged":true}`) {
		t.Fatalf("serve initialize = %v", res)
	}
	if res := protoInitialize(t, legacy, "2024-11-05"); string(res["protocolVersion"]) != `"2024-11-05"` {
		t.Fatalf("serve legacy initialize = %v", res)
	}

	rl := modern.call("resources/list", nil)
	if !strings.Contains(rl.raw, `leanproxy://`+a+`/file:///`+a+`/notes.txt`) || !strings.Contains(rl.raw, `leanproxy://`+b+`/file:///`+b+`/big.txt`) {
		t.Fatalf("serve resources/list = %s", rl.raw)
	}
	// Read twice: never answered from a cache (each read reaches the
	// upstream; both are redacted).
	for i := 0; i < 2; i++ {
		rr := modern.call("resources/read", map[string]string{"uri": "leanproxy://" + a + "/file:///" + a + "/notes.txt"})
		if rr.Error != nil || !strings.Contains(rr.raw, "from "+a) || strings.Contains(rr.raw, protoAWSKey) {
			t.Fatalf("serve resources/read = %s", rr.raw)
		}
	}
	pg := legacy.call("prompts/get", map[string]interface{}{"name": a + ".greet", "arguments": map[string]string{"who": "Bob"}})
	if pg.Error != nil || !strings.Contains(pg.raw, "Hello Bob from "+a) {
		t.Fatalf("serve prompts/get = %s", pg.raw)
	}

	// Simple gateway mode relays invoke_tool arguments byte for byte
	// (#307 carry-over: they used to go through a map and lose precision).
	inv := modern.call("invoke_tool", json.RawMessage(`{"server_name":"`+a+`","tool_name":"echo","arguments":{"id":9007199254740993}}`))
	deadline := time.Now().Add(20 * time.Second)
	for inv.Error != nil && time.Now().Before(deadline) {
		// The server's tools become known to the router after its first
		// background refresh.
		time.Sleep(100 * time.Millisecond)
		inv = modern.call("invoke_tool", json.RawMessage(`{"server_name":"`+a+`","tool_name":"echo","arguments":{"id":9007199254740993}}`))
	}
	if inv.Error != nil || !strings.Contains(inv.raw, `9007199254740993`) || strings.Contains(inv.raw, `9007199254740992`) {
		t.Fatalf("serve invoke_tool = %s", inv.raw)
	}

	// A routed backend tool call keeps big integers and rich results.
	tc := modern.call("tools/call", map[string]interface{}{"name": b + ".rich", "arguments": map[string]interface{}{}})
	if tc.Error != nil || !strings.Contains(tc.raw, `"structuredContent":{"id":9007199254740993`) || !strings.Contains(tc.raw, `"type":"resource_link"`) || strings.Contains(tc.raw, protoAWSKey) {
		t.Fatalf("serve tools/call rich = %s", tc.raw)
	}

	// The list change is pushed to every initialized connection.
	modern.call("tools/call", map[string]interface{}{"name": b + ".notify_resources", "arguments": map[string]interface{}{}})
	if !modern.waitNote("notifications/resources/list_changed", 10*time.Second) || !legacy.waitNote("notifications/resources/list_changed", 10*time.Second) {
		t.Fatalf("list_changed not pushed: modern %v legacy %v", modern.notes, legacy.notes)
	}
}
