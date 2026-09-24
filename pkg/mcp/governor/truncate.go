package governor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// BytesPerToken is the pkg/reporter.Estimator ratio (1 token ≈ 4 bytes).
// The governor converts token budgets to byte budgets with it, so its
// numbers match every other token count LeanProxy reports.
const BytesPerToken = 4

// Tokens estimates the tokens of n bytes, rounding up like
// pkg/reporter.Estimator.EstimateTokens.
func Tokens(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + BytesPerToken - 1) / BytesPerToken
}

// Share of the budget kept from the start and from the end of a text.
const (
	headShare = 70
	tailShare = 20
)

// OmittedKey is the member the structural JSON truncation adds to report
// what it left out.
const OmittedKey = "__leanproxy_omitted"

// TextMarker is the line inserted where a text was cut.
func TextMarker(omittedTokens int, id string, offset int) string {
	return fmt.Sprintf("… [LeanProxy: %s tokens omitted — call read_result with id=%s, offset=%d to page] …", groupThousands(omittedTokens), id, offset)
}

// TextCut is the result of TruncateText.
type TextCut struct {
	// Text is the head, the marker line and the tail.
	Text string
	// Offset is where the omitted part starts in the original (the offset
	// the marker points read_result at).
	Offset int
	// OmittedTokens is the estimate of the omitted part.
	OmittedTokens int
}

