// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"sync"
	"testing"
)

func TestBindStateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state BindState
		want  string
	}{
		{"active", BindActive, "active"},
		{"standby", BindStandby, "standby"},
		{"zero value is active", BindState(0), "active"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.state.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDevBrokerCreateQueueReportsActive(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var reported []BindState

	b := NewDevBroker()
	_, err := b.CreateQueue("bind-state-q", QueueOptions{
		Named: true,
		OnBindState: func(s BindState) {
			mu.Lock()
			defer mu.Unlock()
			reported = append(reported, s)
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 {
		t.Fatalf("OnBindState called %d times, want 1", len(reported))
	}
	if reported[0] != BindActive {
		t.Errorf("reported %v, want %v", reported[0], BindActive)
	}
}

func TestDevBrokerCreateQueueDuplicateDoesNotReport(t *testing.T) {
	t.Parallel()

	b := NewDevBroker()
	if _, err := b.CreateQueue("dup-bind-state-q", QueueOptions{Named: true}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	called := false
	_, err := b.CreateQueue("dup-bind-state-q", QueueOptions{
		Named:       true,
		OnBindState: func(BindState) { called = true },
	})
	if err != nil {
		t.Fatalf("CreateQueue duplicate: %v", err)
	}
	if called {
		t.Error("OnBindState called on a duplicate CreateQueue; the Solace broker never calls it")
	}
}

func TestDevBrokerCreateQueueDirectDoesNotReport(t *testing.T) {
	t.Parallel()

	b := NewDevBroker()
	called := false
	_, err := b.CreateQueue("direct-bind-state-q", QueueOptions{
		OnBindState: func(BindState) { called = true },
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if called {
		t.Error("OnBindState called for a direct queue; the Solace broker never calls it")
	}
}

func TestNetworkDevBrokerCreateQueueReportsActiveOnce(t *testing.T) {
	t.Parallel()

	c := NewNetworkDevBroker("127.0.0.1:0", "bind-state-client", "bind-state-default", testLogger())

	var reported []BindState
	record := func(s BindState) { reported = append(reported, s) }

	if _, err := c.CreateQueue("net-bind-state-q", QueueOptions{Named: true, OnBindState: record}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if _, err := c.CreateQueue("net-bind-state-q", QueueOptions{Named: true, OnBindState: record}); err != nil {
		t.Fatalf("CreateQueue duplicate: %v", err)
	}
	if _, err := c.CreateQueue("net-direct-q", QueueOptions{OnBindState: record}); err != nil {
		t.Fatalf("CreateQueue direct: %v", err)
	}

	if len(reported) != 1 || reported[0] != BindActive {
		t.Fatalf("reported %v, want exactly one %v from the creating named call", reported, BindActive)
	}
}

func TestDevBrokerCreateQueueNilOnBindState(t *testing.T) {
	t.Parallel()

	b := NewDevBroker()
	if _, err := b.CreateQueue("nil-bind-state-q", QueueOptions{Named: true}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
}
