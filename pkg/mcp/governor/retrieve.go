package governor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Page is one read_result page.
type Page struct {
	// Text is data[Offset:End], verbatim: concatenating the pages of a
	// result gives the result back.
	Text string
	// Offset and End delimit the page in bytes; Total is the result size.
	Offset, End, Total int
}

// Done reports whether the page reaches the end of the result.
func (p Page) Done() bool { return p.End >= p.Total }

// ReadPage returns the page of data starting at offset (snapped back to a
// UTF-8 boundary) and at most limit bytes long, cut just after a line
// break when one falls in its second half, else on a rune boundary. A
// page always makes progress.
func ReadPage(data string, offset, limit int) (Page, error) {
	if offset < 0 || offset > len(data) {
		return Page{}, fmt.Errorf("offset %d is outside the result (0 to %d bytes)", offset, len(data))
	}
	offset = runeStart(data, offset)
	if limit <= 0 {
		limit = DefaultMaxTokens * BytesPerToken
	}
	end := offset + limit
	if end >= len(data) {
		end = len(data)
	} else {
		cut := cutBackward(data[offset:], limit) + offset
		if cut <= offset {
			// A single rune longer than the page: take it whole.
			cut = offset + 1
			for cut < len(data) && !isRuneStartByte(data[cut]) {
				cut++
			}
		}
		end = cut
	}
	return Page{Text: data[offset:end], Offset: offset, End: end, Total: len(data)}, nil
}

func isRuneStartByte(b byte) bool { return b&0xC0 != 0x80 }

// grepContext is the number of lines shown around each grep match.
const grepContext = 2

// maxPatternLen caps a read_result grep pattern.
const maxPatternLen = 1024

// GrepResult is the output of Grep.
type GrepResult struct {
	// Text is the matching lines ("12:line") with their context lines
	// ("11-line"), groups separated by "--", like grep -n -C2.
	Text string
	// Matches is the number of matching lines written; Total is the number
	// of matching lines from the start line on.
	Matches, Total int
	// NextLine is the line to continue from when the output was cut at
	// the limit (0 when complete).
	NextLine int
}

// maxGrepLine caps how much of one line Grep shows.
const maxGrepLine = 1000

