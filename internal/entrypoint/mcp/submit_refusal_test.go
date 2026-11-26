// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func refusalAdapter(t *testing.T) *Adapter {
	t.Helper()
	return &Adapter{
		cfg:        mustParseConfig(t, nil),
		toolMap:    map[string]toolMapping{"known_tool": {agentName: "test-agent"}},
		agentTools: make(map[string][]string),
		tasks:      make(map[string]*taskState),
		logger:     noopLogger(),
	}
}

func toolCall(name, message string) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	req.Params.Name = name
	if message != "" {
		req.Params.Arguments = map[string]any{"message": message}
	}
	return req
}

func TestMCPToolCallRefusalsReportTheirReason(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		message  string
		reason   string
	}{
		{"a call with no message is malformed", "known_tool", "", taxonomy.RejectMalformed},
		{"a tool nothing is mapped to has no agent to reach", "no_such_tool", "hello", taxonomy.RejectAgentNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, reason, err := refusalAdapter(t).callTool(context.Background(), c.toolName, toolCall(c.toolName, c.message))
			if err != nil {
				t.Fatalf("callTool returned an error rather than a tool result: %v", err)
			}
			if reason != c.reason {
				t.Errorf("refusal reason = %q, want %q", reason, c.reason)
			}
		})
	}
}

func TestMCPRefusalReasonsAreInTheClosedSet(t *testing.T) {
	calls := []mcplib.CallToolRequest{
		toolCall("known_tool", ""),
		toolCall("no_such_tool", "hello"),
		toolCall("no_such_tool", ""),
	}
	for _, call := range calls {
		_, reason, _ := refusalAdapter(t).callTool(context.Background(), call.Params.Name, call)
		if reason == "" {
			continue
		}
		if !slices.Contains(taxonomy.RejectReasons, reason) {
			t.Errorf("refusal reason %q is outside the closed set", reason)
		}
	}
}

func TestAnMCPRefusalYieldsOneReason(t *testing.T) {
	_, reason, _ := refusalAdapter(t).callTool(context.Background(), "no_such_tool", toolCall("no_such_tool", ""))
	if reason != taxonomy.RejectMalformed {
		t.Errorf("refusal reason = %q, want the answering gate's %q alone", reason, taxonomy.RejectMalformed)
	}
}

func TestTheMCPAdapterDeclaresTheEntrypointItsRefusalsReport(t *testing.T) {
	a := &Adapter{}
	if !a.MachineIngress() {
		t.Fatal("the MCP adapter no longer declares machine ingress, so its refusals would report a submitter it has none of")
	}
	if got := a.EntrypointType(); got != "mcp" {
		t.Errorf("EntrypointType = %q, want %q", got, "mcp")
	}
	if !slices.Contains(taxonomy.GatewayTypes, a.EntrypointType()) {
		t.Errorf("EntrypointType %q is outside the catalog's set, so the refusal would name no entrypoint at all", a.EntrypointType())
	}
}

func TestAnMCPRefusalWithNoGatewayIsInert(t *testing.T) {
	a := refusalAdapter(t)
	if _, err := a.handleToolCall(context.Background(), "no_such_tool", toolCall("no_such_tool", "hello")); err != nil {
		t.Fatalf("handleToolCall: %v", err)
	}
}
