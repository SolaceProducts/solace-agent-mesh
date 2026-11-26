// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type skillOutboxFixture struct {
	svc    *SkillService
	repo   *store.SkillRepo
	outbox *store.OutboxRepo
}

func newSkillOutboxFixture(t *testing.T) *skillOutboxFixture {
	t.Helper()
	db := testOpenDB(t)
	logger := slog.New(slog.DiscardHandler)

	repo := store.NewSkillRepo(db)
	outboxRepo := store.NewOutboxRepo(db)

	svc := NewSkillService(db, repo, logger)
	svc.WithOutbox(outboxRepo)
	return &skillOutboxFixture{svc: svc, repo: repo, outbox: outboxRepo}
}

func (f *skillOutboxFixture) drain(ctx context.Context, t *testing.T) {
	t.Helper()
	events, err := f.outbox.GetDispatchable(ctx, 100)
	if err != nil || len(events) == 0 {
		return
	}
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	_ = f.outbox.MarkPublished(ctx, ids)
}

func (f *skillOutboxFixture) unpublished(ctx context.Context, t *testing.T) []store.OutboxEvent {
	t.Helper()
	events, err := f.outbox.GetDispatchable(ctx, 100)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	return events
}

func (f *skillOutboxFixture) seedReady(ctx context.Context, t *testing.T, name, bundled string) *store.Skill {
	t.Helper()
	bt := bundled
	sk := &store.Skill{
		Name: name, Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	return sk
}

func TestSkillService_PatchConfig_WritesOutboxEvent(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := f.seedReady(ctx, t, "ob_skill", skillSchemaSecretRequired)

	f.drain(ctx, t)

	if _, err := f.svc.PatchConfig(ctx, sk.ID, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-cfg"},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event after PatchConfig, got %d", len(events))
	}
	e := events[0]
	if e.EventType != "skill.config.updated" {
		t.Errorf("EventType = %q, want skill.config.updated", e.EventType)
	}
	if e.AggregateID != sk.ID {
		t.Errorf("AggregateID = %q, want %q (skill UUID)", e.AggregateID, sk.ID)
	}

	var payload skillOutboxPayload
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Name != sk.Name {
		t.Errorf("payload.Name = %q, want %q", payload.Name, sk.Name)
	}
	if payload.UserID != "alice" {
		t.Errorf("payload.UserID = %q, want alice", payload.UserID)
	}
}

func TestSkillService_PatchConfig_AtomicWithRowUpsert(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := f.seedReady(ctx, t, "ob_atomic_skill", skillSchemaSecretRequired)
	f.drain(ctx, t)

	if _, err := f.svc.PatchConfig(ctx, sk.ID, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-1"},
		},
	}, "alice"); err != nil {
		t.Fatalf("first PatchConfig: %v", err)
	}
	if got := len(f.unpublished(ctx, t)); got != 1 {
		t.Fatalf("after first PatchConfig: got %d events, want 1", got)
	}
	f.drain(ctx, t)

	if _, err := f.svc.PatchConfig(ctx, sk.ID, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"model_id": "gpt-5"},
		},
	}, "bob"); err != nil {
		t.Fatalf("second PatchConfig: %v", err)
	}
	if got := len(f.unpublished(ctx, t)); got != 1 {
		t.Fatalf("after second PatchConfig: got %d events, want 1", got)
	}

	resp, err := f.svc.GetConfig(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	render := resp.Values["render"]
	if render["api_key"] != redactedSentinel {
		t.Errorf("api_key gone after second patch, got %v — upsert may have rolled back", render["api_key"])
	}
	if render["model_id"] != "gpt-5" {
		t.Errorf("model_id = %v, want gpt-5", render["model_id"])
	}
}

func TestSkillService_PatchConfig_NoOutboxRepo_DoesNotPanic(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))
	ctx := t.Context()

	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "nilob_skill", Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := svc.PatchConfig(ctx, sk.ID, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-1"},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig with nil outbox: %v", err)
	}
}

