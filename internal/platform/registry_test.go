// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

func TestDisplayName(t *testing.T) {
	plain := a2a.AgentCard{Name: "Builder"}
	if got := DisplayName(&plain); got != "Builder" {
		t.Errorf("plain card: got %q, want Builder", got)
	}

	platformCard := a2a.AgentCard{
		Name: "workflow_019e394b_528f_737e_a032_45f04b8c1a2c",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    ExtensionURIDisplayName,
				Params: map[string]any{"display_name": "shipment-request-workflow"},
			}},
		},
	}
	if got := DisplayName(&platformCard); got != "shipment-request-workflow" {
		t.Errorf("platform card: got %q, want shipment-request-workflow", got)
	}

	if got := DisplayName(nil); got != "" {
		t.Errorf("nil card: got %q, want empty string", got)
	}

	empty := a2a.AgentCard{
		Name: "fallback",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    ExtensionURIDisplayName,
				Params: map[string]any{"display_name": ""},
			}},
		},
	}
	if got := DisplayName(&empty); got != "fallback" {
		t.Errorf("empty display_name: got %q, want fallback", got)
	}
}

func TestAgentRegistry_AddOrUpdate(t *testing.T) {
	r := NewAgentRegistry()

	card := a2a.AgentCard{Name: "agent1", Description: "test"}

	if !r.AddOrUpdate(card) {
		t.Error("expected new=true on first add")
	}
	if r.Count() != 1 {
		t.Errorf("Count = %d, want 1", r.Count())
	}

	if r.AddOrUpdate(card) {
		t.Error("expected new=false on second add")
	}
	if r.Count() != 1 {
		t.Errorf("Count = %d, want 1", r.Count())
	}
}

func TestAgentRegistry_GetAndRemove(t *testing.T) {
	r := NewAgentRegistry()
	r.AddOrUpdate(a2a.AgentCard{Name: "a1"})

	if c := r.Get("a1"); c == nil {
		t.Error("expected to find a1")
	}
	if c := r.Get("nonexistent"); c != nil {
		t.Error("expected nil for nonexistent")
	}

	if !r.Remove("a1") {
		t.Error("expected Remove to return true")
	}
	if r.Remove("a1") {
		t.Error("expected Remove to return false for already-removed")
	}
}

func TestAgentRegistry_RemoveInstance_RollingRestart(t *testing.T) {
	reg := NewAgentRegistry()
	card := a2a.AgentCard{Name: "Orchestrator"}

	reg.AddOrUpdateInstance(card, "A")
	reg.AddOrUpdateInstance(card, "B")

	if reg.RemoveInstance("Orchestrator", "A") {
		t.Fatal("stale removal from superseded instance A must not remove the agent")
	}
	if reg.Get("Orchestrator") == nil {
		t.Fatal("agent should still be present after a stale removal")
	}

	if !reg.RemoveInstance("Orchestrator", "B") {
		t.Fatal("removal from the current instance B should remove the agent")
	}
	if reg.Get("Orchestrator") != nil {
		t.Fatal("agent should be gone after the current instance's removal")
	}
}

func TestAgentRegistry_RemoveInstance_LegacyEmptyIDsHonored(t *testing.T) {
	reg := NewAgentRegistry()
	reg.AddOrUpdate(a2a.AgentCard{Name: "no-id-entry"})
	if !reg.RemoveInstance("no-id-entry", "B") {
		t.Fatal("removal should be honored when the registered entry has no id (legacy)")
	}
	reg.AddOrUpdate(a2a.AgentCard{Name: "no-id-removal"})
	if !reg.RemoveInstance("no-id-removal", "") {
		t.Fatal("removal with empty id should be honored for a legacy publisher")
	}
	reg.AddOrUpdateInstance(a2a.AgentCard{Name: "id-entry"}, "A")
	if reg.RemoveInstance("id-entry", "") || reg.Get("id-entry") == nil {
		t.Fatal("a removal without an id must not evict an instance that advertises one")
	}
}

func TestGatewayRegistry_RemoveInstance_RollingRestart(t *testing.T) {
	reg := NewGatewayRegistry()
	card := a2a.AgentCard{Name: "gw-1"}
	reg.AddOrUpdateInstance(card, "A")
	reg.AddOrUpdateInstance(card, "B")

	if reg.RemoveInstance("gw-1", "A") {
		t.Fatal("stale removal from superseded gateway instance A must not remove it")
	}
	if reg.Get("gw-1") == nil {
		t.Fatal("gateway should still be present after a stale removal")
	}
	if !reg.RemoveInstance("gw-1", "B") {
		t.Fatal("removal from the current gateway instance B should remove it")
	}
	if reg.Get("gw-1") != nil {
		t.Fatal("gateway should be gone after the current instance's removal")
	}
}

