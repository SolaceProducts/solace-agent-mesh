// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func TestGovernor_FilesWhatSmartContextManagementSparedACallAtItsAverageInputRate(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.cacheBrokenOut.Alias)
	e.AvoidedInputBytes = new(int64(400))

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	if row.AvoidedInputTokens == nil || *row.AvoidedInputTokens != 100 {
		t.Errorf("avoided_input_tokens = %v, want 100", row.AvoidedInputTokens)
	}
	if row.AvoidedInputCost == nil || *row.AvoidedInputCost != 2_070_000 {
		t.Errorf("avoided_input_cost = %v, want 2,070,000 (100 × 20,700 per token)", row.AvoidedInputCost)
	}
}

func TestGovernor_APricedCallWithNoInputTokensPricesItsSavingAtTheInputRate(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)
	e.Tokens = spend.Tokens{Output: 50}
	e.AvoidedInputBytes = new(int64(400))

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	want := r.models.flatFor.Cost(pricing.BucketInput, 100)
	if row.AvoidedInputTokens == nil || *row.AvoidedInputTokens != 100 || row.AvoidedInputCost == nil || *row.AvoidedInputCost != want {
		t.Errorf("avoided = %v tokens, %v cost; want 100 and %d, the input rate", row.AvoidedInputTokens, row.AvoidedInputCost, want)
	}
}

func TestGovernor_AModelWithoutCacheRatesPricesItsSavingAtTheInputRate(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)
	e.AvoidedInputBytes = new(int64(400))

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	want := r.models.flatFor.Cost(pricing.BucketInput, 100)
	if row.AvoidedInputCost == nil || *row.AvoidedInputCost != want {
		t.Errorf("avoided cost = %v, want %d, 100 tokens at the input rate", row.AvoidedInputCost, want)
	}
}

func TestGovernor_AnUnpricedCallFilesItsAvoidedTokensWithNoCost(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.unpriced.Alias)
	e.AvoidedInputBytes = new(int64(400))

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	if row.AvoidedInputTokens == nil || *row.AvoidedInputTokens != 100 || row.AvoidedInputCost != nil {
		t.Errorf("avoided = %v tokens, %v cost; want 100 and NULL", row.AvoidedInputTokens, row.AvoidedInputCost)
	}
}

func TestGovernor_ACountedCallThatSparedNothingFilesZeroAvoidedTokensAndCost(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)
	e.AvoidedInputBytes = new(int64(0))

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	if row.AvoidedInputTokens == nil || *row.AvoidedInputTokens != 0 || row.AvoidedInputCost == nil || *row.AvoidedInputCost != 0 {
		t.Errorf("avoided = %v tokens, %v cost; want 0 and 0", row.AvoidedInputTokens, row.AvoidedInputCost)
	}
}

func TestGovernor_ACallTheAgentDidNotCountFilesNoSavings(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	if row.AvoidedInputTokens != nil || row.AvoidedInputCost != nil {
		t.Errorf("avoided = %v tokens, %v cost; want both NULL", row.AvoidedInputTokens, row.AvoidedInputCost)
	}
}
