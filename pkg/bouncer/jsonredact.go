package bouncer

import (
	"bytes"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// Lossless JSON redaction.
//
// redactJSONLossless walks a JSON document with a hand-written validating
// scanner and records edits instead of building a tree: the output is the
// input with a few byte ranges replaced. Everything that is not edited
// (numbers, key order, whitespace, escaping, unicode) is copied through
// unchanged, and when nothing is edited the input slice itself is returned.
//
// What gets edited:
//   - the value of a sensitive key (IsSensitiveKey), whatever its type
//     (string, number, array, object) — booleans, null and empty strings
//     carry no secret and are kept. An object under a sensitive key that is
//     itself a member of a JSON Schema name→schema map ("properties",
//     "patternProperties", "definitions", "$defs") is a schema, not a
//     value, and is kept too (tools/list must stay usable);
//   - the parts of a string literal (value or object key) that match a
//     pattern. Patterns see the decoded string, and the match is mapped back
//     to the raw bytes, so escapes outside the match are preserved;
//   - strings whose decoded value is itself a JSON object or array (MCP
//     content[].text) are redacted recursively, up to maxNestedJSONDepth
//     levels and maxNestedJSONBytes per string; the inner edits are mapped
//     back into the outer literal and re-escaped, so the rest of the inner
//     document stays byte-identical.

const (
	// maxNestedJSONDepth is how many levels of JSON-inside-a-string are
	// parsed (the top-level document is level 0).
	maxNestedJSONDepth = 3
	// maxNestedJSONBytes caps the decoded size of a string that is parsed as
	// nested JSON; larger strings are scanned as text.
	maxNestedJSONBytes = 8 << 20
	// maxJSONNesting bounds container nesting (as encoding/json does);
	// deeper documents fall back to byte-level redaction.
	maxJSONNesting = 10000
)

var (
	redactedMarker = []byte(SecretRedacted)
	redactedValue  = []byte(`"` + SecretRedacted + `"`)
)

// jsonEdit replaces data[start:end] with repl.
type jsonEdit struct {
	start, end int
	repl       []byte
}

// valueCtx carries what the parent object says about the value being
// scanned.
type valueCtx struct {
	sensitive  bool // value of a sensitive key: redact whatever it is
	schemaSlot bool // member of a JSON Schema "properties" map: keep objects
	propsMap   bool // value of a JSON Schema name→schema map ("properties", "$defs", ...)
	entropyKey bool // key contains a key-like word (entropy detector)
}

type jsonScanner struct {
	r        *Redactor
	data     []byte
	pos      int
	level    int
	nest     int
	suppress int
	edits    []jsonEdit
	count    int
	mask     []uint64
	dec      []byte
	keyBuf   []byte
	spans    []span
}

var jsonScannerPool = sync.Pool{New: func() interface{} { return &jsonScanner{} }}

func (r *Redactor) acquireScanner(data []byte, level int) *jsonScanner {
	s := jsonScannerPool.Get().(*jsonScanner)
	s.r = r
	s.data = data
	s.pos = 0
	s.level = level
	s.nest = 0
	s.suppress = 0
	s.count = 0
	s.edits = s.edits[:0]
	s.spans = s.spans[:0]
	s.mask = s.mask[:0]
	for i := 0; i < r.maskWords(); i++ {
		s.mask = append(s.mask, 0)
	}
	return s
}

func releaseScanner(s *jsonScanner) {
	// dec and keyBuf may hold decoded secrets: scrub before pooling.
	constantTimeZero(s.dec[:cap(s.dec)])
	constantTimeZero(s.keyBuf[:cap(s.keyBuf)])
	s.dec = s.dec[:0]
	s.keyBuf = s.keyBuf[:0]
	for i := range s.edits {
		s.edits[i].repl = nil
	}
	s.edits = s.edits[:0]
	s.r = nil
	s.data = nil
	jsonScannerPool.Put(s)
}

// redactJSONLossless redacts data (a complete JSON document). ok is false
// when data is not valid JSON.
func (r *Redactor) redactJSONLossless(data []byte, level int) (out []byte, count int, ok bool) {
	s := r.acquireScanner(data, level)
	defer releaseScanner(s)
	if !s.run() {
		return nil, 0, false
	}
	if len(s.edits) == 0 {
		return data, s.count, true
	}
	return applyEdits(data, s.edits), s.count, true
}

// scanEdits scans data and returns a private copy of the edits.
func (r *Redactor) scanEdits(data []byte, level int) (edits []jsonEdit, count int, ok bool) {
	s := r.acquireScanner(data, level)
	defer releaseScanner(s)
	if !s.run() {
		return nil, 0, false
	}
	if len(s.edits) > 0 {
		edits = append([]jsonEdit(nil), s.edits...)
	}
	return edits, s.count, true
}

func applyEdits(data []byte, edits []jsonEdit) []byte {
	grow := 0
	for _, e := range edits {
		if d := len(e.repl) - (e.end - e.start); d > 0 {
			grow += d
		}
	}
	out := make([]byte, 0, len(data)+grow)
	pos := 0
	for _, e := range edits {
		out = append(out, data[pos:e.start]...)
		out = append(out, e.repl...)
		pos = e.end
	}
	return append(out, data[pos:]...)
}

func (s *jsonScanner) addEdit(start, end int, repl []byte) {
	s.edits = append(s.edits, jsonEdit{start: start, end: end, repl: repl})
}

func (s *jsonScanner) run() bool {
	s.skipWS()
	if !s.value(valueCtx{}) {
		return false
	}
	s.skipWS()
	return s.pos == len(s.data)
}

func (s *jsonScanner) skipWS() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *jsonScanner) value(ctx valueCtx) bool {
	if s.pos >= len(s.data) {
		return false
	}
	switch c := s.data[s.pos]; {
	case c == '{':
		if ctx.sensitive && !ctx.schemaSlot && s.suppress == 0 {
			return s.redactContainer(s.object)
		}
		return s.object(ctx)
	case c == '[':
		if ctx.sensitive && s.suppress == 0 {
			return s.redactContainer(s.array)
		}
		return s.array(valueCtx{})
	case c == '"':
		return s.stringValue(ctx)
	case c == '-' || (c >= '0' && c <= '9'):
		start := s.pos
		if !s.number() {
			return false
		}
		if ctx.sensitive && s.suppress == 0 {
			s.addEdit(start, s.pos, redactedValue)
			s.count++
		}
		return true
	case c == 't':
		return s.literal("true")
	case c == 'f':
		return s.literal("false")
	case c == 'n':
		return s.literal("null")
	}
	return false
}

