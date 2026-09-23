package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

// benchGuard is an enabled guard with the default policy bands.
func benchGuard(b *testing.B) *InjectionGuard {
	b.Helper()
	g := &InjectionGuard{}
	g.Set(injection.NewClassifier(), injection.NewDispatcherWithQuarantineDir(nil, b.TempDir()))
	return g
}

func benchRequest() *Request {
	return &Request{JSONRPC: "2.0", Method: MethodToolsCall, ID: 1, Params: json.RawMessage(
		`{"name":"github_search_code","arguments":{"query":"parseConfig language:go","repo":"acme/api","per_page":30,"page":1}}`)}
}

// benchPage is a benign README-like tool result of about size bytes.
func benchPage(size int) json.RawMessage {
	para := "The proxy forwards each request to the upstream server and relays the response. " +
		"Configure the server list in leanproxy.yaml, then run `make test` to check the build. " +
		"See docs/configuration.md for every option; issues and pull requests are welcome.\n"
	text, _ := json.Marshal(strings.Repeat(para, size/len(para)+1))
	return json.RawMessage(`{"content":[{"type":"text","text":` + string(text) + `}],"isError":false}`)
}

func BenchmarkInjectionGuard_Request(b *testing.B) {
	g := benchGuard(b)
	req := benchRequest()
	params := req.Params
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.Params = params
		if resp := g.Check(req); resp != nil {
			b.Fatal("benign request blocked")
		}
	}
}

// BenchmarkInjectionGuard_RoundTrip runs a benign tools/call and a benign
// result of 2 KiB and 64 KiB through the middleware (the request check and,
// since #315, the response classification).
func BenchmarkInjectionGuard_RoundTrip(b *testing.B) {
	for _, size := range []int{2 << 10, 64 << 10} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			g := benchGuard(b)
			page := benchPage(size)
			next := func(ctx context.Context, req *Request) (*Response, error) {
				return &Response{JSONRPC: "2.0", ID: req.ID, Result: page}, nil
			}
			h := g.Middleware()(next)
			req := benchRequest()
			params := req.Params
			b.SetBytes(int64(len(page)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req.Params = params
				if _, err := h(context.Background(), req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