func TestSkillService_PatchConfig_OutboxCarriesAgentIDs(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := f.seedReady(ctx, t, "fanout_skill", skillSchemaSecretRequired)

	agentRepo := store.NewAgentRepo(f.svc.db)
	a1 := &store.Agent{ID: "f0000000-0000-0000-0000-000000000a01", Name: "fanout-a", Description: strPtrLocal("a"), CreatedBy: "u", UpdatedBy: "u"}
	a2 := &store.Agent{ID: "f0000000-0000-0000-0000-000000000a02", Name: "fanout-b", Description: strPtrLocal("b"), CreatedBy: "u", UpdatedBy: "u"}
	for _, a := range []*store.Agent{a1, a2} {
		if err := agentRepo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", a.Name, err)
		}
		if err := f.repo.SetAgentSkillsWithConfigs(ctx, a.ID, []store.AgentSkillRow{{AgentID: a.ID, SkillID: sk.ID}}); err != nil {
			t.Fatalf("bind agent %s to skill: %v", a.Name, err)
		}
	}
	f.drain(ctx, t)

	if _, err := f.svc.PatchConfig(ctx, sk.ID, SkillConfigPatchRequest{
		Values: map[string]map[string]any{"render": {"api_key": "sk-fanout"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}
	var payload skillOutboxPayload
	if err := json.Unmarshal([]byte(events[0].Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, id := range payload.AgentIDs {
		gotIDs[id] = true
	}
	if !gotIDs[a1.ID] || !gotIDs[a2.ID] {
		t.Errorf("AgentIDs = %v, want both %s and %s present so RedeployPublisher fans out to every bound agent",
			payload.AgentIDs, a1.ID, a2.ID)
	}
}

func strPtrLocal(s string) *string { return &s }

func TestSkillService_UploadZIP_OutboxOmitsAgentIDs(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	f.svc.WithStorage(objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns")
	ctx := t.Context()

	sk := f.seedReady(ctx, t, "b009_upload_skill", skillSchemaSecretRequired)

	agentRepo := store.NewAgentRepo(f.svc.db)
	agent := &store.Agent{
		ID: "b0090000-0000-0000-0000-000000000a01", Name: "b009-bound", Description: strPtrLocal("a"),
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := f.repo.SetAgentSkillsWithConfigs(ctx, agent.ID, []store.AgentSkillRow{{AgentID: agent.ID, SkillID: sk.ID}}); err != nil {
		t.Fatalf("bind agent: %v", err)
	}
	f.drain(ctx, t)

	skillMD := "---\nname: b009_upload_skill\ndescription: test\n---\n\nContent here.\n"
	zipData := buildSkillZIPWithContent(t, skillMD)
	if err := f.svc.UploadZIP(ctx, sk.ID, sk.Name, zipData, "alice"); err != nil {
		t.Fatalf("UploadZIP: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event after UploadZIP, got %d", len(events))
	}
	var payload skillOutboxPayload
	if err := json.Unmarshal([]byte(events[0].Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.AgentIDs) != 0 {
		t.Errorf("UploadZIP outbox AgentIDs = %v, want empty (would race resolveSkillImpliedToolsets's pending-skill gate)", payload.AgentIDs)
	}

	got, err := f.repo.GetByName(ctx, sk.Name)
	if err != nil || got == nil {
		t.Fatalf("re-read skill: skill=%v err=%v", got, err)
	}
	if got.DiscoveryStatus != "pending" {
		t.Errorf("DiscoveryStatus = %q, want pending", got.DiscoveryStatus)
	}
}

func TestSkillService_Update_OutboxOmitsAgentIDs(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := f.seedReady(ctx, t, "b009_update_skill", skillSchemaSecretRequired)

	agentRepo := store.NewAgentRepo(f.svc.db)
	agent := &store.Agent{
		ID: "b0090000-0000-0000-0000-000000000a02", Name: "b009-update-bound", Description: strPtrLocal("a"),
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := f.repo.SetAgentSkillsWithConfigs(ctx, agent.ID, []store.AgentSkillRow{{AgentID: agent.ID, SkillID: sk.ID}}); err != nil {
		t.Fatalf("bind agent: %v", err)
	}
	f.drain(ctx, t)

	if _, err := f.svc.Update(ctx, sk.ID, UpdateSkillRequest{Description: "updated"}, "alice"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event after Update, got %d", len(events))
	}
	if got := events[0].EventType; got != eventSkillUpdated {
		t.Errorf("EventType = %q, want %q", got, eventSkillUpdated)
	}
	if got := events[0].AggregateID; got != sk.ID {
		t.Errorf("AggregateID = %q, want %q", got, sk.ID)
	}
	var payload skillOutboxPayload
	if err := json.Unmarshal([]byte(events[0].Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.AgentIDs) != 0 {
		t.Errorf("Update outbox AgentIDs = %v, want empty (PATCH only changes description; redeploying here races UploadZIP's pending flip)", payload.AgentIDs)
	}
	if payload.Name != sk.Name {
		t.Errorf("payload.Name = %q, want %q", payload.Name, sk.Name)
	}
	if payload.UserID != "alice" {
		t.Errorf("payload.UserID = %q, want %q", payload.UserID, "alice")
	}
}

func TestSkillService_UpsertFromInit_EmitsRedeployOutboxWhenChanged(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	prev := `["toolset-old"]`
	sk := &store.Skill{
		Name: "b009_init_skill", Origin: "platform", DiscoveryStatus: "pending",
		Toolsets: &prev, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	agentRepo := store.NewAgentRepo(f.svc.db)
	a1 := &store.Agent{ID: "b0090000-0000-0000-0000-000000000b01", Name: "b009-init-a", Description: strPtrLocal("a"), CreatedBy: "u", UpdatedBy: "u"}
	a2 := &store.Agent{ID: "b0090000-0000-0000-0000-000000000b02", Name: "b009-init-b", Description: strPtrLocal("b"), CreatedBy: "u", UpdatedBy: "u"}
	for _, a := range []*store.Agent{a1, a2} {
		if err := agentRepo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", a.Name, err)
		}
		if err := f.repo.SetAgentSkillsWithConfigs(ctx, a.ID, []store.AgentSkillRow{{AgentID: a.ID, SkillID: sk.ID}}); err != nil {
			t.Fatalf("bind agent %s: %v", a.Name, err)
		}
	}
	f.drain(ctx, t)

	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:   sk.Name,
		Description: "freshly discovered",
		Toolsets:    []string{"toolset-new"},
	})

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event after UpsertFromInit, got %d", len(events))
	}
	e := events[0]
	if e.EventType != "skill.updated" {
		t.Errorf("EventType = %q, want skill.updated", e.EventType)
	}
	var payload skillOutboxPayload
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	got := map[string]bool{}
	for _, id := range payload.AgentIDs {
		got[id] = true
	}
	if !got[a1.ID] || !got[a2.ID] {
		t.Errorf("AgentIDs = %v, want both %s and %s present", payload.AgentIDs, a1.ID, a2.ID)
	}

	updated, err := f.repo.GetByName(ctx, sk.Name)
	if err != nil || updated == nil {
		t.Fatalf("re-read skill: %v", err)
	}
	if updated.DiscoveryStatus != "ready" {
		t.Errorf("DiscoveryStatus = %q, want ready", updated.DiscoveryStatus)
	}
	if updated.Toolsets == nil || *updated.Toolsets != `["toolset-new"]` {
		t.Errorf("Toolsets = %v, want fresh value", updated.Toolsets)
	}
}

func TestSkillService_UpsertFromInit_NoOutboxWhenUnchanged(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	desc := "stable"
	toolsets := `["t1"]`
	sk := &store.Skill{
		Name: "b009_stable_skill", Origin: "platform", DiscoveryStatus: "ready",
		Description: &desc, Toolsets: &toolsets, UpdatedBy: "system",
		CreatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.drain(ctx, t)

	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:   sk.Name,
		Description: desc,
		Toolsets:    []string{"t1"},
	})

	if got := len(f.unpublished(ctx, t)); got != 0 {
		t.Errorf("expected no outbox events for unchanged content, got %d", got)
	}
}

func makeDatesShapeBundledTools(order []int) []remotetool.SkillBundledTool {
	all := []remotetool.SkillBundledTool{
		{
			ToolName:    "dates__weekday_of",
			Description: "Returns the day of the week for an ISO date.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"date":   map[string]any{"type": "string"},
					"locale": map[string]any{"type": "string"},
				},
				"required": []any{"date"},
			},
		},
		{
			ToolName:    "dates__date_math",
			Description: "Adds or subtracts dates with optional workday/holiday skipping.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"op":     map[string]any{"type": "string"},
					"start":  map[string]any{"type": "string"},
					"unit":   map[string]any{"type": "string"},
					"amount": map[string]any{"type": "integer"},
				},
				"required": []any{"op", "start", "unit"},
			},
		},
		{
			ToolName:    "dates__parse_date",
			Description: "Resolves a natural-language date phrase to an ISO date.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":      map[string]any{"type": "string"},
					"reference": map[string]any{"type": "string"},
					"locale":    map[string]any{"type": "string"},
				},
				"required": []any{"text"},
			},
		},
		{
			ToolName:    "dates__render_calendar",
			Description: "Renders a deterministic calendar with rich annotations.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"start":  map[string]any{"type": "string"},
					"end":    map[string]any{"type": "string"},
					"format": map[string]any{"type": "string"},
				},
			},
		},
	}
	out := make([]remotetool.SkillBundledTool, len(order))
	for i, idx := range order {
		out[i] = all[idx]
	}
	return out
}

