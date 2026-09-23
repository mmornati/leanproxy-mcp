package toolsearch

import (
	"strings"
	"unicode"
)

// stopwords are dropped from both documents and queries. The list is the
// one the audit prototype (docs/audit/experiments/search_eval.py) was
// evaluated with: short function words plus "get", "list" and "show", which
// start half of all tool names and descriptions and so carry no signal.
var stopwords = func() map[string]struct{} {
	words := strings.Fields(`a an the in of to my me for and or is are with that this
		what which how i on at by from be do did show get list
		all any some so far it its was were has have been can we our your you
		they them their there about as if into up then than too very just
		please want need would could should will does am us`)
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}()

// Tokenize splits text into search terms: it breaks camelCase boundaries,
// lowercases, splits on anything that is not a letter or digit (so "_" and
// "-" separate words too), drops stopwords and applies Stem to each word.
func Tokenize(text string) []string {
	return appendTokens(nil, text)
}

// appendTokens appends the terms of text to dst.
func appendTokens(dst []string, text string) []string {
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		w := string(word)
		word = word[:0]
		if _, stop := stopwords[w]; stop {
			return
		}
		dst = append(dst, Stem(w))
	}
	var prev rune
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// camelCase boundary: a lower-case letter or digit followed
			// by an upper-case letter ("pullNumber" -> "pull number").
			if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				flush()
			}
			word = append(word, unicode.ToLower(r))
		default:
			flush()
		}
		prev = r
	}
	flush()
	return dst
}

// stemSuffixes are tried in order; the first one that matches a word
// longer than four characters is removed ("ies" becomes "y").
var stemSuffixes = [...]string{"ing", "ies", "es", "s", "ed"}

// Stem is a deliberately light English stemmer: on words longer than four
// characters it strips the first matching suffix of "ing", "ies" (-> "y"),
// "es", "s" and "ed". It is applied identically to documents and queries,
// so a crude stem ("messag" for "messages") still matches.
func Stem(w string) string {
	if len(w) <= 4 {
		return w
	}
	for _, suf := range stemSuffixes {
		if strings.HasSuffix(w, suf) {
			if suf == "ies" {
				return w[:len(w)-3] + "y"
			}
			return w[:len(w)-len(suf)]
		}
	}
	return w
}
