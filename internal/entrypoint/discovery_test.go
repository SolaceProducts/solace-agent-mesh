// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

func TestRemoveInstance_RollingRestartKeepsLiveAgent(t *testing.T) {
	reg := NewAgentRegistry()
	card := a2a.AgentCard{Name: "agent_x"}

	reg.AddOrUpdateInstance(card, "A")
	reg.AddOrUpdateInstance(card, "B")

	if reg.RemoveInstance("agent_x", "A") {
		t.Fatal("removal from superseded instance A must not remove the agent")
	}
	if reg.Get("agent_x") == nil {
		t.Fatal("agent should still be present after a stale removal")
	}

	if !reg.RemoveInstance("agent_x", "B") {
		t.Fatal("removal from the current instance B should remove the agent")
	}
	if reg.Get("agent_x") != nil {
		t.Fatal("agent should be gone after the current instance's removal")
	}
}

func TestRemoveInstance_LegacyEmptyIDsHonored(t *testing.T) {
	t.Run("removal lacks id", func(t *testing.T) {
		reg := NewAgentRegistry()
		reg.AddOrUpdate(a2a.AgentCard{Name: "a"})
		if !reg.RemoveInstance("a", "") {
			t.Fatal("removal with empty id should be honored for a legacy publisher")
		}
	})
	t.Run("removal lacks id, entry has one", func(t *testing.T) {
		reg := NewAgentRegistry()
		reg.AddOrUpdateInstance(a2a.AgentCard{Name: "a"}, "A")
		if reg.RemoveInstance("a", "") || reg.Get("a") == nil {
			t.Fatal("a removal without an id must not evict an instance that advertises one")
		}
	})
	t.Run("entry lacks id", func(t *testing.T) {
		reg := NewAgentRegistry()
		reg.AddOrUpdate(a2a.AgentCard{Name: "a"})
		if !reg.RemoveInstance("a", "B") {
			t.Fatal("removal should be honored when the registered entry has no id (legacy)")
		}
	})
	t.Run("unknown agent", func(t *testing.T) {
		reg := NewAgentRegistry()
		if reg.RemoveInstance("missing", "A") {
			t.Fatal("removing an unknown agent should report false")
		}
	})
}

func TestMatchMentionedAgent(t *testing.T) {
	cards := []a2a.AgentCard{
		{Name: "agent_a"},
		{Name: "agent_aaa"},
		{Name: "Manager"},
		cardWithDisplay("workflow_019e394b_528f_737e_a032_45f04b8c1a2c", "shipment-request-workflow"),
	}

	cases := []struct {
		name string
		text string
		want string
	}{
		{"boundary at end of text", "hi @agent_a", "agent_a"},
		{"boundary on punctuation", "hi @agent_a please help", "agent_a"},
		{"longer name still routable", "hi @agent_aaa please help", "agent_aaa"},
		{"case-insensitive card name", "hey @MANAGER", "Manager"},
		{"display-name routes via extension", "@shipment-request-workflow now", "workflow_019e394b_528f_737e_a032_45f04b8c1a2c"},
		{"unrelated text returns empty", "no mention here", ""},
		{"bare @ no name returns empty", "hi @ what's up", ""},
		{"agent token embedded in larger identifier", "ping @agent_aaa_workspace", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchMentionedAgent(cards, tc.text); got != tc.want {
				t.Errorf("text %q: got %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func cardWithDisplay(brokerName, display string) a2a.AgentCard {
	return a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": display},
			}},
		},
	}
}

