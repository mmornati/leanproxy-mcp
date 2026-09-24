package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// End-to-end coverage for issue #320 (field projection): the real binary
// with the bigmcp upstream returning wide GitHub-like objects and arrays,
// through `server run --stdio`, `server run --http` and `serve`:
//
//   - the github.* drop pack shrinks a 30-issue listing, keeps valid JSON
//     and exact numbers, and adds a note and (2025-06-18) a resource_link;
//     read_result serves the full redacted original;
//   - a keep rule returns only the listed fields, nested arrays included;
//   - invoke_tool's fields argument is applied and never reaches the
//     upstream;
//   - non-JSON text and error results are untouched;
//   - structuredContent is projected unless the tool declares an
//     outputSchema;
//   - projection then truncation keeps the result within max_tokens.

const projectionOn = `response:
  enabled: true
  max_tokens: 4000
  tools:
    - match: "raw.*"
      passthrough: true
  projections:
    - match: "big.wide_structured"
      keep: ["issues[].number", "issues[].title"]
    - match: "big.big_json"
      keep: ["[].number"]
    - match: "big.*"
      drop: ["**.node_id", "**.*_url", "**.url", "**.reactions", "**.avatar_url", "**.gravatar_id"]
`

type projToolResult struct {
	Content           []govContent    `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func decodeProjResult(t *testing.T, m protoMsg) projToolResult {
	t.Helper()
	if m.Error != nil {
		t.Fatalf("JSON-RPC error: %s", m.raw)
	}
	var r projToolResult
	if err := json.Unmarshal(m.Result, &r); err != nil {
		t.Fatalf("invalid tool result: %v: %.300s", err, m.raw)
	}
	return r
}

func projNote(r projToolResult) string {
	for _, c := range r.Content {
		if c.Type == "text" && strings.HasPrefix(c.Text, "[LeanProxy: ") && strings.Contains(c.Text, "projected by") {
			return c.Text
		}
	}
	return ""
}

func projLinks(r projToolResult) []string {
	var out []string
	for _, c := range r.Content {
		if c.Type == "resource_link" {
			out = append(out, c.URI)
		}
	}
	return out
}

// projectionScenario runs the acceptance criteria on client c (a session
// that negotiated 2025-06-18). withFields: the front end has invoke_tool.
func projectionScenario(t *testing.T, c *protoClient, call governorFrontEnd, withFields bool) {
	t.Helper()
	secret := "ghp_" + strings.Repeat("a1B2", 9)

	// Reference: the passthrough copy (redacted, neither projected nor
	// shortened).
	raw := decodeProjResult(t, call(c, "raw", "wide_issues"))
	if len(raw.Content) != 1 || projNote(raw) != "" {
		t.Fatalf("passthrough result was projected: %d items", len(raw.Content))
	}
	full := raw.Content[0].Text
	if strings.Contains(full, secret) || !strings.Contains(full, "[SECRET_REDACTED]") {
		t.Fatal("the reference result is not redacted")
	}

	// github.* drop pack: ≥ 50% smaller, valid JSON, numbers exact.
	r := decodeProjResult(t, call(c, "big", "wide_issues"))
	text := r.Content[0].Text
	var issues []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &issues); err != nil || len(issues) != 30 {
		t.Fatalf("projected JSON invalid (%v) or not 30 issues: %.300s", err, text)
	}
	if saving := 100 * (1 - float64(len(text))/float64(len(full))); saving < 50 {
		t.Fatalf("projection saved %.1f%%, want >= 50%%", saving)
	}
	if strings.Contains(text, "node_id") || strings.Contains(text, "_url") || strings.Contains(text, "reactions") {
		t.Fatalf("dropped fields remain: %.300s", text)
	}
	if string(issues[3]["id"]) != "9007199254740903" || string(issues[3]["number"]) != "4203" {
		t.Fatalf("numbers changed: id=%s number=%s", issues[3]["id"], issues[3]["number"])
	}
	if !strings.Contains(text, `"title":"Crash <4200> & retry"`) {
		t.Fatalf("HTML-escaped or altered title: %.300s", text)
	}
	note := projNote(r)
	if !strings.Contains(note, `rule "big.*"`) || !strings.Contains(note, "result_id=r_") {
		t.Fatalf("no projection note: %+v", r.Content)
	}
	id := govMarkerID(t, note)
	if links := projLinks(r); len(links) != 1 || links[0] != "leanproxy://results/"+id {
		t.Fatalf("resource links = %v", links)
	}
	if got := readAllPages(t, c, id); got != full {
		t.Fatalf("read_result of the projection id (%d bytes) differs from the full redacted result (%d bytes)", len(got), len(full))
	}
	rr := c.call("resources/read", map[string]string{"uri": "leanproxy://results/" + id})
	var contents struct {
		Contents []struct{ Text string } `json:"contents"`
	}
	if rr.Error != nil || json.Unmarshal(rr.Result, &contents) != nil || len(contents.Contents) != 1 || contents.Contents[0].Text != full {
		t.Fatalf("resources/read of the full result: %.300s", rr.raw)
	}

	// keep rule: only the listed field (the projected list is then
	// truncated to the budget: it may end with the omission object).
	k := decodeProjResult(t, call(c, "big", "big_json"))
	var kept []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(k.Content[0].Text), &kept); err != nil || len(kept) < 500 {
		t.Fatalf("keep projection: %v, %d items", err, len(kept))
	}
	for _, it := range kept {
		if _, omitted := it["__leanproxy_omitted"]; omitted {
			continue
		}
		if len(it) != 1 || it["number"] == nil {
			t.Fatalf("keep projection kept %v", it)
		}
	}

	// structuredContent: projected without an outputSchema (keep rule),
	// untouched with one (only the text is projected, by the drop rule).
	s := decodeProjResult(t, call(c, "big", "wide_structured"))
	var sc struct {
		Issues []map[string]json.RawMessage `json:"issues"`
	}
	if err := json.Unmarshal(s.StructuredContent, &sc); err != nil || len(sc.Issues) != 20 || len(sc.Issues[0]) != 2 {
		t.Fatalf("structuredContent not projected: %v %.300s", err, s.StructuredContent)
	}
	st := decodeProjResult(t, call(c, "big", "wide_strict"))
	if !strings.Contains(string(st.StructuredContent), "node_id") || strings.Contains(st.Content[0].Text, "node_id") {
		t.Fatalf("strict outputSchema: structuredContent must be untouched and the text projected")
	}

	// Non-JSON text and error results are untouched by projection.
	small := decodeProjResult(t, call(c, "big", "small"))
	if len(small.Content) != 1 || small.Content[0].Text != "just a few words" {
		t.Fatalf("non-JSON text changed: %+v", small.Content)
	}
	e := decodeProjResult(t, call(c, "big", "big_error"))
	if !e.IsError || len(e.Content) != 1 || projNote(e) != "" {
		t.Fatalf("error result modified")
	}

	// Projection then truncation: within budget, both ids readable.
	h := call(c, "big", "wide_huge")
	if got := (len(h.raw) + 3) / 4; got > governorBudget {
		t.Fatalf("projected+truncated response line is %d tokens, want <= %d", got, governorBudget)
	}
	hr := decodeProjResult(t, h)
	var huge []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(hr.Content[0].Text), &huge); err != nil {
		t.Fatalf("projected+truncated JSON invalid: %v", err)
	}
	if _, ok := huge[len(huge)-1]["__leanproxy_omitted"]; !ok || projNote(hr) == "" || len(projLinks(hr)) != 2 {
		t.Fatalf("want a truncation marker, a projection note and 2 links: %+v", hr.Content[1:])
	}

	if !withFields {
		return
	}
	// fields: applied, and not forwarded upstream.
	f := decodeProjResult(t, c.call("tools/call", map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{
		"server": "big", "tool": "wide_issues", "arguments": map[string]interface{}{"state": "open"}, "fields": []string{"[].number", "[].labels[].name"}}}))
	var fi []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(f.Content[0].Text), &fi); err != nil || len(fi) != 30 || len(fi[0]) != 2 || string(fi[0]["labels"]) != `[{"name":"bug"}]` {
		t.Fatalf("fields not applied: %v %.300s", err, f.Content[0].Text)
	}
	if !strings.Contains(projNote(f), "your fields argument") {
		t.Fatalf("fields note: %q", projNote(f))
	}
	// echo_args answers with the params it received; fields keeps only
	// "name", so the full copy (read_result) is what the upstream saw.
	echo := decodeProjResult(t, c.call("tools/call", map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{
		"server": "big", "tool": "echo_args", "arguments": map[string]interface{}{"q": 1}, "fields": []string{"name"}}}))
	if echo.Content[0].Text != `{"name":"echo_args"}` {
		t.Fatalf("fields on echo_args: %q", echo.Content[0].Text)
	}
	if seen := readAllPages(t, c, govMarkerID(t, projNote(echo))); strings.Contains(seen, "fields") || !strings.Contains(seen, `"q":1`) {
		t.Fatalf("the upstream saw %s", seen)
	}
	// A bad fields argument leaves the result as the rules make it.
	b := decodeProjResult(t, c.call("tools/call", map[string]interface{}{"name": "invoke_tool", "arguments": map[string]interface{}{
		"server": "raw", "tool": "wide_issues", "fields": []string{"items[0]"}}}))
	if len(b.Content) != 1 || b.Content[0].Text != full {
		t.Fatal("a bad fields argument must leave the result unchanged")
	}
}

func TestResponseProjection_Stdio(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, projectionOn))
	protoInitialize(t, c, "2025-06-18")
	projectionScenario(t, c, govViaInvoke, true)

	// tools/list documents fields on invoke_tool while the governor is on.
	tl := c.call("tools/list", nil)
	if !strings.Contains(tl.raw, `"fields":{"type":"array"`) {
		t.Fatalf("invoke_tool does not declare fields: %.600s", tl.raw)
	}
}

func TestResponseProjection_StdioLegacyProtocol(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, projectionOn))
	protoInitialize(t, c, "2024-11-05")
	r := decodeProjResult(t, govViaInvoke(c, "big", "wide_issues"))
	if len(r.Content) != 2 || projNote(r) == "" || len(projLinks(r)) != 0 {
		t.Fatalf("2024-11-05 must get the note and no resource_link: %+v", r.Content)
	}
}

func TestResponseProjection_DefaultPack(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, "response:\n  enabled: true\n  max_tokens: 0\n  default_projections: true\n"))
	protoInitialize(t, c, "2025-06-18")
	r := decodeProjResult(t, govViaInvoke(c, "big", "wide_issues"))
	text := r.Content[0].Text
	if !strings.Contains(projNote(r), "the default projections") || strings.Contains(text, "node_id") || strings.Contains(text, "html_url") || !strings.Contains(text, `"url":`) {
		t.Fatalf("default pack: %.400s", text)
	}
}

func TestResponseProjection_StreamableHTTP(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	p := startHTTPProxy(t, proxyBin, writeGovernorConfig(t, bigBin, projectionOn), nil)
	_, c := newHTTPProto(t, p.url)
	_, other := newHTTPProto(t, p.url)
	protoInitialize(t, c, "2025-06-18")
	protoInitialize(t, other, "2025-06-18")
	projectionScenario(t, c, govViaInvoke, true)

	// Another session cannot read the full copy.
	r := decodeProjResult(t, govViaInvoke(c, "big", "wide_issues"))
	id := govMarkerID(t, projNote(r))
	x := decodeToolResult(t, other.call("tools/call", map[string]interface{}{"name": "read_result", "arguments": map[string]interface{}{"result_id": id}}))
	if !x.IsError {
		t.Fatalf("another session read the full result: %+v", x)
	}
}

func TestResponseProjection_Serve(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, projectionOn)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	logs := &syncBuffer{}
	cmd := exec.Command(proxyBin, "serve", "--config", cfg, "--listen", addr, "--metrics-bind", "off", "--dashboard-bind", "off")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
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
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Write(serveAuthLine()); err != nil {
		t.Fatal(err)
	}
	rd := bufio.NewReaderSize(conn, 1<<20)
	c := &protoClient{t: t,
		send: func(b []byte) error { _, err := conn.Write(b); return err },
		next: func(d time.Duration) (string, bool) {
			_ = conn.SetReadDeadline(time.Now().Add(d))
			line, err := rd.ReadString('\n')
			if err != nil {
				return "", false
			}
			return strings.TrimRight(line, "\n"), true
		},
	}
	protoInitialize(t, c, "2025-06-18")
	// serve's gateway has no real invoke_tool (it answers a forwarding
	// stub, before and after this change), so fields is not exercised
	// there; configured projections behave the same.
	projectionScenario(t, c, govViaNamespaced, false)
}
