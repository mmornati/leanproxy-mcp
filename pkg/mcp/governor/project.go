package governor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Field projection (issue #320): drop the fields of a JSON result the model
// does not need before the result enters its context.
//
// # Path syntax
//
// A projection path is a small, documented subset of JSONPath-like
// selectors, separated by dots:
//
//	name     an object member ("title"); "*" in a name matches any run of
//	         characters ("*_url"), a lone "*" any key
//	[]       every element of an array ("[].number", "labels[].name")
//	**       any depth, zero or more levels, through objects and arrays
//	         ("**.node_id")
//
// A name step applied to an array applies to each of its elements, so
// "labels.name" and "labels[].name" select the same values. A leading "$"
// or "$." is accepted and ignored. Keys containing ".", "[" or "]" cannot be
// addressed. Array indexes and slices are not part of the syntax
// (read_result's jsonpath has them).
//
// # Semantics
//
// A projection is either an allowlist (keep) or a denylist (drop), never
// both:
//
//   - drop removes every object member a path selects, wherever it is;
//     everything else is kept byte for byte;
//   - keep rebuilds the document with only the members on the paths: a
//     value a path selects is kept whole; the objects and arrays leading to
//     it keep only the members that lead to a kept value; arrays keep their
//     elements (each projected the same way). A member a path names
//     literally (or an element it selects with "[]") that is null, a
//     scalar or a container without the rest of the path is kept as it is
//     ("assignee": null stays, a label without a name becomes {}); one
//     only reached through a glob or "**" is dropped.
//
// Projection works on the document's bytes (SplitObject / SplitArray),
// never decoding values: kept members stay in document order and numbers
// keep their exact text (no float64 round-trip), and the result is
// compacted (json.Compact: insignificant whitespace removed, no HTML
// escaping, strings and numbers untouched).

// Projection limits (config rules and the model's fields argument).
const (
	// MaxProjectionPaths caps the paths of one rule or fields argument.
	MaxProjectionPaths = 100
	// MaxProjectionPathLen caps the length of one path.
	MaxProjectionPathLen = 256
)

// DefaultDropPaths is the built-in pack of response.default_projections:
// conservative drop rules for the usual API noise (URLs derived from ids,
// GraphQL node ids, avatars, HAL links, self links, ETags). Never a keep.
var DefaultDropPaths = []string{
	"**.*_url",
	"**.node_id",
	"**.avatar_url",
	"**.gravatar_id",
	"**._links",
	"**.self",
	"**.etag",
}

// ProjectionMode says whether a projection keeps or drops what its paths
// select.
type ProjectionMode int

// Projection modes.
const (
	ProjectKeep ProjectionMode = iota
	ProjectDrop
)

func (m ProjectionMode) String() string {
	if m == ProjectDrop {
		return "drop"
	}
	return "keep"
}

type projStepKind uint8

const (
	projName projStepKind = iota // an object member (glob)
	projElem                     // [] : every array element
	projDeep                     // ** : any depth
)

type projStep struct {
	kind projStepKind
	glob string
}

// Projection is a compiled keep or drop rule.
type Projection struct {
	Mode  ProjectionMode
	Paths []string
	steps [][]projStep
}

// CompileProjection compiles a keep or a drop list (exactly one of them
// non-empty).
func CompileProjection(keep, drop []string) (*Projection, error) {
	switch {
	case len(keep) > 0 && len(drop) > 0:
		return nil, errors.New("keep and drop are mutually exclusive")
	case len(keep) == 0 && len(drop) == 0:
		return nil, errors.New("keep or drop is required")
	}
	p := &Projection{Mode: ProjectKeep, Paths: keep}
	if len(drop) > 0 {
		p.Mode, p.Paths = ProjectDrop, drop
	}
	if len(p.Paths) > MaxProjectionPaths {
		return nil, fmt.Errorf("at most %d paths, got %d", MaxProjectionPaths, len(p.Paths))
	}
	p.steps = make([][]projStep, 0, len(p.Paths))
	for _, raw := range p.Paths {
		steps, err := parseProjPath(raw)
		if err != nil {
			return nil, err
		}
		if p.Mode == ProjectDrop && steps[len(steps)-1].kind != projName {
			return nil, fmt.Errorf("path %q: a drop path must end with a key name", raw)
		}
		p.steps = append(p.steps, steps)
	}
	return p, nil
}

// parseProjPath parses one projection path.
func parseProjPath(raw string) ([]projStep, error) {
	s := strings.TrimSpace(raw)
	if len(s) > MaxProjectionPathLen {
		return nil, fmt.Errorf("path %.40q…: longer than %d bytes", s, MaxProjectionPathLen)
	}
	s = strings.TrimPrefix(s, "$")
	if strings.HasPrefix(s, ".") && !strings.HasPrefix(s, "..") {
		s = s[1:]
	}
	if s == "" {
		return nil, fmt.Errorf("path %q: empty", raw)
	}
	var steps []projStep
	for _, seg := range strings.Split(s, ".") {
		elems := 0
		for strings.HasSuffix(seg, "[]") {
			seg = seg[:len(seg)-2]
			elems++
		}
		switch {
		case seg == "" && elems == 0:
			return nil, fmt.Errorf("path %q: empty segment", raw)
		case strings.ContainsAny(seg, "[]"):
			return nil, fmt.Errorf("path %q: only [] is supported inside brackets (no indexes or slices)", raw)
		case seg == "**":
			steps = append(steps, projStep{kind: projDeep})
		case strings.Contains(seg, "**"):
			return nil, fmt.Errorf("path %q: ** must be a whole segment", raw)
		case seg != "":
			steps = append(steps, projStep{kind: projName, glob: seg})
		}
		for ; elems > 0; elems-- {
			steps = append(steps, projStep{kind: projElem})
		}
	}
	if steps[len(steps)-1].kind == projDeep {
		return nil, fmt.Errorf("path %q: ** cannot end a path", raw)
	}
	return steps, nil
}

// globMatch matches a key against a name step: "*" matches any run of
// characters, everything else is literal.
func globMatch(glob, key string) bool {
	i := strings.IndexByte(glob, '*')
	if i < 0 {
		return glob == key
	}
	if !strings.HasPrefix(key, glob[:i]) {
		return false
	}
	key, glob = key[i:], glob[i+1:]
	for {
		j := strings.IndexByte(glob, '*')
		if j < 0 {
			return strings.HasSuffix(key, glob)
		}
		k := strings.Index(key, glob[:j])
		if k < 0 {
			return false
		}
		key, glob = key[k+j:], glob[j+1:]
	}
}

// ProjectionStats describe one applied projection.
type ProjectionStats struct {
	// Removed counts the members (and, for keep, elements) left out; a
	// subtree left out whole counts once.
	Removed int
}

// Apply projects a JSON object or array. changed is false (and out nil)
// when nothing was left out; err is set when doc is not a JSON object or
// array: the caller keeps the original either way.
func (p *Projection) Apply(doc []byte) (out []byte, stats ProjectionStats, changed bool, err error) {
	doc = bytes.TrimSpace(doc)
	if len(doc) == 0 || (doc[0] != '{' && doc[0] != '[') || !json.Valid(doc) {
		return nil, stats, false, errors.New("not a JSON object or array")
	}
	return p.ApplyValid(doc)
}

// ApplyValid is Apply for a document already known to be a valid JSON
// object or array.
func (p *Projection) ApplyValid(doc []byte) (out []byte, stats ProjectionStats, changed bool, err error) {
	doc = bytes.TrimSpace(doc)
	pr := projector{p: p}
	start := pr.closure(pr.initial())
	var projected []byte
	if p.Mode == ProjectDrop {
		projected = pr.drop(doc, start)
	} else {
		projected, _ = pr.keep(doc, start)
	}
	if pr.err != nil {
		return nil, stats, false, pr.err
	}
	if pr.removed == 0 {
		return nil, stats, false, nil
	}
	var buf bytes.Buffer
	buf.Grow(len(projected))
	if err := json.Compact(&buf, projected); err != nil {
		return nil, stats, false, fmt.Errorf("projected document: %w", err)
	}
	return buf.Bytes(), ProjectionStats{Removed: pr.removed}, true, nil
}

// projState is a position in one path: the next step to match.
type projState struct {
	path, step int
}

type projector struct {
	p       *Projection
	removed int
	err     error
	arena   []projState
}

func (pr *projector) initial() []projState {
	out := make([]projState, len(pr.p.steps))
	for i := range out {
		out[i] = projState{path: i}
	}
	return out
}

// closure adds, for every state on a "**" step, the state past it ("**"
// matches zero levels too).
func (pr *projector) closure(states []projState) []projState {
	for i := 0; i < len(states); i++ {
		s := states[i]
		steps := pr.p.steps[s.path]
		if s.step < len(steps) && steps[s.step].kind == projDeep {
			states = addState(states, projState{s.path, s.step + 1})
		}
	}
	return states
}

func addState(states []projState, s projState) []projState {
	for _, t := range states {
		if t == s {
			return states
		}
	}
	return append(states, s)
}

// advance moves states into a child: an array element (elem) or the member
// named key. full is true when a path ends on the child; direct when a
// path names the child itself (a literal name or a "[]" step), not only
// through a glob, "**" or a name step applied across an array.
//
// The returned sets are carved from one arena and never modified once
// returned; a set equal to states (the usual case under "**") is states
// itself, so walking a large document allocates little.
func (pr *projector) advance(states []projState, elem bool, key string) (next []projState, full, direct bool) {
	if pr.stays(states, elem, key) {
		return states, false, false
	}
	start := len(pr.arena)
	add := func(s projState) {
		for _, t := range pr.arena[start:] {
			if t == s {
				return
			}
		}
		pr.arena = append(pr.arena, s)
	}
	for _, s := range states {
		steps := pr.p.steps[s.path]
		if s.step >= len(steps) {
			continue
		}
		switch st := steps[s.step]; st.kind {
		case projDeep:
			add(s)
		case projElem:
			if elem {
				add(projState{s.path, s.step + 1})
				direct = true
			}
		case projName:
			if elem {
				// A name step on an array applies to its elements.
				add(s)
			} else if globMatch(st.glob, key) {
				add(projState{s.path, s.step + 1})
				direct = direct || strings.IndexByte(st.glob, '*') < 0
			}
		}
	}
	for i := start; i < len(pr.arena); i++ {
		s := pr.arena[i]
		if steps := pr.p.steps[s.path]; s.step < len(steps) && steps[s.step].kind == projDeep {
			add(projState{s.path, s.step + 1})
		}
	}
	end := len(pr.arena)
	switch {
	case end == start:
		return nil, false, direct
	case sameStates(pr.arena[start:end], states):
		pr.arena = pr.arena[:start]
		next = states
	default:
		next = pr.arena[start:end:end]
	}
	for _, s := range next {
		if s.step == len(pr.p.steps[s.path]) {
			full = true
			break
		}
	}
	return next, full, direct
}

// stays reports, cheaply, that moving into the child leaves states as they
// are: every state is a "**" or a step right after one (states is closed),
// and no step past a "**" matches the child. It is the common case of a
// drop pack ("**.node_id", ...) walking the members it does not drop.
func (pr *projector) stays(states []projState, elem bool, key string) bool {
	for _, s := range states {
		steps := pr.p.steps[s.path]
		if s.step >= len(steps) {
			return false
		}
		st := steps[s.step]
		if st.kind == projDeep {
			continue
		}
		if s.step == 0 || steps[s.step-1].kind != projDeep {
			return false
		}
		switch {
		case st.kind == projElem && elem:
			return false
		case st.kind == projName && !elem && globMatch(st.glob, key):
			return false
		}
	}
	return len(states) > 0
}

func sameStates(a, b []projState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isContainer(v []byte) bool { return len(v) > 0 && (v[0] == '{' || v[0] == '[') }

// memberKey decodes a raw member key (escapes only when present).
func memberKey(raw []byte) string {
	if bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1])
	}
	var k string
	if json.Unmarshal(raw, &k) != nil {
		return ""
	}
	return k
}