func TestAgentRegistry_FindByConfiguredName(t *testing.T) {
	reg := NewAgentRegistry()

	if got := reg.FindByConfiguredName(""); got != nil {
		t.Errorf("empty name should return nil, got %+v", got)
	}

	if got := reg.FindByConfiguredName("missing"); got != nil {
		t.Errorf("unknown name should return nil, got %+v", got)
	}

	reg.AddOrUpdate(a2a.AgentCard{Name: "Manager"})
	if got := reg.FindByConfiguredName("Manager"); got == nil || got.Name != "Manager" {
		t.Errorf("match by card name: got %+v, want Manager", got)
	}

	reg.AddOrUpdate(cardWithDisplay(
		"workflow_019e394b_528f_737e_a032_45f04b8c1a2c", "shipment-request-workflow",
	))
	got := reg.FindByConfiguredName("shipment-request-workflow")
	if got == nil {
		t.Fatal("display-name lookup returned nil")
	}
	if got.Name != "workflow_019e394b_528f_737e_a032_45f04b8c1a2c" {
		t.Errorf("display-name lookup: got Name=%q, want UUID-derived instance", got.Name)
	}

	reg.AddOrUpdate(cardWithDisplay("Manager", "shipment-request-workflow"))
	if got := reg.FindByConfiguredName("Manager"); got == nil || got.Name != "Manager" {
		t.Errorf("card-name precedence: got %+v, want Manager", got)
	}

	tie := NewAgentRegistry()
	tie.AddOrUpdate(cardWithDisplay("workflow_zzz", "duplicate"))
	tie.AddOrUpdate(cardWithDisplay("workflow_aaa", "duplicate"))
	tie.AddOrUpdate(cardWithDisplay("workflow_mmm", "duplicate"))
	if got := tie.FindByConfiguredName("duplicate"); got == nil || got.Name != "workflow_aaa" {
		t.Errorf("tie-break: got %+v, want workflow_aaa (lowest broker name)", got)
	}
}

func ephemeralCardWithDisplay(brokerName, display string) a2a.AgentCard {
	card := cardWithDisplay(brokerName, display)
	card.Capabilities.Extensions = append(card.Capabilities.Extensions,
		a2a.AgentExtension{URI: a2a.EphemeralAgentExtensionURI})
	return card
}

func TestAgentRegistry_FindByConfiguredName_SkipsEphemeralWrapper(t *testing.T) {
	const (
		workflowName = "workflow_01a1d59c_c06d_717c_85fc_d067b3322cdd"
		wrapperName  = "agent_01a1d59c_c522_73d4_86b1_60f1c71fe0f8"
		display      = "MessageResolutionWorkflow"
	)
	reg := NewAgentRegistry()
	reg.AddOrUpdate(cardWithDisplay(workflowName, display))
	reg.AddOrUpdate(ephemeralCardWithDisplay(wrapperName, display))

	if got := reg.FindByConfiguredName(display); got == nil || got.Name != workflowName {
		t.Errorf("display-name lookup: got %+v, want %s (the wrapper sorts first but must never match)", got, workflowName)
	}
	if got := reg.FindByConfiguredName(wrapperName); got == nil || got.Name != wrapperName {
		t.Errorf("exact lookup of the wrapper's broker name: got %+v, want %s", got, wrapperName)
	}

	onlyWrapper := NewAgentRegistry()
	onlyWrapper.AddOrUpdate(ephemeralCardWithDisplay(wrapperName, display))
	if got := onlyWrapper.FindByConfiguredName(display); got != nil {
		t.Errorf("display-name lookup with only the wrapper registered: got %+v, want nil", got)
	}
}

func TestAgentRegistry_ResolveByDefaultName_SkipsEphemeralWrapper(t *testing.T) {
	reg := NewAgentRegistry()
	reg.AddOrUpdate(ephemeralCardWithDisplay("agent_01a1d59c_c522_73d4_86b1_60f1c71fe0f8", "Support"))
	reg.AddOrUpdate(cardWithDisplay("agent_01a1d59c_ffff_73d4_86b1_60f1c71fe0f8", "Support"))

	if got := reg.ResolveByDefaultName(""); got.Name != "agent_01a1d59c_ffff_73d4_86b1_60f1c71fe0f8" {
		t.Errorf("implicit default: got %+v, want the first non-ephemeral agent", got)
	}

	onlyWrapper := NewAgentRegistry()
	onlyWrapper.AddOrUpdate(ephemeralCardWithDisplay("agent_01a1d59c_c522_73d4_86b1_60f1c71fe0f8", "Support"))
	if got := onlyWrapper.ResolveByDefaultName(""); got != (AgentResolution{}) {
		t.Errorf("implicit default with only a wrapper: got %+v, want no agent available", got)
	}
	if got := onlyWrapper.ResolveByDefaultName("Support"); got != (AgentResolution{Misconfigured: true}) {
		t.Errorf("configured default carried only by a wrapper: got %+v, want Misconfigured", got)
	}
}

