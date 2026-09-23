package mcp

import "testing"

func TestSplitToolName(t *testing.T) {
	servers := []string{"git", "github", "my_srv"}

	tests := []struct {
		name       string
		ref        string
		wantServer string
		wantTool   string
		wantErr    bool
	}{
		{name: "dot form", ref: "git.status", wantServer: "git", wantTool: "status"},
		{name: "underscore form", ref: "git_status", wantServer: "git", wantTool: "status"},
		{
			name:       "overlapping names pick the longer match",
			ref:        "github_search_issues",
			wantServer: "github",
			wantTool:   "search_issues",
		},
		{
			name:       "overlapping names, short server, underscore tool",
			ref:        "git_hub_status",
			wantServer: "git",
			wantTool:   "hub_status",
		},
		{name: "server name itself has an underscore, underscore form", ref: "my_srv_tool", wantServer: "my_srv", wantTool: "tool"},
		{name: "server name itself has an underscore, dot form", ref: "my_srv.tool", wantServer: "my_srv", wantTool: "tool"},
		{name: "no matching server", ref: "unknown_tool", wantErr: true},
		{name: "server name with nothing after separator", ref: "git_", wantErr: true},
		{name: "empty ref", ref: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, tool, err := SplitToolName(tt.ref, servers)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SplitToolName(%q) = (%q, %q, nil), want error", tt.ref, server, tool)
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitToolName(%q) unexpected error: %v", tt.ref, err)
			}
			if server != tt.wantServer || tool != tt.wantTool {
				t.Errorf("SplitToolName(%q) = (%q, %q), want (%q, %q)", tt.ref, server, tool, tt.wantServer, tt.wantTool)
			}
		})
	}
}

func TestSplitToolName_NoServers(t *testing.T) {
	if _, _, err := SplitToolName("anything_here", nil); err == nil {
		t.Fatal("expected error when no servers are configured")
	}
}