// TruncateText shortens s to at most budget bytes: about 70% of the budget
// from the start and 20% from the end, both cut on line boundaries when a
// line break is close enough (never inside a UTF-8 sequence), joined by a
// marker line naming the result id and the offset of the omitted part.
// ok is false when s already fits, or when the budget is too small to
// hold the marker.
func TruncateText(s string, budget int, id string) (TextCut, bool) {
	if len(s) <= budget {
		return TextCut{}, false
	}
	// Room for the marker (and the two line breaks around it); the
	// omitted-token count and the offset are bounded by len(s).
	markerRoom := len(TextMarker(Tokens(len(s)), id, len(s))) + 2
	if budget < markerRoom+64 {
		return TextCut{}, false
	}
	headBudget := budget * headShare / 100
	tailBudget := budget * tailShare / 100
	if headBudget+tailBudget+markerRoom > budget {
		tailBudget = max(0, budget-markerRoom-headBudget)
	}

	headEnd := cutBackward(s, headBudget)
	tailStart := cutForward(s, len(s)-tailBudget)
	if tailStart < headEnd {
		tailStart = headEnd
	}
	omitted := Tokens(tailStart - headEnd)
	marker := TextMarker(omitted, id, headEnd)

	var b strings.Builder
	b.Grow(headEnd + len(marker) + 2 + len(s) - tailStart)
	b.WriteString(s[:headEnd])
	if headEnd > 0 && s[headEnd-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(marker)
	if tailStart < len(s) {
		b.WriteByte('\n')
		b.WriteString(s[tailStart:])
	}
	return TextCut{Text: b.String(), Offset: headEnd, OmittedTokens: omitted}, true
}

// cutBackward returns an end offset <= limit: just after the last line
// break in the second half of s[:limit], or else limit moved back to a
// rune boundary.
func cutBackward(s string, limit int) int {
	if limit >= len(s) {
		return len(s)
	}
	if limit <= 0 {
		return 0
	}
	if i := strings.LastIndexByte(s[:limit], '\n'); i >= 0 && i+1 >= limit/2 {
		return i + 1
	}
	return runeStart(s, limit)
}

// cutForward returns a start offset >= from: just after the first line
// break in the first half of s[from:], or else from moved back to a rune
// boundary (so no byte is lost between two cuts).
func cutForward(s string, from int) int {
	if from <= 0 {
		return 0
	}
	if from >= len(s) {
		return len(s)
	}
	rest := len(s) - from
	if i := strings.IndexByte(s[from:], '\n'); i >= 0 && i+1 <= rest/2 {
		return from + i + 1
	}
	return runeStart(s, from)
}

// runeStart moves i back to the start of the UTF-8 sequence it points in.
func runeStart(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

func groupThousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// IsJSON reports whether s is a JSON object or array (the documents the
// governor truncates structurally).
func IsJSON(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return false
	}
	return json.Valid([]byte(t))
}

// minNested is the smallest budget worth truncating a nested value into;
// below it the value is dropped and counted instead.
const minNested = 96

// TruncateJSON shortens a JSON document to at most budget bytes without
// ever producing invalid JSON: arrays keep their first elements and end
// with an {"__leanproxy_omitted": {"items": N, "result_id": id}} element;
// objects keep their smaller members whole, shorten the large ones
// recursively and report dropped members with a "__leanproxy_omitted"
// member; long strings keep their start and end with a marker. Members and
// elements that are kept are kept byte for byte. ok is false when doc
// already fits or is not valid JSON.
func TruncateJSON(doc []byte, budget int, id string) ([]byte, bool) {
	if !json.Valid(doc) {
		return nil, false
	}
	return TruncateValidJSON(doc, budget, id)
}

// TruncateValidJSON is TruncateJSON for a document already known to be
// valid JSON (it is not validated again; the output always is).
func TruncateValidJSON(doc []byte, budget int, id string) ([]byte, bool) {
	doc = bytes.TrimSpace(doc)
	if len(doc) <= budget {
		return nil, false
	}
	t := jsonTruncator{id: id}
	target := budget
	for attempt := 0; attempt < 5; attempt++ {
		out := t.value(doc, target)
		if len(out) <= budget && json.Valid(out) {
			return out, true
		}
		target = target * 3 / 5
	}
	// Pathological documents (thousands of tiny nested members): report
	// the whole document as omitted.
	return t.omitted(map[string]int{"bytes": len(doc)}), true
}

type jsonTruncator struct{ id string }

// omitted renders {"__leanproxy_omitted": {<counts>, "result_id": id}}.
func (t jsonTruncator) omitted(counts map[string]int) []byte {
	return []byte(`{"` + OmittedKey + `":` + t.omittedBody(counts) + `}`)
}

func (t jsonTruncator) omittedBody(counts map[string]int) string {
	var b strings.Builder
	b.WriteByte('{')
	for _, k := range []string{"items", "keys", "bytes"} {
		if n, ok := counts[k]; ok {
			fmt.Fprintf(&b, "%q:%d,", k, n)
		}
	}
	fmt.Fprintf(&b, `"result_id":%q}`, t.id)
	return b.String()
}

// value truncates one JSON value to about budget bytes.
func (t jsonTruncator) value(raw []byte, budget int) []byte {
	if len(raw) <= budget {
		return raw
	}
	switch raw[0] {
	case '[':
		return t.array(raw, budget)
	case '{':
		return t.object(raw, budget)
	case '"':
		return t.str(raw, budget)
	default:
		return raw // numbers, booleans, null: kept whole
	}
}

func (t jsonTruncator) array(raw []byte, budget int) []byte {
	elems, err := SplitArray(raw)
	if err != nil {
		return raw
	}
	marker := func(n int) []byte { return t.omitted(map[string]int{"items": n}) }
	// Worst-case marker size (the count is at most len(elems)).
	reserve := len(marker(len(elems))) + 1
	out := []byte{'['}
	used := 2 + reserve
	kept := 0
	for _, el := range elems {
		sep := 0
		if kept > 0 {
			sep = 1
		}
		if used+sep+len(el) <= budget {
			out = appendSep(out, kept)
			out = append(out, el...)
			used += sep + len(el)
			kept++
			continue
		}
		if room := budget - used - sep; room >= minNested && isContainerOrString(el) {
			out = appendSep(out, kept)
			out = append(out, t.value(el, room)...)
			kept++
		}
		break
	}
	if omitted := len(elems) - kept; omitted > 0 {
		out = appendSep(out, kept)
		out = append(out, marker(omitted)...)
	}
	return append(out, ']')
}

func (t jsonTruncator) object(raw []byte, budget int) []byte {
	members, err := SplitObject(raw)
	if err != nil {
		return raw
	}
	reserve := len(`,"`+OmittedKey+`":`) + len(t.omittedBody(map[string]int{"keys": len(members)}))
	sizes := make([]int, len(members))
	for i, m := range members {
		sizes[i] = len(m.Key) + 1 + len(m.Value) + 1
	}
	alloc := waterFill(sizes, budget-2-reserve)

	out := []byte{'{'}
	kept, dropped := 0, 0
	for i, m := range members {
		var v []byte
		switch room := alloc[i] - len(m.Key) - 2; {
		case alloc[i] >= sizes[i]:
			v = m.Value
		case room >= minNested && isContainerOrString(m.Value):
			v = t.value(m.Value, room)
		default:
			dropped++
			continue
		}
		out = appendSep(out, kept)
		out = append(out, m.Key...)
		out = append(out, ':')
		out = append(out, v...)
		kept++
	}
	if dropped > 0 {
		out = appendSep(out, kept)
		out = append(out, `"`+OmittedKey+`":`...)
		out = append(out, t.omittedBody(map[string]int{"keys": dropped})...)
	}
	return append(out, '}')
}

// str keeps the start and the end of a long JSON string with a marker in
// between.
func (t jsonTruncator) str(raw []byte, budget int) []byte {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return raw
	}
	marker := fmt.Sprintf(" … [LeanProxy: %s bytes omitted, result_id=%s] … ", groupThousands(len(s)), t.id)
	// Escaping can grow the text; keep a margin.
	room := (budget - 2 - len(marker)) * 4 / 5
	if room < 16 {
		out, _ := MarshalNoEscape("… [LeanProxy: omitted, result_id=" + t.id + "] …")
		return out
	}
	head := runeStart(s, room*3/4)
	tail := runeStart(s, len(s)-room/4)
	if tail < head {
		tail = head
	}
	out, err := MarshalNoEscape(s[:head] + marker + s[tail:])
	if err != nil {
		return raw
	}
	return out
}

