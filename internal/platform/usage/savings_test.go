// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func (f *serviceFixture) saved(userID, userSub string, tokens, cents int64) {
	f.t.Helper()
	entry := store.SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: at(2026, 10, 3, 9, 0).UnixMilli(), Surface: "agent",
		ModelName: new("m"), TraceID: new(idgen.New()), UncachedInputTokens: 1,
		AvoidedInputTokens: new(tokens),
	}
	if cents >= 0 {
		entry.Cost = &spend.Cost{}
		entry.AvoidedInputCost = new(cents * 100_000_000)
	}
	switch {
	case userID != "":
		entry.UserID = new(userID)
	default:
		entry.UserSub = new(userSub)
	}
	if _, err := store.NewSpendLedgerRepo(f.db).Insert(f.t.Context(), []store.SpendLedgerEntry{entry}); err != nil {
		f.t.Fatalf("file a saving call: %v", err)
	}
	if err := store.NewSpendRollupRepo(f.db).RefreshDay(f.t.Context(), startOfDay(entry.CompletedTime), true); err != nil {
		f.t.Fatalf("roll up: %v", err)
	}
}

func (f *serviceFixture) unmeasured(userSub string) {
	f.t.Helper()
	entry := store.SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: at(2026, 10, 3, 9, 0).UnixMilli(), Surface: "agent",
		ModelName: new("m"), TraceID: new(idgen.New()), UncachedInputTokens: 1, UserSub: new(userSub), Cost: &spend.Cost{},
	}
	if _, err := store.NewSpendLedgerRepo(f.db).Insert(f.t.Context(), []store.SpendLedgerEntry{entry}); err != nil {
		f.t.Fatalf("file an unmeasured call: %v", err)
	}
	if err := store.NewSpendRollupRepo(f.db).RefreshDay(f.t.Context(), startOfDay(entry.CompletedTime), true); err != nil {
		f.t.Fatalf("roll up: %v", err)
	}
}

func TestService_SavingsAreTheTenantsAndSelfSavingsTheCallersAlone(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	f.historyBuilt()
	parminder := f.user("parminder")
	f.saved(parminder, "", 1_000, 5)
	f.saved("", "someone-else", 500, 2)
	f.saved("", "no-price", 250, -1)
	f.unmeasured("someone-else")

	tenant, err := f.svc.Savings(t.Context(), WindowRequest{})
	if err != nil {
		t.Fatalf("savings: %v", err)
	}
	if tenant.AvoidedInputTokens != 1_750 || tenant.AvoidedInputCost != 7*100_000_000 || tenant.Calls != 4 || tenant.MeasuredCalls != 3 || tenant.PricedMeasuredCalls != 2 || tenant.UnpricedCalls != 1 || !tenant.HistoryComplete {
		t.Errorf("tenant = %+v, want 1750 tokens and $7 over 4 calls, 3 measured, 2 of them priced, 1 unpriced, with the history complete", tenant)
	}
	self, err := f.svc.SelfSavings(t.Context(), Caller{PlatformUserID: parminder, Subject: "parminder"}, WindowRequest{})
	if err != nil {
		t.Fatalf("self savings: %v", err)
	}
	if self.AvoidedInputTokens != 1_000 || self.AvoidedInputCost != 5*100_000_000 || self.Calls != 1 || self.PricedMeasuredCalls != 1 || self.Window != tenant.Window {
		t.Errorf("self = %+v, want the caller's 1000 tokens and $5 alone over the tenant's window", self)
	}
	nobody, err := f.svc.SelfSavings(t.Context(), Caller{}, WindowRequest{})
	if err != nil || nobody.UsageSavings != (store.UsageSavings{}) {
		t.Errorf("a caller with no subject = %+v, %v; want zeros", nobody, err)
	}
}
