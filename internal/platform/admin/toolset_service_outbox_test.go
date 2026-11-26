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

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type outboxFixture struct {
	svc       *ToolsetService
	repo      *store.ToolPackageRepo
	outbox    *store.OutboxRepo
	agentRepo *store.AgentRepo
	ns        string
}

func newOutboxFixture(t *testing.T) *outboxFixture {
	t.Helper()
	db := testOpenDB(t)
	logger := slog.New(slog.DiscardHandler)

	repo := store.NewToolPackageRepo(db)
	outboxRepo := store.NewOutboxRepo(db)
	agentRepo := store.NewAgentRepo(db)

	resourceStore := objectstore.NewStore(objectstore.NewMemoryClient())
	storage := newToolsetStorage(resourceStore, logger)

	svc := NewToolsetService(db, repo, storage, outboxRepo, "ns", logger)
	return &outboxFixture{
		svc:       svc,
		repo:      repo,
		outbox:    outboxRepo,
		agentRepo: agentRepo,
		ns:        "ns",
	}
}

func (f *outboxFixture) createPkg(ctx context.Context, t *testing.T, name string) *ToolsetResponse {
	t.Helper()
	resp, err := f.svc.Create(ctx, CreateToolsetRequest{Name: name, Description: "d"}, "u")
	if err != nil {
		t.Fatalf("create package %q: %v", name, err)
	}
	return resp
}

func (f *outboxFixture) unpublished(ctx context.Context, t *testing.T) []store.OutboxEvent {
	t.Helper()
	events, err := f.outbox.GetDispatchable(ctx, 100)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	return events
}

func TestToolsetService_Upload_WritesOutboxEvent(t *testing.T) {
	t.Parallel()
	f := newOutboxFixture(t)
	ctx := t.Context()

	pkg := f.createPkg(ctx, t, "ob_pkg")

	agent := &store.Agent{Name: "agent-ob", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := f.agentRepo.Create(ctx, agent, []string{pkg.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	zipBytes := buildValidZIP(t, map[string][]byte{"tool.py": []byte("x = 1")})
	if err := f.svc.Upload(ctx, pkg.ID, "p.zip", zipBytes, "alice"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}
	e := events[0]
	if e.EventType != "tool.uploaded" {
		t.Errorf("EventType = %q, want tool.uploaded", e.EventType)
	}
	if e.AggregateID != pkg.ID {
		t.Errorf("AggregateID = %q, want %q (pkg UUID)", e.AggregateID, pkg.ID)
	}

	var payload redeployPayload
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Name != pkg.Name {
		t.Errorf("payload.Name = %q, want %q", payload.Name, pkg.Name)
	}
	if payload.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", payload.UserID)
	}
	if len(payload.AgentIDs) != 1 || payload.AgentIDs[0] != agent.ID {
		t.Errorf("AgentIDs = %v, want [%s]", payload.AgentIDs, agent.ID)
	}
}

func TestToolsetService_Delete_WritesOutboxEvent(t *testing.T) {
	t.Parallel()
	f := newOutboxFixture(t)
	ctx := t.Context()

	pkg := f.createPkg(ctx, t, "ob_del")
	agent := &store.Agent{Name: "agent-del", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := f.agentRepo.Create(ctx, agent, []string{pkg.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	if err := f.svc.Delete(ctx, pkg.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(events))
	}
	e := events[0]
	if e.EventType != "tool.deleted" {
		t.Errorf("EventType = %q, want tool.deleted", e.EventType)
	}
	if e.AggregateID != pkg.ID {
		t.Errorf("AggregateID = %q, want %q (pkg UUID)", e.AggregateID, pkg.ID)
	}
	var payload redeployPayload
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Name != pkg.Name {
		t.Errorf("payload.Name = %q, want %q", payload.Name, pkg.Name)
	}
	if len(payload.AgentIDs) != 1 || payload.AgentIDs[0] != agent.ID {
		t.Errorf("AgentIDs = %v, want [%s]", payload.AgentIDs, agent.ID)
	}
}

func TestToolsetService_Upload_NoAgentsStillWritesEvent(t *testing.T) {
	t.Parallel()
	f := newOutboxFixture(t)
	ctx := t.Context()

	pkg := f.createPkg(ctx, t, "ob_noagents")
	zipBytes := buildValidZIP(t, map[string][]byte{"tool.py": []byte("x = 1")})
	if err := f.svc.Upload(ctx, pkg.ID, "p.zip", zipBytes, "bob"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 event even with no agents, got %d", len(events))
	}
	if events[0].EventType != "tool.uploaded" {
		t.Errorf("EventType = %q, want tool.uploaded", events[0].EventType)
	}
}

func TestToolsetService_PatchConfig_WritesOutboxEvent(t *testing.T) {
	t.Parallel()
	f := newOutboxFixture(t)
	ctx := t.Context()

	pkg := f.createPkg(ctx, t, "ob_cfg")

	agent := &store.Agent{Name: "agent-cfg", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := f.agentRepo.Create(ctx, agent, []string{pkg.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	if err := f.repo.UpdateZipInfo(ctx, pkg.ID, "python", "s3://fake", "pkg.zip", 1, "u"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}
	schemaJSON := `[{"key":"api_key","type":"string","secret":true}]`
	params := `{}`
	f.svc.HandleToolInit(ctx, "ob_cfg__do", "desc", ToolInitMeta{Params: &params, ConfigSchema: &schemaJSON})

	if events, err := f.outbox.GetDispatchable(ctx, 100); err == nil && len(events) > 0 {
		ids := make([]string, len(events))
		for i, e := range events {
			ids[i] = e.ID
		}
		_ = f.outbox.MarkPublished(ctx, ids)
	}

	if _, err := f.svc.PatchConfig(ctx, pkg.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-cfg"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	events := f.unpublished(ctx, t)
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event after PatchConfig, got %d", len(events))
	}
	e := events[0]
	if e.EventType != "tool.config.updated" {
		t.Errorf("EventType = %q, want tool.config.updated", e.EventType)
	}
	if e.AggregateID != pkg.ID {
		t.Errorf("AggregateID = %q, want %q (pkg UUID)", e.AggregateID, pkg.ID)
	}

	var payload redeployPayload
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Name != pkg.Name {
		t.Errorf("payload.Name = %q, want %q", payload.Name, pkg.Name)
	}
	if payload.UserID != "alice" {
		t.Errorf("UserID = %q, want alice", payload.UserID)
	}
	if len(payload.AgentIDs) != 1 || payload.AgentIDs[0] != agent.ID {
		t.Errorf("AgentIDs = %v, want [%s]", payload.AgentIDs, agent.ID)
	}
}

func TestToolsetService_NoOutboxRepo_DoesNotPanic(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	logger := slog.New(slog.DiscardHandler)
	repo := store.NewToolPackageRepo(db)
	resourceStore := objectstore.NewStore(objectstore.NewMemoryClient())
	storage := newToolsetStorage(resourceStore, logger)

	svc := NewToolsetService(db, repo, storage, nil, "ns", logger)
	ctx := t.Context()

	resp, err := svc.Create(ctx, CreateToolsetRequest{Name: "nilob"}, "u")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	zipBytes := buildValidZIP(t, map[string][]byte{"tool.py": []byte("x = 1")})
	if err := svc.Upload(ctx, resp.ID, "p.zip", zipBytes, "bob"); err != nil {
		t.Fatalf("Upload with nil outbox: %v", err)
	}
	if err := svc.Delete(ctx, resp.ID); err != nil {
		t.Fatalf("Delete with nil outbox: %v", err)
	}
}
