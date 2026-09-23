package bouncer

// Lossless JSON string walking, for callers outside the redactor that need
// the same guarantees as redactJSONLossless (issue #315: the prompt-injection
// guard classifies the decoded text of requests and responses and rewrites
// matching spans in place).
//
// WalkJSONStrings reuses the redactor's validating scanner primitives
// (scanString, number, literal, the escape decoder) so both stages agree on
// what a valid document and a decoded string are. It never builds a tree:
// the visitor receives the raw bounds of every string literal together with
// the path of object keys / array indexes that leads to it, and edits made
// with ApplyJSONEdits leave every other byte of the document unchanged.

// JSONPathElem is one step of the path to a JSON value: an object member
// (Key set, Index -1) or an array element (Key nil, Index >= 0). Key is the
// decoded member name; it aliases the walker's buffers and is only valid
// during the visit.
type JSONPathElem struct {
	Key   []byte
	Index int
}

// JSONString is one string literal of a document, as seen by a
// WalkJSONStrings visitor.
type JSONString struct {
	// Path leads from the root to the value (for an object key: to the
	// member the key names, so the key itself is Path[len(Path)-1].Key).
	// It aliases the walker's state and is only valid during the visit.
	Path []JSONPathElem
	// IsKey reports whether the literal is an object key.
	IsKey bool
	// Start and End bound the raw literal content in the document, quotes
	// excluded: the literal is data[Start-1:End+1].
	Start, End int
	// Escaped reports whether the raw content contains backslash escapes
	// (when false the raw content is the decoded value).
	Escaped bool
}

// JSONEdit replaces data[Start:End] with Repl.
type JSONEdit struct {
	Start, End int
	Repl       []byte
}

// jsonWalker walks a document with the redactor's scanner primitives.
type jsonWalker struct {
	s     jsonScanner
	path  []JSONPathElem
	keys  [][]byte // per-depth decoded-key buffers, reused
	visit func(JSONString)
}

// WalkJSONStrings calls visit for every string literal of data (a complete
// JSON document), keys included, in document order. It reports false when
// data is not valid JSON (visit may already have been called for a prefix).
func WalkJSONStrings(data []byte, visit func(JSONString)) bool {
	w := &jsonWalker{s: jsonScanner{data: data}, visit: visit}
	w.s.skipWS()
	if !w.value() {
		return false
	}
	w.s.skipWS()
	return w.s.pos == len(data)
}

func (w *jsonWalker) value() bool {
	s := &w.s
	if s.pos >= len(s.data) {
		return false
	}
	switch c := s.data[s.pos]; {
	case c == '{':
		return w.object()
	case c == '[':
		return w.array()
	case c == '"':
		cs, ce, esc, ok := s.scanString()
		if !ok {
			return false
		}
		w.visit(JSONString{Path: w.path, Start: cs, End: ce, Escaped: esc})
		return true
	case c == '-' || (c >= '0' && c <= '9'):
		return s.number()
	case c == 't':
		return s.literal("true")
	case c == 'f':
		return s.literal("false")
	case c == 'n':
		return s.literal("null")
	}
	return false
}

