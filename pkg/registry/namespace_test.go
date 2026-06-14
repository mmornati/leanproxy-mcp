package registry

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestNamespaceManager_Load(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  engineering:
    description: "Engineering team tools"
    servers:
      - github
      - jira
  ops:
    servers:
      - aws
      - kubernetes
`
	ctx := context.Background()
	err := mgr.Load(ctx, strings.NewReader(yamlConfig))
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	namespaces := mgr.GetAllNamespaces(ctx)
	if len(namespaces) != 2 {
		t.Errorf("GetAllNamespaces() returned %d, want 2", len(namespaces))
	}
}

func TestNamespaceManager_LoadNested(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  engineering:
    description: "Engineering team"
    servers:
      - github
    children:
      frontend:
        servers:
          - storybook
`
	ctx := context.Background()
	err := mgr.Load(ctx, strings.NewReader(yamlConfig))
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	ns, err := mgr.GetNamespace(ctx, "engineering.frontend")
	if err != nil {
		t.Errorf("GetNamespace(engineering.frontend) failed: %v", err)
	}
	if ns == nil {
		t.Error("GetNamespace(engineering.frontend) returned nil")
	}

	namespaces := mgr.GetAllNamespaces(ctx)
	if len(namespaces) < 2 {
		t.Errorf("GetAllNamespaces() returned %d, want at least 2", len(namespaces))
	}
}

func TestNamespaceManager_GetNamespace(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  testns:
    description: "Test namespace"
    servers:
      - server1
      - server2
`
	ctx := context.Background()
	mgr.Load(ctx, strings.NewReader(yamlConfig))

	ns, err := mgr.GetNamespace(ctx, "testns")
	if err != nil {
		t.Fatalf("GetNamespace() failed: %v", err)
	}
	if ns.Name != "testns" {
		t.Errorf("ns.Name = %v, want testns", ns.Name)
	}
	if ns.Description != "Test namespace" {
		t.Errorf("ns.Description = %v, want 'Test namespace'", ns.Description)
	}
	if len(ns.Servers) != 2 {
		t.Errorf("len(ns.Servers) = %d, want 2", len(ns.Servers))
	}
}

func TestNamespaceManager_GetNamespaceNotFound(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	ctx := context.Background()

	_, err := mgr.GetNamespace(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent namespace")
	}
}

func TestNamespaceManager_CheckAccess(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  restricted:
    allowed_clients:
      - client1
      - client2
    servers:
      - server1
  open:
    servers:
      - server2
`
	ctx := context.Background()
	mgr.Load(ctx, strings.NewReader(yamlConfig))

	err := mgr.CheckAccess(ctx, "restricted", "client1")
	if err != nil {
		t.Errorf("CheckAccess() for client1 failed: %v", err)
	}

	err = mgr.CheckAccess(ctx, "restricted", "client2")
	if err != nil {
		t.Errorf("CheckAccess() for client2 failed: %v", err)
	}

	err = mgr.CheckAccess(ctx, "restricted", "unauthorized")
	if err == nil {
		t.Error("Expected error for unauthorized client")
	}

	err = mgr.CheckAccess(ctx, "open", "anyclient")
	if err != nil {
		t.Errorf("CheckAccess() for open namespace failed: %v", err)
	}
}

func TestNamespaceManager_CheckAccessWildcard(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  public:
    allowed_clients:
      - "*"
    servers:
      - server1
`
	ctx := context.Background()
	mgr.Load(ctx, strings.NewReader(yamlConfig))

	err := mgr.CheckAccess(ctx, "public", "anyone")
	if err != nil {
		t.Errorf("CheckAccess() with wildcard failed: %v", err)
	}
}

func TestNamespaceManager_GetServerNamespace(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  eng:
    servers:
      - github
      - jira
  ops:
    servers:
      - aws
`
	ctx := context.Background()
	mgr.Load(ctx, strings.NewReader(yamlConfig))

	ns, err := mgr.GetServerNamespace(ctx, "github")
	if err != nil {
		t.Errorf("GetServerNamespace(github) failed: %v", err)
	}
	if ns != "eng" {
		t.Errorf("GetServerNamespace(github) = %v, want eng", ns)
	}

	ns, err = mgr.GetServerNamespace(ctx, "aws")
	if err != nil {
		t.Errorf("GetServerNamespace(aws) failed: %v", err)
	}
	if ns != "ops" {
		t.Errorf("GetServerNamespace(aws) = %v, want ops", ns)
	}

	_, err = mgr.GetServerNamespace(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for server not in any namespace")
	}
}

func TestNamespaceManager_GetChildNamespaces(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  parent:
    servers:
      - server1
    children:
      child1:
        servers:
          - server2
      child2:
        servers:
          - server3
`
	ctx := context.Background()
	mgr.Load(ctx, strings.NewReader(yamlConfig))

	children, err := mgr.GetChildNamespaces(ctx, "parent")
	if err != nil {
		t.Errorf("GetChildNamespaces() failed: %v", err)
	}
	if len(children) < 2 {
		t.Errorf("GetChildNamespaces() returned %d, want at least 2", len(children))
	}
}

func TestNamespaceManager_GetToolsForNamespace(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  engineering:
    servers:
      - github
      - jira
`
	ctx := context.Background()
	mgr.Load(ctx, strings.NewReader(yamlConfig))

	tools, err := mgr.GetToolsForNamespace(ctx, "engineering")
	if err != nil {
		t.Errorf("GetToolsForNamespace() failed: %v", err)
	}
	if len(tools) == 0 {
		t.Error("GetToolsForNamespace() returned empty list")
	}
}

func TestNamespaceManager_NestedHierarchy(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	yamlConfig := `
namespaces:
  root:
    servers:
      - rootserver
    children:
      level1:
        servers:
          - level1server
        children:
          level2:
            servers:
              - level2server
`
	ctx := context.Background()
	mgr.Load(ctx, strings.NewReader(yamlConfig))

	ns, err := mgr.GetNamespace(ctx, "root.level1.level2")
	if err != nil {
		t.Errorf("GetNamespace() for deeply nested failed: %v", err)
	}
	if ns == nil {
		t.Error("Deeply nested namespace not found")
	}

	namespaces := mgr.GetAllNamespaces(ctx)
	if len(namespaces) < 3 {
		t.Errorf("GetAllNamespaces() returned %d, want at least 3", len(namespaces))
	}
}

func TestNamespaceManager_LoadEmpty(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	ctx := context.Background()
	err := mgr.Load(ctx, strings.NewReader("namespaces: {}"))
	if err != nil {
		t.Errorf("Load() with empty config failed: %v", err)
	}

	namespaces := mgr.GetAllNamespaces(ctx)
	if len(namespaces) != 0 {
		t.Errorf("GetAllNamespaces() returned %d, want 0", len(namespaces))
	}
}

func TestNamespaceManager_LoadNil(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	ctx := context.Background()
	err := mgr.Load(ctx, nil)
	if err != nil {
		t.Errorf("Load() with nil reader failed: %v", err)
	}
}

func TestNamespaceManager_CheckAccessNamespaceNotFound(t *testing.T) {
	logger := newTestLogger(t)
	mgr := NewNamespaceManager(logger)

	ctx := context.Background()

	err := mgr.CheckAccess(ctx, "nonexistent", "client")
	if err == nil {
		t.Error("Expected error for nonexistent namespace in CheckAccess")
	}
}

func newTestLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}
