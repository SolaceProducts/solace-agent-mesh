// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import "testing"

func TestDevBrokerConnectionState(t *testing.T) {
	b := &DevBroker{}
	if got := b.ConnectionState(); got != 0 {
		t.Errorf("disconnected: got %d, want 0", got)
	}
	b.connected = true
	if got := b.ConnectionState(); got != 1 {
		t.Errorf("connected: got %d, want 1", got)
	}
}

func TestNetworkDevBrokerConnectionState(t *testing.T) {
	c := &NetworkDevBroker{}
	if got := c.ConnectionState(); got != 0 {
		t.Errorf("disconnected: got %d, want 0", got)
	}
	c.connected.Store(true)
	if got := c.ConnectionState(); got != 1 {
		t.Errorf("connected: got %d, want 1", got)
	}
}
