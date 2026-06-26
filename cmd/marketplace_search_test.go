package cmd

import (
	"bytes"
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

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := runMarketplaceSearch(cmd, []string{"test"})
	if err == nil {
		t.Fatal("expected error when cache is empty")
	}
	if !strings.Contains(err.Error(), "marketplace sync") {
		t.Errorf("error should mention 'marketplace sync', got: %v", err)
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
	if !strings.Contains(output, "TRUST") {
		t.Errorf("output should have TRUST column header: %s", output)
	}
	if !strings.Contains(output, "LAST RELEASE") {
		t.Errorf("output should have LAST RELEASE column header: %s", output)
	}
	if !strings.Contains(output, "OPEN ISSUES") {
		t.Errorf("output should have OPEN ISSUES column header: %s", output)
	}
	if !strings.Contains(output, "DOWNLOADS") {
		t.Errorf("output should have DOWNLOADS column header: %s", output)
	}
	if !strings.Contains(output, "EST. TOKENS/TURN") {
		t.Errorf("output should have EST. TOKENS/TURN column header: %s", output)
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

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runMarketplaceSearch(cmd, []string{""}); err != nil {
		t.Fatalf("expected success with empty query, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "github") {
		t.Errorf("empty query should match all entries: %s", stdout.String())
	}
}
