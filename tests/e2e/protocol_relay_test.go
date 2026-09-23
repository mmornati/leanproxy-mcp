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

// End-to-end coverage for issue #308 (MCP protocol upgrade, part 2),
// driving the real binary through both front ends with protomcp upstreams
// (tests/e2e/testdata/protomcp) that send server-to-client requests:
//
//   - elicitation/create is relayed mid-call (message prefixed with the
//     server name), the client's answer reaches the upstream redacted, and
//     the call completes;
//   - a client without the capability makes the upstream get -32601 at
//     once;
//   - sampling/createMessage is refused by default and relayed with
//     allow_sampling: true; roots/list is relayed, or answered from a
//     static servers[].roots list;
//   - progress notifications reach the client with its own token during a
//     2 s call (the upstream sees a proxy token);
//   - a client cancel reaches the upstream as a cancel notification, and
//     an upstream cancel of a relayed request reaches the client;
//   - notifications/resources/updated reaches the subscribed client,
//     namespaced.

// notificationCancelled is the MCP cancel notification.
const notificationCancelled = "notifications/cancelled" //nolint:misspell // MCP protocol method name

// relayCaps is what a fully capable client declares.
var relayCaps = map[string]interface{}{
	"elicitation": map[string]interface{}{},
	"sampling":    map[string]interface{}{},
	"roots":       map[string]interface{}{"listChanged": true},
}

