// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

func TestTheInboxPruneRunsOnTheLeaderAndKeepsRecentNotifications(t *testing.T) {
	core := newTestCore(t, true, map[string]any{"retention_days": float64(7)})
	jobs := core.LeaderJobs()
	if len(jobs) != 1 || jobs[0].Name != leader.JobNotifyInboxPrune || jobs[0].Every != pruneInterval {
		t.Fatalf("leader jobs = %+v, want the inbox prune every %s", jobs, pruneInterval)
	}
	ctx := t.Context()
	old, err := core.inbox.Add(ctx, "u1", "old", "", nil, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	recent, err := core.inbox.Add(ctx, "u1", "recent", "", nil, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.db.ExecContext(ctx, `UPDATE notifications SET created_at = ?, status = 'read' WHERE id = ?`,
		time.Now().Add(-8*24*time.Hour).UnixMilli(), old); err != nil {
		t.Fatal(err)
	}

	if err = jobs[0].Run(ctx, leader.Term{Number: 1}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	for id, want := range map[string]int{old: 0, recent: 1} {
		var n int
		if err := core.db.QueryRowContext(ctx, `SELECT count(*) FROM notifications WHERE id = ?`, id).Scan(&n); err != nil || n != want {
			t.Errorf("notification %s count = %d (%v), want %d", id, n, err, want)
		}
	}
}

func TestAnInboxKeptForeverOffersNoPrune(t *testing.T) {
	if jobs := newTestCore(t, true, map[string]any{"retention_days": float64(0)}).LeaderJobs(); len(jobs) != 0 {
		t.Errorf("leader jobs with retention off = %+v, want none", jobs)
	}
	if jobs := newTestCore(t, false, map[string]any{"retention_days": float64(7)}).LeaderJobs(); len(jobs) != 0 {
		t.Errorf("leader jobs without an inbox = %+v, want none", jobs)
	}
}

func TestPruneRemovesDedupKeysPastRetention(t *testing.T) {
	core := newTestCore(t, true, map[string]any{"retention_days": float64(7)})
	ctx := t.Context()
	for _, k := range []string{"old", "recent"} {
		if _, err := core.inbox.ClaimDedup(ctx, "u1", k); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := core.db.ExecContext(ctx, `UPDATE notify_dedup_keys SET created_at = ? WHERE dedup = 'old'`,
		time.Now().Add(-8*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	if err := core.prune(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
	for k, wantClaimable := range map[string]bool{"old": true, "recent": false} {
		if claimed, err := core.inbox.ClaimDedup(ctx, "u1", k); err != nil || claimed != wantClaimable {
			t.Errorf("ClaimDedup(%s) after prune = %v (%v), want %v", k, claimed, err, wantClaimable)
		}
	}
}
