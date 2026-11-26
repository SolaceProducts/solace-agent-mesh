// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import "testing"

func TestSolaceBrokerConnectionState(t *testing.T) {
	b := &SolaceBroker{}

	if got := b.ConnectionState(); got != 2 {
		t.Errorf("fresh broker: ConnectionState()=%d, want 2 (reconnecting)", got)
	}

	b.disconnected.Store(true)
	if got := b.ConnectionState(); got != 0 {
		t.Errorf("disconnected: ConnectionState()=%d, want 0", got)
	}
	b.disconnected.Store(false)

	b.rebuilding.Store(true)
	if got := b.ConnectionState(); got != 2 {
		t.Errorf("rebuilding: ConnectionState()=%d, want 2", got)
	}
	b.rebuilding.Store(false)

	b.rebuildRequested.Store(true)
	if got := b.ConnectionState(); got != 2 {
		t.Errorf("rebuildRequested: ConnectionState()=%d, want 2", got)
	}
}
