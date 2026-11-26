// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestDataRetentionService_TaskHistorySweepsOldMCPArtifactLinks(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	links := store.NewMCPArtifactLinkRepo(db)
	for _, uri := range []string{"artifact://s/old.pdf", "artifact://s/new.pdf"} {
		if err := links.Put(ctx, store.MCPArtifactLink{MCPSessionID: "mcp-a", URI: uri, AgentName: "Ag", UserID: "u", SessionID: "s", Filename: "f"}); err != nil {
			t.Fatalf("put %s: %v", uri, err)
		}
	}
	past := time.Now().Add(-40 * 24 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx, `UPDATE mcp_artifact_links SET created_at = ? WHERE uri = ?`, past, "artifact://s/old.pdf"); err != nil {
		t.Fatalf("age the old link: %v", err)
	}

	svc := NewDataRetentionService(DataRetentionConfig{Enabled: true, TaskRetentionDays: 30, FeedbackRetentionDays: 30, BatchSize: 10},
		store.NewTaskRepo(db), store.NewFeedbackRepo(db), store.NewSSEEventRepo(db), store.NewTaskEventRepo(db), nil, slog.Default()).
		WithMCPArtifactLinks(links)
	if err := svc.cleanupTaskHistory(ctx, time.Now()); err != nil {
		t.Fatalf("cleanupTaskHistory: %v", err)
	}

	left, err := links.ListBySession(ctx, "mcp-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 1 || left[0].URI != "artifact://s/new.pdf" {
		t.Fatalf("links left = %+v, want only the one inside the task retention window", left)
	}
}
