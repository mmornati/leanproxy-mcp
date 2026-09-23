package toolsearch

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

type catalogFile struct {
	Servers []struct {
		Name  string `json:"name"`
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	} `json:"servers"`
}

type intent struct {
	Query  string `json:"query"`
	Server string `json:"server"`
	Tool   string `json:"tool"`
	Source string `json:"source"`
}

// loadCatalogIndex indexes testdata/catalog.json (the 118-tool audit
// catalog, also used by tests/harness).
func loadCatalogIndex(t testing.TB, opts Options) *Index {
	t.Helper()
	data, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var cat catalogFile
	if err := json.Unmarshal(data, &cat); err != nil {
		t.Fatal(err)
	}
	ix := New(opts)
	for _, s := range cat.Servers {
		tools := make([]Tool, 0, len(s.Tools))
		for _, tl := range s.Tools {
			tools = append(tools, Tool{Name: tl.Name, Description: tl.Description, InputSchema: tl.InputSchema})
		}
		ix.SetServerTools(s.Name, tools)
	}
	return ix
}

func loadIntents(t testing.TB) []intent {
	t.Helper()
	data, err := os.ReadFile("testdata/intents.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Intents []intent `json:"intents"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Intents
}

type recall struct{ n, at1, at5 int }

func (r recall) r1() float64 { return float64(r.at1) / float64(r.n) }
func (r recall) r5() float64 { return float64(r.at5) / float64(r.n) }

// TestRecall is the acceptance test of #305: BM25 only (no network), on the
// full labeled set, recall@5 >= 85% and recall@1 >= 65%.
func TestRecall(t *testing.T) {
	ix := loadCatalogIndex(t, Options{})
	if ix.Len() != 118 {
		t.Fatalf("indexed %d tools, want 118", ix.Len())
	}
	intents := loadIntents(t)
	independent := 0
	for _, in := range intents {
		if in.Source == "independent" {
			independent++
		}
	}
	if len(intents) < 74 || independent < 30 {
		t.Fatalf("labeled set has %d intents (%d independent), want >= 74 (>= 30 independent)", len(intents), independent)
	}

	by := map[string]*recall{"all": {}, "audit": {}, "independent": {}}
	for _, in := range intents {
		hits := ix.Search(context.Background(), Query{Text: in.Query, K: 5})
		rank := 0
		for i, h := range hits {
			if h.Tool.Server == in.Server && h.Tool.Name == in.Tool {
				rank = i + 1
				break
			}
		}
		for _, key := range []string{"all", in.Source} {
			r := by[key]
			r.n++
			if rank == 1 {
				r.at1++
			}
			if rank >= 1 {
				r.at5++
			}
		}
		if rank != 1 {
			top := "none"
			if len(hits) > 0 {
				top = hits[0].Tool.Server + "/" + hits[0].Tool.Name
			}
			t.Logf("rank %d for %q (want %s/%s, top %s)", rank, in.Query, in.Server, in.Tool, top)
		}
	}
	for _, key := range []string{"audit", "independent", "all"} {
		r := by[key]
		t.Logf("%-11s n=%d recall@1=%.1f%% recall@5=%.1f%%", key, r.n, 100*r.r1(), 100*r.r5())
	}
	all := by["all"]
	if all.r5() < 0.85 {
		t.Errorf("recall@5 = %.1f%%, want >= 85%%", 100*all.r5())
	}
	if all.r1() < 0.65 {
		t.Errorf("recall@1 = %.1f%%, want >= 65%%", 100*all.r1())
	}
}
