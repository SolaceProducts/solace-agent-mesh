// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestStripForgedSchedulerSource(t *testing.T) {
	forged := map[string]any{"source": a2a.SourceScheduler, "keep": "x"}
	stripForgedSchedulerSource(forged)
	if _, ok := forged["source"]; ok {
		t.Error("forged source=scheduler should be stripped")
	}
	if forged["keep"] != "x" {
		t.Error("unrelated metadata must be preserved")
	}

	legit := map[string]any{"source": "builder_test"}
	stripForgedSchedulerSource(legit)
	if legit["source"] != "builder_test" {
		t.Errorf("non-scheduler source = %v, want preserved", legit["source"])
	}

	stripForgedSchedulerSource(nil)
}
