// Package toolsearch is the ranked, cross-server tool index behind the
// search_tools gateway tool (issue #305). It scores every cached tool
// against a natural-language query with Okapi BM25 over the tool's server
// name, tool name, description, title, parameter names and parameter
// descriptions, optionally fused (Reciprocal Rank Fusion) with the cosine
// similarity of embeddings when an Embedder is configured.
//
// The index is fed per server (SetServerTools) from the proxy's tool cache
// and answers queries lock-free from an immutable snapshot, so a refresh of
// one server never blocks a search.
package toolsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Okapi BM25 parameters (issue #305).
const (
	DefaultK1 = 1.2
	DefaultB  = 0.75
)

// DefaultK is the number of hits returned when a query asks for none.
const DefaultK = 5

// MaxK is the largest number of hits a query can ask for.
const MaxK = 20

// Field weights: a term's frequency in a document is the sum of the weights
// of the fields it occurs in.
const (
	weightServer      = 1.0
	weightName        = 2.0
	weightDescription = 1.0
	weightTitle       = 1.0
	weightParamName   = 1.0
	weightParamDesc   = 0.5
	weightAnnotation  = 1.0
)

// Tool is one tool as the index sees it.
type Tool struct {
	Server      string
	Name        string
	Description string
	InputSchema json.RawMessage
	// Title and ReadOnlyHint come from the MCP tool title / annotations
	// when the upstream server provides them.
	Title        string
	ReadOnlyHint bool
}

// Hit is one search result.
type Hit struct {
	Tool  Tool
	Score float64
}

// Query is one search request.
type Query struct {
	Text string
	// K is the number of hits wanted: 0 means DefaultK, and it is capped
	// at MaxK.
	K int
	// Server, when set, restricts the results to that server's tools.
	Server string
	// Exclude, when set, drops the tools it reports before the top K are
	// taken (tool pinning hides tools awaiting approval, #310).
	Exclude func(server, name string) bool
}

// skip reports whether q filters out t.
func (q *Query) skip(t Tool) bool {
	if q.Server != "" && t.Server != q.Server {
		return true
	}
	return q.Exclude != nil && q.Exclude(t.Server, t.Name)
}

// Options configure an Index. The zero value is BM25 with the default
// synonyms.
type Options struct {
	// Synonyms maps a query word to the words it expands to (e.g.
	// "pr" -> "pull request"). Nil means DefaultSynonyms; use an empty,
	// non-nil map for none.
	Synonyms map[string]string
	// Logger receives the hybrid mode's background errors. Nil means
	// slog.Default().
	Logger *slog.Logger
}

// DefaultSynonyms is the small built-in synonym table.
func DefaultSynonyms() map[string]string {
	return map[string]string{
		"pr":     "pull request",
		"ticket": "issue",
		"bug":    "issue",
		"ci":     "workflow action",
		"repo":   "repository",
	}
}

// Index is a BM25 (optionally hybrid) index over the tools of every server.
// All methods are safe for concurrent use.
type Index struct {
	synonyms map[string][]string
	logger   *slog.Logger

	// mu serializes writers (SetServerTools / RemoveServer); readers
	// load snap without locking.
	mu      sync.Mutex
	servers map[string][]*doc
	snap    atomic.Pointer[snapshot]

	hybrid atomic.Pointer[hybridState]
}

// doc is one indexed tool.
type doc struct {
	tool  Tool
	terms map[string]float64 // weighted term frequency
	len   float64            // sum of the weights
	hash  string             // tool definition hash (embedding cache key)
}

type posting struct {
	doc int
	tf  float64
}

// snapshot is an immutable view of the index at one point in time.
type snapshot struct {
	docs     []*doc // sorted by (server, name)
	postings map[string][]posting
	idf      map[string]float64
	norm     []float64 // k1 * (1 - b + b*len/avgLen), per doc
}

// New returns an empty index.
func New(opts Options) *Index {
	syn := opts.Synonyms
	if syn == nil {
		syn = DefaultSynonyms()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ix := &Index{
		synonyms: compileSynonyms(syn),
		logger:   logger,
		servers:  make(map[string][]*doc),
	}
	ix.snap.Store(&snapshot{postings: map[string][]posting{}, idf: map[string]float64{}})
	return ix
}

// compileSynonyms stems the keys and tokenizes the expansions. A key that
// does not reduce to exactly one term, or an expansion with no term, is
// skipped (Config.Validate rejects them earlier).
func compileSynonyms(in map[string]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		keys := Tokenize(k)
		exp := Tokenize(v)
		if len(keys) != 1 || len(exp) == 0 {
			continue
		}
		out[keys[0]] = append(out[keys[0]], exp...)
	}
	return out
}

// SetServerTools replaces the indexed tools of one server. Only that
// server's tools are re-tokenized; the corpus statistics are then rebuilt
// into a new snapshot that queries pick up atomically.
func (ix *Index) SetServerTools(server string, tools []Tool) {
	docs := make([]*doc, 0, len(tools))
	for _, t := range tools {
		t.Server = server
		docs = append(docs, newDoc(t))
	}
	ix.mu.Lock()
	if len(docs) == 0 {
		delete(ix.servers, server)
	} else {
		ix.servers[server] = docs
	}
	ix.rebuildLocked()
	ix.mu.Unlock()
	ix.kickEmbedder()
}

// RemoveServer drops every tool of server from the index.
func (ix *Index) RemoveServer(server string) {
	ix.SetServerTools(server, nil)
}

// Len returns the number of indexed tools.
func (ix *Index) Len() int {
	return len(ix.snap.Load().docs)
}

func newDoc(t Tool) *doc {
	d := &doc{tool: t, terms: make(map[string]float64)}
	add := func(text string, w float64) {
		for _, term := range Tokenize(text) {
			d.terms[term] += w
			d.len += w
		}
	}
	add(t.Server, weightServer)
	add(t.Name, weightName)
	add(t.Description, weightDescription)
	add(t.Title, weightTitle)
	if t.ReadOnlyHint {
		add("read only", weightAnnotation)
	}
	for _, p := range schemaParams(t.InputSchema) {
		add(p.name, weightParamName)
		add(p.description, weightParamDesc)
	}
	d.hash = toolHash(t)
	return d
}

type param struct{ name, description string }

// schemaParams returns the top-level properties of a JSON Schema, sorted
// by name so the document (and its hash) never depends on map order.
func schemaParams(schema json.RawMessage) []param {
	if len(schema) == 0 {
		return nil
	}
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil
	}
	out := make([]param, 0, len(s.Properties))
	for name, raw := range s.Properties {
		var p struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(raw, &p) // a non-object (boolean) schema has no description
		out = append(out, param{name: name, description: p.Description})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// toolHash identifies a tool definition: the embedding cache key.
func toolHash(t Tool) string {
	h := sha256.New()
	for _, part := range []string{t.Server, t.Name, t.Title, t.Description, string(t.InputSchema)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// rebuildLocked recomputes the postings and corpus statistics. ix.mu must
// be held.
func (ix *Index) rebuildLocked() {
	var docs []*doc
	for _, ds := range ix.servers {
		docs = append(docs, ds...)
	}
	sort.Slice(docs, func(i, j int) bool { return lessTool(docs[i].tool, docs[j].tool) })

	snap := &snapshot{
		docs:     docs,
		postings: make(map[string][]posting),
		idf:      make(map[string]float64),
		norm:     make([]float64, len(docs)),
	}
	var total float64
	for i, d := range docs {
		total += d.len
		for term, tf := range d.terms {
			snap.postings[term] = append(snap.postings[term], posting{doc: i, tf: tf})
		}
	}
	n := float64(len(docs))
	for term, ps := range snap.postings {
		df := float64(len(ps))
		snap.idf[term] = logIDF(n, df)
	}
	avg := 1.0
	if len(docs) > 0 && total > 0 {
		avg = total / n
	}
	for i, d := range docs {
		snap.norm[i] = DefaultK1 * (1 - DefaultB + DefaultB*d.len/avg)
	}
	ix.snap.Store(snap)
}

func lessTool(a, b Tool) bool {
	if a.Server != b.Server {
		return a.Server < b.Server
	}
	return a.Name < b.Name
}

// queryTerms tokenizes a query and appends the synonym expansions.
func (ix *Index) queryTerms(text string) []string {
	terms := Tokenize(text)
	n := len(terms)
	for _, t := range terms[:n] {
		exp, ok := ix.synonyms[t]
		if !ok && len(t) > 1 && strings.HasSuffix(t, "s") {
			// Short plurals the stemmer leaves alone ("prs", "bugs").
			exp = ix.synonyms[strings.TrimSuffix(t, "s")]
		}
		terms = append(terms, exp...)
	}
	return terms
}

// Search returns the best hits for q, best first. Ties are broken by
// server then tool name, so the order is deterministic. With hybrid mode
// enabled (EnableHybrid), the BM25 ranking is fused with the embedding
// ranking; if the query cannot be embedded, it falls back to BM25 only.
func (ix *Index) Search(ctx context.Context, q Query) []Hit {
	k := q.K
	if k <= 0 {
		k = DefaultK
	}
	if k > MaxK {
		k = MaxK
	}
	q.Server = strings.TrimSpace(q.Server)
	snap := ix.snap.Load()
	ranked := ix.bm25(snap, q)
	if hs := ix.hybrid.Load(); hs != nil {
		ranked = hs.fuse(ctx, snap, q, ranked, ix.logger.Debug)
	}
	if len(ranked) > k {
		ranked = ranked[:k]
	}
	hits := make([]Hit, len(ranked))
	for i, r := range ranked {
		hits[i] = Hit{Tool: snap.docs[r.doc].tool, Score: r.score}
	}
	return hits
}

type scored struct {
	doc   int
	score float64
}

// bm25 returns every document with a positive BM25 score for q (after the
// server filter), best first.
func (ix *Index) bm25(snap *snapshot, q Query) []scored {
	if len(snap.docs) == 0 {
		return nil
	}
	scores := make([]float64, len(snap.docs))
	for _, term := range ix.queryTerms(q.Text) {
		ps, ok := snap.postings[term]
		if !ok {
			continue
		}
		idf := snap.idf[term]
		for _, p := range ps {
			tf := p.tf
			scores[p.doc] += idf * tf * (DefaultK1 + 1) / (tf + snap.norm[p.doc])
		}
	}
	out := make([]scored, 0, 32)
	for i, s := range scores {
		if s <= 0 {
			continue
		}
		if q.skip(snap.docs[i].tool) {
			continue
		}
		out = append(out, scored{doc: i, score: s})
	}
	sortScored(out)
	return out
}

// sortScored orders by score, then by (server, name): docs are stored in
// (server, name) order, so the doc index is the tie-break.
func sortScored(s []scored) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].score != s[j].score {
			return s[i].score > s[j].score
		}
		return s[i].doc < s[j].doc
	})
}

// Servers returns the names of the indexed servers, sorted.
func (ix *Index) Servers() []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	out := make([]string, 0, len(ix.servers))
	for name := range ix.servers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// logIDF is the BM25 inverse document frequency of a term found in df of
// n documents. The "1 +" keeps it positive for very common terms.
func logIDF(n, df float64) float64 {
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}
