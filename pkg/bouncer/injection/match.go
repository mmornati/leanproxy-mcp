package injection

import (
	"bytes"
	"math"
	"sort"
	"sync"
)

// Trigger prefilter and windowed matching (issue #315).
//
// Go's regexp engine has no DFA: over a long text it runs an NFA at a few
// MB/s per pattern, so running every pattern over a 100 KiB tool result
// cost tens of milliseconds each. Instead:
//
//  1. one pass over the normalized text finds the occurrences of every
//     pattern's Triggers (a table keyed by first byte; triggers that start
//     with a letter or digit are only tried at word starts, where the
//     patterns' \b puts them);
//  2. a pattern none of whose triggers occurs is skipped;
//  3. otherwise the regex runs only on windows of text around the
//     occurrences. A window is at most InjectionPattern.window bytes, the
//     input size under which the engine uses its bounded backtracker, which
//     is much faster than the NFA. Windows are snapped to line starts/ends
//     (or, on long lines, to spaces) so ^, $ and \b keep their meaning at
//     the window edges.
//
// A pattern without Triggers (a custom pattern that does not declare any)
// runs over the whole text.

// wholeTextMax is the text size under which windows are not worth it: the
// whole text is one window.
const wholeTextMax = 256

type trigRef struct {
	pattern int
	lit     []byte
	// prefix and mask hold the first (up to) four bytes of lit, for a
	// one-comparison reject.
	prefix, mask uint32
}

// triggerIndex maps the first two bytes of every trigger to the triggers
// that start with them (one-byte triggers are kept apart).
type triggerIndex struct {
	// by2 holds, for a two-byte key (first byte | second byte << 8), the
	// index + 1 of its list in lists; 0 means none.
	by2    [1 << 16]uint16
	lists  [][]trigRef
	single [256][]trigRef
	// first marks the bytes some trigger starts with.
	first [256]bool
	empty bool
}

func buildTriggerIndex(patterns []*InjectionPattern) *triggerIndex {
	ix := &triggerIndex{empty: true}
	for i, p := range patterns {
		for _, t := range p.Triggers {
			if t == "" {
				continue
			}
			lit := []byte(t)
			var prefix, mask uint32
			for k := 0; k < 4 && k < len(lit); k++ {
				prefix |= uint32(lit[k]) << (8 * k)
				mask |= 0xff << (8 * k)
			}
			ref := trigRef{pattern: i, lit: lit, prefix: prefix, mask: mask}
			ix.first[lit[0]] = true
			ix.empty = false
			if len(lit) == 1 {
				ix.single[lit[0]] = append(ix.single[lit[0]], ref)
				continue
			}
			key := uint16(lit[0]) | uint16(lit[1])<<8
			if ix.by2[key] == 0 {
				if len(ix.lists) >= math.MaxUint16 {
					// Out of list slots (cannot happen with realistic
					// trigger sets): fall back to a first-byte check.
					ix.single[lit[0]] = append(ix.single[lit[0]], ref)
					continue
				}
				ix.lists = append(ix.lists, nil)
				ix.by2[key] = uint16(len(ix.lists)) // #nosec G115 -- bounded by math.MaxUint16 above
			}
			ix.lists[ix.by2[key]-1] = append(ix.lists[ix.by2[key]-1], ref)
		}
	}
	return ix
}

// wordByte marks the bytes of a word (ASCII letters, digits, '_').
var wordByte = func() (t [256]bool) {
	for c := 0; c < 256; c++ {
		t[c] = c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c >= 'A' && c <= 'Z'
	}
	return t
}()

// spaceNL reads a line break as a space (triggers are written with
// spaces).
func spaceNL(c byte) byte {
	if c == '\n' {
		return ' '
	}
	return c
}

// scan appends to occ[i] the offsets in text where a trigger of pattern i
// starts. Line breaks in text match spaces in triggers.
func (ix *triggerIndex) scan(text []byte, occ [][]int) {
	if ix == nil || ix.empty {
		return
	}
	for i := 0; i < len(text); {
		c := text[i]
		if f := spaceNL(c); ix.first[f] {
			if cands := ix.single[f]; len(cands) > 0 {
				ix.try(text, i, cands, occ)
			}
			if i+1 < len(text) {
				if l := ix.by2[uint16(f)|uint16(spaceNL(text[i+1]))<<8]; l != 0 {
					ix.try(text, i, ix.lists[l-1], occ)
				}
			}
		}
		i++
		if wordByte[c] {
			// Letter/digit triggers only start at word starts: skip the
			// rest of the word.
			for i < len(text) && wordByte[text[i]] {
				i++
			}
		}
	}
}

// try records the triggers of cands that occur at text[i:].
func (ix *triggerIndex) try(text []byte, i int, cands []trigRef, occ [][]int) {
	rest := text[i:]
	var head uint32
	for k := 0; k < 4 && k < len(rest); k++ {
		head |= uint32(spaceNL(rest[k])) << (8 * k)
	}
	for _, t := range cands {
		if head&t.mask != t.prefix || len(t.lit) > len(rest) || (len(t.lit) > 4 && !litEqual(rest[4:], t.lit[4:])) {
			continue
		}
		o := occ[t.pattern]
		if n := len(o); n > 0 && o[n-1] == i {
			continue
		}
		occ[t.pattern] = append(o, i)
	}
}

