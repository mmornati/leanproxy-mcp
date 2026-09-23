package bouncer

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func builtinRedactor() *Redactor { return NewRedactor(PatternsToRegexps(BuiltInPatterns)) }

func TestRedactJSON_AcceptanceNumbersAndPassword(t *testing.T) {
	in := `{"n":12345678901234567,"password":12345678}`
	want := `{"n":12345678901234567,"password":"[SECRET_REDACTED]"}`
	out, n, err := builtinRedactor().RedactJSON([]byte(in))
	if err != nil || n != 1 || string(out) != want {
		t.Fatalf("got %s (n=%d, err=%v), want %s", out, n, err, want)
	}
}

func TestRedactJSON_PreservesBytesWhenClean(t *testing.T) {
	in := []byte("{\n  \"z\": 1.50,\n  \"a\": \"<b>&amp;</b> caf\\u00e9 \\/ \\\"q\\\"\",\n  \"big\": -12345678901234567890e-3,\n  \"arr\": [ true, false, null, {} , [] ]\n}\n")
	out, n, err := builtinRedactor().RedactJSON(in)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("clean JSON changed:\n got %q\nwant %q", out, in)
	}
}

func TestRedactJSON_SensitiveKeysAnyType(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"token":["a","b"]}`, `{"token":"[SECRET_REDACTED]"}`},
		{`{"token":123}`, `{"token":"[SECRET_REDACTED]"}`},
		{`{"client_secret":{"v":1,"w":[2]}}`, `{"client_secret":"[SECRET_REDACTED]"}`},
		{`{"X-Api-Key":"abc"}`, `{"X-Api-Key":"[SECRET_REDACTED]"}`},
		{`{"aws_secret_access_key":"abc","access_token":"t","refresh_token":"r","private_key":"k","Set-Cookie":"c"}`,
			`{"aws_secret_access_key":"[SECRET_REDACTED]","access_token":"[SECRET_REDACTED]","refresh_token":"[SECRET_REDACTED]","private_key":"[SECRET_REDACTED]","Set-Cookie":"[SECRET_REDACTED]"}`},
		{`{"db_password":"pw","Authorization":"Basic x"}`, `{"db_password":"[SECRET_REDACTED]","Authorization":"[SECRET_REDACTED]"}`},
		// booleans, null and empty strings carry nothing to hide.
		{`{"token":true,"password":null,"secret":""}`, `{"token":true,"password":null,"secret":""}`},
		// "token" is exact-match only.
		{`{"next_page_token":"abc","max_tokens":5}`, `{"next_page_token":"abc","max_tokens":5}`},
		// key escapes are decoded before matching.
		{`{"pass\u0077ord":"x"}`, `{"pass\u0077ord":"[SECRET_REDACTED]"}`},
	}
	r := builtinRedactor()
	for _, c := range cases {
		out, _, _ := r.RedactJSON([]byte(c.in))
		if string(out) != c.want {
			t.Errorf("in %s\n got %s\nwant %s", c.in, out, c.want)
		}
	}
}

func TestRedactJSON_SchemaPropertiesKept(t *testing.T) {
	// tools/list: a parameter named "password" or "token" is a schema, not
	// a credential, and must reach the client intact.
	in := `{"tools":[{"name":"login","inputSchema":{"type":"object","properties":{"password":{"type":"string","description":"the password"},"token":{"type":"string"}},"required":["password"]}}]}`
	out, n, _ := builtinRedactor().RedactJSON([]byte(in))
	if n != 0 || string(out) != in {
		t.Fatalf("schema changed (n=%d): %s", n, out)
	}
	defs := `{"$defs":{"Token":{"type":"object"}},"definitions":{"secret":{"type":"string"}},"items":{"$ref":"#/$defs/Token"}}`
	if out, n, _ := builtinRedactor().RedactJSON([]byte(defs)); n != 0 || string(out) != defs {
		t.Fatalf("schema definitions changed (n=%d): %s", n, out)
	}
	// A string under "properties" is still a value.
	in2 := `{"properties":{"password":"hunter2"}}`
	out, _, _ = builtinRedactor().RedactJSON([]byte(in2))
	if string(out) != `{"properties":{"password":"[SECRET_REDACTED]"}}` {
		t.Fatalf("got %s", out)
	}
}

func TestRedactJSON_ObjectKeysScanned(t *testing.T) {
	key := newFakeGen(3).githubPAT()
	in := `{"` + key + `":1,"ok":2}`
	out, n, _ := builtinRedactor().RedactJSON([]byte(in))
	if n != 1 || string(out) != `{"[SECRET_REDACTED]":1,"ok":2}` {
		t.Fatalf("got %s n=%d", out, n)
	}
}

func TestRedactJSON_PartialStringKeepsEscapes(t *testing.T) {
	key := newFakeGen(4).awsKeyID()
	in := `{"msg":"caf\u00e9 <x> key=` + key + `\n\u00e9nd"}`
	out, _, _ := builtinRedactor().RedactJSON([]byte(in))
	want := `{"msg":"caf\u00e9 <x> key=[SECRET_REDACTED]\n\u00e9nd"}`
	if string(out) != want {
		t.Fatalf("got %s\nwant %s", out, want)
	}
	// A secret spelled with escapes is still found; the whole escaped range
	// is replaced.
	esc := strings.Replace(key, "K", `\u004b`, 1)
	out, n, _ := builtinRedactor().RedactJSON([]byte(`{"m":"x ` + esc + ` y"}`))
	if n != 1 || string(out) != `{"m":"x [SECRET_REDACTED] y"}` {
		t.Fatalf("escaped secret: got %s", out)
	}
}

// mcpToolResult wraps text as an MCP tools/call result.
func mcpToolResult(text string) []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"type": "text", "text": text}},
	})
	return b
}

func TestRedactJSON_NestedContentText(t *testing.T) {
	g := newFakeGen(5)
	pat := g.githubPAT()
	pw := g.password()
	inner := `{"rows":[{"id":12345678901234567,"name":"<Ann & Bob>","password":"` + pw + `"},{"note":"uses ` + pat + `"}],"n":1.0}`
	doc := mcpToolResult(inner)
	out, n, err := builtinRedactor().RedactJSON(doc)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v out=%s", n, err, out)
	}
	var res struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	wantInner := `{"rows":[{"id":12345678901234567,"name":"<Ann & Bob>","password":"[SECRET_REDACTED]"},{"note":"uses [SECRET_REDACTED]"}],"n":1.0}`
	if res.Content[0].Text != wantInner {
		t.Fatalf("inner text:\n got %s\nwant %s", res.Content[0].Text, wantInner)
	}
	// Everything outside the edited ranges is byte-identical: the outer
	// document still uses json.Marshal's HTML-safe escaping.
	if !bytes.Contains(out, []byte(`\u003cAnn \u0026 Bob\u003e`)) {
		t.Fatalf("outer escaping changed: %s", out)
	}
}

func TestRedactJSON_NestedDepthLimit(t *testing.T) {
	pw := "hunter2hunter2"
	doc := `{"password":"` + pw + `"}`
	for i := 0; i < 4; i++ { // four levels of JSON-in-a-string
		b, _ := json.Marshal(map[string]string{"text": doc})
		doc = string(b)
	}
	out, _, _ := builtinRedactor().RedactJSON([]byte(doc))
	// Level 4 is beyond maxNestedJSONDepth: scanned as text, where a bare
	// password has no pattern.
	if !strings.Contains(string(out), pw) {
		t.Fatalf("expected level-4 content to be left alone: %s", out)
	}
	doc = `{"password":"` + pw + `"}`
	for i := 0; i < 3; i++ {
		b, _ := json.Marshal(map[string]string{"text": doc})
		doc = string(b)
	}
	out, n, _ := builtinRedactor().RedactJSON([]byte(doc))
	if n != 1 || strings.Contains(string(out), pw) {
		t.Fatalf("level-3 password should be redacted: %s", out)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid output %s", out)
	}
}

func TestRedactJSON_NestedInvalidJSONScannedAsText(t *testing.T) {
	key := newFakeGen(6).awsKeyID()
	doc := mcpToolResult(`{"broken": ` + key)
	out, n, _ := builtinRedactor().RedactJSON(doc)
	if n != 1 || strings.Contains(string(out), key) || !json.Valid(out) {
		t.Fatalf("got %s", out)
	}
}

func TestRedactJSON_InvalidFallsBackToBytes(t *testing.T) {
	key := newFakeGen(8).awsKeyID()
	for _, in := range []string{
		`{"k": "` + key + `"`,           // truncated
		`{"k": "` + key + `"} trailing`, // trailing data
		`not json ` + key,
		`{"k": "` + key + "\x01" + `"}`, // raw control character
	} {
		out, n, err := builtinRedactor().RedactJSON([]byte(in))
		if err != nil || n != 1 || strings.Contains(string(out), key) {
			t.Errorf("%q: out=%q n=%d err=%v", in, out, n, err)
		}
	}
	// Deep nesting falls back instead of recursing without bound.
	deep := strings.Repeat("[", maxJSONNesting+1) + strings.Repeat("]", maxJSONNesting+1)
	out, _, _ := builtinRedactor().RedactJSON([]byte(deep))
	if string(out) != deep {
		t.Error("deep nesting should pass through the byte-level path unchanged")
	}
}

func TestRedactJSON_ValidatesLikeEncodingJSON(t *testing.T) {
	r := builtinRedactor()
	for _, in := range []string{
		`{}`, `[]`, `0`, `-0.5e+10`, `"x"`, ` true `, `null`, `{"a":[1,{"b":"c"}]}`,
		`{`, `[1,]`, `{"a" 1}`, `01`, `1.`, `-`, `"\x"`, `"\u12"`, `{"a":1,}`, `tru`, ``, `  `, `[1 2]`, `{"a":1 "b":2}`,
	} {
		_, _, ok := r.redactJSONLossless([]byte(in), 0)
		if ok != json.Valid([]byte(in)) {
			t.Errorf("%q: scanner ok=%v, json.Valid=%v", in, ok, json.Valid([]byte(in)))
		}
	}
}

// randomJSON builds a random JSON document with no secrets: large
// integers, unicode, HTML-significant characters, escapes, random
// whitespace and JSON documents nested inside strings.
func randomJSON(rng *rand.Rand, depth int) string {
	ws := func() string { return []string{"", "", " ", "\n  ", "\t"}[rng.Intn(5)] }
	str := func() string {
		parts := []string{"héllo", "<b>", "&amp;", "日本語", `\"`, `\\`, `\n`, `\u00e9`, `\ud83d\ude00`, `\/`, "x", "42", " ", "&", ">"}
		var b strings.Builder
		for i := rng.Intn(6); i >= 0; i-- {
			b.WriteString(parts[rng.Intn(len(parts))])
		}
		return `"` + b.String() + `"`
	}
	keys := []string{"id", "name", "data", "items", "text", "value", "count", "meta", "é", "a<b"}
	switch k := rng.Intn(9); {
	case depth <= 0 || k < 3:
		switch rng.Intn(6) {
		case 0:
			return strconv.FormatInt(rng.Int63(), 10) + strconv.Itoa(rng.Intn(10))
		case 1:
			return "-" + strconv.Itoa(rng.Intn(1000)) + ".0" + strconv.Itoa(rng.Intn(100)) + "e+" + strconv.Itoa(rng.Intn(30))
		case 2:
			return []string{"true", "false", "null"}[rng.Intn(3)]
		case 3:
			// JSON nested in a string.
			b, _ := json.Marshal(randomJSON(rng, depth-1))
			return string(b)
		default:
			return str()
		}
	case k < 6:
		var b strings.Builder
		b.WriteString("{" + ws())
		for i := rng.Intn(4); i > 0; i-- {
			b.WriteString(`"` + keys[rng.Intn(len(keys))] + `"` + ws() + ":" + ws() + randomJSON(rng, depth-1))
			if i > 1 {
				b.WriteString(ws() + "," + ws())
			}
		}
		b.WriteString(ws() + "}")
		return b.String()
	default:
		var b strings.Builder
		b.WriteString("[" + ws())
		for i := rng.Intn(4); i > 0; i-- {
			b.WriteString(randomJSON(rng, depth-1))
			if i > 1 {
				b.WriteString("," + ws())
			}
		}
		b.WriteString(ws() + "]")
		return b.String()
	}
}

func TestRedactJSON_PropertyRoundTripByteIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(306))
	r := NewRedactorWithOptions(PatternsToRegexps(BuiltInPatterns), RedactorOptions{Entropy: true})
	for i := 0; i < 2000; i++ {
		in := []byte(randomJSON(rng, 5))
		if !json.Valid(in) {
			t.Fatalf("generator produced invalid JSON: %s", in)
		}
		out, n, err := r.RedactJSON(in)
		if err != nil || n != 0 || !bytes.Equal(out, in) {
			t.Fatalf("round trip changed a clean document (n=%d, err=%v):\n in %s\nout %s", n, err, in, out)
		}
	}
}