func TestSkillService_UpsertFromInit_NoOutboxWhenUnchanged_DatesShape(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := &store.Skill{
		Name: "dates_shape_stable", Origin: "platform", DiscoveryStatus: "pending",
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.drain(ctx, t)

	bundled := makeDatesShapeBundledTools([]int{0, 1, 2, 3})
	desc := "Deterministic date math and calendar rendering."

	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:    sk.Name,
		Description:  desc,
		BundledTools: bundled,
	})
	if got := len(f.unpublished(ctx, t)); got != 1 {
		t.Fatalf("expected 1 outbox event after first UpsertFromInit, got %d", got)
	}
	f.drain(ctx, t)

	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:    sk.Name,
		Description:  desc,
		BundledTools: bundled,
	})
	if got := len(f.unpublished(ctx, t)); got != 0 {
		t.Errorf("expected no outbox events for identical second republish, got %d", got)
	}
}

func TestSkillService_UpsertFromInit_NoOutboxWhenUnchanged_DatesShapeReordered(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := &store.Skill{
		Name: "dates_shape_reordered", Origin: "platform", DiscoveryStatus: "pending",
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.drain(ctx, t)

	desc := "Deterministic date math and calendar rendering."

	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:    sk.Name,
		Description:  desc,
		BundledTools: makeDatesShapeBundledTools([]int{0, 1, 2, 3}),
	})
	if got := len(f.unpublished(ctx, t)); got != 1 {
		t.Fatalf("expected 1 outbox event after first UpsertFromInit, got %d", got)
	}
	f.drain(ctx, t)

	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:    sk.Name,
		Description:  desc,
		BundledTools: makeDatesShapeBundledTools([]int{3, 1, 0, 2}),
	})
	if got := len(f.unpublished(ctx, t)); got != 0 {
		t.Errorf("expected no outbox events for reordered-but-equivalent bundled tools, got %d (chronic redeploy loop 2026-05-28)", got)
	}
}

