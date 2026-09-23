package toolpin

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Render returns the text form of a canonical definition that diffs are
// computed on: one "field:" header per present field, the description one
// line per line, schemas and annotations pretty-printed with sorted keys.
func Render(canonical json.RawMessage) string {
	if len(canonical) == 0 {
		return ""
	}
	var d Definition
	if err := json.Unmarshal(canonical, &d); err != nil {
		return string(canonical) + "\n"
	}
	var b strings.Builder
	b.WriteString("name: " + d.Name + "\n")
	if d.Title != "" {
		b.WriteString("title: " + d.Title + "\n")
	}
	if d.Description != "" {
		b.WriteString("description:\n")
		for _, line := range strings.Split(d.Description, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	for _, f := range []struct {
		name string
		raw  json.RawMessage
	}{{"inputSchema", d.InputSchema}, {"outputSchema", d.OutputSchema}, {"annotations", d.Annotations}} {
		if len(f.raw) == 0 {
			continue
		}
		b.WriteString(f.name + ":\n")
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, f.raw, "  ", "  "); err != nil {
			pretty.Reset()
			pretty.Write(f.raw)
		}
		b.WriteString("  ")
		b.Write(pretty.Bytes())
		b.WriteString("\n")
	}
	return b.String()
}

// UnifiedDiff returns the unified diff (3 lines of context) of two
// canonical definitions; either may be empty (added or removed tool).
func UnifiedDiff(oldDef, newDef json.RawMessage, oldLabel, newLabel string) string {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(Render(oldDef)),
		B:        difflib.SplitLines(Render(newDef)),
		FromFile: oldLabel,
		ToFile:   newLabel,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return diff
}

// Diff returns the diff of a tool pin: approved definition against the
// pending one (empty when nothing is pending).
func (t *ToolPin) Diff(server, tool string) string {
	if t == nil || t.Pending == nil {
		return ""
	}
	oldLabel := server + "/" + tool + " (approved)"
	if t.Hash == "" {
		oldLabel = server + "/" + tool + " (not pinned)"
	}
	return UnifiedDiff(t.Definition, t.Pending.Definition, oldLabel, server+"/"+tool+" (now served)")
}
