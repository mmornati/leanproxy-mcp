package bouncer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Sidecar output verification (issue #315, audit S10).
//
// The sidecar LLM receives the request params and returns them redacted.
// Because the params are attacker-influenced text, a prompt planted in them
// can steer the model into returning a different request: another tool
// name, other arguments. VerifySidecarOutput accepts the sidecar output only
// when it is a redaction of its input:
//
//   - the same JSON structure: the same object keys, array lengths and value
//     types, at every level;
//   - numbers, booleans and null unchanged;
//   - routing fields (name, server, tool, server_name, tool_name, method,
//     uri) unchanged, wherever they appear;
//   - every other string either unchanged, or the original with some parts
//     removed and/or replaced by a redaction marker ([VALUE_REDACTED],
//     [SECRET_REDACTED], [REDACTED], ...): what remains of it, read in order,
//     must come from the original string.

// sidecarRoutingKeys are the members whose string values select what runs.
var sidecarRoutingKeys = map[string]bool{
	"name": true, "server": true, "tool": true, "server_name": true, "tool_name": true,
	"method": true, "uri": true,
}

// redactionMarkerRe matches the placeholders a redaction may introduce.
var redactionMarkerRe = regexp.MustCompile(`\[[A-Z0-9_ ]*REDACTED[A-Z0-9_ ]*\]|\*{3,}`)

// VerifySidecarOutput returns an error describing the first difference that
// is not a redaction of orig.
func VerifySidecarOutput(orig, out []byte) error {
	a, err := decodeSidecarJSON(orig)
	if err != nil {
		return fmt.Errorf("input is not valid JSON: %w", err)
	}
	b, err := decodeSidecarJSON(out)
	if err != nil {
		return fmt.Errorf("output is not valid JSON: %w", err)
	}
	return compareRedacted("$", "", a, b)
}

func decodeSidecarJSON(data []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func compareRedacted(path, key string, a, b interface{}) error {
	switch av := a.(type) {
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: object became %T", path, b)
		}
		if len(av) != len(bv) {
			return fmt.Errorf("%s: %d keys became %d", path, len(av), len(bv))
		}
		for k, v := range av {
			w, ok := bv[k]
			if !ok {
				return fmt.Errorf("%s: key %q removed or renamed", path, k)
			}
			if err := compareRedacted(path+"."+k, k, v, w); err != nil {
				return err
			}
		}
		return nil
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok {
			return fmt.Errorf("%s: array became %T", path, b)
		}
		if len(av) != len(bv) {
			return fmt.Errorf("%s: %d items became %d", path, len(av), len(bv))
		}
		for i := range av {
			if err := compareRedacted(fmt.Sprintf("%s[%d]", path, i), "", av[i], bv[i]); err != nil {
				return err
			}
		}
		return nil
	case string:
		bv, ok := b.(string)
		if !ok {
			return fmt.Errorf("%s: string became %T", path, b)
		}
		if av == bv {
			return nil
		}
		if sidecarRoutingKeys[key] {
			return fmt.Errorf("%s: routing field changed", path)
		}
		if !isRedactionOf(av, bv) {
			return fmt.Errorf("%s: string is not a redaction of the original", path)
		}
		return nil
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok || av != bv {
			return fmt.Errorf("%s: number changed", path)
		}
		return nil
	case bool:
		bv, ok := b.(bool)
		if !ok || av != bv {
			return fmt.Errorf("%s: boolean changed", path)
		}
		return nil
	case nil:
		if b != nil {
			return fmt.Errorf("%s: null changed", path)
		}
		return nil
	}
	return fmt.Errorf("%s: unexpected JSON value %T", path, a)
}

// isRedactionOf reports whether out is orig with parts removed or replaced
// by redaction markers: the text left once the markers are removed must be
// a sequence of pieces of orig, in order.
func isRedactionOf(orig, out string) bool {
	pos := 0
	for _, piece := range redactionMarkerRe.Split(out, -1) {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		i := strings.Index(orig[pos:], piece)
		if i < 0 {
			return false
		}
		pos += i + len(piece)
	}
	return true
}
