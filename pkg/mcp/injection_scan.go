package mcp

import (
	"sync"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

// What the prompt-injection guard reads (issue #315).
//
// The guard classifies the decoded text of a message, never its raw JSON:
// every string literal in scope is decoded (JSON escapes resolved), strings
// that are themselves JSON documents (a tool's text content is often one)
// are opened up to maxNestedScanDepth levels, and the result is normalized
// by the classifier (injection.TextBuilder). Scope depends on the message:
//
//   - a request: every string of params, keys included;
//   - a tool result (tools/call, invoke_tool, serve's namespaced tool
//     methods): content[].text, content[].resource.text and every string
//     of structuredContent (keys included) — not _meta, annotations, URIs,
//     mime types or binary data;
//   - resources/read: contents[].text;
//   - prompts/get: messages[].content.text (and embedded resource text).
//
// Text beyond the configured cap (max_scan_bytes, 256 KiB by default) is
// sampled: the head and the tail are classified.

// scanKind is the kind of message being classified.
type scanKind int

const (
	scanNone scanKind = iota
	scanRequest
	scanTool
	scanResource
	scanPrompt
)

// maxNestedScanDepth is how many levels of JSON-inside-a-string are opened.
const maxNestedScanDepth = 3

// maxNestedScanBytes caps the decoded size of a string parsed as nested
// JSON; larger strings are classified as text.
const maxNestedScanBytes = 1 << 20

// segment is one in-scope string literal: its raw content bounds.
type segment struct {
	start, end int
	escaped    bool
	// routing marks request strings that select the target (tool name,
	// server, resource URI): classified, but never rewritten by redact.
	routing bool
}

func keyIs(e bouncer.JSONPathElem, k string) bool {
	return e.Index < 0 && string(e.Key) == k
}

// inScope reports whether string s of a message of kind k is classified.
func inScope(k scanKind, s bouncer.JSONString) bool {
	p := s.Path
	switch k {
	case scanRequest:
		return true
	case scanTool:
		if len(p) == 0 {
			return false
		}
		switch {
		case keyIs(p[0], "structuredContent"):
			return len(p) > 1 || !s.IsKey
		case keyIs(p[0], "content"):
			return !s.IsKey && isContentText(p[1:])
		}
	case scanResource:
		return !s.IsKey && len(p) == 3 && keyIs(p[0], "contents") && p[1].Index >= 0 && keyIs(p[2], "text")
	case scanPrompt:
		if s.IsKey || len(p) < 4 || !keyIs(p[0], "messages") || p[1].Index < 0 || !keyIs(p[2], "content") {
			return false
		}
		rest := p[3:]
		if rest[0].Index >= 0 {
			return isContentText(rest)
		}
		return (len(rest) == 1 && keyIs(rest[0], "text")) ||
			(len(rest) == 2 && keyIs(rest[0], "resource") && keyIs(rest[1], "text"))
	}
	return false
}

// isContentText matches [i].text and [i].resource.text under a content
// array.
func isContentText(p []bouncer.JSONPathElem) bool {
	switch {
	case len(p) == 2:
		return p[0].Index >= 0 && keyIs(p[1], "text")
	case len(p) == 3:
		return p[0].Index >= 0 && keyIs(p[1], "resource") && keyIs(p[2], "text")
	}
	return false
}

// requestRouting reports whether request string s selects the target of the
// call (so the redact action leaves it alone).
func requestRouting(invokeEnvelope bool, s bouncer.JSONString) bool {
	if s.IsKey {
		return false
	}
	p := s.Path
	if len(p) == 1 {
		switch string(p[0].Key) {
		case "name", "uri", "server", "tool", "server_name", "tool_name":
			return p[0].Index < 0
		}
	}
	return invokeEnvelope && len(p) == 2 && keyIs(p[0], "arguments") && (keyIs(p[1], "server") || keyIs(p[1], "tool"))
}

// collectSegments returns the in-scope strings of data. ok is false when
// data is not valid JSON.
func collectSegments(data []byte, k scanKind, invokeEnvelope bool) (segs []segment, ok bool) {
	ok = bouncer.WalkJSONStrings(data, func(s bouncer.JSONString) {
		if !inScope(k, s) {
			return
		}
		segs = append(segs, segment{
			start: s.Start, end: s.End, escaped: s.Escaped,
			routing: k == scanRequest && requestRouting(invokeEnvelope, s),
		})
	})
	return segs, ok
}

// decodeSegment returns the decoded value of data[start:end] (a raw string
// literal content), reusing buf.
func decodeSegment(buf *[]byte, raw []byte, escaped bool) []byte {
	if !escaped {
		return raw
	}
	*buf = bouncer.AppendDecodedJSONString((*buf)[:0], raw)
	return *buf
}

// looksLikeJSON reports whether b starts, after whitespace, with { or [.
func looksLikeJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

// addText adds decoded text to tb, opening nested JSON documents.
func addText(tb *injection.TextBuilder, dec []byte, depth int) {
	if depth < maxNestedScanDepth && len(dec) <= maxNestedScanBytes && looksLikeJSON(dec) {
		var inner []segment
		if bouncer.WalkJSONStrings(dec, func(s bouncer.JSONString) {
			inner = append(inner, segment{start: s.Start, end: s.End, escaped: s.Escaped})
		}) {
			var buf []byte
			for _, s := range inner {
				addText(tb, decodeSegment(&buf, dec[s.start:s.end], s.escaped), depth+1)
			}
			return
		}
	}
	tb.Add(dec)
}

// decodeBufPool recycles the buffers strings are decoded into.
var decodeBufPool = sync.Pool{New: func() interface{} { b := make([]byte, 0, 4096); return &b }}

// buildScanText adds the text of segs to tb, sampling the head and the
// tail when the segments hold more than max raw bytes.
func buildScanText(tb *injection.TextBuilder, data []byte, segs []segment, max int) {
	total := 0
	for _, s := range segs {
		total += s.end - s.start
	}
	bp := decodeBufPool.Get().(*[]byte)
	defer func() {
		if cap(*bp) <= 1<<20 {
			decodeBufPool.Put(bp)
		}
	}()
	buf := (*bp)[:0]
	defer func() { *bp = buf[:0] }()
	if total <= max {
		for _, s := range segs {
			addText(tb, decodeSegment(&buf, data[s.start:s.end], s.escaped), 0)
		}
		return
	}

	// Head: whole segments while they fit, then a prefix of the next one.
	head := max / 2
	used, i, headCut := 0, 0, -1
	for ; i < len(segs) && used < head; i++ {
		s := segs[i]
		n := s.end - s.start
		if used+n <= head {
			addText(tb, decodeSegment(&buf, data[s.start:s.end], s.escaped), 0)
			used += n
			continue
		}
		headCut = safeCut(data, s.start, s.start+head-used)
		tb.Add(decodeSegment(&buf, data[s.start:headCut], s.escaped))
		break
	}

	// Tail: whole segments from the end while they fit, then a suffix.
	tail := max - head
	used = 0
	j := len(segs) - 1
	var parts [][2]int
	for ; j >= i && used < tail; j-- {
		s := segs[j]
		lo := s.start
		if j == i && headCut >= 0 {
			lo = headCut
		}
		n := s.end - lo
		if n <= 0 {
			break
		}
		if used+n <= tail {
			parts = append(parts, [2]int{lo, s.end})
			used += n
			continue
		}
		start := safeStart(data, lo, s.end, s.end-(tail-used))
		if start < s.end {
			parts = append(parts, [2]int{start, s.end})
		}
		break
	}
	for k := len(parts) - 1; k >= 0; k-- {
		p := parts[k]
		buf = bouncer.AppendDecodedJSONString(buf[:0], data[p[0]:p[1]])
		tb.Add(buf)
	}
}

// safeCut returns a cut position <= cut such that data[start:cut] does not
// end inside an escape sequence.
func safeCut(data []byte, start, cut int) int {
	lo := cut - 6
	if lo < start {
		lo = start
	}
	for p := cut - 1; p >= lo; p-- {
		if data[p] != '\\' {
			continue
		}
		q := p
		for q > start && data[q-1] == '\\' {
			q--
		}
		if (p-q+1)%2 == 0 {
			return cut // the run ends with a complete "\\" pair
		}
		n := 2
		if p+1 < len(data) && data[p+1] == 'u' {
			n = 6
		}
		if p+n > cut {
			return p
		}
		return cut
	}
	return cut
}

// safeStart returns a start position >= from such that data[pos:end]
// begins at an escape boundary: the byte before it can neither start nor
// continue an escape sequence.
func safeStart(data []byte, segStart, end, from int) int {
	if from <= segStart {
		return segStart
	}
	for p := from; p < end; p++ {
		switch c := data[p-1]; {
		case c == '\\' || c == 'u' || c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F':
			continue
		}
		return p
	}
	return end
}