// Grep returns the lines of data matching pattern (RE2 syntax), from line
// startLine (1-based; 0 means 1) on, each with its line number and
// grepContext lines of context, in at most limit bytes (the first group is
// always shown). Lines longer than maxGrepLine bytes are shortened.
func Grep(data, pattern string, startLine, limit int) (GrepResult, error) {
	if len(pattern) > maxPatternLen {
		return GrepResult{}, fmt.Errorf("grep pattern longer than %d bytes", maxPatternLen)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return GrepResult{}, fmt.Errorf("invalid grep pattern: %w", err)
	}
	if startLine < 1 {
		startLine = 1
	}
	if limit <= 0 {
		limit = DefaultMaxTokens * BytesPerToken
	}
	lines := strings.Split(data, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(data, "\n") {
		lines = lines[:len(lines)-1]
	}
	n := len(lines)
	match := make(map[int]bool)
	matches := func(j int) bool {
		m, ok := match[j]
		if !ok {
			m = re.MatchString(lines[j])
			match[j] = m
		}
		return m
	}
	cost := func(j int) int { return min(len(lines[j]), maxGrepLine) + 24 }

	var res GrepResult
	var b strings.Builder
	lastShown := 0 // lines before this index were written (or skipped)
	for i := startLine - 1; i < n; i++ {
		if !matches(i) {
			continue
		}
		if res.NextLine > 0 {
			res.Total++
			continue
		}
		from := max(i-grepContext, lastShown, startLine-1)
		to := min(i+grepContext, n-1)
		size := 0
		for j := from; j <= to; j++ {
			size += cost(j)
		}
		if b.Len()+size > limit && res.Matches > 0 {
			res.NextLine = i + 1
			res.Total++
			continue
		}
		// Matches inside the context extend the group (like grep -C),
		// while it fits.
		for j := i + 1; j <= to && j < n; j++ {
			if !matches(j) {
				continue
			}
			newTo := min(j+grepContext, n-1)
			add := 0
			for k := to + 1; k <= newTo; k++ {
				add += cost(k)
			}
			if b.Len()+size+add > limit {
				break
			}
			size += add
			to = newTo
		}
		if lastShown > 0 && from > lastShown {
			b.WriteString("--\n")
		}
		for j := from; j <= to; j++ {
			sep := "-"
			if j >= i && matches(j) {
				sep = ":"
				res.Matches++
				res.Total++
			}
			line := lines[j]
			if len(line) > maxGrepLine {
				line = line[:runeStart(line, maxGrepLine)] + " … [line shortened]"
			}
			b.WriteString(strconv.Itoa(j + 1))
			b.WriteString(sep)
			b.WriteString(line)
			b.WriteByte('\n')
		}
		lastShown = to + 1
		i = to
	}
	res.Text = b.String()
	return res, nil
}

// JSONPath evaluates a JSONPath subset over doc and returns the matched
// values, byte for byte, as a JSON array. Supported: the root $, .name and
// ['name'] children, .* and [*] wildcards, [n] indexes (negative counts
// from the end), [start:end] slices, and ..name / ..* recursive descent.
func JSONPath(doc []byte, expr string) ([]byte, int, error) {
	steps, err := parsePath(expr)
	if err != nil {
		return nil, 0, err
	}
	nodes := [][]byte{bytes.TrimSpace(doc)}
	for _, st := range steps {
		var next [][]byte
		for _, n := range nodes {
			next = st.apply(n, next)
			if len(next) > maxPathNodes {
				return nil, 0, fmt.Errorf("jsonpath matches more than %d values; narrow it (e.g. a slice)", maxPathNodes)
			}
		}
		nodes = next
	}
	out := []byte{'['}
	for i, n := range nodes {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, n...)
	}
	return append(out, ']'), len(nodes), nil
}

// maxPathNodes caps the values one JSONPath step may produce.
const maxPathNodes = 100000

type stepKind int

const (
	stepChild stepKind = iota
	stepWildcard
	stepIndex
	stepSlice
	stepDescendant
)

type pathStep struct {
	kind       stepKind
	name       string // child / descendant ("" + wildcard for ..*)
	wildcard   bool   // descendant ..*
	index      int
	start, end *int
}

func parsePath(expr string) ([]pathStep, error) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "$") {
		return nil, fmt.Errorf("jsonpath must start with $")
	}
	s := expr[1:]
	var steps []pathStep
	for s != "" {
		switch {
		case strings.HasPrefix(s, ".."):
			s = s[2:]
			if strings.HasPrefix(s, "*") {
				steps = append(steps, pathStep{kind: stepDescendant, wildcard: true})
				s = s[1:]
				continue
			}
			name, rest := readName(s)
			if name == "" {
				return nil, fmt.Errorf("jsonpath: expected a name after \"..\"")
			}
			steps = append(steps, pathStep{kind: stepDescendant, name: name})
			s = rest
		case strings.HasPrefix(s, "."):
			s = s[1:]
			if strings.HasPrefix(s, "*") {
				steps = append(steps, pathStep{kind: stepWildcard})
				s = s[1:]
				continue
			}
			name, rest := readName(s)
			if name == "" {
				return nil, fmt.Errorf("jsonpath: expected a name after \".\"")
			}
			steps = append(steps, pathStep{kind: stepChild, name: name})
			s = rest
		case strings.HasPrefix(s, "["):
			end := strings.IndexByte(s, ']')
			if end < 0 {
				return nil, fmt.Errorf("jsonpath: missing ]")
			}
			st, err := parseBracket(strings.TrimSpace(s[1:end]))
			if err != nil {
				return nil, err
			}
			steps = append(steps, st)
			s = s[end+1:]
		default:
			return nil, fmt.Errorf("jsonpath: unexpected %q", s)
		}
	}
	return steps, nil
}

func readName(s string) (name, rest string) {
	i := 0
	for i < len(s) && s[i] != '.' && s[i] != '[' {
		i++
	}
	return s[:i], s[i:]
}