func TestAgentRegistry_SupportsExtension(t *testing.T) {
	reg := NewAgentRegistry()
	const uri = "https://solace.com/sam/ext/session-control/v1"

	if reg.SupportsExtension("missing", uri) {
		t.Error("unknown agent should not support any extension")
	}

	reg.AddOrUpdate(a2a.AgentCard{Name: "plain"})
	if reg.SupportsExtension("plain", uri) {
		t.Error("agent with no extensions should not support uri")
	}

	reg.AddOrUpdate(a2a.AgentCard{
		Name: "session-aware",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: "https://example.com/other/v1"},
				{URI: uri},
			},
		},
	})
	if !reg.SupportsExtension("session-aware", uri) {
		t.Error("agent advertising uri should be reported as supported")
	}

	if reg.SupportsExtension("session-aware", uri+"-x") {
		t.Error("different URI should not match")
	}
}

func TestAgentRegistry_DisplayNameResolver(t *testing.T) {
	reg := NewAgentRegistry()
	const slug = "agent_019ea471_5051_73c7_893a_7679460fd074"
	reg.AddOrUpdate(a2a.AgentCard{
		Name: slug,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": "Support"},
			}},
		},
	})

	resolve := reg.DisplayNameResolver()
	if got := resolve(slug); got != "Support" {
		t.Errorf("resolver(%q) = %q, want Support", slug, got)
	}
	if got := resolve("agent_unknown"); got != "" {
		t.Errorf("resolver(unknown) = %q, want empty", got)
	}
}

func TestAgentRegistry_AddOrUpdate(t *testing.T) {
	reg := NewAgentRegistry()

	card := a2a.AgentCard{Name: "agent-1", Description: "Test agent"}
	isNew := reg.AddOrUpdate(card)
	if !isNew {
		t.Error("first AddOrUpdate should return true (new)")
	}
	if reg.Count() != 1 {
		t.Errorf("Count() = %d, want 1", reg.Count())
	}

	isNew = reg.AddOrUpdate(card)
	if isNew {
		t.Error("second AddOrUpdate should return false (update)")
	}
	if reg.Count() != 1 {
		t.Errorf("Count() = %d, want 1 after update", reg.Count())
	}

	card2 := a2a.AgentCard{Name: "agent-2"}
	isNew = reg.AddOrUpdate(card2)
	if !isNew {
		t.Error("AddOrUpdate of new agent should return true")
	}
	if reg.Count() != 2 {
		t.Errorf("Count() = %d, want 2", reg.Count())
	}
}

func TestAgentRegistry_Get(t *testing.T) {
	reg := NewAgentRegistry()
	reg.AddOrUpdate(a2a.AgentCard{Name: "agent-1", Description: "desc"})

	got := reg.Get("agent-1")
	if got == nil {
		t.Fatal("Get should return non-nil for existing agent")
	}
	if got.Description != "desc" {
		t.Errorf("Description = %q, want %q", got.Description, "desc")
	}

	if reg.Get("no-such-agent") != nil {
		t.Error("Get should return nil for non-existent agent")
	}
}

func TestAgentRegistry_Remove(t *testing.T) {
	reg := NewAgentRegistry()
	reg.AddOrUpdate(a2a.AgentCard{Name: "agent-1"})
	reg.AddOrUpdate(a2a.AgentCard{Name: "agent-2"})

	v := reg.Version()
	reg.Remove("agent-1")
	if reg.Count() != 1 {
		t.Errorf("Count() = %d, want 1 after remove", reg.Count())
	}
	if reg.Version() <= v {
		t.Error("version should increment on remove")
	}

	v = reg.Version()
	reg.Remove("no-such-agent")
	if reg.Version() != v {
		t.Error("version should not increment on remove of non-existent")
	}
}

