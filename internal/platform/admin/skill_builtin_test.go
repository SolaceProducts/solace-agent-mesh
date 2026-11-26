// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestSkillService_ListWithBuiltins_PagesOneVirtualList(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewSkillService(db, store.NewSkillRepo(db), slog.New(slog.DiscardHandler))
	ctx := context.Background()
	for i := range 5 {
		if _, err := svc.Create(ctx, CreateSkillRequest{Name: fmt.Sprintf("custom-%d", i)}, "user-1"); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	builtins := visibleBuiltinSkills(nil)
	want := len(builtins) + 5

	for _, pageSize := range []int{1, 3, len(builtins), len(builtins) + 1, 100} {
		var ids []string
		for page := 1; ; page++ {
			resp, err := svc.ListWithBuiltins(ctx, page, pageSize, "")
			if err != nil {
				t.Fatalf("pageSize %d page %d: %v", pageSize, page, err)
			}
			if resp.Meta.Pagination.Count != want {
				t.Fatalf("pageSize %d: count %d, want %d", pageSize, resp.Meta.Pagination.Count, want)
			}
			if len(resp.Data) == 0 {
				break
			}
			for _, s := range resp.Data {
				ids = append(ids, s.ID)
			}
		}
		if len(ids) != want {
			t.Fatalf("pageSize %d: walked %d items, want %d", pageSize, len(ids), want)
		}
		for i, b := range builtins {
			if ids[i] != b.ID {
				t.Fatalf("pageSize %d: item %d is %q, want built-in %q first", pageSize, i, ids[i], b.ID)
			}
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("pageSize %d: %q listed twice", pageSize, id)
			}
			seen[id] = true
		}
	}

	for origin, wantCount := range map[string]int{"custom": 5, "builtin": len(builtins)} {
		resp, err := svc.ListWithBuiltins(ctx, 1, 100, origin)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Data) != wantCount || resp.Meta.Pagination.Count != wantCount {
			t.Errorf("origin %s: %d items, count %d, want %d", origin, len(resp.Data), resp.Meta.Pagination.Count, wantCount)
		}
	}
}