// redactContainer validates the object or array at pos without looking
// inside it and replaces it as a whole.
func (s *jsonScanner) redactContainer(parse func(valueCtx) bool) bool {
	start := s.pos
	s.suppress++
	ok := parse(valueCtx{})
	s.suppress--
	if !ok {
		return false
	}
	s.addEdit(start, s.pos, redactedValue)
	s.count++
	return true
}

func (s *jsonScanner) literal(lit string) bool {
	if len(s.data)-s.pos < len(lit) || string(s.data[s.pos:s.pos+len(lit)]) != lit {
		return false
	}
	s.pos += len(lit)
	return true
}

func (s *jsonScanner) number() bool {
	d := s.data
	i := s.pos
	if d[i] == '-' {
		i++
	}
	if i >= len(d) {
		return false
	}
	switch {
	case d[i] == '0':
		i++
	case d[i] >= '1' && d[i] <= '9':
		for i < len(d) && d[i] >= '0' && d[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < len(d) && d[i] == '.' {
		i++
		j := i
		for i < len(d) && d[i] >= '0' && d[i] <= '9' {
			i++
		}
		if i == j {
			return false
		}
	}
	if i < len(d) && (d[i] == 'e' || d[i] == 'E') {
		i++
		if i < len(d) && (d[i] == '+' || d[i] == '-') {
			i++
		}
		j := i
		for i < len(d) && d[i] >= '0' && d[i] <= '9' {
			i++
		}
		if i == j {
			return false
		}
	}
	s.pos = i
	return true
}

func (s *jsonScanner) object(ctx valueCtx) bool {
	s.pos++ // '{'
	s.nest++
	defer func() { s.nest-- }()
	if s.nest > maxJSONNesting {
		return false
	}
	s.skipWS()
	if s.pos < len(s.data) && s.data[s.pos] == '}' {
		s.pos++
		return true
	}
	for {
		if s.pos >= len(s.data) || s.data[s.pos] != '"' {
			return false
		}
		cs, ce, esc, ok := s.scanString()
		if !ok {
			return false
		}
		child := valueCtx{schemaSlot: ctx.propsMap}
		if s.suppress == 0 {
			s.classifyKey(s.data[cs:ce], esc, &child)
			s.analyzeString(cs, ce, esc, false, false)
		}
		s.skipWS()
		if s.pos >= len(s.data) || s.data[s.pos] != ':' {
			return false
		}
		s.pos++
		s.skipWS()
		if !s.value(child) {
			return false
		}
		s.skipWS()
		if s.pos >= len(s.data) {
			return false
		}
		switch s.data[s.pos] {
		case ',':
			s.pos++
			s.skipWS()
		case '}':
			s.pos++
			return true
		default:
			return false
		}
	}
}

func (s *jsonScanner) array(valueCtx) bool {
	s.pos++ // '['
	s.nest++
	defer func() { s.nest-- }()
	if s.nest > maxJSONNesting {
		return false
	}
	s.skipWS()
	if s.pos < len(s.data) && s.data[s.pos] == ']' {
		s.pos++
		return true
	}
	for {
		if !s.value(valueCtx{}) {
			return false
		}
		s.skipWS()
		if s.pos >= len(s.data) {
			return false
		}
		switch s.data[s.pos] {
		case ',':
			s.pos++
			s.skipWS()
		case ']':
			s.pos++
			return true
		default:
			return false
		}
	}
}

// classifyKey fills the sensitive / properties / entropy flags of the
// value that follows key (raw literal content).
func (s *jsonScanner) classifyKey(raw []byte, esc bool, child *valueCtx) {
	key := raw
	if esc {
		s.keyBuf = decodeJSONString(s.keyBuf[:0], raw, nil)
		key = s.keyBuf
	}
	var buf [64]byte
	norm := normalizeKey(buf[:0], key)
	child.sensitive = isSensitiveNormalizedKey(norm)
	switch string(norm) {
	case "properties", "patternproperties", "definitions", "$defs":
		// JSON Schema maps from a name to a schema.
		child.propsMap = true
	}
	if s.r.entropy {
		for _, kw := range entropyKeywords {
			if bytes.Contains(norm, []byte(kw)) {
				child.entropyKey = true
				break
			}
		}
	}
}

func (s *jsonScanner) stringValue(ctx valueCtx) bool {
	start := s.pos
	cs, ce, esc, ok := s.scanString()
	if !ok {
		return false
	}
	if s.suppress > 0 {
		return true
	}
	if ctx.sensitive {
		if ce > cs && !bytes.Equal(s.data[start:s.pos], redactedValue) {
			s.addEdit(start, s.pos, redactedValue)
			s.count++
		}
		return true
	}
	s.analyzeString(cs, ce, esc, ctx.entropyKey, true)
	return true
}

// strStop marks the bytes that end the fast path of scanString.
var strStop = func() (t [256]bool) {
	for c := 0; c < 0x20; c++ {
		t[c] = true
	}
	t['"'] = true
	t['\\'] = true
	return t
}()

// scanString consumes the string literal at pos (which must be '"') and
// returns the bounds of its raw content and whether it contains escapes.
func (s *jsonScanner) scanString() (cs, ce int, esc, ok bool) {
	d := s.data
	i := s.pos + 1
	cs = i
	for {
		for i < len(d) && !strStop[d[i]] {
			i++
		}
		if i >= len(d) {
			return 0, 0, false, false
		}
		switch c := d[i]; {
		case c == '"':
			s.pos = i + 1
			return cs, i, esc, true
		case c < 0x20:
			return 0, 0, false, false
		}
		// backslash
		esc = true
		i++
		if i >= len(d) {
			return 0, 0, false, false
		}
		switch d[i] {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			i++
		case 'u':
			if i+4 >= len(d) {
				return 0, 0, false, false
			}
			for k := 1; k <= 4; k++ {
				if hexVal(d[i+k]) < 0 {
					return 0, 0, false, false
				}
			}
			i += 5
		default:
			return 0, 0, false, false
		}
	}
}

// analyzeString redacts the secrets inside the string literal whose raw
// content is data[cs:ce].
func (s *jsonScanner) analyzeString(cs, ce int, esc, keyCtx, allowNested bool) {
	r := s.r
	raw := s.data[cs:ce]
	if len(raw) == 0 {
		return
	}
	entropyCtx := keyCtx && r.entropy
	if !esc {
		if !r.kw.scan(raw, s.mask) && len(r.always) == 0 && !entropyCtx {
			return
		}
		s.spans = r.findSpansMasked(raw, s.mask, keyCtx, s.spans)
		for _, sp := range s.spans {
			s.addEdit(cs+sp.start, cs+sp.end, redactedMarker)
		}
		s.count += len(s.spans)
		return
	}

	s.dec = decodeJSONString(s.dec[:0], raw, nil)
	dec := s.dec
	if allowNested && s.level < maxNestedJSONDepth && len(dec) <= maxNestedJSONBytes && looksLikeJSONContainer(dec) {
		edits, count, ok := r.scanEdits(dec, s.level+1)
		if ok {
			if len(edits) > 0 {
				units := decodeUnits(raw)
				for _, e := range edits {
					rs, re := mapDecodedRange(units, len(raw), e.start, e.end)
					s.addEdit(cs+rs, cs+re, escapeJSONStringBytes(e.repl))
				}
			}
			s.count += count
			return
		}
		// Not valid JSON after all: scan it as text below.
	}
	if !r.kw.scan(dec, s.mask) && len(r.always) == 0 && !entropyCtx {
		return
	}
	s.spans = r.findSpansMasked(dec, s.mask, keyCtx, s.spans)
	if len(s.spans) == 0 {
		return
	}
	units := decodeUnits(raw)
	for _, sp := range s.spans {
		rs, re := mapDecodedRange(units, len(raw), sp.start, sp.end)
		s.addEdit(cs+rs, cs+re, redactedMarker)
	}
	s.count += len(s.spans)
}

// looksLikeJSONContainer reports whether b starts (after whitespace) with
// '{' or '['.
func looksLikeJSONContainer(b []byte) bool {
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

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func hex4(b []byte) rune {
	return rune(hexVal(b[0])<<12 | hexVal(b[1])<<8 | hexVal(b[2])<<4 | hexVal(b[3]))
}

// decodeUnit is the raw byte range [start, end) of the escape (or plain
// byte) that produced one decoded byte.
type decodeUnit struct{ start, end int }

// decodeJSONString appends the decoded value of the raw (already
// validated) string content to dst. When units is non-nil, it also appends,
// for every decoded byte, the raw range that produced it. Invalid surrogate
// escapes decode to U+FFFD as in encoding/json; raw bytes (including
// invalid UTF-8) are copied as they are.
func decodeJSONString(dst []byte, raw []byte, units *[]decodeUnit) []byte {
	var tmp [utf8.UTFMax]byte
	for i := 0; i < len(raw); {
		c := raw[i]
		if c != '\\' {
			dst = append(dst, c)
			if units != nil {
				*units = append(*units, decodeUnit{i, i + 1})
			}
			i++
			continue
		}
		start := i
		var out []byte
		switch raw[i+1] {
		case '"', '\\', '/':
			tmp[0] = raw[i+1]
			out = tmp[:1]
			i += 2
		case 'b':
			tmp[0] = '\b'
			out = tmp[:1]
			i += 2
		case 'f':
			tmp[0] = '\f'
			out = tmp[:1]
			i += 2
		case 'n':
			tmp[0] = '\n'
			out = tmp[:1]
			i += 2
		case 'r':
			tmp[0] = '\r'
			out = tmp[:1]
			i += 2
		case 't':
			tmp[0] = '\t'
			out = tmp[:1]
			i += 2
		case 'u':
			r := hex4(raw[i+2 : i+6])
			i += 6
			if utf16.IsSurrogate(r) {
				r2 := utf8.RuneError
				if i+6 <= len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
					if dr := utf16.DecodeRune(r, hex4(raw[i+2:i+6])); dr != utf8.RuneError {
						r2 = dr
						i += 6
					}
				}
				r = r2
			}
			out = tmp[:utf8.EncodeRune(tmp[:], r)]
		}
		dst = append(dst, out...)
		if units != nil {
			for range out {
				*units = append(*units, decodeUnit{start, i})
			}
		}
	}
	return dst
}

// decodeUnits returns the decoded-byte → raw-range map of raw.
func decodeUnits(raw []byte) []decodeUnit {
	units := make([]decodeUnit, 0, len(raw))
	decodeJSONString(nil, raw, &units)
	return units
}

// mapDecodedRange maps the decoded range [ds, de) to the raw range that
// covers every escape it touches.
func mapDecodedRange(units []decodeUnit, rawLen, ds, de int) (int, int) {
	rs, re := rawLen, rawLen
	if ds < len(units) {
		rs = units[ds].start
	}
	if de > 0 && de-1 < len(units) {
		re = units[de-1].end
	}
	if re < rs {
		re = rs
	}
	return rs, re
}

// escapeJSONStringBytes returns b escaped for use inside a JSON string
// literal (no HTML escaping). b itself is returned when nothing needs
// escaping.
func escapeJSONStringBytes(b []byte) []byte {
	need := false
	for _, c := range b {
		if c == '"' || c == '\\' || c < 0x20 {
			need = true
			break
		}
	}
	if !need {
		return b
	}
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)+8)
	for _, c := range b {
		switch {
		case c == '"' || c == '\\':
			out = append(out, '\\', c)
		case c == '\n':
			out = append(out, '\\', 'n')
		case c == '\r':
			out = append(out, '\\', 'r')
		case c == '\t':
			out = append(out, '\\', 't')
		case c < 0x20:
			out = append(out, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		default:
			out = append(out, c)
		}
	}
	return out
}
