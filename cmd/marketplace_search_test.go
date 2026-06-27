package cmd

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

func TestMarketplaceSearchCmd_Registered(t *testing.T) {
	var found bool
	for _, c := range marketplaceCmd.Commands() {
		if c.Use == "search <query>" {
			found = true
			if c.Short == "" {
				t.Error("search command should have a short description")
			}
			if c.Args == nil {
				t.Error("search command should declare an Args validator")
			}
			break
		}
	}
	if !found {
		t.Fatal("'search' subcommand not registered on marketplaceCmd")
	}
}

func TestMarketplaceSearchCmd_RejectsNoArgs(t *testing.T) {
	if err := marketplaceSearchCmd.Args(marketplaceSearchCmd, []string{}); err == nil {
		t.Error("expected error when no args provided")
	}
}

func TestMarketplaceSearchCmd_RejectsMultipleArgs(t *testing.T) {
	if err := marketplaceSearchCmd.Args(marketplaceSearchCmd, []string{"a", "b"}); err == nil {
		t.Error("expected error when multiple args provided")
	}
}

func TestRunMarketplaceSearch_EmptyCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceSearch(cmd, []string{"test"}); err != nil {
		t.Fatalf("empty cache should not error, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "marketplace sync") {
		t.Errorf("output should hint at 'marketplace sync', got: %s", stdout.String())
	}
}

func TestRunMarketplaceSearch_NoMatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{Name: "github", Description: "GitHub MCP server"},
		},
	})

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceSearch(cmd, []string{"zzzzz"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No servers found") {
		t.Errorf("expected 'No servers found', got: %s", stdout.String())
	}
}

func TestRunMarketplaceSearch_WithMatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{
				Name:          "github",
				Description:   "GitHub integration",
				Transport:     "stdio",
				Command:       "gh-mcp",
				TrustScore:    90,
				LastRelease:   time.Now().Format(time.RFC3339),
				OpenIssues:    3,
				Downloads:     50000,
				TokensPerTurn: 1200,
			},
			{
				Name:          "gitlab",
				Description:   "GitLab integration",
				Transport:     "stdio",
				Command:       "gl-mcp",
				TrustScore:    85,
				LastRelease:   "2025-01-15",
				OpenIssues:    12,
				Downloads:     8000,
				TokensPerTurn: 900,
			},
			{
				Name:        "database",
				Description: "PostgreSQL MCP server",
			},
		},
	})

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceSearch(cmd, []string{"git"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()

	if !strings.Contains(output, "github") {
		t.Errorf("output should contain 'github': %s", output)
	}
	if !strings.Contains(output, "gitlab") {
		t.Errorf("output should contain 'gitlab': %s", output)
	}
	if strings.Contains(output, "database") {
		t.Errorf("output should NOT contain 'database': %s", output)
	}
	wantCols := []string{"name", "trust", "last release", "open issues", "downloads", "est tokens/turn"}
	headerLine := strings.SplitN(output, "\n", 2)[0]
	if !strings.HasPrefix(headerLine, "name") {
		t.Fatalf("first output line should start with 'name', got: %q", headerLine)
	}
	lastIdx := -1
	for _, col := range wantCols {
		idx := strings.Index(headerLine, col)
		if idx < 0 {
			t.Errorf("header missing column %q, got: %q", col, headerLine)
			continue
		}
		if idx <= lastIdx {
			t.Errorf("column %q appears out of order at idx %d (prev %d): %q", col, idx, lastIdx, headerLine)
		}
		lastIdx = idx
	}
}

func TestRunMarketplaceSearch_EmptyQuery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{Name: "github"},
		},
	})

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceSearch(cmd, []string{""}); err == nil {
		t.Fatal("expected error for empty query (would match everything)")
	}
}

func TestRunMarketplaceSearch_WhitespaceQuery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{Name: "github"},
		},
	})

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceSearch(cmd, []string{"   "}); err == nil {
		t.Fatal("expected error for whitespace-only query (would match everything)")
	}
}

func TestRunMarketplaceSearch_RespectsLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	entries := make([]registry.RegistryFeedEntry, 0, 10)
	for i := 0; i < 10; i++ {
		entries = append(entries, registry.RegistryFeedEntry{Name: fmt.Sprintf("server-%02d", i)})
	}
	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries:  entries,
	})

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	prev := searchLimit
	searchLimit = 3
	defer func() { searchLimit = prev }()

	if err := runMarketplaceSearch(cmd, []string{"server"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	// 1 header row + 3 data rows = 4 total.
	if len(rows) != 4 {
		t.Errorf("expected 4 rows (header + 3 matches), got %d:\n%s", len(rows), stdout.String())
	}
}
