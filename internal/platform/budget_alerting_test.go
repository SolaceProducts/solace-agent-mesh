// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/budgetstate"
)

type fixedAdmins []budgetstate.Recipient

func (a fixedAdmins) BudgetAdmins(context.Context) ([]budgetstate.Recipient, error) { return a, nil }

func TestPlatformCore_BudgetAdminsAreRetriableUntilTheAdminRoutesSetThem(t *testing.T) {
	p := &PlatformCore{}
	if _, err := p.BudgetAdmins(t.Context()); !errors.Is(err, errBudgetAdminsNotReady) {
		t.Fatalf("BudgetAdmins before SetBudgetAdmins: err = %v, want errBudgetAdminsNotReady", err)
	}

	want := budgetstate.Recipient{PlatformUserID: "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f00", LoginID: "admin@example.com"}
	p.SetBudgetAdmins(fixedAdmins{want})
	got, err := p.BudgetAdmins(t.Context())
	if err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("BudgetAdmins = %+v, %v; want the directory's admins", got, err)
	}
}
