package toolpin

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// IsInvisible reports the characters Sanitize strips from tool metadata:
// zero-width characters and marks (U+200B..U+200F), bidi embeddings and
// overrides (U+202A..U+202E), invisible operators (U+2060..U+2064), bidi
// isolates (U+2066..U+2069), the zero-width no-break space (U+FEFF) and
// Unicode tag characters (U+E0000..U+E007F, "ASCII smuggling").
func IsInvisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F,
		r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x2064,
		r >= 0x2066 && r <= 0x2069,
		r == 0xFEFF,
		r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}

// isHiddenText reports the subset of IsInvisible that can hide or reorder
// text rather than merely join glyphs: bidi controls and tag characters.
func isHiddenText(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || (r >= 0xE0000 && r <= 0xE007F)
}

// StripInvisible returns s without the IsInvisible characters, and whether
// any was removed. s is returned as is when it holds none.
func StripInvisible(s string) (string, bool) {
	if !hasInvisible(s) {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !IsInvisible(r) {
			b.WriteRune(r)
		}
	}
	return b.String(), true
}

func hasInvisible(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		// Every stripped character encodes to 3 bytes starting 0xE2 or
		// 0xEF, or to 4 bytes starting 0xF3.
		if c != 0xE2 && c != 0xEF && c != 0xF3 {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if IsInvisible(r) {
			return true
		}
		i += size
	}
	return false
}

// StripInvisibleJSON removes the IsInvisible characters from every string
// value (not the object keys) of a JSON document, and reports whether it
// changed anything. A document without any is returned byte for byte; a
// changed one is re-encoded canonically (see Canonical). Invalid JSON is
// returned unchanged.
func StripInvisibleJSON(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 || (!hasInvisible(string(raw)) && !bytes.Contains(raw, []byte(`\u`))) {
		return raw, false
	}
	v, present, err := decodeRaw(raw)
	if err != nil || !present {
		return raw, false
	}
	changed := false
	v = stripValue(v, &changed)
	if !changed {
		return raw, false
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return raw, false
	}
	return buf.Bytes(), true
}

func stripValue(v interface{}, changed *bool) interface{} {
	switch x := v.(type) {
	case string:
		s, ok := StripInvisible(x)
		if ok {
			*changed = true
		}
		return s
	case []interface{}:
		for i := range x {
			x[i] = stripValue(x[i], changed)
		}
	case map[string]interface{}:
		for k := range x {
			x[k] = stripValue(x[k], changed)
		}
	}
	return v
}
