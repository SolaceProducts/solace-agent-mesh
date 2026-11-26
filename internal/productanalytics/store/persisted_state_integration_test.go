// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store_test

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics/store"
)

func newState(t *testing.T) (*productanalytics.PersistedState, *store.DB) {
	t.Helper()
	db := store.NewTestDB(t)
	return productanalytics.NewPersistedState(store.NewProductAnalyticsRepo(db), nil), db
}

func TestPersistedStateOverRealRepoRotatesTheMarkerAcrossAWithdrawal(t *testing.T) {
	state, _ := newState(t)
	ctx := context.Background()

	before := state.Identity(ctx).Marker
	if !store.IsValidInstallID(before) {
		t.Fatalf("marker for a collecting install = %q, want a UUID", before)
	}

	if err := state.SetConsent(ctx, productanalytics.ConsentDenied); err != nil {
		t.Fatal(err)
	}
	if got := state.Identity(ctx).Marker; got != "" {
		t.Errorf("marker while denied = %q, want empty", got)
	}

	if err := state.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatal(err)
	}
	after := state.Identity(ctx).Marker
	if !store.IsValidInstallID(after) {
		t.Fatalf("marker after opting back in = %q, want a UUID", after)
	}
	if after == before {
		t.Errorf("marker after opting back in = %q, want a fresh one — the two collection periods are linkable", after)
	}
}

func TestPersistedStateOverRealRepoKeepsTheMarkerWhenCollectionStaysOn(t *testing.T) {
	state, db := newState(t)
	ctx := context.Background()

	held := state.Identity(ctx).Marker
	if !store.IsValidInstallID(held) {
		t.Fatalf("marker for a collecting install = %q, want a UUID", held)
	}

	if err := state.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatal(err)
	}

	row, err := store.NewProductAnalyticsRepo(db).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil || row.InstallID != held {
		t.Fatalf("stored marker = %v, want the held %q", row, held)
	}
	if got := state.Identity(ctx).Marker; got != held {
		t.Errorf("marker after recording a decision = %q, want the held %q", got, held)
	}
}
