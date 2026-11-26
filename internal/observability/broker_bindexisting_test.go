// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type fakeBinderBroker struct {
	broker.Broker
	bound    *string
	consumed *string
}

func (f fakeBinderBroker) BindExisting(name string) (broker.BoundQueue, error) {
	*f.bound = name
	return nil, nil
}

func (f fakeBinderBroker) ConsumeExisting(name string) (broker.Queue, error) {
	*f.consumed = name
	return nil, nil
}

func TestInstrumentedBroker_ForwardsBindExisting(t *testing.T) {
	var bound, consumed string
	wrapped := NewInstrumentedBroker(fakeBinderBroker{bound: &bound, consumed: &consumed})

	binder, ok := wrapped.(broker.ExistingQueueBinder)
	if !ok {
		t.Fatal("InstrumentedBroker does not satisfy broker.ExistingQueueBinder")
	}
	if _, err := binder.BindExisting("ns/q/old"); err != nil {
		t.Fatalf("BindExisting: %v", err)
	}
	if bound != "ns/q/old" {
		t.Errorf("inner broker bound %q, want ns/q/old", bound)
	}
}

func TestInstrumentedBroker_BindExistingUnsupported(t *testing.T) {
	wrapped := NewInstrumentedBroker(fakeNoStateBroker{}).(broker.ExistingQueueBinder)
	if _, err := wrapped.BindExisting("ns/q/old"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("BindExisting err = %v, want errors.ErrUnsupported", err)
	}
}

func TestInstrumentedBroker_ForwardsConsumeExisting(t *testing.T) {
	var bound, consumed string
	wrapped := NewInstrumentedBroker(fakeBinderBroker{bound: &bound, consumed: &consumed})

	binder, ok := wrapped.(broker.ExistingQueueBinder)
	if !ok {
		t.Fatal("InstrumentedBroker does not satisfy broker.ExistingQueueBinder")
	}
	if _, err := binder.ConsumeExisting("preprov/q/x"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	if consumed != "preprov/q/x" {
		t.Errorf("inner broker consumed %q, want preprov/q/x", consumed)
	}
}

func TestInstrumentedBroker_ConsumeExistingUnsupported(t *testing.T) {
	wrapped := NewInstrumentedBroker(fakeNoStateBroker{}).(broker.ExistingQueueBinder)
	if _, err := wrapped.ConsumeExisting("preprov/q/x"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ConsumeExisting err = %v, want errors.ErrUnsupported", err)
	}
}
