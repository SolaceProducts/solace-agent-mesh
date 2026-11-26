// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMCPEffectHints_DiscoveryToPersistedManifest(t *testing.T) {
	t.Parallel()
	readOnly, notDestructive, openWorld := true, false, true

	if got := declaredEffectHints(mcp.ToolAnnotation{}); got != nil {
		t.Fatalf("a tool with no hints produced %+v, want nil so the DTO omits annotations", got)
	}
	if got := declaredEffectHints(mcp.ToolAnnotation{OpenWorldHint: &openWorld}); got == nil || got.OpenWorldHint == nil || !*got.OpenWorldHint {
		t.Fatalf("a tool whose only hint is openWorldHint produced %+v, want the hint kept", got)
	}
	hints := declaredEffectHints(mcp.ToolAnnotation{ReadOnlyHint: &readOnly, DestructiveHint: &notDestructive, OpenWorldHint: &openWorld})

	entries := buildMCPManifestEntries([]MCPDiscoveredTool{
		{Name: "lookup", Description: "Looks up", Annotations: hints},
		{Name: "plain", Description: "Says nothing"},
	})
	got, ok := entries[0]["annotations"].(map[string]any)
	if !ok || got["readOnlyHint"] != true || got["destructiveHint"] != false || got["openWorldHint"] != true {
		t.Fatalf("entries[0].annotations = %#v, want readOnlyHint true, destructiveHint false and openWorldHint true under their MCP names", entries[0]["annotations"])
	}
	if _, present := entries[1]["annotations"]; present {
		t.Errorf("entries[1] carries annotations %v for a server that gave none", entries[1]["annotations"])
	}

	wire, err := json.Marshal(MCPDiscoveredTool{Name: "lookup", Annotations: hints})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"name":"lookup","description":"","inputSchema":null,"annotations":{"readOnlyHint":true,"destructiveHint":false,"openWorldHint":true}}`
	if string(wire) != want {
		t.Errorf("wire = %s\nwant  %s", wire, want)
	}
}