func TestRedactJSON_PropertySecretsAlwaysRemoved(t *testing.T) {
	// Insert a secret into random documents at random string positions:
	// the output must stay valid JSON and never contain the secret.
	rng := rand.New(rand.NewSource(20))
	g := newFakeGen(21)
	r := builtinRedactor()
	gens := []func() string{g.awsKeyID, g.githubPAT, g.openAIProject, g.googleAPIKey, g.jwt, g.slackBot}
	for i := 0; i < 500; i++ {
		secret := gens[i%len(gens)]()
		in := randomJSON(rng, 4)
		idx := strings.Index(in, `"x`)
		if idx < 0 {
			in = `{"wrap":` + in + `,"s":"pre ` + secret + ` post"}`
		} else {
			in = in[:idx+1] + secret + " " + in[idx+1:]
		}
		out, _, _ := r.RedactJSON([]byte(in))
		if strings.Contains(string(out), secret) {
			t.Fatalf("secret survived:\n in %s\nout %s", in, out)
		}
		if !json.Valid(out) {
			t.Fatalf("invalid output:\n in %s\nout %s", in, out)
		}
	}
}

func TestRedactJSON_NoSecretReturnsInputWithoutCopy(t *testing.T) {
	in := []byte(`{"a":"b"}`)
	out, _, _ := builtinRedactor().RedactJSON(in)
	if &out[0] != &in[0] {
		t.Error("expected the input slice to be returned unchanged")
	}
}

