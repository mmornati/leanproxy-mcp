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
	"sync"
	"syscall"
	"testing"
	"time"
)

// End-to-end coverage for the per-tool policy (#314): the real binary, in
// both front ends, in front of testdata/pinmcp serving annotated tools.

// writePolicyUpstream makes pinmcp serve: add, echo, delete_all and wipe
// (destructiveHint: true), and read (readOnlyHint + idempotentHint).
func writePolicyUpstream(e *pinEnv) {
	e.t.Helper()
	data, _ := json.Marshal(map[string]interface{}{
		"server": "pinmcp",
		"tools": []map[string]interface{}{
			{"name": "add", "description": "Adds two numbers."},
			{"name": "echo", "description": "Echoes the text back."},
			{"name": "delete_all", "description": "Deletes everything."},
			{"name": "wipe", "description": "Wipes the disk.", "annotations": map[string]bool{"destructiveHint": true}},
			{"name": "read", "description": "Reads a file.", "annotations": map[string]bool{"readOnlyHint": true, "idempotentHint": true}},
		},
	})
	writeFile(e.t, e.state, string(data))
}

// policyConfig is the issue's example policy on the test server.
func (e *pinEnv) policyConfig() string {
	e.t.Helper()
	path := filepath.Join(e.dir, "leanproxy-policy.yaml")
	writeFile(e.t, path, fmt.Sprintf(`version: "1.0"
servers:
  - name: %[1]s
    transport: stdio
    enabled: true
    stdio:
      command: %[2]q
      args: [%[3]q, %[4]q]
policy:
  default: allow
  unknown_tools: deny
  rules:
    - match: "%[1]s.add"
      action: confirm
    - match: "%[1]s.delete_*"
      action: deny
    - match: "*"
      annotations: { destructiveHint: true }
      action: confirm
`, e.server, e.fakeBin, e.state, e.calls))
	return path
}

// policyClient answers elicitation/create with the decision its answer
// field holds ("" leaves it unanswered).
type policyClient struct {
	*protoClient
	mu     sync.Mutex
	answer string
	asked  []string
}

func newPolicyClient(c *protoClient) *policyClient {
	pc := &policyClient{protoClient: c}
	c.onRequest = func(m protoMsg) map[string]interface{} {
		if m.Method != "elicitation/create" {
			return map[string]interface{}{"error": map[string]interface{}{"code": -32601, "message": "unknown"}}
		}
		pc.mu.Lock()
		defer pc.mu.Unlock()
		pc.asked = append(pc.asked, string(m.Params))
		switch pc.answer {
		case "":
			return nil
		case "decline":
			return map[string]interface{}{"result": map[string]interface{}{"action": "decline"}}
		}
		return map[string]interface{}{"result": map[string]interface{}{"action": "accept", "content": map[string]string{"decision": pc.answer}}}
	}
	return pc
}

func (pc *policyClient) set(answer string) {
	pc.mu.Lock()
	pc.answer = answer
	pc.mu.Unlock()
}

func (pc *policyClient) askedCount() int {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return len(pc.asked)
}

