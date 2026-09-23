package toolsearch

import (
	"context"
	"math"
	"strings"
	"sync"
	"time"
)

// Embedder turns text into a vector. pkg/cache/embedder's Ollama and OpenAI
// clients are adapted to it by the caller.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// RRFK is the Reciprocal Rank Fusion constant: a document ranked r (1 based)
// in a list contributes 1/(RRFK+r) to its fused score.
const RRFK = 60

// queryEmbedTimeout bounds the query embedding of one search; on timeout the
// search answers with BM25 only.
const queryEmbedTimeout = 3 * time.Second

// embedRetryInterval is how often the background embedder retries tools it
// could not embed yet.
const embedRetryInterval = time.Minute

// hybridState is the optional embedding side of the index.
type hybridState struct {
	emb  Embedder
	kick chan struct{}

	mu      sync.RWMutex
	vectors map[string][]float32 // by tool definition hash
}

// EnableHybrid turns on hybrid ranking: tool documents are embedded in the
// background (one goroutine, stopped when ctx ends), their vectors cached by
// tool-definition hash, and every search fuses the BM25 ranking with the
// cosine ranking by Reciprocal Rank Fusion. Tools not embedded yet only
// take part through BM25. Only the first call has an effect.
func (ix *Index) EnableHybrid(ctx context.Context, emb Embedder) {
	if emb == nil {
		return
	}
	hs := &hybridState{emb: emb, kick: make(chan struct{}, 1), vectors: make(map[string][]float32)}
	if !ix.hybrid.CompareAndSwap(nil, hs) {
		return
	}
	go ix.embedLoop(ctx, hs)
	ix.kickEmbedder()
}

// HybridEnabled reports whether EnableHybrid was called.
func (ix *Index) HybridEnabled() bool { return ix.hybrid.Load() != nil }

func (ix *Index) kickEmbedder() {
	if hs := ix.hybrid.Load(); hs != nil {
		select {
		case hs.kick <- struct{}{}:
		default:
		}
	}
}

func (ix *Index) embedLoop(ctx context.Context, hs *hybridState) {
	ticker := time.NewTicker(embedRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hs.kick:
		case <-ticker.C:
		}
		if _, err := ix.EmbedPending(ctx); err != nil && ctx.Err() == nil {
			ix.logger.Warn("tool search: embedding tool descriptions failed; hybrid ranking uses BM25 for the missing tools until the next retry", "error", err)
		}
	}
}

// EmbedPending embeds every indexed tool without a cached vector and drops
// the vectors of tools no longer indexed. It stops at the first error. It
// returns the number of tools embedded. Without hybrid mode it does nothing.
func (ix *Index) EmbedPending(ctx context.Context) (int, error) {
	hs := ix.hybrid.Load()
	if hs == nil {
		return 0, nil
	}
	snap := ix.snap.Load()
	live := make(map[string]struct{}, len(snap.docs))
	var missing []*doc
	hs.mu.RLock()
	for _, d := range snap.docs {
		live[d.hash] = struct{}{}
		if _, ok := hs.vectors[d.hash]; !ok {
			missing = append(missing, d)
		}
	}
	hs.mu.RUnlock()

	hs.mu.Lock()
	for h := range hs.vectors {
		if _, ok := live[h]; !ok {
			delete(hs.vectors, h)
		}
	}
	hs.mu.Unlock()

	done := 0
	for _, d := range missing {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		v, err := hs.emb.Embed(ctx, embedText(d.tool))
		if err != nil {
			return done, err
		}
		if len(v) == 0 {
			continue
		}
		hs.mu.Lock()
		hs.vectors[d.hash] = v
		hs.mu.Unlock()
		done++
	}
	return done, nil
}

// embedText is the text embedded for a tool.
func embedText(t Tool) string {
	var sb strings.Builder
	sb.WriteString(t.Server)
	sb.WriteString(" ")
	sb.WriteString(t.Name)
	if t.Title != "" {
		sb.WriteString(" (")
		sb.WriteString(t.Title)
		sb.WriteString(")")
	}
	sb.WriteString(": ")
	sb.WriteString(t.Description)
	if ps := schemaParams(t.InputSchema); len(ps) > 0 {
		sb.WriteString(" Parameters:")
		for _, p := range ps {
			sb.WriteString(" ")
			sb.WriteString(p.name)
		}
	}
	return sb.String()
}

// fuse combines the BM25 ranking with the embedding ranking of q by
// Reciprocal Rank Fusion. When the query cannot be embedded it returns bm
// unchanged.
func (hs *hybridState) fuse(ctx context.Context, snap *snapshot, q Query, bm []scored, logf func(msg string, args ...any)) []scored {
	if len(snap.docs) == 0 || strings.TrimSpace(q.Text) == "" {
		return bm
	}
	qctx, cancel := context.WithTimeout(ctx, queryEmbedTimeout)
	defer cancel()
	qv, err := hs.emb.Embed(qctx, q.Text)
	if err != nil || len(qv) == 0 {
		logf("tool search: query embedding failed, using BM25 only", "error", err)
		return bm
	}

	dense := make([]scored, 0, len(snap.docs))
	hs.mu.RLock()
	for i, d := range snap.docs {
		if q.skip(d.tool) {
			continue
		}
		v, ok := hs.vectors[d.hash]
		if !ok {
			continue
		}
		if c, ok := cosine(qv, v); ok {
			dense = append(dense, scored{doc: i, score: c})
		}
	}
	hs.mu.RUnlock()
	sortScored(dense)
	return rrf(bm, dense)
}

// rrf fuses rankings by Reciprocal Rank Fusion (constant RRFK), best first,
// ties broken by (server, name).
func rrf(lists ...[]scored) []scored {
	fused := make(map[int]float64)
	for _, l := range lists {
		for r, s := range l {
			fused[s.doc] += 1 / float64(RRFK+r+1)
		}
	}
	out := make([]scored, 0, len(fused))
	for d, s := range fused {
		out = append(out, scored{doc: d, score: s})
	}
	sortScored(out)
	return out
}

// cosine returns the cosine similarity of a and b; ok is false when they
// differ in length or one is all zeros.
func cosine(a, b []float32) (float64, bool) {
	if len(a) != len(b) || len(a) == 0 {
		return 0, false
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), true
}
