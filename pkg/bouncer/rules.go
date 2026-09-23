package bouncer

import (
	"math"
	"math/bits"
	"regexp"
	"sync"
)

// rule is one compiled redaction pattern plus the metadata the engine needs
// to run it cheaply: the capture group that holds the secret and whether it
// has a keyword prefilter.
type rule struct {
	name  string
	re    *regexp.Regexp
	group int
	// always is set for rules without keywords (custom patterns with no
	// literal prefix): they run over every string.
	always bool
}

// entropyKeywords gate the generic high-entropy detector: a candidate
// token must sit within entropyKeywordDistance bytes of one of them.
var entropyKeywords = []string{"key", "secret", "token", "password"}

const (
	entropyMinTokenLen     = 20
	entropyThreshold       = 4.0
	entropyKeywordDistance = 20
)

// builtinMeta maps a built-in pattern's compiled regexp back to its
// metadata, so NewRedactor([]*regexp.Regexp) (the historical constructor)
// still gets keywords and capture groups for built-ins.
var builtinMeta = func() map[*regexp.Regexp]*SecretPattern {
	m := make(map[*regexp.Regexp]*SecretPattern, len(BuiltInPatterns))
	for i := range BuiltInPatterns {
		m[BuiltInPatterns[i].Pattern] = &BuiltInPatterns[i]
	}
	return m
}()

// kwEntry is one keyword of the prefilter. anchor is the offset of the
// two-byte sequence indexed in kwMatcher.bigrams.
type kwEntry struct {
	kw     string
	anchor int
	fold   bool
	rules  []int
}

// kwMatcher finds which rules may match a piece of text in a single pass:
// every keyword is indexed by one of its byte pairs (the rarest one, by a
// rough frequency estimate), the text is scanned pair by pair against a
// 64 Kbit bitmap, and only a bitmap hit is verified against the keywords
// that share that pair. Single-byte keywords use a 256-entry table.
type kwMatcher struct {
	bigrams [1024]uint64
	entries map[uint16][]*kwEntry
	single  [256][]int
	anySgl  bool
	words   int
}

func (m *kwMatcher) add(kw string, fold bool, ruleIdx int) {
	if kw == "" {
		return
	}
	if fold {
		kw = asciiLower(kw)
	}
	if len(kw) == 1 {
		c := kw[0]
		m.single[c] = appendUniqueInt(m.single[c], ruleIdx)
		if fold && isASCIILetter(c) {
			m.single[c^0x20] = appendUniqueInt(m.single[c^0x20], ruleIdx)
		}
		m.anySgl = true
		return
	}
	// Reuse an existing entry for the same keyword.
	anchor := rarestPair(kw)
	key := uint16(kw[anchor])<<8 | uint16(kw[anchor+1])
	for _, e := range m.entries[key] {
		if e.kw == kw && e.fold == fold {
			e.rules = appendUniqueInt(e.rules, ruleIdx)
			return
		}
	}
	e := &kwEntry{kw: kw, anchor: anchor, fold: fold, rules: []int{ruleIdx}}
	for _, k := range pairVariants(kw[anchor], kw[anchor+1], fold) {
		m.bigrams[k>>6] |= 1 << (k & 63)
		m.entries[k] = append(m.entries[k], e)
	}
}

// scan sets in mask the bit of every rule whose keyword occurs in s and
// reports whether any bit was set.
func (m *kwMatcher) scan(s []byte, mask []uint64) bool {
	hit := false
	n := len(s)
	if n == 0 {
		return false
	}
	if m.anySgl {
		for _, c := range s {
			if rs := m.single[c]; rs != nil {
				for _, ri := range rs {
					mask[ri>>6] |= 1 << (ri & 63)
				}
				hit = true
			}
		}
	}
	for i := 0; i+1 < n; i++ {
		k := uint16(s[i])<<8 | uint16(s[i+1])
		if m.bigrams[k>>6]&(1<<(k&63)) == 0 {
			continue
		}
		for _, e := range m.entries[k] {
			start := i - e.anchor
			if start < 0 || start+len(e.kw) > n {
				continue
			}
			if !keywordAt(s[start:start+len(e.kw)], e.kw, e.fold) {
				continue
			}
			for _, ri := range e.rules {
				mask[ri>>6] |= 1 << (ri & 63)
			}
			hit = true
		}
	}
	return hit
}

func keywordAt(s []byte, kw string, fold bool) bool {
	if !fold {
		return string(s) == kw
	}
	for i := 0; i < len(kw); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != kw[i] {
			return false
		}
	}
	return true
}

