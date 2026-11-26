// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"errors"
	"testing"
)

func TestDeliver_DropsUndecodableAndUnaddressedEvents(t *testing.T) {
	a := adapterWithAuthorizer(t, &stubAuthorizer{})

	if s := a.deliver(t.Context(), ingressJob{msg: pulledMessage{MessageID: "m1"}, err: errors.New("not json")}); !s.ack {
		t.Errorf("undecodable event settlement = %+v, want ack: redelivering it can never succeed", s)
	}
	unaddressed := ingressJob{msg: pulledMessage{MessageID: "m2"}, ev: inboundEvent{Kind: kindMessage, EventKey: "spaces/S/messages/2"}}
	if s := a.deliver(t.Context(), unaddressed); !s.ack {
		t.Errorf("unaddressed event settlement = %+v, want ack", s)
	}
}

func TestDeliver_TellsSenderOnceRetriesAreExhausted(t *testing.T) {
	chat := newFakeChat()
	a := adapterWithAuthorizer(t, &stubAuthorizer{err: errors.New("identity service down")})
	a.writer = newChatWriter(chat, newSpaceThrottle(0), a.logger)
	job := ingressJob{msg: pulledMessage{MessageID: "m3"}, ev: inboundEvent{
		Kind: kindMessage, Addressed: true, IsDM: true, EventKey: "spaces/DM1/messages/3",
		SpaceName: "spaces/DM1", ThreadName: "spaces/DM1/threads/T1",
		SenderName: "users/1", SenderEmail: "ada@example.com", Text: "hello",
	}}

	attempts := 0
	for {
		attempts++
		s := a.deliver(t.Context(), job)
		if s.ack {
			break
		}
		if s.delay <= 0 {
			t.Fatalf("attempt %d nacked with no backoff", attempts)
		}
		if attempts > maxTransientAttempts {
			t.Fatalf("still redelivering after %d attempts", attempts)
		}
	}
	if attempts != maxTransientAttempts {
		t.Errorf("acked after %d attempts, want the %d-attempt budget", attempts, maxTransientAttempts)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgTemporaryFailure) })

	if s := a.deliver(t.Context(), job); !s.ack {
		t.Errorf("redelivery after exhaustion settlement = %+v, want ack as a duplicate", s)
	}
	_, texts := chat.snapshot()
	notices := 0
	for _, text := range texts {
		if text == msgTemporaryFailure {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("sender told %d times, want once", notices)
	}
}
