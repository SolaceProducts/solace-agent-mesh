// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
)

type listenerCapturingService struct {
	solace.MessagingService
	attempt solace.ReconnectionAttemptListener
}

func (s *listenerCapturingService) AddReconnectionAttemptListener(l solace.ReconnectionAttemptListener) uint64 {
	s.attempt = l
	return 1
}

func (s *listenerCapturingService) AddReconnectionListener(solace.ReconnectionListener) uint64 {
	return 2
}

func (s *listenerCapturingService) AddServiceInterruptionListener(solace.ServiceInterruptionListener) uint64 {
	return 3
}

type reconnectEvent struct{}

func (reconnectEvent) GetTimestamp() time.Time { return time.Now() }
func (reconnectEvent) GetBrokerURI() string    { return "tcp://broker:55555" }
func (reconnectEvent) GetMessage() string      { return "reconnecting" }
func (reconnectEvent) GetCause() error         { return nil }

func TestSolaceBroker_ReconnectionAttemptReportsStandby(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	svc := &listenerCapturingService{}

	var got []BindState
	if _, err := b.CreateQueue("events-q", QueueOptions{
		Named:       true,
		Exclusive:   true,
		OnBindState: func(s BindState) { got = append(got, s) },
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := b.CreateQueue("plain-q", QueueOptions{Named: true}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	b.mu.Lock()
	b.registerLifecycleListenersLocked(svc)
	b.mu.Unlock()
	if svc.attempt == nil {
		t.Fatal("no reconnection-attempt listener registered")
	}

	svc.attempt(reconnectEvent{})
	if len(got) != 1 || got[0] != BindStandby {
		t.Fatalf("watcher saw %v, want [standby]", got)
	}

	if err := b.DeleteQueue("events-q"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	svc.attempt(reconnectEvent{})
	if len(got) != 1 {
		t.Errorf("a deleted queue's watcher must not be told anything, saw %v", got)
	}
}
