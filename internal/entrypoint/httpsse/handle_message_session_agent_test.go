// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func newMessageHandlerWithDB(t *testing.T) (*messageHandler, *store.SessionRepo) {
	t.Helper()
	adapter, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	return &messageHandler{core: core, sseManager: adapter.sseManager, sessRepo: sessRepo}, sessRepo
}

func TestEnsureSessionAgent_CreatesNewSession(t *testing.T) {
	h, sessRepo := newMessageHandlerWithDB(t)
	ctx := context.Background()

	id := idgen.New()
	projectID := idgen.New()
	h.ensureSessionAgent(ctx, nil, chatSession{contextID: id, userID: "u1", agentName: "AgentA", projectID: projectID}, slog.New(slog.DiscardHandler))

	got, err := sessRepo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("session not created")
	}
	if got.AgentID == nil || *got.AgentID != "AgentA" {
		t.Errorf("AgentID = %v, want AgentA", got.AgentID)
	}
	if got.ProjectID == nil || *got.ProjectID != projectID {
		t.Errorf("ProjectID = %v, want %s", got.ProjectID, projectID)
	}
}

func TestEnsureSessionAgent_StampsEvalSource(t *testing.T) {
	h, sessRepo := newMessageHandlerWithDB(t)
	ctx := context.Background()

	id := idgen.New()
	h.ensureSessionAgent(ctx, nil, chatSession{contextID: id, userID: "u1", agentName: "AgentA", source: "eval"}, slog.New(slog.DiscardHandler))

	got, err := sessRepo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.Source == nil || *got.Source != "eval" {
		t.Errorf("Source = %v, want eval", func() any {
			if got == nil {
				return nil
			}
			return got.Source
		}())
	}
}

func TestAllowedSessionSource_OnlyHonorsEval(t *testing.T) {
	cases := map[string]string{
		"eval":         "eval",
		"scheduler":    "",
		"chat":         "",
		"builder_test": "",
		"":             "",
	}
	for in, want := range cases {
		if got := allowedSessionSource(map[string]any{"session_source": in}); got != want {
			t.Errorf("allowedSessionSource(%q) = %q, want %q", in, got, want)
		}
	}
	if got := allowedSessionSource(map[string]any{}); got != "" {
		t.Errorf("allowedSessionSource(no key) = %q, want empty", got)
	}
	if got := allowedSessionSource(map[string]any{"session_source": 42}); got != "" {
		t.Errorf("allowedSessionSource(non-string) = %q, want empty", got)
	}
}

func TestEnsureSessionAgent_BackfillsExistingNullAgent(t *testing.T) {
	h, sessRepo := newMessageHandlerWithDB(t)
	ctx := context.Background()

	id := idgen.New()
	if err := sessRepo.Create(ctx, &store.Session{ID: id, UserID: "u1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := sessRepo.GetByID(ctx, id)
	if before.AgentID != nil {
		t.Fatalf("precondition: AgentID = %v, want nil", before.AgentID)
	}
	time.Sleep(2 * time.Millisecond)

	h.ensureSessionAgent(ctx, before, chatSession{contextID: id, userID: "u1", agentName: "AgentA"}, slog.New(slog.DiscardHandler))

	got, _ := sessRepo.GetByID(ctx, id)
	if got.AgentID == nil || *got.AgentID != "AgentA" {
		t.Errorf("AgentID = %v, want AgentA (backfilled)", got.AgentID)
	}
	if got.UpdatedTime != before.UpdatedTime {
		t.Errorf("UpdatedTime = %d, want unchanged %d", got.UpdatedTime, before.UpdatedTime)
	}
}

func TestEnsureSessionAgent_BestEffortWhenDBFails(t *testing.T) {
	_, core, sessRepo, _, _ := setupTestAdapterWithDB(t)
	if db := core.Store(); db != nil {
		_ = db.Close()
	}
	h := &messageHandler{core: core, sseManager: nil, sessRepo: sessRepo}

	h.ensureSessionAgent(context.Background(), nil, chatSession{contextID: idgen.New(), userID: "u1", agentName: "AgentA"}, slog.New(slog.DiscardHandler))
}

func TestEnsureSessionAgent_DoesNotOverwriteExistingAgent(t *testing.T) {
	h, sessRepo := newMessageHandlerWithDB(t)
	ctx := context.Background()

	id := idgen.New()
	original := "AgentOriginal"
	if err := sessRepo.Create(ctx, &store.Session{ID: id, UserID: "u1", AgentID: &original}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	existing, err := sessRepo.GetByID(ctx, id)
	if err != nil || existing == nil {
		t.Fatalf("read seeded session: %v, %v", existing, err)
	}
	h.ensureSessionAgent(ctx, existing, chatSession{contextID: id, userID: "u1", agentName: "AgentNew"}, slog.New(slog.DiscardHandler))

	got, _ := sessRepo.GetByID(ctx, id)
	if got.AgentID == nil || *got.AgentID != "AgentOriginal" {
		t.Errorf("AgentID = %v, want unchanged AgentOriginal", got.AgentID)
	}
}