// drop removes the members the paths select.
func (pr *projector) drop(node []byte, states []projState) []byte {
	if pr.err != nil || len(states) == 0 || !isContainer(node) {
		return node
	}
	if node[0] == '[' {
		elems, err := SplitArray(node)
		if err != nil {
			pr.err = err
			return node
		}
		changed := false
		next, _, _ := pr.advance(states, true, "")
		for i, el := range elems {
			if !isContainer(el) {
				continue
			}
			if v := pr.drop(el, next); len(v) != len(el) || !bytes.Equal(v, el) {
				elems[i], changed = v, true
			}
		}
		if !changed {
			return node
		}
		return appendArray(nil, elems)
	}
	members, err := SplitObject(node)
	if err != nil {
		pr.err = err
		return node
	}
	kept := members[:0] // filtered in place: writes never pass reads
	changed := false
	for _, m := range members {
		next, full, _ := pr.advance(states, false, memberKey(m.Key))
		if full {
			pr.removed++
			changed = true
			continue
		}
		if len(next) > 0 && isContainer(m.Value) {
			if v := pr.drop(m.Value, next); len(v) != len(m.Value) || !bytes.Equal(v, m.Value) {
				m.Value, changed = v, true
			}
		}
		kept = append(kept, m)
	}
	if !changed {
		return node
	}
	return appendObject(nil, kept)
}

