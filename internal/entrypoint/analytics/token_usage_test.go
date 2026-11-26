// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestService_TokenUsage_ContentTierGate(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	svc := NewService(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, user string, in, out int) {
		task := &store.Task{
			ID: id, UserID: user, StartTime: base, EndTime: i64p(base + 1000),
			Status: strp("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			TotalInputTokens: ip(in), TotalOutputTokens: ip(out), TotalCachedInputTokens: ip(0),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("a0000000-0000-0000-0000-000000000001", "alice", 10, 20)
	mk("a0000000-0000-0000-0000-000000000002", "bob", 5, 5)

	page := func(q Query) Query { q.PageNumber, q.PageSize = 1, 100; return q }

	if _, err := svc.TokenUsage(ctx, page(Query{GroupBy: "user", IncludeContent: false})); !errors.Is(err, ErrForbidden) {
		t.Errorf("groupBy=user without content tier: err = %v, want ErrForbidden", err)
	}

	byUser, err := svc.TokenUsage(ctx, page(Query{GroupBy: "user", IncludeContent: true}))
	if err != nil {
		t.Fatalf("groupBy=user with content tier: unexpected err = %v", err)
	}
	got := make(map[string]TokenUsageRow, len(byUser.Data))
	for _, r := range byUser.Data {
		got[r.GroupKey] = r
	}
	if len(got) != 2 {
		t.Fatalf("groupBy=user rows = %d, want 2 (alice, bob)", len(got))
	}
	if a := got["alice"]; a.TotalInputTokens != 10 || a.TotalOutputTokens != 20 {
		t.Errorf("alice tokens = %d in / %d out, want 10/20", a.TotalInputTokens, a.TotalOutputTokens)
	}
	if b := got["bob"]; b.TotalInputTokens != 5 || b.TotalOutputTokens != 5 {
		t.Errorf("bob tokens = %d in / %d out, want 5/5", b.TotalInputTokens, b.TotalOutputTokens)
	}

	byDay, err := svc.TokenUsage(ctx, page(Query{GroupBy: "day", IncludeContent: false}))
	if err != nil {
		t.Fatalf("groupBy=day without content tier: unexpected err = %v", err)
	}
	if len(byDay.Data) != 1 {
		t.Fatalf("groupBy=day rows = %d, want 1 (both tasks share a day)", len(byDay.Data))
	}
	if d := byDay.Data[0]; d.TotalInputTokens != 15 || d.TotalOutputTokens != 25 {
		t.Errorf("day totals = %d in / %d out, want 15/25 (alice+bob)", d.TotalInputTokens, d.TotalOutputTokens)
	}
}