func (w *jsonWalker) object() bool {
	s := &w.s
	s.pos++ // '{'
	s.nest++
	defer func() { s.nest-- }()
	if s.nest > maxJSONNesting {
		return false
	}
	depth := len(w.path)
	for len(w.keys) <= depth {
		w.keys = append(w.keys, nil)
	}
	w.path = append(w.path, JSONPathElem{Index: -1})
	defer func() { w.path = w.path[:depth] }()
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
		key := s.data[cs:ce]
		if esc {
			w.keys[depth] = decodeJSONString(w.keys[depth][:0], key, nil)
			key = w.keys[depth]
		}
		w.path[depth] = JSONPathElem{Key: key, Index: -1}
		w.visit(JSONString{Path: w.path, IsKey: true, Start: cs, End: ce, Escaped: esc})
		s.skipWS()
		if s.pos >= len(s.data) || s.data[s.pos] != ':' {
			return false
		}
		s.pos++
		s.skipWS()
		if !w.value() {
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

func (w *jsonWalker) array() bool {
	s := &w.s
	s.pos++ // '['
	s.nest++
	defer func() { s.nest-- }()
	if s.nest > maxJSONNesting {
		return false
	}
	depth := len(w.path)
	w.path = append(w.path, JSONPathElem{Index: 0})
	defer func() { w.path = w.path[:depth] }()
	s.skipWS()
	if s.pos < len(s.data) && s.data[s.pos] == ']' {
		s.pos++
		return true
	}
	for i := 0; ; i++ {
		w.path[depth] = JSONPathElem{Index: i}
		if !w.value() {
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

// AppendDecodedJSONString appends the decoded value of a string literal's
// raw content (as bounded by a JSONString) to dst.
func AppendDecodedJSONString(dst, raw []byte) []byte {
	return decodeJSONString(dst, raw, nil)
}

// JSONDecodedSpanMapper maps byte ranges of a decoded string back to the raw
// literal content it was decoded from.
type JSONDecodedSpanMapper struct {
	units  []decodeUnit
	rawLen int
}

// NewJSONDecodedSpanMapper builds the mapper for raw (a literal's content).
func NewJSONDecodedSpanMapper(raw []byte) *JSONDecodedSpanMapper {
	return &JSONDecodedSpanMapper{units: decodeUnits(raw), rawLen: len(raw)}
}

// Raw returns the raw range covering every escape that produced the decoded
// range [ds, de).
func (m *JSONDecodedSpanMapper) Raw(ds, de int) (int, int) {
	return mapDecodedRange(m.units, m.rawLen, ds, de)
}

// EscapeJSONStringContent returns b escaped for use inside a JSON string
// literal (no HTML escaping, no surrounding quotes).
func EscapeJSONStringContent(b []byte) []byte {
	return escapeJSONStringBytes(b)
}

// ApplyJSONEdits returns data with the (sorted, non-overlapping) edits
// applied. data is returned unchanged when there are no edits.
func ApplyJSONEdits(data []byte, edits []JSONEdit) []byte {
	if len(edits) == 0 {
		return data
	}
	internal := make([]jsonEdit, len(edits))
	for i, e := range edits {
		internal[i] = jsonEdit{start: e.Start, end: e.End, repl: e.Repl}
	}
	return applyEdits(data, internal)
}

// WalkJSONObjectMembers calls fn for every member of the top-level object of
// data with the member's decoded key (only valid during the call) and the
// bounds of its raw value (data[start:end]). It reports false when data is
// not a valid JSON object.
func WalkJSONObjectMembers(data []byte, fn func(key []byte, start, end int)) bool {
	w := &jsonWalker{s: jsonScanner{data: data}, visit: func(JSONString) {}}
	s := &w.s
	s.skipWS()
	if s.pos >= len(data) || data[s.pos] != '{' {
		return false
	}
	s.pos++
	s.nest++
	s.skipWS()
	if s.pos < len(data) && data[s.pos] == '}' {
		s.pos++
		s.skipWS()
		return s.pos == len(data)
	}
	var keyBuf []byte
	for {
		if s.pos >= len(data) || data[s.pos] != '"' {
			return false
		}
		cs, ce, esc, ok := s.scanString()
		if !ok {
			return false
		}
		key := data[cs:ce]
		if esc {
			keyBuf = decodeJSONString(keyBuf[:0], key, nil)
			key = keyBuf
		}
		s.skipWS()
		if s.pos >= len(data) || data[s.pos] != ':' {
			return false
		}
		s.pos++
		s.skipWS()
		start := s.pos
		if !w.value() {
			return false
		}
		fn(key, start, s.pos)
		s.skipWS()
		if s.pos >= len(data) {
			return false
		}
		switch data[s.pos] {
		case ',':
			s.pos++
			s.skipWS()
		case '}':
			s.pos++
			s.skipWS()
			return s.pos == len(data)
		default:
			return false
		}
	}
}
