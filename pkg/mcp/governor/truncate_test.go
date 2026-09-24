package governor

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const testID = "r_abcdefghijklmnopqrstuvwxyz"

// bigText is n lines of numbered prose (about 60 bytes each).
func bigText(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %05d: the quick brown fox jumps over the lazy dog\n", i)
	}
	return b.String()
}

func TestTruncateText_HeadTailMarkerWithinBudget(t *testing.T) {
	s := bigText(3500) // ~200 KB
	if len(s) < 190_000 {
		t.Fatalf("fixture too small: %d", len(s))
	}
	budget := 4000 * BytesPerToken
	cut, ok := TruncateText(s, budget, testID)
	if !ok {
		t.Fatal("expected truncation")
	}
	if Tokens(len(cut.Text)) > 4000 {
		t.Fatalf("truncated text is %d tokens, want <= 4000", Tokens(len(cut.Text)))
	}
	if !strings.HasPrefix(cut.Text, "line 00001:") {
		t.Errorf("head missing: %.80q", cut.Text)
	}
	if !strings.HasSuffix(cut.Text, "line 03500: the quick brown fox jumps over the lazy dog\n") {
		t.Errorf("tail missing: %q", cut.Text[len(cut.Text)-80:])
	}
	marker := TextMarker(cut.OmittedTokens, testID, cut.Offset)
	if !strings.Contains(cut.Text, "\n"+marker+"\n") {
		t.Fatalf("marker line missing or not on its own line")
	}
	if !strings.Contains(marker, "tokens omitted — call read_result with id="+testID+", offset=") {
		t.Errorf("unexpected marker %q", marker)
	}
	// Both cuts are on line boundaries.
	if s[cut.Offset-1] != '\n' {
		t.Errorf("head does not end on a line boundary")
	}
	head, _, _ := strings.Cut(cut.Text, marker)
	if head != s[:cut.Offset] {
		t.Errorf("head is not the original prefix")
	}
	if len(head) > budget*headShare/100 {
		t.Errorf("head %d bytes > 70%% of budget", len(head))
	}
	if cut.OmittedTokens < 40000 {
		t.Errorf("omitted tokens %d look wrong", cut.OmittedTokens)
	}
}

func TestTruncateText_NeverSplitsARune(t *testing.T) {
	// One long line of multi-byte runes: no line break to cut on.
	s := strings.Repeat("héllo wörld ✓ 日本語 ", 5000)
	for _, budget := range []int{1000, 1001, 1002, 1003, 4097, 16000} {
		cut, ok := TruncateText(s, budget, testID)
		if !ok {
			t.Fatalf("budget %d: expected truncation", budget)
		}
		if !json.Valid(mustMarshal(t, cut.Text)) || !strings.Contains(cut.Text, "LeanProxy") {
			t.Fatal("invalid text")
		}
		if len(cut.Text) > budget {
			t.Errorf("budget %d: got %d bytes", budget, len(cut.Text))
		}
		if !utf8Valid(cut.Text) {
			t.Errorf("budget %d: output is not valid UTF-8", budget)
		}
	}
}

func TestTruncateText_FitsOrTooSmall(t *testing.T) {
	if _, ok := TruncateText("short", 100, testID); ok {
		t.Error("a text that fits must not be truncated")
	}
	if _, ok := TruncateText(strings.Repeat("x", 1000), 50, testID); ok {
		t.Error("a budget smaller than the marker must be refused")
	}
}

func TestTruncateJSON_ArrayKeepsFirstElementsAndCountsTheRest(t *testing.T) {
	doc := jsonArray(1000)
	budget := 4000 * BytesPerToken
	out, ok := TruncateJSON(doc, budget, testID)
	if !ok {
		t.Fatal("expected truncation")
	}
	if len(out) > budget {
		t.Fatalf("got %d bytes > budget %d", len(out), budget)
	}
	var elems []map[string]json.RawMessage
	if err := json.Unmarshal(out, &elems); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	last := elems[len(elems)-1]
	var om struct {
		Items    int    `json:"items"`
		ResultID string `json:"result_id"`
	}
	if err := json.Unmarshal(last[OmittedKey], &om); err != nil {
		t.Fatalf("no omission object in %s", out[len(out)-120:])
	}
	kept := len(elems) - 1
	if om.Items != 1000-kept || om.ResultID != testID {
		t.Errorf("omission = %+v, kept %d", om, kept)
	}
	// Kept elements are the first ones, byte for byte.
	orig, _ := SplitArray(doc)
	got, _ := SplitArray(out)
	for i := 0; i < kept; i++ {
		if string(orig[i]) != string(got[i]) {
			t.Fatalf("element %d changed", i)
		}
	}
}

