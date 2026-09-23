package bouncer

import (
	"strings"
	"testing"
)

func pathString(p []JSONPathElem) string {
	var sb strings.Builder
	for _, e := range p {
		if e.Index >= 0 {
			sb.WriteString("[" + string(rune('0'+e.Index)) + "]")
			continue
		}
		sb.WriteString("." + string(e.Key))
	}
	return sb.String()
}

func TestWalkJSONStrings(t *testing.T) {
	bs := string(rune(92))
	doc := `{"content":[{"type":"text","text":"a` + bs + `nb"}],"k` + bs + `u0065y":{"x":[1,true,null,"z"]},"n":-1.5e3}`
	type seen struct {
		path    string
		key     bool
		raw     string
		escaped bool
	}
	var got []seen
	ok := WalkJSONStrings([]byte(doc), func(s JSONString) {
		got = append(got, seen{pathString(s.Path), s.IsKey, doc[s.Start:s.End], s.Escaped})
	})
	if !ok {
		t.Fatal("valid document rejected")
	}
	want := []seen{
		{".content", true, "content", false},
		{".content[0].type", true, "type", false},
		{".content[0].type", false, "text", false},
		{".content[0].text", true, "text", false},
		{".content[0].text", false, "a" + bs + "nb", true},
		{".key", true, "k" + bs + "u0065y", true},
		{".key.x", true, "x", false},
		{".key.x[3]", false, "z", false},
		{".n", true, "n", false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d strings: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("string %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	for _, bad := range []string{``, `{`, `{"a":}`, `[1,]`, `"abc`, `{"a":1}x`, `{"a":"` + bs + `x"}`, `tru`} {
		if WalkJSONStrings([]byte(bad), func(JSONString) {}) {
			t.Errorf("invalid %q accepted", bad)
		}
	}
	deep := strings.Repeat("[", maxJSONNesting+1) + strings.Repeat("]", maxJSONNesting+1)
	if WalkJSONStrings([]byte(deep), func(JSONString) {}) {
		t.Error("nesting limit not enforced")
	}
}

func TestDecodeAndMapSpans(t *testing.T) {
	bs := string(rune(92))
	raw := []byte("x" + bs + "u0069gnore" + bs + "tme")
	dec := AppendDecodedJSONString(nil, raw)
	if string(dec) != "xignore\tme" {
		t.Fatalf("decoded %q", dec)
	}
	m := NewJSONDecodedSpanMapper(raw)
	rs, re := m.Raw(1, 7)
	if string(raw[rs:re]) != bs+"u0069gnore" {
		t.Fatalf("mapped %q", raw[rs:re])
	}
	out := ApplyJSONEdits([]byte(`"`+string(raw)+`"`), []JSONEdit{{Start: 1 + rs, End: 1 + re, Repl: []byte("[X]")}})
	if string(out) != `"x[X]`+bs+`tme"` {
		t.Fatalf("edited %s", out)
	}
	if got := EscapeJSONStringContent([]byte("a\"b\n")); string(got) != `a`+bs+`"b`+bs+`n` {
		t.Fatalf("escaped %s", got)
	}
}

func TestWalkJSONObjectMembers(t *testing.T) {
	doc := ` { "content" : [ 1 , 2 ] , "structuredContent":{"a":"b"}, "isError": false } `
	var keys []string
	var vals []string
	ok := WalkJSONObjectMembers([]byte(doc), func(key []byte, start, end int) {
		keys = append(keys, string(key))
		vals = append(vals, doc[start:end])
	})
	if !ok || strings.Join(keys, ",") != "content,structuredContent,isError" ||
		strings.Join(vals, "|") != `[ 1 , 2 ]|{"a":"b"}|false` {
		t.Fatalf("ok=%v keys=%v vals=%v", ok, keys, vals)
	}
	for _, bad := range []string{`[]`, `{"a":1`, `{"a" 1}`, `{} x`, `"s"`} {
		if WalkJSONObjectMembers([]byte(bad), func([]byte, int, int) {}) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !WalkJSONObjectMembers([]byte(` {} `), func([]byte, int, int) { t.Fatal("no members") }) {
		t.Fatal("empty object rejected")
	}
}
