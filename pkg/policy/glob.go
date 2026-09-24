package policy

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// glob is a precompiled "server.tool" pattern. `*` matches any run of
// characters (dots included, so "*" matches every tool and "*.delete_*"
// every server's delete_ tools), `?` exactly one character; everything else
// is literal and case-sensitive. Matching never allocates.
type glob struct {
	kind  globKind
	lit   string   // globExact: the name; globPrefix: the prefix
	parts []string // globStars: the literals between the stars
	runes []rune   // globGeneral: the pattern
}

type globKind uint8

const (
	globExact   globKind = iota // no wildcard: string equality
	globAny                     // "*"
	globPrefix                  // "lit*"
	globStars                   // only '*' wildcards
	globGeneral                 // '?' somewhere
)

// validGlob rejects the syntax this matcher does not support, so a pattern
// written for path.Match ("[abc]", escapes) fails at config load instead of
// silently never matching.
func validGlob(p string) error {
	if strings.ContainsAny(p, `[]\`) {
		return errors.New("only the * and ? wildcards are supported (no [classes] or \\ escapes)")
	}
	if !utf8.ValidString(p) {
		return errors.New("not valid UTF-8")
	}
	return nil
}

func compileGlob(p string) glob {
	switch {
	case !strings.ContainsAny(p, "*?"):
		return glob{kind: globExact, lit: p}
	case strings.ContainsRune(p, '?'):
		return glob{kind: globGeneral, runes: []rune(p)}
	case strings.Trim(p, "*") == "":
		return glob{kind: globAny}
	case strings.IndexByte(p, '*') == len(p)-1:
		return glob{kind: globPrefix, lit: p[:len(p)-1]}
	default:
		return glob{kind: globStars, parts: strings.Split(p, "*")}
	}
}

func (g *glob) match(s string) bool {
	switch g.kind {
	case globExact:
		return s == g.lit
	case globAny:
		return true
	case globPrefix:
		return strings.HasPrefix(s, g.lit)
	case globStars:
		return matchStars(g.parts, s)
	default:
		return matchGeneral(g.runes, s)
	}
}

// matchStars matches a pattern made of literals separated by '*' (parts,
// as strings.Split returns them: at least two). The first literal anchors
// the start, the last one the end, and the middle ones are found leftmost
// in order, which is exact for '*'-only patterns.
func matchStars(parts []string, s string) bool {
	first, last := parts[0], parts[len(parts)-1]
	if len(s) < len(first)+len(last) || !strings.HasPrefix(s, first) || !strings.HasSuffix(s, last) {
		return false
	}
	s = s[len(first) : len(s)-len(last)]
	for _, part := range parts[1 : len(parts)-1] {
		if part == "" {
			continue
		}
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return true
}

// matchGeneral is the iterative wildcard matcher ('*' and '?', one '?'
// per rune), linear in the common case and O(len(p)*len(s)) at worst.
func matchGeneral(p []rune, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		if pi < len(p) {
			if p[pi] == '*' {
				star, mark = pi, si
				pi++
				continue
			}
			r, size := utf8.DecodeRuneInString(s[si:])
			if p[pi] == '?' || p[pi] == r {
				pi++
				si += size
				continue
			}
		}
		if star < 0 {
			return false
		}
		_, size := utf8.DecodeRuneInString(s[mark:])
		mark += size
		pi, si = star+1, mark
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
