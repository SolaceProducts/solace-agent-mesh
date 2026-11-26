// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestServiceActivityFeedback_OwnerScopeIsolatesRows(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	ctx := context.Background()

	store.SeedFeedback(t, db, &store.Feedback{
		TaskID: "90000000-0000-0000-0000-000000000001", UserID: "alice",
		SessionID: "90000000-0000-0000-0000-000000000011", Rating: "up",
		SubjectID: "AliceAgent",
	})
	store.SeedFeedback(t, db, &store.Feedback{
		TaskID: "90000000-0000-0000-0000-000000000002", UserID: "bob",
		SessionID: "90000000-0000-0000-0000-000000000012", Rating: "down",
		SubjectID: "BobAgent",
	})

	taskIDs := func(q Query) []string {
		got, err := svc.ActivityFeedback(ctx, q)
		if err != nil {
			t.Fatalf("ActivityFeedback: %v", err)
		}
		out := make([]string, 0, len(got.Data))
		for _, r := range got.Data {
			out = append(out, r.TaskID)
		}
		return out
	}

	alice := taskIDs(Query{OwnerUserID: "alice"})
	if len(alice) != 1 || alice[0] != "90000000-0000-0000-0000-000000000001" {
		t.Errorf("alice-scoped rows = %v, want only alice's task", alice)
	}

	all := taskIDs(Query{})
	if len(all) != 2 {
		t.Errorf("tenant-wide (empty OwnerUserID) rows = %v, want 2 rows", all)
	}
}

func TestServiceActivityFeedback_CommentAlwaysIncluded(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	ctx := context.Background()

	comment := "not shared with anyone"
	store.SeedFeedback(t, db, &store.Feedback{
		TaskID: "90000000-0000-0000-0000-000000000003", UserID: "carol",
		SessionID: "90000000-0000-0000-0000-000000000013", Rating: "down",
		Comment: &comment, ShareContent: false, SubjectID: "CarolAgent",
	})

	got, err := svc.ActivityFeedback(ctx, Query{OwnerUserID: "carol"})
	if err != nil {
		t.Fatalf("ActivityFeedback: %v", err)
	}
	if len(got.Data) != 1 {
		t.Fatalf("rows = %d, want 1", len(got.Data))
	}
	if got.Data[0].Comment == nil || *got.Data[0].Comment != comment {
		t.Errorf("Comment = %v, want %q (must not be redacted despite ShareContent=false)", got.Data[0].Comment, comment)
	}
}

func TestServiceActivityFeedback_TruncatedAtFetchCap(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	svc.activityFeedbackFetchLimit = 2
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		store.SeedFeedback(t, db, &store.Feedback{
			TaskID:    fmt.Sprintf("91000000-0000-0000-0000-00000000000%d", i),
			UserID:    "dave",
			SessionID: fmt.Sprintf("91000000-0000-0000-0000-00000000001%d", i),
			Rating:    "up",
			SubjectID: "DaveAgent",
		})
	}

	got, err := svc.ActivityFeedback(ctx, Query{OwnerUserID: "dave"})
	if err != nil {
		t.Fatalf("ActivityFeedback: %v", err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("rows = %d, want 2 (capped)", len(got.Data))
	}
	if !got.Meta.Truncated {
		t.Error("Meta.Truncated = false, want true (fetch cap was hit)")
	}
}

func TestServiceActivityFeedback_NotTruncatedBelowCap(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	ctx := context.Background()

	store.SeedFeedback(t, db, &store.Feedback{
		TaskID: "92000000-0000-0000-0000-000000000001", UserID: "erin",
		SessionID: "92000000-0000-0000-0000-000000000011", Rating: "up",
		SubjectID: "ErinAgent",
	})

	got, err := svc.ActivityFeedback(ctx, Query{OwnerUserID: "erin"})
	if err != nil {
		t.Fatalf("ActivityFeedback: %v", err)
	}
	if got.Meta.Truncated {
		t.Error("Meta.Truncated = true, want false (well below cap)")
	}
}

func TestServiceActivityFeedback_SubjectRefReconstructsWrappedSlug(t *testing.T) {
	const dashedID = "019ea471-5051-73c7-893a-7679460fd074"
	const wrappedSlug = "agent_019ea471_5051_73c7_893a_7679460fd074"

	svc := (&Service{}).WithAgentNameResolver(func(s string) string {
		switch s {
		case wrappedSlug:
			return "Support Agent"
		case "BuiltinName":
			return "Builtin Display Name"
		}
		return ""
	})

	deployed := svc.subjectRef(dashedID, store.SubjectTypeAgent)
	if deployed.ID != dashedID || deployed.Name != "Support Agent" {
		t.Errorf("subjectRef(deployed) = %+v, want {ID:%s Name:Support Agent}", deployed, dashedID)
	}

	builtin := svc.subjectRef("BuiltinName", store.SubjectTypeAgent)
	if builtin.ID != "BuiltinName" || builtin.Name != "Builtin Display Name" {
		t.Errorf("subjectRef(builtin) = %+v, want {ID:BuiltinName Name:Builtin Display Name}", builtin)
	}

	unresolved := svc.subjectRef("unknown-id", store.SubjectTypeWorkflow)
	if unresolved.ID != "unknown-id" || unresolved.Name != "unknown-id" || unresolved.Type != store.SubjectTypeWorkflow {
		t.Errorf("subjectRef(unresolved) = %+v, want fallback to bare id as name, Type=workflow", unresolved)
	}
}