func appendSep(out []byte, kept int) []byte {
	if kept > 0 {
		return append(out, ',')
	}
	return out
}

func isContainerOrString(raw []byte) bool {
	return len(raw) > 0 && (raw[0] == '[' || raw[0] == '{' || raw[0] == '"')
}

// waterFill splits budget among items of the given sizes: items smaller
// than their fair share are granted in full, and what they leave is shared
// equally among the larger ones.
func waterFill(sizes []int, budget int) []int {
	alloc := make([]int, len(sizes))
	if budget <= 0 || len(sizes) == 0 {
		return alloc
	}
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	// Insertion sort by size: member counts are small.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && sizes[order[j]] < sizes[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	remaining := budget
	for k, i := range order {
		share := remaining / (len(order) - k)
		if sizes[i] <= share {
			alloc[i] = sizes[i]
		} else {
			alloc[i] = share
		}
		remaining -= alloc[i]
	}
	return alloc
}

// WaterFill is waterFill for the governor middleware, which splits one
// call's budget among its content items the same way.
func WaterFill(sizes []int, budget int) []int { return waterFill(sizes, budget) }

// MarshalNoEscape encodes v like json.Marshal, but without HTML escaping (so "<", ">" and "&"
// keep their size and look).
func MarshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Member is one member of a JSON object: its key (a JSON string) and its
// raw value, both byte for byte as in the document.
type Member struct {
	Key   []byte
	Value []byte
}

// errBadJSON is returned by the splitters for input that is not the JSON
// value they expect.
var errBadJSON = errors.New("invalid JSON")

// SplitArray returns the raw elements of a JSON array, byte for byte. It
// scans the document without decoding it; raw must be valid JSON (the
// governor checks that once, before splitting).
func SplitArray(raw []byte) ([][]byte, error) {
	i := skipWS(raw, 0)
	if i >= len(raw) || raw[i] != '[' {
		return nil, errBadJSON
	}
	i = skipWS(raw, i+1)
	out := make([][]byte, 0, 16)
	if i < len(raw) && raw[i] == ']' {
		return out, nil
	}
	for i < len(raw) {
		end, err := skipValue(raw, i)
		if err != nil {
			return nil, err
		}
		out = append(out, raw[i:end])
		i = skipWS(raw, end)
		if i >= len(raw) {
			return nil, errBadJSON
		}
		switch raw[i] {
		case ',':
			i = skipWS(raw, i+1)
		case ']':
			return out, nil
		default:
			return nil, errBadJSON
		}
	}
	return nil, errBadJSON
}

// SplitObject returns the members of a JSON object in document order,
// byte for byte, without decoding it; raw must be valid JSON.
func SplitObject(raw []byte) ([]Member, error) {
	i := skipWS(raw, 0)
	if i >= len(raw) || raw[i] != '{' {
		return nil, errBadJSON
	}
	i = skipWS(raw, i+1)
	out := make([]Member, 0, 8)
	if i < len(raw) && raw[i] == '}' {
		return out, nil
	}
	for i < len(raw) {
		if raw[i] != '"' {
			return nil, errBadJSON
		}
		keyEnd, err := skipString(raw, i)
		if err != nil {
			return nil, err
		}
		key := raw[i:keyEnd]
		i = skipWS(raw, keyEnd)
		if i >= len(raw) || raw[i] != ':' {
			return nil, errBadJSON
		}
		i = skipWS(raw, i+1)
		end, err := skipValue(raw, i)
		if err != nil {
			return nil, err
		}
		out = append(out, Member{Key: key, Value: raw[i:end]})
		i = skipWS(raw, end)
		if i >= len(raw) {
			return nil, errBadJSON
		}
		switch raw[i] {
		case ',':
			i = skipWS(raw, i+1)
		case '}':
			return out, nil
		default:
			return nil, errBadJSON
		}
	}
	return nil, errBadJSON
}

func skipWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the index just after the JSON string starting at i.
// It jumps from quote to quote (bytes.IndexByte) and skips the quotes an
// odd run of backslashes escapes.
func skipString(b []byte, i int) (int, error) {
	i++
	for i < len(b) {
		q := bytes.IndexByte(b[i:], '"')
		if q < 0 {
			return 0, errBadJSON
		}
		i += q
		bs := 0
		for j := i - 1; j >= 0 && b[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 {
			return i + 1, nil
		}
		i++
	}
	return 0, errBadJSON
}

// skipValue returns the index just after the JSON value starting at i.
func skipValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, errBadJSON
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for ; i < len(b); i++ {
			switch b[i] {
			case '"':
				end, err := skipString(b, i)
				if err != nil {
					return 0, err
				}
				i = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1, nil
				}
			}
		}
		return 0, errBadJSON
	default:
		start := i
		for i < len(b) && b[i] != ',' && b[i] != ']' && b[i] != '}' && b[i] != ' ' && b[i] != '\t' && b[i] != '\n' && b[i] != '\r' {
			i++
		}
		if i == start {
			return 0, errBadJSON
		}
		return i, nil
	}
}
