package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// End-to-end coverage for issue #321: in-session dedup of repeated results,
// and optional local-LLM summarization, on the real binary, through
// `server run --stdio` and `server run --http`, plus `serve`.

const governorDedupOn = `response:
  enabled: true
  max_tokens: 4000
  dedup: on
  tools:
    - match: "raw.*"
      passthrough: true
`

// fakeOllamaGenerate starts an httptest server answering /api/generate
// like the real sidecar client expects.
func fakeOllamaGenerate(t *testing.T, respond func(prompt string) (string, time.Duration)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text, delay := respond(body.Prompt)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "test", "response": text, "done": true})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func governorSummarizeConfig(ollamaURL string, timeout string) string {
	if timeout == "" {
		timeout = "5s"
	}
	return fmt.Sprintf(`response:
  enabled: true
  max_tokens: 4000
  tools:
    - match: "raw.*"
      passthrough: true
  summarize:
    enabled: true
    url: %q
    tools: ["big.*"]
    threshold_tokens: 100
    max_summary_tokens: 200
    timeout: %q
`, ollamaURL, timeout)
}

func TestResponseGovernorDedup_Stdio(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, governorDedupOn))
	protoInitialize(t, c, "2025-06-18")

	r1 := decodeToolResult(t, govViaInvoke(c, "big", "big_text"))
	if len(r1.Content) < 1 || strings.Contains(r1.Content[0].Text, "identical to the result of") {
		t.Fatalf("first call must not be deduped: %+v", r1)
	}

	r2 := decodeToolResult(t, govViaInvoke(c, "big", "big_text"))
	if len(r2.Content) != 1 || !strings.Contains(r2.Content[0].Text, "identical to the result of big.big_text returned earlier") {
		t.Fatalf("second, identical call must be deduped: %+v", r2)
	}
	id := govMarkerID(t, r2.Content[0].Text)
	got := readAllPages(t, c, id)
	if len(got) < 190_000 {
		t.Fatalf("read_result did not return the full deduped content: %d bytes", len(got))
	}
}

func TestResponseGovernorDedup_CrossSessionHTTP_NotDeduped(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	p := startHTTPProxy(t, proxyBin, writeGovernorConfig(t, bigBin, governorDedupOn), nil)
	_, a := newHTTPProto(t, p.url)
	_, b := newHTTPProto(t, p.url)
	protoInitialize(t, a, "2025-06-18")
	protoInitialize(t, b, "2025-06-18")

	ra := decodeToolResult(t, govViaInvoke(a, "big", "big_text"))
	if strings.Contains(ra.Content[0].Text, "identical to the result of") {
		t.Fatalf("session a's first call must not be deduped: %+v", ra)
	}
	// Session b never saw this content: it must get the full result, not a
	// dedup marker, even though the upstream returns byte-identical
	// content. Two calls on b confirm dedup works within b's own session
	// but never leaks across from a.
	rb1 := decodeToolResult(t, govViaInvoke(b, "big", "big_text"))
	if len(rb1.Content) < 1 || strings.Contains(rb1.Content[0].Text, "identical to the result of") {
		t.Fatalf("session b must not be told about session a's result: %+v", rb1)
	}
	rb2 := decodeToolResult(t, govViaInvoke(b, "big", "big_text"))
	if !strings.Contains(rb2.Content[0].Text, "identical to the result of") {
		t.Fatalf("session b's own second call must be deduped against its own first: %+v", rb2)
	}
}

func TestResponseGovernorSummarize_Stdio(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	ollama := fakeOllamaGenerate(t, func(prompt string) (string, time.Duration) {
		if !strings.Contains(prompt, "Summarize the following tool result") {
			t.Errorf("unexpected prompt: %.200s", prompt)
		}
		return "Repeats a filler line; one line carried a redacted token.", 0
	})
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, governorSummarizeConfig(ollama.URL, "")))
	protoInitialize(t, c, "2025-06-18")

	r := decodeToolResult(t, govViaInvoke(c, "big", "big_text"))
	if len(r.Content) < 1 {
		t.Fatalf("empty result: %+v", r)
	}
	text := r.Content[0].Text
	if !strings.Contains(text, "[LeanProxy: summary of big.big_text") {
		t.Fatalf("no summary marker: %.300q", text)
	}
	if !strings.Contains(text, "Repeats a filler line") {
		t.Fatalf("summary text missing: %.300q", text)
	}
	if strings.Contains(text, "line 00001:") {
		t.Fatal("the raw content must not still be present once summarized")
	}
	id := govMarkerID(t, text)
	full := readAllPages(t, c, id)
	if !strings.HasPrefix(full, "line 00001:") {
		t.Fatalf("read_result did not return the full pre-summary content: %.200q", full)
	}
}

func TestResponseGovernorSummarize_TimeoutFallsBackToTruncation(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	ollama := fakeOllamaGenerate(t, func(prompt string) (string, time.Duration) {
		return "too slow", 2 * time.Second
	})
	c := startProtoStdio(t, proxyBin, writeGovernorConfig(t, bigBin, governorSummarizeConfig(ollama.URL, "100ms")))
	protoInitialize(t, c, "2025-06-18")

	r := decodeToolResult(t, govViaInvoke(c, "big", "big_text"))
	text := r.Content[0].Text
	if strings.Contains(text, "[LeanProxy: summary of") {
		t.Fatalf("summarization must have timed out: %.200q", text)
	}
	if !strings.Contains(text, "tokens omitted — call read_result with id=") {
		t.Fatalf("expected the ordinary truncation fallback: %.300q", text)
	}
}

func TestResponseGovernorDedup_Serve(t *testing.T) {
	proxyBin, bigBin := buildGovernorBinaries(t)
	cfg := writeGovernorConfig(t, bigBin, governorDedupOn)
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
	r := bufio.NewReaderSize(conn, 1<<20)
	c := &protoClient{t: t,
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
	protoInitialize(t, c, "2025-06-18")

	r1 := decodeToolResult(t, govViaNamespaced(c, "big", "big_text"))
	if len(r1.Content) < 1 || strings.Contains(r1.Content[0].Text, "identical to the result of") {
		t.Fatalf("first call: %+v", r1)
	}
	r2 := decodeToolResult(t, govViaNamespaced(c, "big", "big_text"))
	if !strings.Contains(r2.Content[0].Text, "identical to the result of") {
		t.Fatalf("dedup must behave the same under serve: %+v", r2)
	}
}
