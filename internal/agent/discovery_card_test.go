// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestPublishAgentCard_StampsInstanceID(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err = brk.Subscribe(q, a2a.DiscoveryAgentCardsTopic("test-ns")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a := New(AgentConfig{Name: "test-agent", Namespace: "test-ns"})
	a.logger = discardLogger()
	ms := newMockServices(nil, nil)
	ms.brk = brk
	a.svc = ms

	if a.instanceID == "" {
		t.Fatal("agent instanceID should be set by New")
	}

	if err = a.publishAgentCard(ctx); err != nil {
		t.Fatalf("publishAgentCard: %v", err)
	}
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive heartbeat card: %v", err)
	}
	if got := msg.UserProperties["instance_id"]; got != a.instanceID {
		t.Errorf("heartbeat instance_id = %q, want %q", got, a.instanceID)
	}
	if msg.UserProperties["removed"] == "true" {
		t.Error("heartbeat card must not be marked removed")
	}

	a.publishRemovalCard(ctx)
	rmMsg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive removal card: %v", err)
	}
	if rmMsg.UserProperties["removed"] != "true" {
		t.Error("removal card must be marked removed")
	}
	if got := rmMsg.UserProperties["instance_id"]; got != a.instanceID {
		t.Errorf("removal instance_id = %q, want %q", got, a.instanceID)
	}
}

func TestBuildAgentCard_ToolRegistrationsExtension(t *testing.T) {
	a := New(AgentConfig{Name: "reg-agent", Namespace: "test-ns"})

	bare := a.BuildAgentCard()
	for _, ext := range bare.Capabilities.Extensions {
		if ext.URI == ExtensionURIToolRegistrations {
			t.Fatal("card must not carry the tool-registrations extension when nothing was recorded")
		}
	}

	a.SetToolRegistrations([]ToolEntryRegistration{
		{ToolType: "openapi", ConnectorID: "conn-1", Name: "openapi", Status: "failed", Error: "load spec from https://x: HTTP 500"},
		{ToolType: "mcp", ConnectorID: "conn-2", Name: "figma", Status: "ready", ToolCount: 4, ToolNames: []string{"create_frame", "export_node", "get_file", "list_pages"}},
	})
	card := a.BuildAgentCard()

	var params map[string]any
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIToolRegistrations {
			params = ext.Params
			break
		}
	}
	if params == nil {
		t.Fatal("tool-registrations extension missing from card")
	}
	entries, _ := params["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	failed := entries[0].(map[string]any)
	if failed["tool_type"] != "openapi" || failed["status"] != "failed" || failed["connector_id"] != "conn-1" {
		t.Errorf("failed entry = %v, want openapi/failed/conn-1", failed)
	}
	if failed["error"] == "" {
		t.Error("failed entry must carry the error summary")
	}
	ready := entries[1].(map[string]any)
	if ready["status"] != "ready" || ready["tool_count"] != 4 {
		t.Errorf("ready entry = %v, want ready with tool_count 4", ready)
	}
	names, _ := ready["tool_names"].([]any)
	if len(names) != 4 || names[0] != "create_frame" {
		t.Errorf("ready entry tool_names = %v, want the 4 registered names", ready["tool_names"])
	}
	if _, present := failed["tool_names"]; present {
		t.Error("failed entry must omit tool_names (no tools registered)")
	}
}