func BenchmarkRedactJSONStructured20MBWithSecrets(b *testing.B) {
	data := buildStructuredPayload(20 << 20)
	g := newFakeGen(1)
	// One secret roughly every 64 KB.
	var buf bytes.Buffer
	for i, part := range bytes.Split(data, []byte(`"owner":null`)) {
		if i > 0 {
			if i%120 == 0 {
				buf.WriteString(`"owner":"` + g.githubPAT() + `"`)
			} else {
				buf.WriteString(`"owner":null`)
			}
		}
		buf.Write(part)
	}
	data = buf.Bytes()
	r := builtinRedactor()
	quietLogs(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, n, _ := r.RedactJSON(data); n == 0 {
			b.Fatal("expected redactions")
		}
	}
}

func BenchmarkRedactJSONStructured20MBEntropy(b *testing.B) {
	data := buildStructuredPayload(20 << 20)
	r := NewRedactorWithOptions(PatternsToRegexps(BuiltInPatterns), RedactorOptions{Entropy: true})
	quietLogs(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := r.RedactJSON(data); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRedactJSON_MutatedInputsNeverPanic(t *testing.T) {
	// Random byte mutations of valid documents: the scanner must agree with
	// encoding/json on validity and never panic; invalid inputs go through
	// the byte-level path.
	rng := rand.New(rand.NewSource(99))
	r := builtinRedactor()
	alphabet := []byte("{}[]\":,\\u0123456789abcdefntrl-+.eE \t\n\x00\xff")
	for i := 0; i < 3000; i++ {
		in := []byte(randomJSON(rng, 3))
		for k := rng.Intn(3) + 1; k > 0 && len(in) > 0; k-- {
			in[rng.Intn(len(in))] = alphabet[rng.Intn(len(alphabet))]
		}
		_, _, ok := r.redactJSONLossless(in, 0)
		if ok != json.Valid(in) {
			t.Fatalf("validity mismatch (scanner %v, encoding/json %v) for %q", ok, json.Valid(in), in)
		}
		if _, _, err := r.RedactJSON(in); err != nil {
			t.Fatal(err)
		}
	}
}
