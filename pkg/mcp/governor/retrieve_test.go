package governor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReadPage_ConcatenationIsTheOriginal(t *testing.T) {
	for name, data := range map[string]string{
		"lines":     bigText(3500),
		"multibyte": strings.Repeat("héllo wörld ✓ 日本語 ", 9000),
		"one line":  strings.Repeat("x", 50_000),
		"empty":     "",
	} {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			offset, pages := 0, 0
			for {
				p, err := ReadPage(data, offset, 16000)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Text) > 16000 {
					t.Fatalf("page of %d bytes", len(p.Text))
				}
				b.WriteString(p.Text)
				pages++
				if p.Done() {
					break
				}
				if p.End <= offset {
					t.Fatal("no progress")
				}
				offset = p.End
			}
			if b.String() != data {
				t.Fatalf("concatenated pages differ from the original (%d vs %d bytes, %d pages)", b.Len(), len(data), pages)
			}
		})
	}
}

func TestReadPage_CutsOnLinesAndRejectsBadOffsets(t *testing.T) {
	data := bigText(100)
	p, _ := ReadPage(data, 0, 1000)
	if !strings.HasSuffix(p.Text, "\n") {
		t.Errorf("page does not end on a line: %q", p.Text[len(p.Text)-20:])
	}
	if _, err := ReadPage(data, -1, 10); err == nil {
		t.Error("negative offset accepted")
	}
	if _, err := ReadPage(data, len(data)+1, 10); err == nil {
		t.Error("offset past the end accepted")
	}
	// A mid-rune offset snaps back to the rune start.
	p, _ = ReadPage("a✓b", 2, 10)
	if p.Text != "✓b" || p.Offset != 1 {
		t.Errorf("page = %+v", p)
	}
}

func TestGrep_LineNumbersAndContext(t *testing.T) {
	data := bigText(100)
	res, err := Grep(data, `line 0005[05]`, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"48-line 00048: the quick brown fox jumps over the lazy dog",
		"49-line 00049: the quick brown fox jumps over the lazy dog",
		"50:line 00050: the quick brown fox jumps over the lazy dog",
		"51-line 00051: the quick brown fox jumps over the lazy dog",
		"52-line 00052: the quick brown fox jumps over the lazy dog",
		"--",
		"53-line 00053: the quick brown fox jumps over the lazy dog",
		"54-line 00054: the quick brown fox jumps over the lazy dog",
		"55:line 00055: the quick brown fox jumps over the lazy dog",
		"56-line 00056: the quick brown fox jumps over the lazy dog",
		"57-line 00057: the quick brown fox jumps over the lazy dog",
		"",
	}, "\n")
	// Contiguous groups are not separated.
	want = strings.Replace(want, "--\n", "", 1)
	if res.Text != want || res.Total != 2 || res.Matches != 2 || res.NextLine != 0 {
		t.Fatalf("grep = %+v\nwant:\n%s", res, want)
	}
}

func TestGrep_SeparatorsLimitAndContinuation(t *testing.T) {
	data := bigText(1000)
	res, err := Grep(data, `line 00(1|5)00`, 1, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "\n--\n") || res.Total != 2 {
		t.Fatalf("grep = %+v", res)
	}
	// Every line matches: the output stops at the limit and says where to go on.
	res, err = Grep(data, `fox`, 1, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Text) > 2000 || res.NextLine == 0 || res.Total != 1000 {
		t.Fatalf("limited grep = %d bytes, next %d, total %d", len(res.Text), res.NextLine, res.Total)
	}
	next, _ := Grep(data, `fox`, res.NextLine, 2000)
	if !strings.HasPrefix(next.Text, fmt.Sprintf("%d:", res.NextLine)) {
		t.Fatalf("continuation starts with %.30q, want line %d", next.Text, res.NextLine)
	}
	if _, err := Grep(data, `(`, 0, 100); err == nil {
		t.Error("invalid regex accepted")
	}
	if _, err := Grep(data, strings.Repeat("a", maxPatternLen+1), 0, 100); err == nil {
		t.Error("oversized pattern accepted")
	}
}

func TestJSONPath(t *testing.T) {
	arr := jsonArray(1000)
	out, n, err := JSONPath(arr, "$[500:510]")
	if err != nil {
		t.Fatal(err)
	}
	var items []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(out, &items); err != nil || n != 10 || len(items) != 10 || items[0].ID != 500 || items[9].ID != 509 {
		t.Fatalf("$[500:510] = %d items %v (%v)", n, items, err)
	}
	doc := []byte(`{"total":2,"items":[{"name":"a","user":{"name":"u1"}},{"name":"b","n":12345678901234567890}],"meta":{"name":"m"}}`)
	for expr, want := range map[string]string{
		"$.total":           `[2]`,
		"$.items[0].name":   `["a"]`,
		"$.items[-1].n":     `[12345678901234567890]`,
		"$['meta']['name']": `["m"]`,
		"$.items[*].name":   `["a","b"]`,
		"$..name":           `["a","u1","b","m"]`,
		"$.items[1:]":       `[{"name":"b","n":12345678901234567890}]`,
		"$.meta.*":          `["m"]`,
		"$.missing":         `[]`,
		"$":                 "[" + string(doc) + "]",
	} {
		got, _, err := JSONPath(doc, expr)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %s, want %s", expr, got, want)
		}
	}
	for _, bad := range []string{"items", "$.", "$[", "$[x]", "$[1:y]", "$..", "$ foo"} {
		if _, _, err := JSONPath(doc, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