func TestAgentRegistry_List(t *testing.T) {
	reg := NewAgentRegistry()
	reg.AddOrUpdate(a2a.AgentCard{Name: "charlie"})
	reg.AddOrUpdate(a2a.AgentCard{Name: "alpha"})
	reg.AddOrUpdate(a2a.AgentCard{Name: "bravo"})

	cards := reg.List()
	if len(cards) != 3 {
		t.Fatalf("List length = %d, want 3", len(cards))
	}
	if cards[0].Name != "alpha" || cards[1].Name != "bravo" || cards[2].Name != "charlie" {
		t.Errorf("List not sorted: %v", []string{cards[0].Name, cards[1].Name, cards[2].Name})
	}
}

func TestAgentRegistry_Names(t *testing.T) {
	reg := NewAgentRegistry()
	reg.AddOrUpdate(a2a.AgentCard{Name: "zeta"})
	reg.AddOrUpdate(a2a.AgentCard{Name: "alpha"})

	names := reg.Names()
	if len(names) != 2 {
		t.Fatalf("Names length = %d, want 2", len(names))
	}
	if names[0] != "alpha" || names[1] != "zeta" {
		t.Errorf("Names not sorted: %v", names)
	}
}

func TestAgentRegistry_ExpireOlderThan(t *testing.T) {
	reg := NewAgentRegistry()

	reg.AddOrUpdate(a2a.AgentCard{Name: "old-agent"})
	time.Sleep(10 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(10 * time.Millisecond)
	reg.AddOrUpdate(a2a.AgentCard{Name: "new-agent"})

	removed := reg.ExpireOlderThan(cutoff)
	if len(removed) != 1 {
		t.Fatalf("ExpireOlderThan removed %d agents, want 1", len(removed))
	}
	if removed[0] != "old-agent" {
		t.Errorf("removed = %v, want [old-agent]", removed)
	}
	if reg.Count() != 1 {
		t.Errorf("Count() = %d, want 1 after expiry", reg.Count())
	}
	if reg.Get("new-agent") == nil {
		t.Error("new-agent should still be in registry")
	}
}

func TestAgentRegistry_Version(t *testing.T) {
	reg := NewAgentRegistry()
	v0 := reg.Version()

	reg.AddOrUpdate(a2a.AgentCard{Name: "a"})
	v1 := reg.Version()
	if v1 <= v0 {
		t.Error("version should increase after AddOrUpdate")
	}

	reg.AddOrUpdate(a2a.AgentCard{Name: "a"})
	if reg.Version() != v1 {
		t.Error("version should not change on an identical heartbeat")
	}

	reg.AddOrUpdate(a2a.AgentCard{Name: "a", Description: "changed"})
	v2 := reg.Version()
	if v2 <= v1 {
		t.Error("version should increase after update")
	}

	reg.Remove("a")
	v3 := reg.Version()
	if v3 <= v2 {
		t.Error("version should increase after Remove")
	}
}

func TestAgentRegistry_InternalNames(t *testing.T) {
	reg := NewAgentRegistry()
	const internalURI = "https://solace.com/a2a/extensions/sam/internal"

	reg.AddOrUpdate(a2a.AgentCard{Name: "Public"})

	reg.AddOrUpdate(a2a.AgentCard{
		Name: "Hidden",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    internalURI,
				Params: map[string]any{"internal": true},
			}},
		},
	})

	reg.AddOrUpdate(a2a.AgentCard{
		Name: "Visible",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    internalURI,
				Params: map[string]any{"internal": true, "show_sessions": true},
			}},
		},
	})

	got := reg.InternalNames()
	if len(got) != 1 || got[0] != "Hidden" {
		t.Errorf("InternalNames = %v, want [Hidden]", got)
	}
}

