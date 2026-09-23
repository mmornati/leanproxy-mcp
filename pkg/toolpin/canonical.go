package toolpin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Definition is the part of an MCP tool definition that is pinned: name,
// title, description, inputSchema, outputSchema and annotations. Icons and
// _meta are not pinned (they do not reach the model as instructions).
type Definition struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
}

// HashPrefix prefixes every pin hash.
const HashPrefix = "sha256:"

// Hash returns "sha256:<hex>" of the canonical JSON of d (see Canonical).
func Hash(d Definition) (string, error) {
	c, err := Canonical(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return HashPrefix + hex.EncodeToString(sum[:]), nil
}

// Canonical returns the canonical JSON encoding of d that Hash hashes:
//
//   - one JSON object with the members name, title, description,
//     inputSchema, outputSchema and annotations; a member whose value is
//     empty (""), absent or JSON null is left out, so "no title" and
//     "title": null hash the same;
//   - object keys sorted by their UTF-8 bytes at every depth, duplicate
//     keys resolved to the last value (as encoding/json decodes them);
//   - no insignificant whitespace;
//   - strings escaped as encoding/json does without HTML escaping (<, >
//     and & stay literal), invalid UTF-8 replaced by U+FFFD;
//   - integer literals kept as written; other numbers written in the
//     shortest form that round-trips through a float64 (1.0 -> 1,
//     1e2 -> 100, 0.50 -> 0.5).
//
// So key order and whitespace never change the hash, while any change of a
// value (a word of the description, a schema property, a hint) does.
func Canonical(d Definition) ([]byte, error) {
	obj := make(map[string]interface{}, 6)
	obj["name"] = d.Name
	if d.Title != "" {
		obj["title"] = d.Title
	}
	if d.Description != "" {
		obj["description"] = d.Description
	}
	for _, f := range []struct {
		key string
		raw json.RawMessage
	}{{"inputSchema", d.InputSchema}, {"outputSchema", d.OutputSchema}, {"annotations", d.Annotations}} {
		v, present, err := decodeRaw(f.raw)
		if err != nil {
			return nil, fmt.Errorf("toolpin: %s of tool %q: %w", f.key, d.Name, err)
		}
		if present {
			obj[f.key] = v
		}
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CanonicalJSON re-encodes one JSON document canonically (see Canonical).
func CanonicalJSON(raw []byte) ([]byte, error) {
	v, present, err := decodeRaw(raw)
	if err != nil {
		return nil, err
	}
	if !present {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeRaw decodes raw with json.Number numbers. present is false for an
// empty or null document.
func decodeRaw(raw []byte) (v interface{}, present bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, false, fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return nil, false, fmt.Errorf("invalid JSON: trailing data")
	}
	return v, true, nil
}

func writeCanonical(buf *bytes.Buffer, v interface{}) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		writeString(buf, x)
	case json.Number:
		buf.WriteString(canonicalNumber(string(x)))
	case []interface{}:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("toolpin: unexpected JSON value %T", v)
	}
	return nil
}

func writeString(buf *bytes.Buffer, s string) {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	// Encode appends a newline.
	buf.Truncate(buf.Len() - 1)
}

// canonicalNumber keeps integer literals verbatim (a float64 would lose
// precision above 2^53) and rewrites the others in their shortest
// round-tripping form, integral values without a fraction.
func canonicalNumber(lit string) string {
	if !strings.ContainsAny(lit, ".eE") {
		if lit == "-0" {
			return "0"
		}
		return lit
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return lit
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
