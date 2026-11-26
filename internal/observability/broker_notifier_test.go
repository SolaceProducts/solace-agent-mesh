// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type notifyingBroker struct {
	broker.Broker
	reconnect []func()
	lost      []func()
}

func (b *notifyingBroker) OnReconnect(fn func())      { b.reconnect = append(b.reconnect, fn) }
func (b *notifyingBroker) OnConnectionLost(fn func()) { b.lost = append(b.lost, fn) }

func TestInstrumentedBroker_ForwardsConnectionCallbacks(t *testing.T) {
	inner := &notifyingBroker{}
	wrapped := NewInstrumentedBroker(inner)

	wrapped.(broker.ReconnectNotifier).OnReconnect(func() {})
	wrapped.(broker.ConnectionLossNotifier).OnConnectionLost(func() {})

	if len(inner.reconnect) != 1 || len(inner.lost) != 1 {
		t.Fatalf("inner got %d reconnect and %d lost callbacks, want 1 and 1", len(inner.reconnect), len(inner.lost))
	}
}

func TestInstrumentedBroker_CallbacksWithoutCapabilityAreIgnored(t *testing.T) {
	wrapped := NewInstrumentedBroker(broker.NewDevBroker())

	wrapped.(broker.ReconnectNotifier).OnReconnect(func() {})
	wrapped.(broker.ConnectionLossNotifier).OnConnectionLost(func() {})
}