func pairVariants(a, b byte, fold bool) []uint16 {
	as := []byte{a}
	bs := []byte{b}
	if fold && isASCIILetter(a) {
		as = append(as, a^0x20)
	}
	if fold && isASCIILetter(b) {
		bs = append(bs, b^0x20)
	}
	out := make([]uint16, 0, len(as)*len(bs))
	for _, x := range as {
		for _, y := range bs {
			out = append(out, uint16(x)<<8|uint16(y))
		}
	}
	return out
}

// rarestPair returns the offset of the byte pair of kw that is least likely
// to occur in ordinary text, so the bitmap rejects as many positions as
// possible.
func rarestPair(kw string) int {
	best, bestScore := 0, math.MaxInt
	for i := 0; i+1 < len(kw); i++ {
		if s := byteCommonness(kw[i]) + byteCommonness(kw[i+1]); s < bestScore {
			best, bestScore = i, s
		}
	}
	return best
}

// byteCommonness is a rough frequency score for a byte in JSON / prose.
func byteCommonness(c byte) int {
	const order = "etaoinsrhldcumfpgwybvkxjqz"
	switch {
	case c >= 'a' && c <= 'z':
		for i := 0; i < len(order); i++ {
			if order[i] == c {
				return 40 - i
			}
		}
	case c >= 'A' && c <= 'Z':
		return 8
	case c >= '0' && c <= '9':
		return 10
	case c == ' ' || c == '"' || c == ',' || c == ':':
		return 50
	case c == '.' || c == '/' || c == '-' || c == '_':
		return 12
	}
	return 4
}

func isASCIILetter(c byte) bool { return (c|0x20) >= 'a' && (c|0x20) <= 'z' }

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func appendUniqueInt(s []int, v int) []int {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// RedactorOptions tunes a Redactor beyond its pattern list.
type RedactorOptions struct {
	// Entropy enables the generic high-entropy detector (off by default):
	// a run of 20+ characters from [A-Za-z0-9+/=_-] with Shannon entropy
	// >= 4.0 bits per character is redacted when a key-like word (key,
	// secret, token, password) is within 20 characters of it, or when it
	// is the value of a JSON key containing such a word.
	Entropy bool
	// Alerts, when set, receives redaction events from RedactStream.
	Alerts *AlertManager
}

// compileRules builds the rule list and keyword matcher for patterns.
// Built-in patterns contribute their keywords and secret group; a custom
// pattern contributes its literal prefix as its keyword, or runs on every
// string when it has none.
func (r *Redactor) compileRules() {
	r.rules = make([]rule, 0, len(r.patterns))
	r.always = r.always[:0]
	m := &kwMatcher{entries: make(map[uint16][]*kwEntry)}
	for _, re := range r.patterns {
		if re == nil {
			continue
		}
		idx := len(r.rules)
		ru := rule{re: re}
		if meta, ok := builtinMeta[re]; ok {
			ru.name = meta.Name
			ru.group = meta.SecretGroup
			for _, kw := range meta.Keywords {
				m.add(kw, meta.KeywordsFold, idx)
			}
			if len(meta.Keywords) == 0 {
				ru.always = true
			}
		} else if prefix, _ := re.LiteralPrefix(); prefix != "" {
			m.add(prefix, false, idx)
		} else {
			ru.always = true
		}
		if ru.always {
			r.always = append(r.always, idx)
		}
		r.rules = append(r.rules, ru)
	}
	r.entropyIdx = len(r.rules)
	if r.entropy {
		for _, kw := range entropyKeywords {
			m.add(kw, true, r.entropyIdx)
		}
	}
	m.words = r.entropyIdx/64 + 1
	r.kw = m
}

// maskWords is the number of uint64 words a rule mask needs.
func (r *Redactor) maskWords() int { return r.kw.words }

// findSpansMasked runs the rules selected by mask (plus the always-on
// rules) over s once each and returns the merged spans, appended to dst.
// keyCtx tells the entropy detector that s is the value of a key-like JSON
// key. mask is cleared on return.
func (r *Redactor) findSpansMasked(s []byte, mask []uint64, keyCtx bool, dst []span) []span {
	dst = dst[:0]
	entropy := false
	for w, word := range mask {
		for word != 0 {
			b := bits.TrailingZeros64(word)
			word &^= 1 << b
			idx := w*64 + b
			if idx == r.entropyIdx {
				entropy = true
				continue
			}
			dst = r.rules[idx].appendSpans(s, dst)
		}
		mask[w] = 0
	}
	for _, idx := range r.always {
		dst = r.rules[idx].appendSpans(s, dst)
	}
	if r.entropy && (entropy || keyCtx) {
		dst = appendEntropySpans(s, keyCtx, dst)
	}
	if len(dst) == 0 {
		return dst
	}
	return mergeSpans(dst)
}

func (ru *rule) appendSpans(s []byte, dst []span) []span {
	if ru.group == 0 {
		for _, loc := range ru.re.FindAllIndex(s, -1) {
			if loc[1] > loc[0] {
				dst = append(dst, span{loc[0], loc[1]})
			}
		}
		return dst
	}
	g := ru.group * 2
	for _, loc := range ru.re.FindAllSubmatchIndex(s, -1) {
		if g+1 < len(loc) && loc[g] >= 0 && loc[g+1] > loc[g] {
			dst = append(dst, span{loc[g], loc[g+1]})
		}
	}
	return dst
}

// maskPool recycles rule masks for findSpansInto.
var maskPool = sync.Pool{New: func() interface{} { s := make([]uint64, 0, 2); return &s }}

func (r *Redactor) acquireMask() []uint64 {
	p := maskPool.Get().(*[]uint64)
	m := (*p)[:0]
	for i := 0; i < r.maskWords(); i++ {
		m = append(m, 0)
	}
	return m
}

func releaseMask(m []uint64) {
	for i := range m {
		m[i] = 0
	}
	m = m[:0]
	maskPool.Put(&m)
}

// isEntropyChar reports whether c belongs to the token alphabet of the
// high-entropy detector.
func isEntropyChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
		c == '+' || c == '/' || c == '=' || c == '_' || c == '-'
}