// litEqual reports whether text starts with lit, reading '\n' in text as
// ' '.
func litEqual(text, lit []byte) bool {
	for k := 0; k < len(lit); k++ {
		if spaceNL(text[k]) != lit[k] {
			return false
		}
	}
	return true
}

// scratch holds the per-call occurrence lists.
type scratch struct {
	occ  [][]int
	hits []int
}

var scratchPool = sync.Pool{New: func() interface{} { return &scratch{} }}

func acquireScratch(n int) *scratch {
	sc := scratchPool.Get().(*scratch)
	for len(sc.occ) < n {
		sc.occ = append(sc.occ, nil)
	}
	sc.occ = sc.occ[:n]
	sc.hits = sc.hits[:0]
	for i := range sc.occ {
		sc.occ[i] = sc.occ[i][:0]
	}
	return sc
}

func releaseScratch(sc *scratch) {
	for _, o := range sc.occ {
		if cap(o) > 1<<14 {
			return // do not pin large buffers
		}
	}
	scratchPool.Put(sc)
}

// matches reports whether p matches text, given the occurrences of its
// triggers.
func (p *InjectionPattern) matches(text []byte, occ []int) bool {
	if len(p.Triggers) == 0 {
		return p.Pattern.Match(text)
	}
	found := false
	p.eachWindow(text, occ, func(start, end int) bool {
		found = p.required(text[start:end]) && p.Pattern.Match(text[start:end])
		return !found
	})
	return found
}

// required reports whether window holds one of p's Requires (or p has
// none).
func (p *InjectionPattern) required(window []byte) bool {
	if len(p.Requires) == 0 {
		return true
	}
	for _, r := range p.Requires {
		if bytes.Contains(window, r) {
			return true
		}
	}
	return false
}

// appendMatches appends the ranges of text p matches (possibly with
// duplicates when windows overlap; MatchSpans merges them).
func (p *InjectionPattern) appendMatches(text []byte, occ []int, dst [][2]int) [][2]int {
	add := func(start, end int) bool {
		if !p.required(text[start:end]) {
			return true
		}
		for _, loc := range p.Pattern.FindAllIndex(text[start:end], -1) {
			if loc[1] > loc[0] {
				dst = append(dst, [2]int{start + loc[0], start + loc[1]})
			}
		}
		return true
	}
	if len(p.Triggers) == 0 {
		add(0, len(text))
		return dst
	}
	p.eachWindow(text, occ, add)
	return dst
}

// eachWindow calls fn with windows of text around the trigger occurrences
// occ (ascending), in ascending order, until fn returns false. Every
// occurrence gets a window reaching about half of p.window on each side,
// so any match that contains the occurrence (matches are phrases, far
// shorter than a window) lies inside it. Neighboring windows are merged
// while the result stays within p.window.
func (p *InjectionPattern) eachWindow(text []byte, occ []int, fn func(start, end int) bool) {
	if len(occ) == 0 {
		return
	}
	if len(text) <= wholeTextMax {
		fn(0, len(text))
		return
	}
	if !sort.IntsAreSorted(occ) {
		sort.Ints(occ)
	}
	win := p.window
	if win <= 0 {
		win = defaultWindow
	}
	snap := win / 8
	half := win/2 - snap
	curStart, curEnd := snapStart(text, occ[0]-half, snap), snapEnd(text, occ[0]+half, snap)
	for _, at := range occ[1:] {
		start, end := snapStart(text, at-half, snap), snapEnd(text, at+half, snap)
		if start <= curEnd && end-curStart <= win {
			if end > curEnd {
				curEnd = end
			}
			continue
		}
		if !fn(curStart, curEnd) {
			return
		}
		curStart, curEnd = start, end
	}
	fn(curStart, curEnd)
}

// snapStart moves a window start back (at most snap bytes) to the start of
// its line or, failing that, to just after a space, so anchors and word
// boundaries at the window start mean what they mean in the whole text.
func snapStart(text []byte, i, snap int) int {
	if i <= 0 {
		return 0
	}
	lo := i - snap
	if lo <= 0 {
		return 0
	}
	for j := i; j >= lo; j-- {
		if text[j] == '\n' {
			return j + 1
		}
	}
	for j := i; j >= lo; j-- {
		if text[j] == ' ' {
			return j + 1
		}
	}
	return i
}

// snapEnd moves a window end forward (at most snap bytes) to the end of its
// line or, failing that, to a space.
func snapEnd(text []byte, i, snap int) int {
	if i >= len(text) {
		return len(text)
	}
	hi := i + snap
	if hi >= len(text) {
		return len(text)
	}
	for j := i; j < hi; j++ {
		if text[j] == '\n' {
			return j
		}
	}
	for j := i; j < hi; j++ {
		if text[j] == ' ' {
			return j
		}
	}
	return i
}
