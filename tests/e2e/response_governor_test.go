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

// End-to-end coverage for issue #319 (response token governor): the real
// binary, with a fake upstream returning huge results, through all three
// front ends (`server run --stdio`, `server run --http`, `serve`):
//
//   - a 200 KB text result comes back within max_tokens with head, tail,
//     marker and (2025-06-18) a resource_link; read_result pages through
//     the whole redacted original; resources/read serves it too;
//   - a 1000-element JSON array stays valid JSON with the omission object,
//     and read_result's jsonpath and grep work;
//   - error and image results are untouched; tools/list lists read_result;
//   - another session's result_id (and an unknown one) is refused;
//   - with spill.disk, the spill files are 0600 and removed on shutdown;
//   - off by default.

const governorBudget = 4000

func buildGovernorBinaries(t *testing.T) (proxyBin, bigBin string) {
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
	bigBin = filepath.Join(dir, "bigmcp")
	for _, b := range []struct{ out, pkg string }{
		{proxyBin, "."},
		{bigBin, "./tests/e2e/testdata/bigmcp"},
	} {
		cmd := exec.Command(goBin, "build", "-o", b.out, b.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", b.pkg, err, out)
		}
	}
	return proxyBin, bigBin
}

// writeGovernorConfig configures "big" (governed) and "raw" (the same
// upstream, passthrough: the reference for the full redacted result).
// response is the response: block ("" for none).
func writeGovernorConfig(t *testing.T, bigBin, response string) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "leanproxy.yaml")
	writeFile(t, cfg, fmt.Sprintf(`version: "1.0"
servers:
  - name: big
    transport: stdio
    enabled: true
    stdio:
      command: %[1]q
  - name: raw
    transport: stdio
    enabled: true
    stdio:
      command: %[1]q
%[2]s`, bigBin, response))
	return cfg
}

const governorOn = `response:
  enabled: true
  max_tokens: 4000
  tools:
    - match: "raw.*"
      passthrough: true
`

type govContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type govToolResult struct {
	Content []govContent `json:"content"`
	IsError bool         `json:"isError"`
}

func decodeToolResult(t *testing.T, m protoMsg) govToolResult {
	t.Helper()
	if m.Error != nil {
		t.Fatalf("JSON-RPC error: %s", m.raw)
	}
	var r govToolResult
	if err := json.Unmarshal(m.Result, &r); err != nil {
		t.Fatalf("invalid tool result: %v: %.300s", err, m.raw)
	}
	return r
}

// governorFrontEnd calls an upstream tool through one front end.
type governorFrontEnd func(c *protoClient, server, tool string) protoMsg

// stdio and HTTP: the invoke_tool gateway tool.
func govViaInvoke(c *protoClient, server, tool string) protoMsg {
	return c.call("tools/call", viaInvokeTool(server, tool))
}