func TestBundledToolsJSONEqual(t *testing.T) {
	t.Parallel()
	ptr := func(s string) *string { return &s }

	tests := []struct {
		name string
		a, b *string
		want bool
	}{
		{
			name: "both nil",
			a:    nil, b: nil,
			want: true,
		},
		{
			name: "one nil, one non-nil",
			a:    nil, b: ptr("[]"),
			want: false,
		},
		{
			name: "byte-identical short-circuits true",
			a:    ptr(`[{"tool_name":"t1"}]`), b: ptr(`[{"tool_name":"t1"}]`),
			want: true,
		},
		{
			name: "left malformed JSON",
			a:    ptr("not-json"), b: ptr(`[{"tool_name":"t1"}]`),
			want: false,
		},
		{
			name: "right malformed JSON",
			a:    ptr(`[{"tool_name":"t1"}]`), b: ptr("not-json"),
			want: false,
		},
		{
			name: "empty tool_name in left rejects",
			a:    ptr(`[{"tool_name":""}]`), b: ptr(`[{"tool_name":"t1"}]`),
			want: false,
		},
		{
			name: "duplicate tool_name in left rejects (would collapse in map)",
			a:    ptr(`[{"tool_name":"t1","description":"v1"},{"tool_name":"t1","description":"v2"}]`),
			b:    ptr(`[{"tool_name":"t1","description":"v2"}]`),
			want: false,
		},
		{
			name: "different tool counts",
			a:    ptr(`[{"tool_name":"t1"},{"tool_name":"t2"}]`),
			b:    ptr(`[{"tool_name":"t1"}]`),
			want: false,
		},
		{
			name: "same set, different content per entry",
			a:    ptr(`[{"tool_name":"t1","description":"first"}]`),
			b:    ptr(`[{"tool_name":"t1","description":"second"}]`),
			want: false,
		},
		{
			name: "same set, different slice order",
			a:    ptr(`[{"tool_name":"a"},{"tool_name":"b"}]`),
			b:    ptr(`[{"tool_name":"b"},{"tool_name":"a"}]`),
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bundledToolsJSONEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("bundledToolsJSONEqual = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSkillService_UpsertFromInit_EmitsOutboxWhenBundledToolContentChanges(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := &store.Skill{
		Name: "dates_shape_content_diff", Origin: "platform", DiscoveryStatus: "pending",
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.drain(ctx, t)

	desc := "Deterministic date math and calendar rendering."

	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:    sk.Name,
		Description:  desc,
		BundledTools: makeDatesShapeBundledTools([]int{0, 1, 2, 3}),
	})
	if got := len(f.unpublished(ctx, t)); got != 1 {
		t.Fatalf("expected 1 outbox event after first UpsertFromInit, got %d", got)
	}
	f.drain(ctx, t)

	mutated := makeDatesShapeBundledTools([]int{0, 1, 2, 3})
	mutated[0].Description = "MUTATED: " + mutated[0].Description
	f.svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:    sk.Name,
		Description:  desc,
		BundledTools: mutated,
	})
	if got := len(f.unpublished(ctx, t)); got != 1 {
		t.Errorf("expected exactly 1 outbox event when a bundled tool's Description changes, got %d — the set-comparator must hash full entry content, not just tool_name", got)
	}
}