// writeRelayConfig configures <prefix>a (defaults) and <prefix>b
// (allow_sampling, static roots), both listing the relay tools.
func writeRelayConfig(t *testing.T, prefix, fakeBin string) (cfg, a, b string) {
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
      args: [%[1]q, "--list-relay-tools"]
  - name: %[2]s
    transport: stdio
    enabled: true
    allow_sampling: true
    roots:
      - uri: file:///work/%[2]s
        name: static-root
    stdio:
      command: %[3]q
      args: [%[2]q, "--list-relay-tools"]
`, a, b, fakeBin))
	return cfg, a, b
}

// answerAll is a client that answers every server-to-client request; the
// elicitation answer carries a fake secret the proxy must redact.
func answerAll(m protoMsg) map[string]interface{} {
	switch m.Method {
	case "elicitation/create":
		if strings.Contains(string(m.Params), "Never mind") {
			return nil // left unanswered: the upstream cancels it
		}
		return map[string]interface{}{"result": map[string]interface{}{
			"action": "accept", "content": map[string]string{"name": "Ada " + protoAWSKey},
		}}
	case "sampling/createMessage":
		return map[string]interface{}{"result": map[string]interface{}{
			"role": "assistant", "model": "e2e", "content": map[string]string{"type": "text", "text": "client summary"},
		}}
	case "roots/list":
		return map[string]interface{}{"result": map[string]interface{}{"roots": []map[string]string{{"uri": "file:///client/root", "name": "client"}}}}
	}
	return map[string]interface{}{"error": map[string]interface{}{"code": -32601, "message": "unknown"}}
}

func protoInitializeWith(t *testing.T, c *protoClient, caps map[string]interface{}) map[string]json.RawMessage {
	t.Helper()
	resp := c.call("initialize", map[string]interface{}{
		"protocolVersion": "2025-11-25", "capabilities": caps,
		"clientInfo": map[string]string{"name": "e2e-relay", "version": "1"},
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

// relayFrontEnd builds the tools/call params that invoke a protomcp tool:
// through invoke_tool on stdio, through the namespaced tool name on serve.
type relayFrontEnd func(server, tool string) map[string]interface{}

func viaInvokeTool(server, tool string) map[string]interface{} {
	return map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{"server": server, "tool": tool}}
}

func viaNamespacedTool(server, tool string) map[string]interface{} {
	return map[string]interface{}{"name": server + "." + tool, "arguments": map[string]interface{}{}}
}

// call invokes tool on server, with _meta when meta is set.
func (f relayFrontEnd) call(c *protoClient, server, tool string, meta map[string]interface{}) protoMsg {
	params := f(server, tool)
	if meta != nil {
		params["_meta"] = meta
	}
	return c.call("tools/call", params)
}

// sawSecret reports the upstream's "sawSecret" flag.
func sawSecret(t *testing.T, m protoMsg) bool {
	t.Helper()
	var r struct {
		SawSecret bool `json:"sawSecret"`
	}
	if err := json.Unmarshal(m.Result, &r); err != nil {
		t.Fatalf("no result in %s", m.raw)
	}
	return r.SawSecret
}

// requestsOf returns the server-to-client requests of method received
// since index from.
func requestsOf(c *protoClient, from int, method string) []protoMsg {
	var out []protoMsg
	for _, r := range c.requests[from:] {
		if r.Method == method {
			out = append(out, r)
		}
	}
	return out
}

// checkRelay runs the capable-client scenario on c.
func checkRelay(t *testing.T, c *protoClient, fe relayFrontEnd, a, b string) {
	t.Helper()
	c.onRequest = answerAll

	// Elicitation relayed mid-call, prefixed; the answer reaches the
	// upstream redacted and the call completes.
	n := len(c.requests)
	res := fe.call(c, a, "ask_user", nil)
	if res.Error != nil || !strings.Contains(toolText(t, res), `"action":"accept"`) {
		t.Fatalf("ask_user = %s", res.raw)
	}
	el := requestsOf(c, n, "elicitation/create")
	if len(el) != 1 || !strings.Contains(string(el[0].Params), `"message":"[`+a+`] What is your name?"`) {
		t.Fatalf("elicitation requests = %+v", el)
	}
	if !strings.HasPrefix(string(el[0].ID), `"lp-`) {
		t.Fatalf("elicitation id %s is not a proxy id", el[0].ID)
	}
	if sawSecret(t, res) {
		t.Fatalf("the client's elicitation answer reached the upstream unredacted: %s", res.raw)
	}

	// Sampling: refused by default, relayed with allow_sampling.
	n = len(c.requests)
	res = fe.call(c, a, "sample", nil)
	if text := toolText(t, res); !strings.Contains(text, "-32601") || !strings.Contains(text, "allow_sampling") {
		t.Fatalf("sampling on %s not refused: %s", a, res.raw)
	}
	if got := requestsOf(c, n, "sampling/createMessage"); len(got) != 0 {
		t.Fatalf("refused sampling reached the client: %+v", got)
	}
	res = fe.call(c, b, "sample", nil)
	if !strings.Contains(toolText(t, res), "client summary") || len(requestsOf(c, n, "sampling/createMessage")) != 1 {
		t.Fatalf("sampling on %s not relayed: %s", b, res.raw)
	}

	// Roots: relayed for a, static for b.
	n = len(c.requests)
	if res = fe.call(c, a, "roots", nil); !strings.Contains(toolText(t, res), "file:///client/root") {
		t.Fatalf("roots on %s = %s", a, res.raw)
	}
	if res = fe.call(c, b, "roots", nil); !strings.Contains(toolText(t, res), "file:///work/"+b) || strings.Contains(toolText(t, res), "client/root") {
		t.Fatalf("static roots on %s = %s", b, res.raw)
	}
	if got := requestsOf(c, n, "roots/list"); len(got) != 1 {
		t.Fatalf("roots/list requests at the client = %d, want 1", len(got))
	}

	// What the proxy declares upstream follows the config.
	if res = fe.call(c, a, "client_caps", nil); strings.Contains(toolText(t, res), "sampling") || !strings.Contains(toolText(t, res), "elicitation") {
		t.Fatalf("capabilities declared to %s = %s", a, res.raw)
	}
	if res = fe.call(c, b, "client_caps", nil); !strings.Contains(toolText(t, res), "sampling") {
		t.Fatalf("capabilities declared to %s = %s", b, res.raw)
	}

	// Progress: the client's own token, during a 2 s call.
	notes := len(c.noteMsgs)
	start := time.Now()
	res = fe.call(c, a, "slow", map[string]interface{}{"progressToken": "tok-e2e"})
	if d := time.Since(start); d < 1500*time.Millisecond {
		t.Fatalf("slow call took %s", d)
	}
	if text := toolText(t, res); !strings.Contains(text, "lp-progress-") || strings.Contains(text, "tok-e2e") {
		t.Fatalf("upstream progress token = %s", text)
	}
	progress := 0
	for _, m := range c.noteMsgs[notes:] {
		if m.Method != "notifications/progress" {
			continue
		}
		if !strings.Contains(string(m.Params), `"progressToken":"tok-e2e"`) {
			t.Fatalf("progress with a foreign token: %s", m.raw)
		}
		progress++
	}
	if progress < 2 {
		t.Fatalf("got %d progress notifications, want >= 2", progress)
	}

	// Client cancel → the upstream gets the cancel notification (and the
	// canceled call is never answered: call() fails on a stray id).
	c.nextID++
	hangID := c.nextID
	hang, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": hangID, "method": "tools/call",
		"params": fe(a, "hang")})
	if err := c.send(append(hang, '\n')); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	cancel, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "method": notificationCancelled,
		"params": map[string]interface{}{"requestId": hangID, "reason": "e2e"}})
	if err := c.send(append(cancel, '\n')); err != nil {
		t.Fatal(err)
	}
	if res = fe.call(c, a, "last_cancel", nil); !strings.Contains(toolText(t, res), "last canceled: ") || strings.HasSuffix(toolText(t, res), "last canceled: ") {
		t.Fatalf("upstream never saw the cancel: %s", res.raw)
	}

	// Upstream cancel of a relayed request → the client gets
	// the cancel notification for the proxy's id.
	n, notes = len(c.requests), len(c.noteMsgs)
	fe.call(c, a, "ask_then_cancel", nil)
	el = requestsOf(c, n, "elicitation/create")
	if len(el) != 1 {
		t.Fatalf("ask_then_cancel sent %d elicitations", len(el))
	}
	want := `"requestId":` + string(el[0].ID)
	deadline := time.Now().Add(5 * time.Second)
	for !noteWith(c.noteMsgs[notes:], notificationCancelled, want) && time.Now().Before(deadline) {
		c.waitNote("never", 200*time.Millisecond)
	}
	if !noteWith(c.noteMsgs[notes:], notificationCancelled, want) {
		t.Fatalf("no cancel notification %s at the client", want)
	}

	// Resource updates reach the subscriber, namespaced.
	uri := "leanproxy://" + a + "/file:///" + a + "/notes.txt"
	if sub := c.call("resources/subscribe", map[string]string{"uri": uri}); sub.Error != nil {
		t.Fatalf("subscribe = %s", sub.raw)
	}
	notes = len(c.noteMsgs)
	fe.call(c, a, "notify_updated", nil)
	deadline = time.Now().Add(5 * time.Second)
	for !noteWith(c.noteMsgs[notes:], "notifications/resources/updated", `"uri":"`+uri+`"`) && time.Now().Before(deadline) {
		c.waitNote("never", 200*time.Millisecond)
	}
	if !noteWith(c.noteMsgs[notes:], "notifications/resources/updated", `"uri":"`+uri+`"`) {
		t.Fatalf("no namespaced notifications/resources/updated; got %v", c.notes)
	}
}

