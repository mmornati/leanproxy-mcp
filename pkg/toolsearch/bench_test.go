package toolsearch

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"
)

// syntheticIndex indexes about n tools: the 118-tool catalog replicated
// under distinct server names, so term statistics stay realistic.
func syntheticIndex(t testing.TB, n int) *Index {
	t.Helper()
	base := loadCatalogIndex(t, Options{})
	snap := base.snap.Load()
	ix := New(Options{})
	for copyN := 0; ix.Len() < n; copyN++ {
		byServer := map[string][]Tool{}
		for _, d := range snap.docs {
			server := fmt.Sprintf("%s%d", d.tool.Server, copyN)
			byServer[server] = append(byServer[server], d.tool)
		}
		for s, tools := range byServer {
			ix.SetServerTools(s, tools)
		}
	}
	return ix
}

// queryLatencies runs every labeled intent rounds times and returns the
// per-query durations, sorted.
func queryLatencies(t testing.TB, ix *Index, rounds int) []time.Duration {
	intents := loadIntents(t)
	out := make([]time.Duration, 0, rounds*len(intents))
	ctx := context.Background()
	for r := 0; r < rounds; r++ {
		for _, in := range intents {
			start := time.Now()
			ix.Search(ctx, Query{Text: in.Query, K: 5})
			out = append(out, time.Since(start))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func p99(d []time.Duration) time.Duration { return d[(len(d)*99)/100] }

// TestSearchLatency1000Tools checks the #305 latency target (p99 < 1 ms
// for 1,000 tools) with a generous margin, since CI runners and the race
// detector are several times slower; BenchmarkSearch1000Tools reports the
// real number.
func TestSearchLatency1000Tools(t *testing.T) {
	ix := syntheticIndex(t, 1000)
	if ix.Len() < 1000 {
		t.Fatalf("indexed %d tools, want >= 1000", ix.Len())
	}
	queryLatencies(t, ix, 2) // warm up
	lat := queryLatencies(t, ix, 10)
	t.Logf("%d tools, %d queries: p50 %s, p99 %s", ix.Len(), len(lat), lat[len(lat)/2], p99(lat))
	if limit := 20 * time.Millisecond; p99(lat) > limit {
		t.Errorf("p99 search latency %s over %s", p99(lat), limit)
	}
}

func BenchmarkSearch1000Tools(b *testing.B) {
	ix := syntheticIndex(b, 1000)
	intents := loadIntents(b)
	ctx := context.Background()
	lat := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in := intents[i%len(intents)]
		start := time.Now()
		ix.Search(ctx, Query{Text: in.Query, K: 5})
		lat = append(lat, time.Since(start))
	}
	b.StopTimer()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	b.ReportMetric(float64(p99(lat).Microseconds()), "p99-µs")
	b.ReportMetric(float64(ix.Len()), "tools")
}

func BenchmarkSetServerTools(b *testing.B) {
	ix := syntheticIndex(b, 1000)
	base := loadCatalogIndex(b, Options{})
	var tools []Tool
	for _, d := range base.snap.Load().docs {
		if d.tool.Server == "github" {
			tools = append(tools, d.tool)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.SetServerTools("github0", tools)
	}
}
