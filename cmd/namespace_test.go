package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

func TestNamespaceCmd_HelpOutput(t *testing.T) {
	cmd := namespaceCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestNamespaceListCmd_HelpOutput(t *testing.T) {
	cmd := namespaceListCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestNamespaceAddCmd_HelpOutput(t *testing.T) {
	cmd := namespaceAddCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestNamespaceAssignCmd_HelpOutput(t *testing.T) {
	cmd := namespaceAssignCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestNamespaceListCmd_EmptyList(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy.yaml")

	cfg := `namespaces: {}
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := namespaceListCmd
	cmd.SetArgs([]string{"--config", configPath})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("list should not error: %v", err)
	}
}

func TestNamespaceAddCmd_AddNamespace(t *testing.T) {
	cmd := namespaceAddCmd
	cmd.SetArgs([]string{"engineering", "--servers=github,jira", "--description=Engineering team"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("add namespace should not error: %v", err)
	}
}

func TestNamespaceAssignCmd_AssignServer(t *testing.T) {
	cmd := namespaceAssignCmd
	cmd.SetArgs([]string{"engineering", "github"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("assign server should not error: %v", err)
	}
}

func TestGetChildNames(t *testing.T) {
	children := map[string]*registry.Namespace{
		"child1": {Name: "child1"},
		"child2": {Name: "child2"},
	}

	result := getChildNames(children)
	if len(result) != 2 {
		t.Errorf("expected 2 children, got %d", len(result))
	}
}

func TestGetChildNames_Empty(t *testing.T) {
	children := map[string]*registry.Namespace{}

	result := getChildNames(children)
	if len(result) != 0 {
		t.Errorf("expected 0 children, got %d", len(result))
	}
}