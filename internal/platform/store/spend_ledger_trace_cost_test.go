// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func TestSpendLedgerRepo_CostByTrace(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	ctx := t.Context()

	onTrace := func(e SpendLedgerEntry, trace string, surface spend.Surface) SpendLedgerEntry {
		e.ID = idgen.New()
		e.TraceID = &trace
		e.Surface = string(surface)
		return e
	}
	priced := pricedLedgerEntry()
	entries := []SpendLedgerEntry{
		onTrace(priced, "trial-a", spend.SurfaceAgent),
		onTrace(priced, "trial-a", spend.SurfaceDecision),
		onTrace(priced, "trial-a", spend.SurfaceEvalJudge),
		onTrace(priced, "trial-b", spend.SurfaceAgent),
		onTrace(unpricedLedgerEntry(), "trial-b", spend.SurfaceAgent),
		onTrace(priced, "other", spend.SurfaceAgent),
	}
	if _, err := repo.Insert(ctx, entries); err != nil {
		t.Fatalf("insert: %v", err)
	}

	costs, err := repo.CostByTrace(ctx, []string{"trial-a", "trial-b", "trial-none"}, []spend.Surface{spend.SurfaceEvalJudge})
	if err != nil {
		t.Fatalf("cost by trace: %v", err)
	}

	if a := costs["trial-a"]; a.Cost != 2*28_200_000 || a.Unpriced != 0 || a.Tokens != 2*(400+50+400+200) {
		t.Errorf("trial-a = %+v, want two priced calls, their tokens, and the judge's left out", a)
	}
	if b := costs["trial-b"]; b.Cost != 28_200_000 || b.Unpriced != 1 {
		t.Errorf("trial-b = %+v, want one priced call and one unpriced", b)
	}
	if _, ok := costs["trial-none"]; ok {
		t.Error("a trace with nothing filed reported a cost")
	}
	if _, ok := costs["other"]; ok {
		t.Error("a trace that was not asked for was returned")
	}
	if none, err := repo.CostByTrace(ctx, nil, nil); err != nil || len(none) != 0 {
		t.Errorf("no traces = %v, %v", none, err)
	}
}

func TestSpendLedgerRepo_CostByTraceOfOnlyUnpricedCalls(t *testing.T) {
	repo := NewSpendLedgerRepo(TestDB(t))
	ctx := t.Context()
	trace := "trial-unpriced"
	entries := []SpendLedgerEntry{unpricedLedgerEntry(), unpricedLedgerEntry()}
	for i := range entries {
		entries[i].ID, entries[i].TraceID, entries[i].Surface = idgen.New(), &trace, string(spend.SurfaceAgent)
	}
	if _, err := repo.Insert(ctx, entries); err != nil {
		t.Fatalf("insert: %v", err)
	}

	costs, err := repo.CostByTrace(ctx, []string{trace}, nil)

	if err != nil {
		t.Fatalf("cost by trace: %v", err)
	}
	if c := costs[trace]; c.Cost != 0 || c.Unpriced != 2 {
		t.Errorf("cost = %+v, want no cost and two unpriced calls", c)
	}
}

func TestSpendLedgerRepo_CostByTraceTakesMoreTracesThanOneInClause(t *testing.T) {
	repo := NewSpendLedgerRepo(TestDB(t))
	ctx := t.Context()
	traces := make([]string, 2*dbutil.InClauseCap+1)
	for i := range traces {
		traces[i] = fmt.Sprintf("trial-%04d", i)
	}
	filed := []string{traces[0], traces[dbutil.InClauseCap], traces[len(traces)-1]}
	var entries []SpendLedgerEntry
	for i := range filed {
		e := pricedLedgerEntry()
		e.ID, e.TraceID, e.Surface = idgen.New(), &filed[i], string(spend.SurfaceAgent)
		entries = append(entries, e)
	}
	if _, err := repo.Insert(ctx, entries); err != nil {
		t.Fatalf("insert: %v", err)
	}

	costs, err := repo.CostByTrace(ctx, append(traces, traces[0]), nil)

	if err != nil {
		t.Fatalf("cost by trace: %v", err)
	}
	for _, trace := range filed {
		if c, ok := costs[trace]; !ok || c.Cost != 28_200_000 {
			t.Errorf("%s = %+v, %v; want its cost from whichever batch read it", trace, c, ok)
		}
	}
	if len(costs) != len(filed) {
		t.Errorf("costs = %d traces, want %d", len(costs), len(filed))
	}
}

func TestSpendLedgerRepo_CostByTraceReportsAFailedBatch(t *testing.T) {
	repo := NewSpendLedgerRepo(TestDB(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	traces := make([]string, dbutil.InClauseCap+1)
	for i := range traces {
		traces[i] = fmt.Sprintf("trial-%04d", i)
	}

	costs, err := repo.CostByTrace(ctx, traces, nil)

	if err == nil || costs != nil {
		t.Errorf("costs = %v, err = %v; want the failure reported and no partial costs", costs, err)
	}
}