func stdioProto(t *testing.T, s *stdioSession) *protoClient {
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

func requirePolicyRefusal(t *testing.T, m protoMsg, what, fragment string) {
	t.Helper()
	if m.Error == nil || m.Error.Code != -32600 || !strings.Contains(m.Error.Message, "Call refused by leanproxy-mcp policy") || !strings.Contains(m.Error.Message, fragment) {
		t.Fatalf("%s: want a -32600 policy refusal containing %q, got %s", what, fragment, m.raw)
	}
}

// waitListed calls list_tools until the server's tools are known.
func waitListed(t *testing.T, c *protoClient, server string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		m := c.call("tools/call", map[string]interface{}{"name": "list_tools", "arguments": map[string]string{"server_name": server}})
		if m.Error == nil {
			if text := toolText(t, m); strings.Contains(text, server+"_echo") {
				return text
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("list_tools never listed %s: %s", server, m.raw)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func invoke(server, tool string) map[string]interface{} {
	return map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{"server": server, "tool": tool, "arguments": map[string]string{"text": "x"}}}
}

func TestPolicy_Stdio(t *testing.T) {
	e := newPinEnv(t, "pol")
	writePolicyUpstream(e)
	cfg := e.policyConfig()

	s := startStdioProxyEnv(t, e.proxyBin, cfg, e.env())
	c := newPolicyClient(stdioProto(t, s))
	protoInitializeWith(t, c.protoClient, map[string]interface{}{"elicitation": map[string]interface{}{}})

	// Discovery: the denied tool is hidden, the confirm ones are marked.
	text := waitListed(t, c.protoClient, e.server)
	if strings.Contains(text, e.server+"_delete_all") || !strings.Contains(text, "1 tool(s) hidden by the leanproxy-mcp policy") {
		t.Fatalf("list_tools must hide the denied tool: %s", text)
	}
	for _, want := range []string{e.server + "_add [confirm]:", e.server + "_wipe [destructive] [confirm]:", e.server + "_echo:", e.server + "_read [read-only]:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("list_tools lacks %q: %s", want, text)
		}
	}
	search := toolText(t, c.call("tools/call", map[string]interface{}{"name": "search_tools", "arguments": map[string]string{"query": "deletes everything"}}))
	if strings.Contains(search, e.server+"_delete_all") {
		t.Fatalf("search_tools must hide the denied tool: %s", search)
	}

	// Unknown tool: refused in every form, the upstream is not called.
	requirePolicyRefusal(t, c.call("tools/call", invoke(e.server, "drop_tables")), "invoke_tool unknown", `does not advertise a tool named "drop_tables"`)
	requirePolicyRefusal(t, c.call("tools/call", map[string]interface{}{"name": e.server + "_drop_tables", "arguments": map[string]string{}}), "namespaced unknown", "policy.unknown_tools: deny")
	// Deny glob.
	requirePolicyRefusal(t, c.call("tools/call", invoke(e.server, "delete_all")), "invoke_tool deny", `denied by policy rules[1] (match "`+e.server+`.delete_*")`)
	requirePolicyRefusal(t, c.call("tools/call", map[string]interface{}{"name": e.server + ".delete_all", "arguments": map[string]string{}}), "dotted deny", "rules[1]")
	// Allowed.
	if m := c.call("tools/call", invoke(e.server, "echo")); m.Error != nil {
		t.Fatalf("echo must be allowed: %s", m.raw)
	}

	// Confirm: approve proceeds, deny / decline refuse.
	c.set("approve")
	if m := c.call("tools/call", invoke(e.server, "add")); m.Error != nil {
		t.Fatalf("approved add: %s", m.raw)
	}
	if c.askedCount() != 1 || !strings.Contains(c.asked[0], "allow "+e.server+".add with arguments") {
		t.Fatalf("confirmation: %v", c.asked)
	}
	for _, a := range []string{"deny", "decline"} {
		c.set(a)
		requirePolicyRefusal(t, c.call("tools/call", invoke(e.server, "add")), "confirm "+a, "requires confirmation")
	}
	// The annotation rule, approved for the session: asked once.
	c.set("approve_session")
	for i := 0; i < 2; i++ {
		if m := c.call("tools/call", map[string]interface{}{"name": e.server + "_wipe", "arguments": map[string]string{}}); m.Error != nil {
			t.Fatalf("wipe approved for the session: %s", m.raw)
		}
	}
	if n := c.askedCount(); n != 4 {
		t.Fatalf("elicitations: %d, want 4 (add x3, wipe once)", n)
	}

	calls := strings.Fields(e.upstreamCalls())
	if strings.Join(calls, ",") != "echo,add,wipe,wipe" {
		t.Fatalf("upstream calls %v: refused calls must never reach the upstream", calls)
	}
	e.stop(s)
	logs := s.stderr.String()
	for _, want := range []string{"outcome=deny_unknown_tool", "outcome=deny ", "outcome=confirm_denied", "outcome=confirm_approved_session", "args_sha256="} {
		if !strings.Contains(logs, want) {
			t.Errorf("audit log lacks %q", want)
		}
	}

	// A client without elicitation support is refused, never allowed.
	s2 := startStdioProxyEnv(t, e.proxyBin, cfg, e.env())
	c2 := stdioProto(t, s2)
	protoInitializeWith(t, c2, map[string]interface{}{})
	waitListed(t, c2, e.server)
	requirePolicyRefusal(t, c2.call("tools/call", invoke(e.server, "add")), "no elicitation", "does not support MCP elicitation")
	e.stop(s2)
	if n := strings.Count(e.upstreamCalls(), "add"); n != 1 {
		t.Fatalf("add reached the upstream %d times, want 1", n)
	}

	// CLI: policy check explains the decision; doctor security reports it.
	out, err := exec.Command(e.proxyBin, "--config", cfg, "policy", "check", e.server+".delete_all").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Decision:    deny (rules[1]") || !strings.Contains(string(out), "<- first match") {
		t.Fatalf("policy check: %v\n%s", err, out)
	}
	out, err = exec.Command(e.proxyBin, "--config", cfg, "policy", "check", e.server+".wipe").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "destructiveHint=true") || !strings.Contains(string(out), "Decision:    confirm (rules[2]") {
		t.Fatalf("policy check wipe (annotations from the tool cache): %v\n%s", err, out)
	}
	out, err = exec.Command(e.proxyBin, "--config", cfg, "policy", "check", e.server+".nope").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Listed:      no") || !strings.Contains(string(out), "Decision:    deny (policy.unknown_tools") {
		t.Fatalf("policy check unknown: %v\n%s", err, out)
	}
	// doctor security exits non-zero whenever any OWASP check is ❌ (#323);
	// this fixture has no injection: block, so MCP06 legitimately fails —
	// only the detail section and exit code shape are asserted here.
	out, err = exec.Command(e.proxyBin, "--config", cfg, "doctor", "security").CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); err != nil && (!ok || exitErr.ExitCode() != 1) {
		t.Fatalf("doctor security: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "## Per-Tool Policy") || !strings.Contains(string(out), `rules[1] (match "`+e.server+`.delete_*") -> deny`) {
		t.Fatalf("doctor security: %v\n%s", err, out)
	}
}

func TestPolicy_Serve(t *testing.T) {
	e := newPinEnv(t, "pols")
	writePolicyUpstream(e)
	cfg := e.policyConfig()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	logs := &syncBuffer{}
	cmd := exec.Command(e.proxyBin, "serve", "--config", cfg, "--listen", addr, "--metrics-bind", "off", "--dashboard-bind", "off")
	cmd.Env = append(os.Environ(), e.env()...)
	cmd.Stdout, cmd.Stderr = logs, logs
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
			t:    t,
			send: func(b []byte) error { _, err := conn.Write(b); return err },
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
	c := newPolicyClient(dial())
	protoInitializeWith(t, c.protoClient, map[string]interface{}{"elicitation": map[string]interface{}{}})
	incapable := dial()
	protoInitializeWith(t, incapable, map[string]interface{}{})

	// Wait until the router knows the server's tools.
	deadline := time.Now().Add(20 * time.Second)
	for {
		m := c.call("tools/call", map[string]interface{}{"name": e.server + ".echo", "arguments": map[string]string{}})
		if m.Error == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("echo never routable: %s", m.raw)
		}
		time.Sleep(150 * time.Millisecond)
	}

	forms := func(tool string) []struct {
		method string
		params interface{}
	} {
		return []struct {
			method string
			params interface{}
		}{
			{"tools/call", map[string]interface{}{"name": e.server + "." + tool, "arguments": map[string]string{}}},
			{"invoke_tool", map[string]interface{}{"server_name": e.server, "tool_name": tool, "arguments": map[string]string{}}},
			{e.server + "." + tool, map[string]string{}},
		}
	}
	for _, f := range forms("drop_tables") {
		requirePolicyRefusal(t, c.call(f.method, f.params), "serve "+f.method+" unknown", "policy.unknown_tools: deny")
	}
	for _, f := range forms("delete_all") {
		requirePolicyRefusal(t, c.call(f.method, f.params), "serve "+f.method+" deny", "rules[1]")
	}

	c.set("approve")
	if m := c.call("tools/call", map[string]interface{}{"name": e.server + ".add", "arguments": map[string]string{}}); m.Error != nil {
		t.Fatalf("serve approved add: %s", m.raw)
	}
	c.set("deny")
	requirePolicyRefusal(t, c.call("tools/call", map[string]interface{}{"name": e.server + ".wipe", "arguments": map[string]string{}}), "serve denied wipe", "the user denied it")
	requirePolicyRefusal(t, incapable.call("tools/call", map[string]interface{}{"name": e.server + ".add", "arguments": map[string]string{}}), "serve incapable", "does not support MCP elicitation")
	if len(requestsOf(incapable, 0, "elicitation/create")) != 0 {
		t.Fatal("the incapable connection must never be asked")
	}

	calls := strings.Fields(e.upstreamCalls())
	if strings.Join(calls, ",") != "echo,add" {
		t.Fatalf("upstream calls %v: refused calls must never reach the upstream", calls)
	}
}