func parseBracket(in string) (pathStep, error) {
	switch {
	case in == "*":
		return pathStep{kind: stepWildcard}, nil
	case len(in) >= 2 && (in[0] == '\'' || in[0] == '"') && in[len(in)-1] == in[0]:
		return pathStep{kind: stepChild, name: in[1 : len(in)-1]}, nil
	case strings.Contains(in, ":"):
		a, b, _ := strings.Cut(in, ":")
		st := pathStep{kind: stepSlice}
		for _, p := range []struct {
			txt string
			dst **int
		}{{a, &st.start}, {b, &st.end}} {
			if t := strings.TrimSpace(p.txt); t != "" {
				n, err := strconv.Atoi(t)
				if err != nil {
					return pathStep{}, fmt.Errorf("jsonpath: invalid slice bound %q", t)
				}
				*p.dst = &n
			}
		}
		return st, nil
	default:
		n, err := strconv.Atoi(in)
		if err != nil {
			return pathStep{}, fmt.Errorf("jsonpath: invalid index %q", in)
		}
		return pathStep{kind: stepIndex, index: n}, nil
	}
}

// apply appends the values st selects from node to out.
func (st pathStep) apply(node []byte, out [][]byte) [][]byte {
	if len(node) == 0 {
		return out
	}
	switch st.kind {
	case stepChild:
		if node[0] != '{' {
			return out
		}
		members, err := SplitObject(node)
		if err != nil {
			return out
		}
		for _, m := range members {
			if keyIs(m.Key, st.name) {
				out = append(out, m.Value)
			}
		}
	case stepWildcard:
		out = append(out, children(node)...)
	case stepIndex:
		elems := arrayElems(node)
		i := st.index
		if i < 0 {
			i += len(elems)
		}
		if i >= 0 && i < len(elems) {
			out = append(out, elems[i])
		}
	case stepSlice:
		elems := arrayElems(node)
		n := len(elems)
		lo, hi := 0, n
		if st.start != nil {
			lo = clampIndex(*st.start, n)
		}
		if st.end != nil {
			hi = clampIndex(*st.end, n)
		}
		if lo < hi {
			out = append(out, elems[lo:hi]...)
		}
	case stepDescendant:
		out = st.descend(node, out)
	}
	return out
}

func (st pathStep) descend(node []byte, out [][]byte) [][]byte {
	if len(out) > maxPathNodes {
		return out
	}
	switch node[0] {
	case '{':
		members, err := SplitObject(node)
		if err != nil {
			return out
		}
		for _, m := range members {
			if st.wildcard || keyIs(m.Key, st.name) {
				out = append(out, m.Value)
			}
			if len(m.Value) > 0 && (m.Value[0] == '{' || m.Value[0] == '[') {
				out = st.descend(m.Value, out)
			}
		}
	case '[':
		for _, el := range arrayElems(node) {
			if st.wildcard {
				out = append(out, el)
			}
			if len(el) > 0 && (el[0] == '{' || el[0] == '[') {
				out = st.descend(el, out)
			}
		}
	}
	return out
}

func clampIndex(i, n int) int {
	if i < 0 {
		i += n
	}
	return max(0, min(i, n))
}

func keyIs(rawKey []byte, name string) bool {
	if bytes.IndexByte(rawKey, '\\') < 0 {
		return len(rawKey) >= 2 && string(rawKey[1:len(rawKey)-1]) == name
	}
	var k string
	return json.Unmarshal(rawKey, &k) == nil && k == name
}

func arrayElems(node []byte) [][]byte {
	if node[0] != '[' {
		return nil
	}
	elems, err := SplitArray(node)
	if err != nil {
		return nil
	}
	return elems
}

func children(node []byte) [][]byte {
	switch node[0] {
	case '[':
		return arrayElems(node)
	case '{':
		members, err := SplitObject(node)
		if err != nil {
			return nil
		}
		out := make([][]byte, len(members))
		for i, m := range members {
			out[i] = m.Value
		}
		return out
	}
	return nil
}