// shannonEntropy returns the Shannon entropy of s in bits per byte.
func shannonEntropy(s []byte) float64 {
	var counts [256]int
	for _, c := range s {
		counts[c]++
	}
	n := float64(len(s))
	h := 0.0
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// appendEntropySpans appends every high-entropy token of s that is near a
// key-like word (or every one, when keyCtx says s is the value of a
// key-like JSON key).
func appendEntropySpans(s []byte, keyCtx bool, dst []span) []span {
	n := len(s)
	for i := 0; i < n; {
		if !isEntropyChar(s[i]) {
			i++
			continue
		}
		start := i
		for i < n && isEntropyChar(s[i]) {
			i++
		}
		if i-start < entropyMinTokenLen || shannonEntropy(s[start:i]) < entropyThreshold {
			continue
		}
		if keyCtx || keywordNear(s, start, i) {
			dst = append(dst, span{start, i})
		}
	}
	return dst
}

// keywordNear reports whether an entropy keyword occurs within
// entropyKeywordDistance bytes of either edge of the token s[start:end].
// Occurrences deep inside a long token (a base64 blob that happens to
// contain "key") do not count.
func keywordNear(s []byte, start, end int) bool {
	return keywordInWindow(s, start-entropyKeywordDistance, start+entropyKeywordDistance) ||
		keywordInWindow(s, end-entropyKeywordDistance, end+entropyKeywordDistance)
}

// keywordInWindow reports whether an entropy keyword overlaps s[lo:hi].
func keywordInWindow(s []byte, lo, hi int) bool {
	const maxKw = 8 // len("password")
	from := max(0, lo-maxKw+1)
	to := min(len(s), hi+maxKw-1)
	w := s[from:to]
	for _, kw := range entropyKeywords {
		for i := 0; i+len(kw) <= len(w); i++ {
			ks := from + i
			if ks+len(kw) <= lo || ks >= hi {
				continue
			}
			if keywordAt(w[i:i+len(kw)], kw, true) {
				return true
			}
		}
	}
	return false
}

var (
	defaultRedactorOnce sync.Once
	defaultRedactor     *Redactor
)

// DefaultRedactor returns a shared redactor built from the built-in
// patterns. It is what RedactSecrets and the log redaction helpers use.
func DefaultRedactor() *Redactor {
	defaultRedactorOnce.Do(func() {
		defaultRedactor = NewRedactor(PatternsToRegexps(BuiltInPatterns))
	})
	return defaultRedactor
}

// RedactText redacts free-form text (log lines, error strings): every
// pattern runs once and the matches are replaced in a single pass.
func (r *Redactor) RedactText(s string) string {
	if s == "" {
		return s
	}
	spans := acquireSpansBuf()
	defer func() { releaseSpansBuf(spans) }()
	spans = r.findSpansInto([]byte(s), spans)
	if len(spans) == 0 {
		return s
	}
	return string(applySpans(make([]byte, 0, len(s)), []byte(s), spans, len(s)))
}

// RedactBytes is RedactText for byte slices; it returns data itself when
// nothing matched, and the number of replaced spans.
func (r *Redactor) RedactBytes(data []byte) ([]byte, int) {
	spans := acquireSpansBuf()
	defer func() { releaseSpansBuf(spans) }()
	spans = r.findSpansInto(data, spans)
	if len(spans) == 0 {
		return data, 0
	}
	return applySpans(make([]byte, 0, len(data)), data, spans, len(data)), len(spans)
}
