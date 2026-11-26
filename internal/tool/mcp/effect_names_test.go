// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func newResourceMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := server.NewMCPServer("resources", "1.0.0", server.WithToolCapabilities(true))
	report := func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultResource("the report", mcpgo.TextResourceContents{
			URI: "file:///reports/summary.md", MIMEType: "text/markdown", Text: "# Summary\nall good\n",
		}), nil
	}
	srv.AddTool(mcpgo.NewTool("read_report", mcpgo.WithDescription("Reads a report"),
		mcpgo.WithReadOnlyHintAnnotation(true), mcpgo.WithDestructiveHintAnnotation(false), mcpgo.WithOpenWorldHintAnnotation(false)), report)
	srv.AddTool(mcpgo.NewTool("rebuild_report", mcpgo.WithDescription("Rebuilds a report")), report)
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)
	return ts
}

func savedNames(t *testing.T, mcpTool *MCPTool) []string {
	t.Helper()
	toolCtx := newMCPToolCtx(t)
	var names []string
	for range 2 {
		result, err := mcpTool.Execute(t.Context(), map[string]any{}, toolCtx)
		if err != nil {
			t.Fatalf("%s Execute: %v", mcpTool.Name(), err)
		}
		details, ok := result.Data["saved_mcp_response_artifact_details"].(*mcpSaveResult)
		if !ok || len(details.ArtifactsSaved) == 0 {
			t.Fatalf("%s result has no saved artifact: %+v", mcpTool.Name(), result.Data)
		}
		for _, saved := range details.ArtifactsSaved {
			names = append(names, saved.DataFilename)
		}
	}
	return names
}

func TestTrustedReadOnlyMCPTool_SavesItsResourcesUnderFreshNamesNotTheServersURI(t *testing.T) {
	ts := newResourceMCPServer(t)
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	cfg := sharedEntryConfig(t, ts.URL, entryFilter{}, withTrustedAnnotations)
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	discovered, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	byName := map[string]*MCPTool{}
	for _, mt := range discovered {
		byName[mt.Name()] = mt
	}

	seen := map[string]bool{}
	for _, name := range savedNames(t, byName["read_report"]) {
		if strings.Contains(name, "summary.md") {
			t.Errorf("a read_only tool saved under %q, a name the server's URI chose", name)
		}
		if seen[name] {
			t.Errorf("a read_only tool saved two results as %q; each must get a fresh name", name)
		}
		seen[name] = true
	}

	kept := 0
	for _, name := range savedNames(t, byName["rebuild_report"]) {
		if strings.HasSuffix(name, "summary.md") {
			kept++
		}
	}
	if kept != 2 {
		t.Errorf("a side_effecting tool kept the URI-derived name %d times in two calls, want 2", kept)
	}
}

func TestWithFreshFilenames_KeepsAShortExtensionAndNeverTheServersName(t *testing.T) {
	t.Parallel()
	items := []ContentItem{
		{Type: ContentTypeResource, Filename: "lookup_resource_0_summary.md"},
		{Type: ContentTypeResource, Filename: "lookup_resource_1_summary.md"},
		{Type: ContentTypeImage, Filename: "payload.averyveryverylongextension"},
	}
	got := withFreshFilenames(items, "lookup")
	for i, want := range []string{".md", ".md", ".bin"} {
		if !strings.HasSuffix(got[i].Filename, want) || got[i].Filename == items[i].Filename {
			t.Errorf("item %d: filename %q from %q, want a fresh name ending %q", i, got[i].Filename, items[i].Filename, want)
		}
	}
	if got[0].Filename == got[1].Filename {
		t.Errorf("two items share the fresh name %q", got[0].Filename)
	}
	if items[0].Filename != "lookup_resource_0_summary.md" {
		t.Errorf("the input slice was modified: %q", items[0].Filename)
	}
}

func TestTrustedReadOnlyMCPTool_AnOperatorSideEffectingOverrideKeepsTheServersNames(t *testing.T) {
	ts := newResourceMCPServer(t)
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	cfg := sharedEntryConfig(t, ts.URL, entryFilter{}, withTrustedAnnotations)
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	discovered, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	set := tool.NewSet()
	for _, mt := range discovered {
		if registerErr := set.Register(mt); registerErr != nil {
			t.Fatalf("Register %s: %v", mt.Name(), registerErr)
		}
	}
	set.SetEffectOverrides(map[string]tool.EffectClass{"read_report": {Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}})

	result, err := set.Dispatch(t.Context(), tool.Call{Name: "read_report", Arguments: "{}"}, newMCPToolCtx(t))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	details, ok := result.Data["saved_mcp_response_artifact_details"].(*mcpSaveResult)
	if !ok || len(details.ArtifactsSaved) == 0 {
		t.Fatalf("result has no saved artifact: %+v", result.Data)
	}
	kept := false
	for _, saved := range details.ArtifactsSaved {
		kept = kept || strings.HasSuffix(saved.DataFilename, "summary.md")
	}
	if !kept {
		t.Errorf("a tool the operator marked side_effecting saved %+v, want one under the server's URI-derived name", details.ArtifactsSaved)
	}
}