func TestAgentRegistry_ExpireOlderThan(t *testing.T) {
	r := NewAgentRegistry()
	r.AddOrUpdate(a2a.AgentCard{Name: "old"})
	time.Sleep(10 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(10 * time.Millisecond)
	r.AddOrUpdate(a2a.AgentCard{Name: "fresh"})

	removed := r.ExpireOlderThan(cutoff)
	if len(removed) != 1 || removed[0] != "old" {
		t.Errorf("removed = %v, want [old]", removed)
	}
	if r.Count() != 1 {
		t.Errorf("Count = %d, want 1", r.Count())
	}
	if c := r.Get("fresh"); c == nil {
		t.Error("fresh should still exist")
	}
}

func TestAgentRegistry_Names(t *testing.T) {
	r := NewAgentRegistry()
	r.AddOrUpdate(a2a.AgentCard{Name: "beta"})
	r.AddOrUpdate(a2a.AgentCard{Name: "alpha"})

	names := r.Names()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("Names = %v, want [alpha beta]", names)
	}
}

func TestGatewayRegistry_Basic(t *testing.T) {
	r := NewGatewayRegistry()

	card := a2a.AgentCard{Name: "gw1"}
	if !r.AddOrUpdate(card) {
		t.Error("expected new=true")
	}
	if r.Count() != 1 {
		t.Errorf("Count = %d, want 1", r.Count())
	}

	ids := r.IDs()
	if len(ids) != 1 || ids[0] != "gw1" {
		t.Errorf("IDs = %v, want [gw1]", ids)
	}
}

func TestGatewayRegistry_ExtensionExtraction(t *testing.T) {
	r := NewGatewayRegistry()

	card := a2a.AgentCard{
		Name: "gw1",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI: ExtensionURIGatewayRole,
					Params: map[string]any{
						"gateway_type": "http_sse",
						"namespace":    "my-ns",
					},
				},
				{
					URI: ExtensionURIDeployment,
					Params: map[string]any{
						"deployment_id": "deploy-123",
					},
				},
			},
		},
	}
	r.AddOrUpdate(card)

	if gt := r.GetGatewayType("gw1"); gt != "http_sse" {
		t.Errorf("GetGatewayType = %q, want %q", gt, "http_sse")
	}
	if ns := r.GetGatewayNamespace("gw1"); ns != "my-ns" {
		t.Errorf("GetGatewayNamespace = %q, want %q", ns, "my-ns")
	}
	if did := r.GetDeploymentID("gw1"); did != "deploy-123" {
		t.Errorf("GetDeploymentID = %q, want %q", did, "deploy-123")
	}
}

func TestIsGatewayCard(t *testing.T) {
	agentCard := &a2a.AgentCard{Name: "agent1"}
	if IsGatewayCard(agentCard) {
		t.Error("agent card should not be detected as gateway")
	}

	gwCard := &a2a.AgentCard{
		Name: "gw1",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: ExtensionURIGatewayRole},
			},
		},
	}
	if !IsGatewayCard(gwCard) {
		t.Error("gateway card should be detected as gateway")
	}
}

func TestAgentToolRegistrations_WireParity(t *testing.T) {
	ag := agent.New(agent.AgentConfig{Name: "agent_x", Namespace: "test-ns"})
	ag.SetToolRegistrations([]agent.ToolEntryRegistration{
		{ToolType: "openapi", ConnectorID: "conn-1", Name: "openapi", Status: "failed", Error: "load spec: HTTP 500"},
		{ToolType: "mcp", ConnectorID: "conn-2", Name: "figma", Status: "ready", ToolCount: 3, ToolNames: []string{"create_frame", "export_node", "get_file"}},
		{ToolType: "openapi", ConnectorID: "conn-3", Name: "pets", Status: "ready", Error: "3 tools discovered, 2 registered", ToolCount: 2, ToolNames: []string{"list_pets", "whoami"}},
	})

	data, err := json.Marshal(ag.BuildAgentCard())
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(data, &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}

	registry := NewAgentRegistry()
	registry.AddOrUpdate(card)

	regs := AgentToolRegistrations(registry.Get("agent_x"))
	if len(regs) != 3 {
		t.Fatalf("registrations = %d, want 3", len(regs))
	}
	want0 := ToolEntryRegistration{ToolType: "openapi", ConnectorID: "conn-1", Name: "openapi", Status: "failed", Error: "load spec: HTTP 500"}
	if !reflect.DeepEqual(regs[0], want0) {
		t.Errorf("regs[0] = %+v, want %+v", regs[0], want0)
	}
	want1 := ToolEntryRegistration{ToolType: "mcp", ConnectorID: "conn-2", Name: "figma", Status: "ready", ToolCount: 3, ToolNames: []string{"create_frame", "export_node", "get_file"}}
	if !reflect.DeepEqual(regs[1], want1) {
		t.Errorf("regs[1] = %+v, want %+v", regs[1], want1)
	}
	want2 := ToolEntryRegistration{ToolType: "openapi", ConnectorID: "conn-3", Name: "pets", Status: "ready", Error: "3 tools discovered, 2 registered", ToolCount: 2, ToolNames: []string{"list_pets", "whoami"}}
	if !reflect.DeepEqual(regs[2], want2) {
		t.Errorf("regs[2] = %+v, want %+v (ready entries may carry a partial-loss error)", regs[2], want2)
	}
}

