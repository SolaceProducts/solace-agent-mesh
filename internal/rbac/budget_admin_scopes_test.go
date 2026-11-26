// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"slices"
	"testing"
)

func TestBudgetAdminScopes_ACallerChangingTheResultDoesNotChangeWhatLaterCallersGet(t *testing.T) {
	want := slices.Clone(BudgetAdminScopes())
	first := BudgetAdminScopes()
	first[0] = "tool:*:*"

	if got := BudgetAdminScopes(); !slices.Equal(got, want) {
		t.Errorf("BudgetAdminScopes() = %v after a caller changed its earlier result, want %v", got, want)
	}
}
