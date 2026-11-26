// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestActivityRollupOrderingIsDeterministic(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	svc := NewService(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	order := []int{7, 2, 11, 0, 5, 9, 3, 8, 1, 10, 4, 6}
	for _, i := range order {
		end := now + int64(i)
		name := fmt.Sprintf("agent_%02d", i)
		if err := taskRepo.Create(ctx, &store.Task{
			ID:     fmt.Sprintf("00000000-0000-0000-0000-0000000000%02d", i),
			UserID: "u", StartTime: now, EndTime: &end, Status: strp("completed"),
			ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: strp(name), HandlerType: strp(store.TaskHandlerTypeAgent),
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	q := Query{StartDate: now - 1000, EndDate: now + 1000, IncludeContent: true, PageSize: 100, PageNumber: 1}

	namesOf := func() []string {
		res, err := svc.ActivityAgentPerformance(ctx, q)
		if err != nil {
			t.Fatalf("ActivityAgentPerformance: %v", err)
		}
		out := make([]string, 0, len(res.Data))
		for _, r := range res.Data {
			out = append(out, r.Agent.Name)
		}
		return out
	}

	first := namesOf()
	if len(first) != 12 {
		t.Fatalf("expected 12 rows, got %d", len(first))
	}
	for i := 1; i < len(first); i++ {
		if first[i-1] >= first[i] {
			t.Errorf("rows not in name-ascending tiebreak order: %q before %q", first[i-1], first[i])
		}
	}
	for run := 1; run < 8; run++ {
		got := namesOf()
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("ordering changed between identical calls (run %d): pos %d %q != %q",
					run, i, got[i], first[i])
			}
		}
	}
}
