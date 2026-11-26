// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import "testing"

func TestEventQueue_IsExclusiveSoTheLedgerHasOneWriter(t *testing.T) {
	_, opts := EventQueue("ns")

	if !opts.Exclusive || !opts.Named {
		t.Errorf("EventQueue options = %+v, want a named exclusive queue", opts)
	}
}
