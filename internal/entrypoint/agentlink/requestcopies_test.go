// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"testing"
	"time"
)

func nothingSettled(string) bool { return false }

func TestTheTwoCopiesOfARequestMatchInEitherOrder(t *testing.T) {
	now := time.Now()
	a, b := copyKeyOf([]byte("a")), copyKeyOf([]byte("b"))
	rc := newRequestCopies(maxTrackedCopies)
	if !rc.seen(a, "", now) {
		t.Fatal("a copy seen before its take was not waiting")
	}
	rc.took(a, false, now)
	rc.took(b, false, now)
	if rc.seen(b, "", now) {
		t.Fatal("a copy seen after its take was waiting")
	}
	if rc.leftWaiting(nothingSettled) {
		t.Fatal("work left behind after both requests were taken")
	}
}

func TestEachCopyOfIdenticalRequestsNeedsATakeOfItsOwn(t *testing.T) {
	now := time.Now()
	a := copyKeyOf([]byte("a"))
	rc := newRequestCopies(maxTrackedCopies)
	rc.seen(a, "", now)
	rc.seen(a, "", now)
	rc.took(a, false, now)
	if !rc.leftWaiting(nothingSettled) {
		t.Fatal("the second of two identical requests was not waiting")
	}
	rc.took(a, false, now)
	if rc.leftWaiting(nothingSettled) {
		t.Fatal("work left behind after both were taken")
	}
}

func TestARequestGivenBackWaitsUntilItIsTakenAgain(t *testing.T) {
	now := time.Now()
	a := copyKeyOf([]byte("a"))
	rc := newRequestCopies(maxTrackedCopies)
	rc.took(a, false, now)
	rc.gaveBack(a, now)
	if !rc.leftWaiting(nothingSettled) {
		t.Fatal("a request given back was not waiting")
	}
	rc.took(a, true, now)
	if rc.leftWaiting(nothingSettled) {
		t.Fatal("a request given back and taken again was still waiting")
	}
	if rc.seen(a, "", now) {
		t.Fatal("the late copy of a request taken was waiting")
	}
}

func TestARedeliveredTakeIsNotKeptToMatchACopyStillToCome(t *testing.T) {
	now := time.Now()
	a := copyKeyOf([]byte("a"))
	rc := newRequestCopies(maxTrackedCopies)
	rc.took(a, true, now)
	if !rc.seen(a, "", now) || !rc.leftWaiting(nothingSettled) {
		t.Fatal("a redelivered take matched a copy that came after it")
	}
}

func TestATakeKeptPastTheMatchWindowLetsItsLateCopyCountAsWaiting(t *testing.T) {
	now := time.Now()
	a := copyKeyOf([]byte("a"))
	rc := newRequestCopies(maxTrackedCopies)
	rc.took(a, false, now)
	rc.expire(now.Add(copyMatchWindow - time.Second))
	rc.expire(now.Add(copyMatchWindow + time.Second))
	if !rc.seen(a, "", now) || !rc.leftWaiting(nothingSettled) {
		t.Fatal("a copy arriving after its take expired was not waiting")
	}
}

func TestARequestWaitingPastItsAgeStillCountsAsWorkLeftBehind(t *testing.T) {
	now := time.Now()
	a := copyKeyOf([]byte("a"))
	rc := newRequestCopies(maxTrackedCopies)
	rc.seen(a, "", now)
	rc.expire(now.Add(waitingCopyAge + time.Second))
	if len(rc.waiting) != 0 {
		t.Fatalf("%d copies still kept past their age", len(rc.waiting))
	}
	rc.took(a, false, now)
	if !rc.leftWaiting(nothingSettled) {
		t.Fatal("a request let go of by age was not work left behind")
	}
	rc.forgetWaiting()
	if rc.leftWaiting(nothingSettled) {
		t.Fatal("work left behind again after the decision that accounted for it")
	}
}

func TestAtTheLimitTheOldestTakeIsLetGoAndItsLateCopyCountsAsWaiting(t *testing.T) {
	now := time.Now()
	keys := []copyKey{copyKeyOf([]byte("a")), copyKeyOf([]byte("b")), copyKeyOf([]byte("c"))}
	rc := newRequestCopies(2)
	for i, k := range keys {
		rc.took(k, false, now.Add(time.Duration(i)*time.Second))
	}
	if len(rc.taken) != 2 {
		t.Fatalf("%d takes kept at a limit of 2", len(rc.taken))
	}
	if !rc.seen(keys[0], "", now) {
		t.Fatal("the late copy of the take let go of was not waiting")
	}
	if rc.seen(keys[1], "", now) || rc.seen(keys[2], "", now) {
		t.Fatal("a late copy of a take kept was waiting")
	}
}

func TestAtTheLimitAWaitingCopyLetGoStillCountsAsWorkLeftBehind(t *testing.T) {
	now := time.Now()
	keys := []copyKey{copyKeyOf([]byte("a")), copyKeyOf([]byte("b")), copyKeyOf([]byte("c"))}
	rc := newRequestCopies(2)
	for i, k := range keys {
		rc.seen(k, "", now.Add(time.Duration(i)*time.Second))
	}
	if len(rc.waiting) != 2 {
		t.Fatalf("%d copies kept at a limit of 2", len(rc.waiting))
	}
	rc.took(keys[1], false, now)
	rc.took(keys[2], false, now)
	if !rc.leftWaiting(nothingSettled) {
		t.Fatal("the copy let go of at the limit was not work left behind")
	}
}

func TestACancelIsWorkLeftBehindOnlyUntilItsEffectIsHad(t *testing.T) {
	now := time.Now()
	rc := newRequestCopies(maxTrackedCopies)
	rc.seen(copyKeyOf([]byte("cancel T1")), "T1", now)
	if rc.leftWaiting(func(taskID string) bool { return taskID == "T1" }) {
		t.Fatal("a cancel already applied was work left behind")
	}
	if !rc.leftWaiting(nothingSettled) {
		t.Fatal("a cancel not yet applied was not work left behind")
	}
	rc.seen(copyKeyOf([]byte("task T2")), "", now)
	if !rc.leftWaiting(func(taskID string) bool { return taskID == "T1" }) {
		t.Fatal("a task waiting behind an applied cancel was not work left behind")
	}
}

func TestADecisionKeepsTheTakesWaitingForTheirCopies(t *testing.T) {
	now := time.Now()
	a := copyKeyOf([]byte("a"))
	rc := newRequestCopies(maxTrackedCopies)
	rc.took(a, false, now)
	rc.forgetWaiting()
	if rc.seen(a, "", now) {
		t.Fatal("the copy of a request taken before the decision was waiting")
	}
}

func TestTheWakeWatcherReadsTheTaskACancelNames(t *testing.T) {
	if got := cancelTarget(cancelRequest("T1")); got != "T1" {
		t.Fatalf("cancel names %q", got)
	}
	for name, payload := range map[string][]byte{
		"task":       request("T1"),
		"unreadable": []byte("not a request"),
		"too large":  append(cancelRequest("T1"), make([]byte, maxCancelBytes)...),
	} {
		if got := cancelTarget(payload); got != "" {
			t.Errorf("%s: read as a cancel of %q", name, got)
		}
	}
}