func TestStringSliceParam(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"all strings", map[string]any{"k": []any{"a", "b"}}, []string{"a", "b"}},
		{"key absent", map[string]any{}, nil},
		{"wrong container type", map[string]any{"k": "not-a-slice"}, nil},
		{"mixed items drop non-strings", map[string]any{"k": []any{"a", 1, true, "b"}}, []string{"a", "b"}},
		{"all non-strings collapse to nil", map[string]any{"k": []any{1, 2.0, false}}, nil},
		{"empty slice", map[string]any{"k": []any{}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stringSliceParam(tc.in, "k"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("stringSliceParam = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestAgentToolRegistrations_AbsentExtension(t *testing.T) {
	if got := AgentToolRegistrations(&a2a.AgentCard{Name: "py-agent"}); got != nil {
		t.Errorf("expected nil for card without extension, got %v", got)
	}
	if got := AgentToolRegistrations(nil); got != nil {
		t.Errorf("expected nil for nil card, got %v", got)
	}
}

func TestRuntimeProblemsFromCard(t *testing.T) {
	since := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	card := a2a.AgentCard{
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			a2a.HealthProblemsExtension([]health.Problem{
				{ID: "queue:x", Severity: health.Degraded, Detail: "provision cooldown", Since: since},
			}),
		}},
	}

	got := RuntimeProblemsFromCard(&card)
	if len(got) != 1 {
		t.Fatalf("RuntimeProblemsFromCard() = %v, want one problem", got)
	}
	if got[0].ID != "queue:x" || got[0].Severity != "degraded" || got[0].Detail != "provision cooldown" || !got[0].Since.Equal(since) {
		t.Errorf("RuntimeProblemsFromCard() = %+v, want {queue:x degraded \"provision cooldown\" %v}", got[0], since)
	}
}

func TestRuntimeProblemsFromCard_AbsentExtension(t *testing.T) {
	if got := RuntimeProblemsFromCard(&a2a.AgentCard{Name: "no-extension"}); got != nil {
		t.Errorf("expected nil for card without extension, got %v", got)
	}
	if got := RuntimeProblemsFromCard(nil); got != nil {
		t.Errorf("expected nil for nil card, got %v", got)
	}
}

func TestRuntimeStatus_MarshalJSON(t *testing.T) {
	t.Run("running has no problems key", func(t *testing.T) {
		b, err := json.Marshal(RunningStatus(nil))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(b), `{"status":"running"}`; got != want {
			t.Errorf("Marshal(RunningStatus(nil)) = %s, want %s", got, want)
		}
	})

	t.Run("degraded includes problems with ISO8601 since", func(t *testing.T) {
		since := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
		b, err := json.Marshal(RunningStatus([]Problem{{ID: "queue:x", Severity: "degraded", Detail: "boom", Since: since}}))
		if err != nil {
			t.Fatal(err)
		}
		want := `{"status":"degraded","problems":[{"id":"queue:x","severity":"degraded","detail":"boom","since":"2026-08-30T12:00:00.000000+00:00"}]}`
		if got := string(b); got != want {
			t.Errorf("Marshal() = %s, want %s", got, want)
		}
	})
}

func TestRuntimeStatus_UnmarshalJSON(t *testing.T) {
	t.Run("degraded round-trips through Marshal then Unmarshal", func(t *testing.T) {
		since := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
		want := RunningStatus([]Problem{{ID: "queue:x", Severity: "degraded", Detail: "boom", Since: since}})
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got RuntimeStatus
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got.Status() != want.Status() {
			t.Errorf("Status() = %q, want %q", got.Status(), want.Status())
		}
		if len(got.Problems()) != 1 || got.Problems()[0].ID != "queue:x" {
			t.Errorf("Problems() = %+v, want one problem with id queue:x", got.Problems())
		}
	})

	t.Run("running with no problems key round-trips to empty Problems", func(t *testing.T) {
		var got RuntimeStatus
		if err := json.Unmarshal([]byte(`{"status":"running"}`), &got); err != nil {
			t.Fatal(err)
		}
		if got.Status() != RuntimeStatusRunning {
			t.Errorf("Status() = %q, want %q", got.Status(), RuntimeStatusRunning)
		}
		if len(got.Problems()) != 0 {
			t.Errorf("Problems() = %+v, want none", got.Problems())
		}
	})

	t.Run("status is forced to degraded when a foreign payload disagrees with its own problems", func(t *testing.T) {
		var got RuntimeStatus
		if err := json.Unmarshal([]byte(`{"status":"running","problems":[{"id":"queue:x","severity":"degraded","detail":"boom","since":""}]}`), &got); err != nil {
			t.Fatal(err)
		}
		if got.Status() != RuntimeStatusDegraded {
			t.Errorf("Status() = %q, want %q — every constructor's invariant (non-empty Problems() implies degraded) must hold on decode too", got.Status(), RuntimeStatusDegraded)
		}
		if len(got.Problems()) != 1 {
			t.Errorf("Problems() = %+v, want the one problem preserved", got.Problems())
		}
	})
}

func TestProblem_MarshalJSON_ZeroSince(t *testing.T) {
	b, err := json.Marshal(Problem{ID: "x", Severity: "degraded", Detail: "d"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"x","severity":"degraded","detail":"d","since":""}`
	if got := string(b); got != want {
		t.Errorf("Marshal(zero Since) = %s, want %s", got, want)
	}
}

func TestProblem_UnmarshalJSON_ZeroSince(t *testing.T) {
	var p Problem
	if err := json.Unmarshal([]byte(`{"id":"x","severity":"degraded","detail":"d","since":""}`), &p); err != nil {
		t.Fatalf("Unmarshal(zero Since) = %v, want no error", err)
	}
	if !p.Since.IsZero() {
		t.Errorf("Since = %v, want zero time.Time", p.Since)
	}
	if p.ID != "x" || p.Severity != "degraded" || p.Detail != "d" {
		t.Errorf("Unmarshal = %+v, want ID/Severity/Detail preserved", p)
	}
}

func TestProblem_MarshalUnmarshalJSON_RoundTrip(t *testing.T) {
	since := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	want := Problem{ID: "queue:x", Severity: "degraded", Detail: "boom", Since: since}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Problem
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestRuntimeStatus_UnmarshalJSON_ProblemWithZeroSince(t *testing.T) {
	wire := `{"status":"degraded","problems":[{"id":"queue:x","severity":"degraded","detail":"boom","since":""}]}`
	var rs RuntimeStatus
	if err := json.Unmarshal([]byte(wire), &rs); err != nil {
		t.Fatalf("Unmarshal = %v, want no error", err)
	}
	if len(rs.Problems()) != 1 || !rs.Problems()[0].Since.IsZero() {
		t.Errorf("Problems() = %+v, want one problem with a zero Since", rs.Problems())
	}
}

func healthCard(name string, problems ...health.Problem) a2a.AgentCard {
	return a2a.AgentCard{
		Name:         name,
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{a2a.HealthProblemsExtension(problems)}},
	}
}

func TestAgentRegistry_RuntimeProblems_UnionAcrossInstances(t *testing.T) {
	r := NewAgentRegistry()
	r.AddOrUpdateInstance(healthCard("agent_a",
		health.Problem{ID: "queue:x", Severity: health.Degraded, Detail: "cooldown"},
		health.Problem{ID: "model:y", Severity: health.Degraded, Detail: "slow"},
	), "awe-1")
	r.AddOrUpdateInstance(healthCard("agent_a",
		health.Problem{ID: "queue:x", Severity: health.Fatal, Detail: "gone"},
	), "awe-2")
	r.AddOrUpdateInstance(healthCard("agent_a"), "awe-3")

	got := r.RuntimeProblems("agent_a")
	if len(got) != 2 {
		t.Fatalf("RuntimeProblems() = %+v, want queue:x and model:y once each", got)
	}
	bySeverity := map[string]string{}
	for _, p := range got {
		bySeverity[p.ID] = p.Severity
	}
	if bySeverity["queue:x"] != health.Fatal.String() || bySeverity["model:y"] != health.Degraded.String() {
		t.Errorf("RuntimeProblems() severities = %v, want queue:x fatal and model:y degraded", bySeverity)
	}
	if got := r.RuntimeProblems("agent_missing"); got != nil {
		t.Errorf("RuntimeProblems(unknown) = %v, want nil", got)
	}
}
