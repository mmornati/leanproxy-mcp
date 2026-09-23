package toolsearch

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEmbedder maps text to a fixed 3-d vector by keyword, so the dense
// ranking is known in advance.
type fakeEmbedder struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return nil, errors.New("embedder down")
	}
	text = strings.ToLower(text)
	switch {
	case strings.Contains(text, "watch"), strings.Contains(text, "device"):
		return []float32{1, 0, 0}, nil
	case strings.Contains(text, "sleep"):
		return []float32{0, 1, 0}, nil
	default:
		return []float32{0, 0, 1}, nil
	}
}

func (f *fakeEmbedder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func hybridIndex(t *testing.T, emb Embedder) *Index {
	t.Helper()
	ix := New(Options{})
	ix.SetServerTools("garmin", []Tool{
		{Name: "get_devices", Description: "List registered hardware."},
		{Name: "get_sleep_data", Description: "Sleep stages for a date."},
		{Name: "link_account", Description: "Link a watch to an account."},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ix.EnableHybrid(ctx, emb)
	if _, err := ix.EmbedPending(ctx); err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestRRF(t *testing.T) {
	bm := []scored{{doc: 2, score: 9}, {doc: 0, score: 5}}
	dense := []scored{{doc: 0, score: 0.9}, {doc: 1, score: 0.5}, {doc: 2, score: 0.1}}
	got := rrf(bm, dense)
	// doc0: 1/62 + 1/61; doc2: 1/61 + 1/63; doc1: 1/62.
	wantOrder := []int{0, 2, 1}
	for i, s := range got {
		if s.doc != wantOrder[i] {
			t.Fatalf("rrf order = %+v, want docs %v", got, wantOrder)
		}
	}
	if want := 1.0/62 + 1.0/61; math.Abs(got[0].score-want) > 1e-12 {
		t.Fatalf("fused score = %v, want %v", got[0].score, want)
	}
	// A tie is broken by doc order (server, name).
	tie := rrf([]scored{{doc: 3, score: 1}}, []scored{{doc: 1, score: 1}})
	if tie[0].doc != 1 || tie[1].doc != 3 {
		t.Fatalf("tie order = %+v", tie)
	}
}

func TestHybridFusesEmbeddingRanking(t *testing.T) {
	emb := &fakeEmbedder{}
	ix := hybridIndex(t, emb)
	ctx := context.Background()

	// BM25 alone only finds link_account ("account"); the embedding
	// ranking puts get_devices ("device" ~ "watch") first as well, so the
	// fusion ranks both, and returns tools BM25 never matched.
	bmOnly := New(Options{})
	bmOnly.SetServerTools("garmin", []Tool{
		{Name: "get_devices", Description: "List registered hardware."},
		{Name: "get_sleep_data", Description: "Sleep stages for a date."},
		{Name: "link_account", Description: "Link a watch to an account."},
	})
	q := Query{Text: "which watches are on my account"}
	if got := names(bmOnly.Search(ctx, q)); !reflect.DeepEqual(got, []string{"garmin/link_account"}) {
		t.Fatalf("BM25 only = %v", got)
	}
	got := names(ix.Search(ctx, q))
	if len(got) != 3 || got[0] != "garmin/link_account" || got[1] != "garmin/get_devices" {
		t.Fatalf("hybrid = %v, want link_account then get_devices", got)
	}

	// The server filter applies to the dense ranking too.
	if got := ix.Search(ctx, Query{Text: "watch", Server: "other"}); len(got) != 0 {
		t.Fatalf("filtered hybrid = %v", names(got))
	}
}

func TestHybridCachesVectorsByDefinitionHash(t *testing.T) {
	emb := &fakeEmbedder{}
	ix := hybridIndex(t, emb)
	ctx := context.Background()
	before := emb.count()
	// Same definitions again: nothing to embed.
	ix.SetServerTools("garmin", []Tool{
		{Name: "get_devices", Description: "List registered hardware."},
		{Name: "get_sleep_data", Description: "Sleep stages for a date."},
		{Name: "link_account", Description: "Link a watch to an account."},
	})
	if n, err := ix.EmbedPending(ctx); err != nil || n != 0 {
		t.Fatalf("re-embedded %d unchanged tools (err %v)", n, err)
	}
	// One changed definition: exactly one embedding.
	ix.SetServerTools("garmin", []Tool{
		{Name: "get_devices", Description: "List registered watches."},
		{Name: "get_sleep_data", Description: "Sleep stages for a date."},
		{Name: "link_account", Description: "Link a watch to an account."},
	})
	if n, err := ix.EmbedPending(ctx); err != nil || n > 1 {
		t.Fatalf("embedded %d tools after one change (err %v)", n, err)
	}
	if emb.count()-before > 1+2 { // one tool + at most the background loop racing us
		t.Fatalf("%d embed calls for one changed tool", emb.count()-before)
	}
	hs := ix.hybrid.Load()
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	if len(hs.vectors) != 3 {
		t.Fatalf("%d cached vectors, want 3 (stale one dropped)", len(hs.vectors))
	}
}

func TestHybridFallsBackToBM25(t *testing.T) {
	emb := &fakeEmbedder{}
	ix := hybridIndex(t, emb)
	emb.mu.Lock()
	emb.fail = true
	emb.mu.Unlock()
	got := names(ix.Search(context.Background(), Query{Text: "which watches are on my account"}))
	if !reflect.DeepEqual(got, []string{"garmin/link_account"}) {
		t.Fatalf("fallback = %v, want the BM25 answer", got)
	}
}

func TestHybridBackgroundEmbedding(t *testing.T) {
	emb := &fakeEmbedder{}
	ix := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ix.EnableHybrid(ctx, emb)
	ix.EnableHybrid(ctx, &fakeEmbedder{}) // second call is a no-op
	if !ix.HybridEnabled() {
		t.Fatal("hybrid not enabled")
	}
	ix.SetServerTools("garmin", []Tool{{Name: "get_sleep_data", Description: "Sleep."}})
	deadline := time.Now().Add(10 * time.Second)
	for {
		hs := ix.hybrid.Load()
		hs.mu.RLock()
		n := len(hs.vectors)
		hs.mu.RUnlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background embedder never embedded the tool")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHybridOffByDefault(t *testing.T) {
	ix := New(Options{})
	if ix.HybridEnabled() {
		t.Fatal("hybrid enabled by default")
	}
	if n, err := ix.EmbedPending(context.Background()); n != 0 || err != nil {
		t.Fatalf("EmbedPending without hybrid = %d, %v", n, err)
	}
	ix.EnableHybrid(context.Background(), nil)
	if ix.HybridEnabled() {
		t.Fatal("nil embedder enabled hybrid mode")
	}
}

func TestCosine(t *testing.T) {
	if c, ok := cosine([]float32{1, 0}, []float32{1, 0}); !ok || c != 1 {
		t.Fatalf("cosine = %v %v", c, ok)
	}
	if _, ok := cosine([]float32{1}, []float32{1, 0}); ok {
		t.Fatal("length mismatch accepted")
	}
	if _, ok := cosine([]float32{0, 0}, []float32{1, 0}); ok {
		t.Fatal("zero vector accepted")
	}
}

func TestEmbedText(t *testing.T) {
	got := embedText(Tool{Server: "s", Name: "n", Title: "T", Description: "d", InputSchema: schema("b", "a")})
	if got != "s n (T): d Parameters: a b" {
		t.Fatalf("embedText = %q", got)
	}
}
