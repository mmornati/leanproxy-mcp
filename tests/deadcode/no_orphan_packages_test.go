// Package deadcode guards against the class of bug issue #303 fixed: a
// package that only its own tests ever import. Such a package is advertised
// in README/docs as if it were live, but nothing in the binary actually
// calls it — see the audit in that issue for the list of packages this
// found (pkg/budget, pkg/federation, pkg/connpool, pkg/proxy/socket, and
// several definitions-only files inside otherwise-live packages).
package deadcode

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/mmornati/leanproxy-mcp"

// allowedPrefixes lists import paths (or path prefixes ending in "/") that
// are allowed to have zero non-test importers within this module: CLI/binary
// entry points (main packages), example server mains, the version stamp
// package used at build time via -ldflags, and the test packages themselves.
var allowedPrefixes = []string{
	modulePath,                       // the module root, package main
	modulePath + "/cmd",              // the `leanproxy-mcp` CLI, invoked by main.go
	modulePath + "/servers/",         // example/reference MCP server mains
	modulePath + "/tests/",           // test-only packages, including this one
	modulePath + "/internal/version", // stamped via -ldflags, not imported
}

func allowed(importPath string) bool {
	for _, p := range allowedPrefixes {
		if importPath == p || strings.HasPrefix(importPath, p+"/") {
			return true
		}
	}
	return false
}

// goListPkg mirrors the subset of `go list -json` output this test needs.
// Imports is the set of packages imported by the package's non-test .go
// files; it deliberately ignores TestImports/XTestImports, so a package
// imported only from _test.go files anywhere in the module still counts as
// having zero non-test importers.
type goListPkg struct {
	ImportPath string
	Name       string
	Imports    []string
}

func TestNoOrphanPackages(t *testing.T) {
	out, err := exec.Command("go", "list", "-json", "./...").Output()
	if err != nil {
		t.Fatalf("go list -json ./...: %v", err)
	}

	pkgs := make([]goListPkg, 0, 128) // rough count of this module's packages; grows via append below
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p goListPkg
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decoding `go list -json` output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list -json ./... returned no packages — test harness is broken")
	}

	importedBy := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			if strings.HasPrefix(imp, modulePath) {
				importedBy[imp] = true
			}
		}
	}

	var orphans []string
	for _, p := range pkgs {
		if allowed(p.ImportPath) {
			continue
		}
		if p.Name == "main" {
			// A non-allowlisted main package is its own kind of orphan risk,
			// but it is not what this check is for; leave it to code review.
			continue
		}
		if !importedBy[p.ImportPath] {
			orphans = append(orphans, p.ImportPath)
		}
	}

	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("packages with no non-test importer in this module (dead code — see issue #303):\n  %s\n\n"+
			"Either wire the package into a command, delete it, or add its import path to allowedPrefixes "+
			"in tests/deadcode/no_orphan_packages_test.go with a comment explaining why it is exempt.",
			strings.Join(orphans, "\n  "))
	}
}