// serve: the namespaced tool, retried until the router knows it.
func govViaNamespaced(c *protoClient, server, tool string) protoMsg {
	deadline := time.Now().Add(20 * time.Second)
	for {
		m := c.call("tools/call", viaNamespacedTool(server, tool))
		if m.Error == nil || time.Now().After(deadline) {
			return m
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func govTokens(r govToolResult) int {
	n := 0
	for _, c := range r.Content {
		n += len(c.Text) + len(c.URI)
	}
	return (n + 3) / 4
}

func govMarkerID(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, "id=r_")
	if i < 0 {
		t.Fatalf("no result id in %.300q", text)
	}
	return text[i+3 : i+3+28]
}

// readAllPages pages a result from offset 0 with read_result.
func readAllPages(t *testing.T, c *protoClient, id string) string {
	t.Helper()
	var b strings.Builder
	offset := 0
	for pages := 0; pages < 200; pages++ {
		r := decodeToolResult(t, c.call("tools/call", map[string]interface{}{"name": "read_result", "arguments": map[string]interface{}{"result_id": id, "offset": offset}}))
		if r.IsError || len(r.Content) != 2 {
			t.Fatalf("read_result page at %d: %+v", offset, r)
		}
		b.WriteString(r.Content[0].Text)
		nav := r.Content[1].Text
		if strings.Contains(nav, "end of result") {
			return b.String()
		}
		i := strings.Index(nav, "offset=")
		if i < 0 {
			t.Fatalf("no next offset in %q", nav)
		}
		if _, err := fmt.Sscanf(nav[i:], "offset=%d]", &offset); err != nil {
			t.Fatalf("parse %q: %v", nav, err)
		}
	}
	t.Fatal("read_result never reached the end")
	return ""
}

// governorScenario runs the acceptance criteria on client c (a session that
// negotiated 2025-06-18) and checks isolation against other (a second
// session on the same proxy, or nil for stdio, which has one session per
// process).
func governorScenario(t *testing.T, c, other *protoClient, call governorFrontEnd, listsTools bool) {
	t.Helper()
	secret := "ghp_" + strings.Repeat("a1B2", 9)

	// tools/list advertises read_result. (serve's gateway has no tools/list
	// of its own: it answers method not found, before and after #319.)
	if listsTools {
		if tl := c.call("tools/list", nil); !strings.Contains(tl.raw, `"name":"read_result"`) {
			t.Fatalf("tools/list lacks read_result: %.500s", tl.raw)
		}
	}

	// Reference: the same result, passthrough (redacted, not shortened).
	raw := decodeToolResult(t, call(c, "raw", "big_text"))
	if len(raw.Content) != 1 || len(raw.Content[0].Text) < 190_000 {
		t.Fatalf("passthrough result was shortened: %d items", len(raw.Content))
	}
	full := raw.Content[0].Text
	if strings.Contains(full, secret) || !strings.Contains(full, "[SECRET_REDACTED]") {
		t.Fatal("the reference result is not redacted")
	}

	// 200 KB text: head, tail, marker, resource_link, within budget.
	r := decodeToolResult(t, call(c, "big", "big_text"))
	if got := govTokens(r); got > governorBudget {
		t.Fatalf("governed result is %d tokens, want <= %d", got, governorBudget)
	}
	if len(r.Content) != 2 || r.Content[1].Type != "resource_link" {
		t.Fatalf("want text + resource_link, got %+v", r.Content)
	}
	text := r.Content[0].Text
	if !strings.HasPrefix(text, "line 00001:") || !strings.HasSuffix(text, "line 03500: the quick brown fox jumps over the lazy dog\n") || !strings.Contains(text, "tokens omitted — call read_result with id=") {
		t.Fatalf("no head/tail/marker: %.200q ... %.200q", text, text[len(text)-200:])
	}
	id := govMarkerID(t, text)
	if r.Content[1].URI != "leanproxy://results/"+id {
		t.Fatalf("resource_link uri = %q", r.Content[1].URI)
	}

	// read_result pages through the whole redacted original.
	if got := readAllPages(t, c, id); got != full {
		t.Fatalf("concatenated pages (%d bytes) differ from the redacted original (%d bytes)", len(got), len(full))
	}
	// resources/read serves it natively.
	rr := c.call("resources/read", map[string]string{"uri": "leanproxy://results/" + id})
	var contents struct {
		Contents []struct{ Text string } `json:"contents"`
	}
	if rr.Error != nil || json.Unmarshal(rr.Result, &contents) != nil || len(contents.Contents) != 1 || contents.Contents[0].Text != full {
		t.Fatalf("resources/read of the spilled result: %.300s", rr.raw)
	}

	// grep: matching lines with line numbers, redacted.
	g := decodeToolResult(t, c.call("tools/call", map[string]interface{}{"name": "read_result", "arguments": map[string]interface{}{"result_id": id, "grep": "token="}}))
	if g.IsError || !strings.Contains(g.Content[0].Text, "1700:line 01700: token=[SECRET_REDACTED]") || strings.Contains(g.Content[0].Text, secret) {
		t.Fatalf("grep: %+v", g)
	}

	// 1000-element JSON array: valid JSON with the omission object.
	j := decodeToolResult(t, call(c, "big", "big_json"))
	if govTokens(j) > governorBudget {
		t.Fatalf("governed JSON is %d tokens", govTokens(j))
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(j.Content[0].Text), &items); err != nil {
		t.Fatalf("governed JSON is invalid: %v", err)
	}
	var om struct {
		Items    int    `json:"items"`
		ResultID string `json:"result_id"`
	}
	if err := json.Unmarshal(items[len(items)-1]["__leanproxy_omitted"], &om); err != nil || om.Items != 1000-(len(items)-1) {
		t.Fatalf("omission object = %+v (%v)", om, err)
	}
	jp := decodeToolResult(t, c.call("tools/call", map[string]interface{}{"name": "read_result", "arguments": map[string]interface{}{"result_id": om.ResultID, "jsonpath": "$[500:510]"}}))
	var page []struct {
		Number int `json:"number"`
	}
	if jp.IsError || json.Unmarshal([]byte(jp.Content[0].Text), &page) != nil || len(page) != 10 || page[0].Number != 500 || page[9].Number != 509 {
		t.Fatalf("jsonpath $[500:510]: %+v", jp)
	}

	// Error and image results are untouched.
	e := decodeToolResult(t, call(c, "big", "big_error"))
	if !e.IsError || len(e.Content) != 1 || len(e.Content[0].Text) < 190_000 {
		t.Fatalf("the error result was modified (%d items)", len(e.Content))
	}
	img := decodeToolResult(t, call(c, "big", "image"))
	if len(img.Content) != 2 || img.Content[0].Type != "image" || len(img.Content[0].Data) != 300_000 || img.Content[1].Text != "a screenshot" {
		t.Fatalf("the image result was modified")
	}

	// Unknown id: a clear error.
	u := decodeToolResult(t, c.call("tools/call", map[string]interface{}{"name": "read_result", "arguments": map[string]interface{}{"result_id": "r_aaaaaaaaaaaaaaaaaaaaaaaaaa"}}))
	if !u.IsError || !strings.Contains(u.Content[0].Text, "not found") {
		t.Fatalf("unknown id: %+v", u)
	}

	// Another session cannot read this session's result.
	if other != nil {
		x := decodeToolResult(t, other.call("tools/call", map[string]interface{}{"name": "read_result", "arguments": map[string]interface{}{"result_id": id}}))
		if !x.IsError || strings.Contains(x.Content[0].Text, "line 0") {
			t.Fatalf("another session read the result: %+v", x)
		}
		xr := other.call("resources/read", map[string]string{"uri": "leanproxy://results/" + id})
		if xr.Error == nil || xr.Error.Code != -32002 {
			t.Fatalf("another session read the resource: %.300s", xr.raw)
		}
		// Its own result is still readable by its owner.
		if got := readAllPages(t, c, id); got != full {
			t.Fatal("owner lost access")
		}
	}
}

func TestResponseGovernor_Stdio(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, governorOn))
	res := protoInitialize(t, c, "2025-06-18")
	if !strings.Contains(string(res["capabilities"]), `"resources"`) {
		t.Fatalf("initialize does not advertise resources: %s", res["capabilities"])
	}
	governorScenario(t, c, nil, govViaInvoke, true)
}

