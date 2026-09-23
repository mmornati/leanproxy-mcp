package injection

import (
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Text normalization (issue #315).
//
// The classifier used to run its regexes over raw JSON, so JSON escapes
// (`\u0020`, `\t`), invisible characters and look-alike letters all hid an
// attack from the patterns while the model downstream read it just fine.
// Every piece of text is now normalized before it is classified:
//
//   - compatibility folding (NFKD: full-width letters, ligatures,
//     mathematical alphanumerics, ...) with combining marks removed, so
//     "ｉｇｎｏｒｅ" and "ignóre" read as "ignore";
//   - zero-width, bidi-control and other format characters (Unicode Cf),
//     variation selectors and filler characters are dropped; Unicode "tag"
//     characters (U+E0020..U+E007E, the "ASCII smuggling" trick) are mapped
//     back to the ASCII they hide;
//   - a few Cyrillic and Greek letters that look like Latin ones are mapped
//     to the Latin letter;
//   - text is lower-cased;
//   - runs of whitespace (and control characters) collapse to one space, or
//     to one newline when the run contains a line break, so line-anchored
//     patterns keep working.
//
// The normalizer can record, for every output byte, the input range that
// produced it, so a match in normalized text maps back to the original
// string (the redact action edits only the matching span).

// normalizer appends normalized text to out.
type normalizer struct {
	out []byte
	// When track is set, srcStart[i] / srcEnd[i] bound the input bytes
	// (offsets relative to the start of the current input) that produced
	// out[i].
	track    bool
	srcStart []int
	srcEnd   []int

	// Pending collapsed whitespace: 0 (none), ' ' or '\n', and the input
	// range of the run.
	pending      byte
	pendingStart int
	pendingEnd   int

	decomp [4 * utf8.UTFMax]byte
}

func (n *normalizer) reset(track bool) {
	n.out = n.out[:0]
	n.srcStart = n.srcStart[:0]
	n.srcEnd = n.srcEnd[:0]
	n.track = track
	n.pending = 0
}

// separate marks a boundary between two independent pieces of text (two
// JSON strings): it acts as a line break.
func (n *normalizer) separate(at int) {
	n.space('\n', at, at)
}

func (n *normalizer) space(c byte, start, end int) {
	if n.pending == 0 {
		n.pending = c
		n.pendingStart = start
	} else if c == '\n' {
		n.pending = '\n'
	}
	n.pendingEnd = end
}

func (n *normalizer) emit(c byte, start, end int) {
	if n.pending != 0 {
		if len(n.out) > 0 {
			n.out = append(n.out, n.pending)
			if n.track {
				n.srcStart = append(n.srcStart, n.pendingStart)
				n.srcEnd = append(n.srcEnd, n.pendingEnd)
			}
		}
		n.pending = 0
	}
	n.out = append(n.out, c)
	if n.track {
		n.srcStart = append(n.srcStart, start)
		n.srcEnd = append(n.srcEnd, end)
	}
}

// emitRun appends a run of plain ASCII (asciiClass 0), lower-cased. Only
// used when offsets are not tracked.
func (n *normalizer) emitRun(run []byte) {
	n.flushPending()
	start := len(n.out)
	n.out = append(n.out, run...)
	lowerASCII(n.out[start:])
}

func (n *normalizer) emitRunString(run string) {
	n.flushPending()
	start := len(n.out)
	n.out = append(n.out, run...)
	lowerASCII(n.out[start:])
}

func (n *normalizer) flushPending() {
	if n.pending != 0 {
		if len(n.out) > 0 {
			n.out = append(n.out, n.pending)
		}
		n.pending = 0
	}
}

func lowerASCII(b []byte) {
	for k, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[k] = c + 'a' - 'A'
		}
	}
}

// asciiClass: 0 = keep, 1 = horizontal space, 2 = line break.
var asciiClass = func() (t [128]byte) {
	for c := 0; c < 0x20; c++ {
		t[c] = 1
	}
	t['\n'], t['\r'], t['\v'], t['\f'] = 2, 2, 2, 2
	t[' '] = 1
	t[0x7f] = 1
	return t
}()

func (n *normalizer) ascii(c byte, start, end int) {
	switch asciiClass[c] {
	case 1:
		n.space(' ', start, end)
	case 2:
		n.space('\n', start, end)
	default:
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		n.emit(c, start, end)
	}
}

// add appends the normalization of src; offsets recorded for it are
// base + offset in src.
func (n *normalizer) add(src []byte, base int) {
	for i := 0; i < len(src); {
		c := src[i]
		if !n.track && c < utf8.RuneSelf && asciiClass[c] == 0 {
			// Fast path: copy a run of plain ASCII, lower-cased.
			j := i + 1
			for j < len(src) && src[j] < utf8.RuneSelf && asciiClass[src[j]] == 0 {
				j++
			}
			n.emitRun(src[i:j])
			i = j
			continue
		}
		if c < utf8.RuneSelf {
			n.ascii(c, base+i, base+i+1)
			i++
			continue
		}
		r, size := utf8.DecodeRune(src[i:])
		n.addRune(r, base+i, base+i+size)
		i += size
	}
}