// TestResponseCache_HonorAnnotations_Stdio: with honor_annotations, a tool
// annotated readOnlyHint + idempotentHint is cached without being listed
// in response_cache.tools; other tools are not (#307 carry-over, #314).
func TestResponseCache_HonorAnnotations_Stdio(t *testing.T) {
	e := newPinEnv(t, "rca")
	writePolicyUpstream(e)
	cfg := filepath.Join(e.dir, "leanproxy-rc.yaml")
	writeFile(t, cfg, fmt.Sprintf(`version: "1.0"
servers:
  - name: %[1]s
    transport: stdio
    enabled: true
    stdio:
      command: %[2]q
      args: [%[3]q, %[4]q]
response_cache:
  enabled: true
  honor_annotations: true
`, e.server, e.fakeBin, e.state, e.calls))
	s := startStdioProxyEnv(t, e.proxyBin, cfg, e.env())
	c := stdioProto(t, s)
	protoInitializeWith(t, c, map[string]interface{}{})
	waitListed(t, c, e.server)
	for i := 0; i < 3; i++ {
		for _, tool := range []string{"read", "echo"} {
			if m := c.call("tools/call", invoke(e.server, tool)); m.Error != nil {
				t.Fatalf("%s: %s", tool, m.raw)
			}
		}
	}
	e.stop(s)
	calls := e.upstreamCalls()
	if strings.Count(calls, "read") != 1 || strings.Count(calls, "echo") != 3 {
		t.Fatalf("upstream calls %q: read (annotated read-only, idempotent) must be cached, echo must not", calls)
	}
}
