package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"time"
	"unicode/utf8"
)

// DefaultCallLogSize is how many tool calls the call log keeps.
const DefaultCallLogSize = 500

// maxCallErrorLen bounds the error text kept per call.
const maxCallErrorLen = 240

// Call statuses.
const (
	CallOK        = "ok"
	CallError     = "error"      // a JSON-RPC or transport error
	CallToolError = "tool_error" // a result with isError: true
)

// CallRecord is one tool call as the proxy saw it. It never holds the
// call's arguments or its result: only who called what, how long it took
// and how it ended.
type CallRecord struct {
	Time       time.Time `json:"time"`
	SessionID  string    `json:"session_id,omitempty"`
	Client     string    `json:"client,omitempty"`
	Server     string    `json:"server"`
	Tool       string    `json:"tool"`
	DurationMs float64   `json:"duration_ms"`
	Status     string    `json:"status"`
	// Error is the (redacted, truncated) error message of a failed call.
	Error         string `json:"error,omitempty"`
	ResponseBytes int    `json:"response_bytes"`
}

// CallLog is a fixed-size ring of the most recent tool calls, for the admin
// views. It is in memory only and safe for concurrent use.
type CallLog struct {
	mu    sync.Mutex
	buf   []CallRecord
	next  int
	full  bool
	total int64
}

// NewCallLog returns a log keeping the last size calls (DefaultCallLogSize
// when size is not positive).
func NewCallLog(size int) *CallLog {
	if size <= 0 {
		size = DefaultCallLogSize
	}
	return &CallLog{buf: make([]CallRecord, size)}
}

// Add records one call.
func (l *CallLog) Add(rec CallRecord) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.buf[l.next] = rec
	l.next++
	if l.next == len(l.buf) {
		l.next, l.full = 0, true
	}
	l.total++
	l.mu.Unlock()
}

// Total is how many calls were recorded since the proxy started.
func (l *CallLog) Total() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

// Recent returns up to n calls, newest first. server, when not empty,
// keeps only that server's calls.
func (l *CallLog) Recent(n int, server string) []CallRecord {
	if l == nil || n <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	count := l.next
	if l.full {
		count = len(l.buf)
	}
	out := make([]CallRecord, 0, min(n, count))
	for i := 1; i <= count && len(out) < n; i++ {
		rec := l.buf[(l.next-i+len(l.buf))%len(l.buf)]
		if server != "" && rec.Server != server {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// truncateText cuts s to at most max bytes on a rune boundary.
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// proxyServerLabel is the "server" of the calls LeanProxy answers itself
// (search_tools, list_tools, ...).
const proxyServerLabel = "leanproxy"

// CallLogMiddleware records every tools/call in log. Install it right
// after the exposure middleware (so names are already "server.tool") and
// outside the firewall (so the error text it keeps is already redacted).
func (h *Handler) CallLogMiddleware(log *CallLog) Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if log == nil || req == nil || req.IsNotification() {
				return next(ctx, req)
			}
			server, tool, _, ok := extractToolCall(req)
			if !ok {
				// list_servers / list_tools: still tool calls worth showing.
				if req.Method != MethodToolsCall {
					return next(ctx, req)
				}
				var p ToolsCallParams
				if json.Unmarshal(req.Params, &p) != nil || p.Name == "" {
					return next(ctx, req)
				}
				tool = p.Name
			}
			if server == "" {
				if s, t, err := SplitToolName(tool, h.pool.ListServers()); err == nil {
					server, tool = s, t
				} else {
					server = proxyServerLabel
				}
			}

			start := time.Now()
			resp, err := next(ctx, req)

			rec := CallRecord{
				Time:       start,
				Server:     server,
				Tool:       tool,
				DurationMs: float64(time.Since(start).Microseconds()) / 1000,
				Status:     CallOK,
			}
			if s := ClientSessionFrom(ctx); s != nil {
				rec.SessionID = s.id
				rec.Client = s.ClientInfo().Name
				s.calls.Add(1)
				s.lastActive.Store(start.UnixNano())
			}
			switch {
			case err != nil:
				rec.Status, rec.Error = CallError, truncateText(err.Error(), maxCallErrorLen)
			case resp != nil && resp.Error != nil:
				rec.Status, rec.Error = CallError, truncateText(resp.Error.Message, maxCallErrorLen)
			case resp != nil:
				rec.ResponseBytes = len(resp.Result)
				if toolResultIsError(resp.Result) {
					rec.Status = CallToolError
				}
			}
			log.Add(rec)
			return resp, err
		}
	}
}

// toolResultIsError reports whether a tools/call result carries
// isError: true.
func toolResultIsError(result json.RawMessage) bool {
	if len(result) == 0 {
		return false
	}
	var r struct {
		IsError bool `json:"isError"`
	}
	return json.Unmarshal(result, &r) == nil && r.IsError
}