func TestResponseGovernor_StdioLegacyProtocol(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, governorOn))
	protoInitialize(t, c, "2024-11-05")
	r := decodeToolResult(t, govViaInvoke(c, "big", "big_text"))
	if len(r.Content) != 1 || !strings.Contains(r.Content[0].Text, "call read_result with id=") {
		t.Fatalf("2024-11-05 must get the marker and no resource_link: %d items", len(r.Content))
	}
	if tl := c.call("tools/list", nil); strings.Contains(tl.raw, `"annotations"`) {
		t.Fatalf("2024-11-05 must not get annotations: %.300s", tl.raw)
	}
}

func TestResponseGovernor_OffByDefault(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, ""))
	protoInitialize(t, c, "2025-06-18")
	r := decodeToolResult(t, govViaInvoke(c, "big", "big_text"))
	if len(r.Content) != 1 || len(r.Content[0].Text) < 190_000 {
		t.Fatal("the governor must be off by default")
	}
	if tl := c.call("tools/list", nil); strings.Contains(tl.raw, "read_result") {
		t.Fatal("read_result listed while the governor is off")
	}
}

func TestResponseGovernor_DiskSpillFilesAre0600AndRemoved(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	dir := filepath.Join(t.TempDir(), "results")
	cfg := writeGovernorConfig(t, bigBin, governorOn+"  spill:\n    disk: true\n    dir: "+dir+"\n")
	s := startStdioProxy(t, proxyBin, cfg)
	c := &protoClient{t: t,
		send: func(b []byte) error { _, err := s.stdin.Write(b); return err },
		next: func(d time.Duration) (string, bool) {
			select {
			case line, ok := <-s.lines:
				return strings.TrimRight(string(line), "\n"), ok
			case <-time.After(d):
				return "", false
			}
		},
	}
	protoInitialize(t, c, "2025-06-18")
	r := decodeToolResult(t, govViaInvoke(c, "big", "big_text"))
	id := govMarkerID(t, r.Content[0].Text)
	files, _ := filepath.Glob(filepath.Join(dir, "*", id))
	if len(files) != 1 {
		t.Fatalf("spill file for %s not found under %s", id, dir)
	}
	fi, err := os.Stat(files[0])
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("spill file mode %v (%v)", fi.Mode().Perm(), err)
	}
	if di, err := os.Stat(filepath.Dir(files[0])); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("spill dir mode %v (%v)", di.Mode().Perm(), err)
	}
	data, _ := os.ReadFile(files[0]) // #nosec G304 -- test-owned temp file
	if strings.Contains(string(data), "ghp_") || !strings.Contains(string(data), "[SECRET_REDACTED]") {
		t.Fatal("the spill file holds an unredacted result")
	}
	// EOF on stdin: the proxy shuts down and removes the spill directory.
	_ = s.stdin.Close()
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("proxy did not exit on EOF")
	}
	if _, err := os.Stat(filepath.Dir(files[0])); !os.IsNotExist(err) {
		t.Fatalf("spill directory survived shutdown: %v", err)
	}
}

func TestResponseGovernor_StreamableHTTP(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	p := startHTTPProxy(t, proxyBin, writeGovernorConfig(t, bigBin, governorOn), nil)
	_, c := newHTTPProto(t, p.url)
	_, other := newHTTPProto(t, p.url)
	protoInitialize(t, c, "2025-06-18")
	protoInitialize(t, other, "2025-06-18")
	governorScenario(t, c, other, govViaInvoke, true)
}

func TestResponseGovernor_Serve(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, governorOn)
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
	dial := func() *protoClient {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if _, err := conn.Write(serveAuthLine()); err != nil {
			t.Fatal(err)
		}
		r := bufio.NewReaderSize(conn, 1<<20)
		return &protoClient{t: t,
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
	c, other := dial(), dial()
	protoInitialize(t, c, "2025-06-18")
	protoInitialize(t, other, "2025-06-18")
	governorScenario(t, c, other, govViaNamespaced, false)
}