func TestTruncateJSON_ObjectKeepsSmallMembersAndShrinksTheBigOne(t *testing.T) {
	doc := []byte(`{"total":1000,"items":` + string(jsonArray(1000)) + `,"next":"cursor-2","big_id":12345678901234567890}`)
	out, ok := TruncateJSON(doc, 8000, testID)
	if !ok {
		t.Fatal("expected truncation")
	}
	if len(out) > 8000 {
		t.Fatalf("got %d bytes", len(out))
	}
	members, err := SplitObject(out)
	if err != nil {
		t.Fatalf("invalid object: %v", err)
	}
	keys := []string{}
	for _, m := range members {
		keys = append(keys, string(m.Key))
	}
	if strings.Join(keys, ",") != `"total","items","next","big_id"` {
		t.Errorf("member order/keys changed: %v", keys)
	}
	if !strings.Contains(string(out), `"big_id":12345678901234567890`) {
		t.Error("large integer not preserved byte for byte")
	}
	if !strings.Contains(string(out), `{"`+OmittedKey+`":{"items":`) {
		t.Error("nested array has no omission element")
	}
}

func TestTruncateJSON_LongStringAndHTML(t *testing.T) {
	doc := []byte(`{"body":` + string(mustMarshal(t, strings.Repeat("<b>&amp;</b> ", 5000))) + `}`)
	out, ok := TruncateJSON(doc, 2000, testID)
	if !ok || !json.Valid(out) || len(out) > 2000 {
		t.Fatalf("ok=%v valid=%v len=%d", ok, json.Valid(out), len(out))
	}
	if strings.Contains(string(out), `\u003c`) {
		t.Error("HTML characters were escaped")
	}
	if !strings.Contains(string(out), "result_id="+testID) {
		t.Error("string marker missing")
	}
}

func TestTruncateJSON_AlwaysValidAndWithinBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		v := randomJSON(rng, 0)
		doc := mustMarshal(t, v)
		budget := 200 + rng.Intn(3000)
		out, ok := TruncateJSON(doc, budget, testID)
		if !ok {
			if len(doc) > budget {
				t.Fatalf("case %d: not truncated (%d bytes > %d)", i, len(doc), budget)
			}
			continue
		}
		if !json.Valid(out) {
			t.Fatalf("case %d: invalid JSON: %s", i, out)
		}
		if len(out) > budget {
			t.Fatalf("case %d: %d bytes > budget %d", i, len(out), budget)
		}
	}
}

func TestWaterFill(t *testing.T) {
	got := WaterFill([]int{10, 1000, 20, 5000}, 1030)
	want := []int{10, 500, 20, 500}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("WaterFill = %v, want %v", got, want)
		}
	}
}

func TestIsJSON(t *testing.T) {
	for s, want := range map[string]bool{`[1,2]`: true, ` {"a":1} `: true, `"x"`: false, `12`: false, `{bad`: false, ``: false} {
		if IsJSON(s) != want {
			t.Errorf("IsJSON(%q) != %v", s, want)
		}
	}
}

func jsonArray(n int) []byte {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{"id": i, "title": fmt.Sprintf("Issue number %d", i), "state": "open", "labels": []string{"bug", "p1"}}
	}
	b, _ := json.Marshal(items)
	return b
}

func randomJSON(rng *rand.Rand, depth int) any {
	switch k := rng.Intn(6); {
	case depth > 3 || k == 0:
		return strings.Repeat("ab✓", rng.Intn(200))
	case k == 1:
		return rng.Int63()
	case k == 2 || k == 3:
		n := rng.Intn(25)
		a := make([]any, n)
		for i := range a {
			a[i] = randomJSON(rng, depth+1)
		}
		return a
	default:
		n := rng.Intn(12)
		m := make(map[string]any, n)
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("k%d", i)] = randomJSON(rng, depth+1)
		}
		return m
	}
}

func mustMarshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := MarshalNoEscape(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "\uFFFD") == s }