// keep rebuilds node with only what the paths select; hit reports whether
// anything under node was selected.
func (pr *projector) keep(node []byte, states []projState) (out []byte, hit bool) {
	if pr.err != nil {
		return node, false
	}
	if node[0] == '[' {
		elems, err := SplitArray(node)
		if err != nil {
			pr.err = err
			return node, false
		}
		out := make([][]byte, 0, len(elems))
		next, full, direct := pr.advance(states, true, "")
		for _, el := range elems {
			v, h, ok := pr.keepChild(el, next, full, direct)
			if !ok {
				pr.removed++
				continue
			}
			hit = hit || h
			out = append(out, v)
		}
		return appendArray(nil, out), hit || len(elems) == 0
	}
	members, err := SplitObject(node)
	if err != nil {
		pr.err = err
		return node, false
	}
	kept := make([]Member, 0, len(members))
	for _, m := range members {
		next, full, direct := pr.advance(states, false, memberKey(m.Key))
		v, h, ok := pr.keepChild(m.Value, next, full, direct)
		if !ok {
			pr.removed++
			continue
		}
		hit = hit || h
		kept = append(kept, Member{Key: m.Key, Value: v})
	}
	return appendObject(nil, kept), hit
}

// keepChild decides one member or element of a keep projection: kept whole
// (a path ends on it), projected (a path goes through it), or left out.
func (pr *projector) keepChild(v []byte, next []projState, full, direct bool) (out []byte, hit, ok bool) {
	switch {
	case full:
		return v, true, true
	case len(next) == 0:
		return nil, false, false
	case isContainer(v):
		removed := pr.removed
		projected, h := pr.keep(v, next)
		if h {
			return projected, true, true
		}
		if direct {
			return projected, false, true
		}
		// Left out whole: count it once, not what was left out inside.
		pr.removed = removed
		return nil, false, false
	case direct:
		// null or a scalar where the path expects more: it is what is there.
		return v, true, true
	}
	return nil, false, false
}

// appendArray writes raw elements as a JSON array.
func appendArray(out []byte, elems [][]byte) []byte {
	n := 2
	for _, e := range elems {
		n += len(e) + 1
	}
	out = slices.Grow(out, n)
	out = append(out, '[')
	for i, e := range elems {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, e...)
	}
	return append(out, ']')
}

// appendObject writes members as a JSON object, in order.
func appendObject(out []byte, members []Member) []byte {
	n := 2
	for _, m := range members {
		n += len(m.Key) + len(m.Value) + 2
	}
	out = slices.Grow(out, n)
	out = append(out, '{')
	for i, m := range members {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, m.Key...)
		out = append(out, ':')
		out = append(out, m.Value...)
	}
	return append(out, '}')
}