func noteWith(notes []protoMsg, method, fragment string) bool {
	for _, m := range notes {
		if m.Method == method && strings.Contains(string(m.Params), fragment) {
			return true
		}
	}
	return false
}

// checkIncapable checks that a client without capabilities gets nothing
// relayed and the upstream is answered -32601 at once.
func checkIncapable(t *testing.T, c *protoClient, fe relayFrontEnd, a string) {
	t.Helper()
	c.onRequest = answerAll
	start := time.Now()
	res := fe.call(c, a, "ask_user", nil)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("ask_user without the capability took %s", d)
	}
	if text := toolText(t, res); !strings.Contains(text, "-32601") {
		t.Fatalf("upstream did not get -32601: %s", res.raw)
	}
	if len(c.requests) != 0 {
		t.Fatalf("an incapable client got server-to-client requests: %+v", c.requests)
	}
}

func TestProtocolRelay_Stdio(t *testing.T) {
	proxyBin, fakeBin := buildProtoBinaries(t)
	cfg, a, b := writeRelayConfig(t, fmt.Sprintf("rs%d", os.Getpid()), fakeBin)

	t.Run("capable client", func(t *testing.T) {
		c := startProtoStdio(t, proxyBin, cfg)
		res := protoInitializeWith(t, c, relayCaps)
		if !strings.Contains(string(res["capabilities"]), `"subscribe":true`) {
			t.Fatalf("resources.subscribe not advertised: %s", res["capabilities"])
		}
		checkRelay(t, c, viaInvokeTool, a, b)
	})

	t.Run("client without capabilities", func(t *testing.T) {
		c := startProtoStdio(t, proxyBin, cfg)
		protoInitializeWith(t, c, map[string]interface{}{})
		checkIncapable(t, c, viaInvokeTool, a)
	})
}

func TestProtocolRelay_Serve(t *testing.T) {
	proxyBin, fakeBin := buildProtoBinaries(t)
	cfg, a, b := writeRelayConfig(t, fmt.Sprintf("rv%d", os.Getpid()), fakeBin)
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

	capable, incapable := dial(), dial()
	protoInitializeWith(t, capable, relayCaps)
	protoInitializeWith(t, incapable, map[string]interface{}{})

	// The relay tools become routable after the first background refresh.
	deadline := time.Now().Add(20 * time.Second)
	for {
		res := relayFrontEnd(viaNamespacedTool).call(capable, b, "client_caps", nil)
		if res.Error == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay tools never routable: %s", res.raw)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// While the capable client is idle, the incapable one's call must not
	// be routed to it.
	checkIncapable(t, incapable, viaNamespacedTool, a)
	if n := len(requestsOf(capable, 0, "elicitation/create")); n != 0 {
		t.Fatalf("another connection's elicitation reached the capable client (%d)", n)
	}
	checkRelay(t, capable, viaNamespacedTool, a, b)
}