func publishedGatewayCard(t *testing.T, state cardState) a2a.AgentCard {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	topic := a2a.DiscoveryGatewayCardsTopic("test-ns")
	if subErr := brk.Subscribe(sub, topic); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	id := gatewayCardID{namespace: "test-ns", gatewayID: "gw-1", instanceID: "inst-1"}
	if pubErr := publishGatewayCard(context.Background(), brk, id, state); pubErr != nil {
		t.Fatalf("publishGatewayCard: %v", pubErr)
	}

	msg, err := brk.Receive(context.Background(), sub)
	if err != nil || msg == nil {
		t.Fatalf("Receive: msg=%v err=%v", msg, err)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(msg.Payload, &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	return card
}

func TestPublishGatewayCard_HealthExtension(t *testing.T) {
	problems := []health.Problem{{ID: "queue:x", Severity: health.Degraded, Detail: "boom"}}
	card := publishedGatewayCard(t, cardState{healthProblems: func() []health.Problem { return problems }})

	got := a2a.HealthProblemsFromCard(card)
	if len(got) != 1 || got[0].ID != "queue:x" || got[0].Severity != health.Degraded {
		t.Fatalf("HealthProblemsFromCard() = %v, want one queue:x Degraded problem", got)
	}
}

func TestPublishGatewayCard_NoHealthExtensionWhenEmpty(t *testing.T) {
	card := publishedGatewayCard(t, cardState{healthProblems: noHealthProblems})
	if got := a2a.HealthProblemsFromCard(card); got != nil {
		t.Errorf("HealthProblemsFromCard() = %v, want nil", got)
	}
}

func noHealthProblems() []health.Problem { return nil }

type healthReportingAdapter struct {
	recordingAdapter
	problems []health.Problem
}

func (a *healthReportingAdapter) HealthProblems() []health.Problem { return a.problems }

func TestCollectHealthProblems_AggregatesCoreTaskLoggerAndAdapter(t *testing.T) {
	adapter := &healthReportingAdapter{problems: []health.Problem{{ID: "eventmesh:handlerA"}}}
	core := NewGatewayCore(adapter, map[string]any{"namespace": "test-ns", "gateway_id": "gw-1"})
	core.health.Set("queue:gw-1/a2a/v1/discovery", health.Degraded, "own queue trouble")
	tl := &TaskLoggerService{}
	tl.health.Set("queue:gw-1/tasklog", health.Fatal, "task log queue gone")
	core.taskLogger.Store(tl)

	got := core.collectHealthProblems()

	ids := make(map[string]bool, len(got))
	for _, p := range got {
		ids[p.ID] = true
	}
	for _, want := range []string{"queue:gw-1/a2a/v1/discovery", "queue:gw-1/tasklog", "eventmesh:handlerA"} {
		if !ids[want] {
			t.Errorf("collectHealthProblems() = %v, missing %q", got, want)
		}
	}
	if len(got) != 3 {
		t.Errorf("collectHealthProblems() returned %d problems, want 3", len(got))
	}
}

func TestCollectHealthProblems_AdapterWithoutHealthReporterContributesNothing(t *testing.T) {
	core := NewGatewayCore(&recordingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw-1"})
	if got := core.collectHealthProblems(); got != nil {
		t.Errorf("collectHealthProblems() = %v, want nil for a healthy core with a non-reporting adapter", got)
	}
}

type panickingHealthReportingAdapter struct {
	recordingAdapter
}

func (a *panickingHealthReportingAdapter) HealthProblems() []health.Problem {
	panic("boom")
}

func TestCollectHealthProblems_AdapterPanicDoesNotCrashCardPublish(t *testing.T) {
	adapter := &panickingHealthReportingAdapter{}
	core := NewGatewayCore(adapter, map[string]any{"namespace": "test-ns", "gateway_id": "gw-1"})
	core.logger = slog.New(slog.DiscardHandler)
	core.health.Set("queue:gw-1/a2a/v1/discovery", health.Degraded, "own queue trouble")

	got := core.collectHealthProblems()

	if len(got) != 1 || got[0].ID != "queue:gw-1/a2a/v1/discovery" {
		t.Errorf("collectHealthProblems() = %v, want only the core's own problem despite the panicking adapter", got)
	}
}
