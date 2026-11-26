// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"testing"
)

func TestMessageAck(t *testing.T) {
	called := false
	m := &Message{}
	m.Ack()

	m.SetAckHandlers(func() { called = true }, nil)
	m.Ack()
	if !called {
		t.Error("ack handler should have been called")
	}
}

func TestMessageNack(t *testing.T) {
	called := false
	m := &Message{}
	m.Nack()

	m.SetAckHandlers(nil, func() { called = true })
	m.Nack()
	if !called {
		t.Error("nack handler should have been called")
	}
}

func TestMessageSetAckHandlers(t *testing.T) {
	ackCalled := false
	nackCalled := false
	m := &Message{}
	m.SetAckHandlers(
		func() { ackCalled = true },
		func() { nackCalled = true },
	)
	m.Ack()
	m.Nack()
	if !ackCalled || !nackCalled {
		t.Error("both handlers should have been called")
	}
}

func TestMessageUserData(t *testing.T) {
	m := &Message{}

	if v := m.GetUserData("key"); v != nil {
		t.Errorf("expected nil, got %v", v)
	}

	m.SetUserData("key", "value")
	if v := m.GetUserData("key"); v != "value" {
		t.Errorf("expected 'value', got %v", v)
	}

	m.SetUserData("key", 42)
	if v := m.GetUserData("key"); v != 42 {
		t.Errorf("expected 42, got %v", v)
	}

	if v := m.GetUserData("missing"); v != nil {
		t.Errorf("expected nil, got %v", v)
	}
}
