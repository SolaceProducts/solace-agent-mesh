// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type fakeConnBroker struct {
	broker.Broker
	state int
}

func (f fakeConnBroker) ConnectionState() int { return f.state }

type fakeNoStateBroker struct {
	broker.Broker
	connected bool
}

func (f fakeNoStateBroker) IsConnected() bool { return f.connected }

func TestInstrumentedBroker_ConnectionStateFallback(t *testing.T) {
	if got := NewInstrumentedBroker(fakeNoStateBroker{connected: true}).(BrokerState).ConnectionState(); got != 1 {
		t.Errorf("connected fallback: got %d, want 1", got)
	}
	if got := NewInstrumentedBroker(fakeNoStateBroker{connected: false}).(BrokerState).ConnectionState(); got != 0 {
		t.Errorf("disconnected fallback: got %d, want 0", got)
	}
}

func TestInstrumentedBroker_ForwardsConnectionState(t *testing.T) {
	wrapped := NewInstrumentedBroker(fakeConnBroker{state: 2})

	bs, ok := wrapped.(BrokerState)
	if !ok {
		t.Fatal("InstrumentedBroker does not satisfy BrokerState — broker.connection.state would be skipped")
	}
	if got := bs.ConnectionState(); got != 2 {
		t.Errorf("ConnectionState()=%d, want 2 forwarded from inner broker", got)
	}
}
