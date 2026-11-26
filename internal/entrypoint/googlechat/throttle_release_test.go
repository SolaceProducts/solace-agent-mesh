// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSpaceThrottleReleasesTheSlotOfAWaiterThatGaveUp(t *testing.T) {
	const interval = 300 * time.Millisecond
	th := newSpaceThrottle(interval)
	if err := th.wait(t.Context(), "spaces/A"); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := th.wait(short, "spaces/A"); err == nil {
		t.Fatal("the waiter did not give up")
	}

	start := time.Now()
	if err := th.wait(t.Context(), "spaces/A"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > interval+interval/2 {
		t.Errorf("the next write waited %v, want about one interval %v: the abandoned slot was not given back", took, interval)
	}
}

func TestSpaceThrottleKeepsTheSlotsAfterAnAbandonedOne(t *testing.T) {
	const interval = 200 * time.Millisecond
	th := newSpaceThrottle(interval)
	_ = th.wait(t.Context(), "spaces/A")
	short, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = th.wait(short, "spaces/A")
		close(done)
	}()
	time.Sleep(5 * time.Millisecond)
	go func() { _ = th.wait(t.Context(), "spaces/A") }()
	<-done
	time.Sleep(5 * time.Millisecond)

	start := time.Now()
	_ = th.wait(t.Context(), "spaces/A")
	if took := time.Since(start); took < interval {
		t.Errorf("the next write waited %v, want it behind the waiter that did not give up", took)
	}
}

func TestUpdateOnceSaysWhenItTimedOutWaitingForASlot(t *testing.T) {
	chat := newFakeChat()
	w := newChatWriter(chat, newSpaceThrottle(time.Minute), nil)
	_ = w.throttle.wait(t.Context(), "spaces/A")
	short, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := w.updateOnce(short, "spaces/A", "spaces/A/messages/m", messageContent{}, true)
	if err == nil || !strings.Contains(err.Error(), "waiting for the space's write slot") {
		t.Errorf("err = %v, want it to say it was waiting for the space's write slot", err)
	}
}