// addString is add for a string (no copy).
func (n *normalizer) addString(src string, base int) {
	for i := 0; i < len(src); {
		c := src[i]
		if !n.track && c < utf8.RuneSelf && asciiClass[c] == 0 {
			j := i + 1
			for j < len(src) && src[j] < utf8.RuneSelf && asciiClass[src[j]] == 0 {
				j++
			}
			n.emitRunString(src[i:j])
			i = j
			continue
		}
		if c < utf8.RuneSelf {
			n.ascii(c, base+i, base+i+1)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(src[i:])
		n.addRune(r, base+i, base+i+size)
		i += size
	}
}

func (n *normalizer) addRune(r rune, start, end int) {
	switch {
	case r == utf8.RuneError:
		return
	case r >= 0xE0020 && r <= 0xE007E:
		// Tag characters mirror printable ASCII.
		n.ascii(byte(r-0xE0000), start, end)
		return
	case isInvisible(r):
		return
	case r == 0x2028 || r == 0x2029 || r == 0x85:
		n.space('\n', start, end)
		return
	case unicode.IsSpace(r):
		n.space(' ', start, end)
		return
	}
	var enc [utf8.UTFMax]byte
	k := utf8.EncodeRune(enc[:], r)
	dec := enc[:k]
	if !norm.NFKD.IsNormal(dec) {
		dec = norm.NFKD.Append(n.decomp[:0], dec...)
	}
	for j := 0; j < len(dec); {
		rr, sz := utf8.DecodeRune(dec[j:])
		j += sz
		if unicode.In(rr, unicode.Mn, unicode.Me) || isInvisible(rr) {
			continue
		}
		rr = unicode.ToLower(rr)
		if l, ok := confusables[rr]; ok {
			rr = l
		}
		if rr < utf8.RuneSelf {
			n.ascii(byte(rr), start, end)
			continue
		}
		if unicode.IsSpace(rr) {
			n.space(' ', start, end)
			continue
		}
		var out [utf8.UTFMax]byte
		m := utf8.EncodeRune(out[:], rr)
		for _, b := range out[:m] {
			n.emit(b, start, end)
		}
	}
}

// isInvisible reports characters that render as nothing: format characters
// (zero-width space/joiners, bidi controls, soft hyphen, BOM, ...),
// variation selectors and the Hangul fillers.
func isInvisible(r rune) bool {
	switch {
	case unicode.Is(unicode.Cf, r):
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
		return true
	case r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0, r == 0x180E:
		return true
	}
	return false
}

// confusables maps lower-case Cyrillic and Greek letters that are commonly
// used as look-alikes of Latin letters.
var confusables = map[rune]rune{
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x',
	'і': 'i', 'ј': 'j', 'ѕ': 's', 'ԁ': 'd', 'һ': 'h', 'ӏ': 'l', 'ԛ': 'q',
	'ԝ': 'w', 'ɡ': 'g', 'ɑ': 'a', 'ı': 'i',
	'α': 'a', 'ο': 'o', 'ν': 'v', 'ι': 'i', 'κ': 'k', 'ρ': 'p', 'υ': 'u',
	'τ': 't', 'ε': 'e',
}

// Normalize returns the normalized form of s that the patterns are matched
// against (see the package comment on normalization above).
func Normalize(s string) string {
	var n normalizer
	n.addString(s, 0)
	return string(n.out)
}

var normalizerPool = sync.Pool{New: func() interface{} { return &normalizer{} }}

func acquireNormalizer(track bool) *normalizer {
	n := normalizerPool.Get().(*normalizer)
	n.reset(track)
	return n
}

func releaseNormalizer(n *normalizer) {
	if cap(n.out) > 1<<20 || cap(n.srcStart) > 1<<18 {
		return // do not pin large buffers
	}
	normalizerPool.Put(n)
}

// TextBuilder accumulates the normalized text of several independent
// strings (for example every string value of a JSON document) for one
// ClassifyNormalized call. Strings are separated by a line break. A
// TextBuilder is not safe for concurrent use; Release returns it to a pool.
type TextBuilder struct {
	n *normalizer
}

// NewTextBuilder returns an empty builder.
func NewTextBuilder() *TextBuilder {
	return &TextBuilder{n: acquireNormalizer(false)}
}

// Add appends the normalization of s (raw, decoded text).
func (b *TextBuilder) Add(s []byte) {
	b.n.separate(0)
	b.n.add(s, 0)
}

// AddString is Add for a string.
func (b *TextBuilder) AddString(s string) {
	b.n.separate(0)
	b.n.addString(s, 0)
}

// Len is the length of the normalized text so far.
func (b *TextBuilder) Len() int { return len(b.n.out) }

// Text returns the normalized text. It aliases the builder's buffer.
func (b *TextBuilder) Text() []byte { return b.n.out }

// Release returns the builder's buffers to the pool; b must not be used
// afterwards.
func (b *TextBuilder) Release() {
	if b.n != nil {
		releaseNormalizer(b.n)
		b.n = nil
	}
}

// FindSpans normalizes s and returns the byte ranges of s (sorted,
// non-overlapping) whose normalized form the classifier's patterns match.
// A match that spans characters dropped by normalization (e.g. a
// zero-width space inside a word) covers them too.
func (c *Classifier) FindSpans(s []byte) [][2]int {
	n := acquireNormalizer(true)
	defer releaseNormalizer(n)
	n.add(s, 0)
	spans := c.MatchSpans(n.out, nil)
	if len(spans) == 0 {
		return nil
	}
	out := spans[:0]
	for _, sp := range spans {
		start, end := n.srcStart[sp[0]], n.srcEnd[sp[1]-1]
		if len(out) > 0 && start <= out[len(out)-1][1] {
			if end > out[len(out)-1][1] {
				out[len(out)-1][1] = end
			}
			continue
		}
		out = append(out, [2]int{start, end})
	}
	return out
}
