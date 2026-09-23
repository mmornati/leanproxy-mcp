//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// conformanceVersions are the MCP revisions the proxy negotiates (#307);
// keep in step with pkg/mcp.SupportedProtocolVersions.
var conformanceVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

// TestHarness_ProtocolConformance is the #307 conformance smoke test: an
// mcp-go client (the SDK real hosts are built on) drives the real binary at
// every supported revision through initialize, tools/list, a gateway tool
// call, and the aggregated resources and prompts of two upstreams.
func TestHarness_ProtocolConformance(t *testing.T) {
	bins := sharedBinaries(t)
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	servers := catalogServers(cat, "--resources")[:2]
	e := newEnv(t, bins, servers, "")

	for _, version := range conformanceVersions {
		t.Run(version, func(t *testing.T) {
			c, err := client.NewStdioMCPClient(bins.proxy, e.vars, "server", "run", "--stdio", "--config", e.cfg,
				"--log-file", filepath.Join(e.dir, "conformance-"+version+".log"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			init, err := c.Initialize(ctx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{
				ProtocolVersion: version,
				ClientInfo:      mcpgo.Implementation{Name: "harness-conformance", Version: "1"},
			}})
			if err != nil {
				t.Fatalf("initialize: %v", err)
			}
			if init.ProtocolVersion != version {
				t.Fatalf("negotiated %s, want %s", init.ProtocolVersion, version)
			}
			if init.Capabilities.Resources == nil || init.Capabilities.Prompts == nil {
				t.Fatalf("resources/prompts not advertised: %+v", init.Capabilities)
			}

			tools, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
			if err != nil || len(tools.Tools) != 4 {
				t.Fatalf("tools/list: %v %+v", err, tools)
			}

			res, err := c.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{
				Name: "list_tools", Arguments: map[string]interface{}{"server_name": servers[0].name},
			}})
			if err != nil || res.IsError || len(res.Content) == 0 {
				t.Fatalf("list_tools: %v %+v", err, res)
			}
			wantStructured := version >= "2025-06-18"
			if got := res.StructuredContent != nil; got != wantStructured {
				t.Fatalf("list_tools structuredContent present = %v, want %v", got, wantStructured)
			}

			resources, err := c.ListResources(ctx, mcpgo.ListResourcesRequest{})
			if err != nil || len(resources.Resources) != 2 {
				t.Fatalf("resources/list: %v %+v", err, resources)
			}
			for i, r := range resources.Resources {
				want := fmt.Sprintf("leanproxy://%s/catalog://%s/readme", servers[i].name, servers[i].name)
				if r.URI != want {
					t.Fatalf("resource %d uri %s, want %s", i, r.URI, want)
				}
				read, err := c.ReadResource(ctx, mcpgo.ReadResourceRequest{Params: mcpgo.ReadResourceParams{URI: r.URI}})
				if err != nil || len(read.Contents) != 1 {
					t.Fatalf("resources/read %s: %v %+v", r.URI, err, read)
				}
				text, ok := read.Contents[0].(mcpgo.TextResourceContents)
				if !ok || text.URI != r.URI || text.Text != "readme of "+servers[i].name {
					t.Fatalf("resources/read %s = %+v", r.URI, read.Contents[0])
				}
			}
			templates, err := c.ListResourceTemplates(ctx, mcpgo.ListResourceTemplatesRequest{})
			if err != nil || len(templates.ResourceTemplates) != 2 {
				t.Fatalf("resources/templates/list: %v %+v", err, templates)
			}

			prompts, err := c.ListPrompts(ctx, mcpgo.ListPromptsRequest{})
			if err != nil || len(prompts.Prompts) != 2 {
				t.Fatalf("prompts/list: %v %+v", err, prompts)
			}
			name := servers[1].name + ".brief"
			got, err := c.GetPrompt(ctx, mcpgo.GetPromptRequest{Params: mcpgo.GetPromptParams{Name: name}})
			if err != nil || len(got.Messages) != 1 {
				t.Fatalf("prompts/get %s: %v %+v", name, err, got)
			}
			msg, _ := json.Marshal(got.Messages[0])
			if !strings.Contains(string(msg), "brief from "+servers[1].name) {
				t.Fatalf("prompts/get %s = %s", name, msg)
			}
		})
	}
}
