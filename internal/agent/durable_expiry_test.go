// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestEveryPendingCallKindGetsADurableExpiry(t *testing.T) {
	const peerBackstop = 30 * time.Second
	const workflowBackstop = 90 * time.Second
	c := &taskCheckpointer{backstops: delegationBackstops{
		peer:     peerBackstop,
		workflow: workflowBackstop,
	}}

	for kind := PendingCallPeer; kind <= highestPendingCallKind; kind++ {
		name, named := pendingCallKindNames[kind]
		if !named {
			t.Errorf("PendingCallKind(%d) has no entry in pendingCallKindNames, so it cannot be persisted at all and every test that iterates that map skips it", int(kind))
			continue
		}
		t.Run(name, func(t *testing.T) {
			withOwn := c.durableExpiry(&PendingToolCall{Kind: kind, Timeout: time.Minute})
			if withOwn <= 0 {
				t.Errorf("a %s pause carrying its own timeout got expiry %v — the correlation row is written with a NULL expiry, so the reaper cannot see it and the task waits forever", name, withOwn)
			}

			withoutOwn := c.durableExpiry(&PendingToolCall{Kind: kind})
			alwaysCarriesItsOwn := kind == PendingCallHILApproval || kind == PendingCallHILAsk || kind == PendingCallAuth ||
				kind == PendingCallRemoteTool || kind == PendingCallToolApproval
			if withoutOwn <= 0 && !alwaysCarriesItsOwn {
				t.Errorf("a %s pause with no timeout of its own got expiry %v and is not on the always-carries-its-own list — add a backstop for it, or add it to the list here and make the pause path guarantee a Timeout", name, withoutOwn)
			}

			switch kind {
			case PendingCallPeer:
				if want := peerBackstop + checkpointExpiryGrace; withoutOwn != want {
					t.Errorf("a peer pause got expiry %v, want %v — it is being given another kind's backstop", withoutOwn, want)
				}
			case PendingCallWorkflow:
				if want := workflowBackstop + checkpointExpiryGrace; withoutOwn != want {
					t.Errorf("a workflow pause got expiry %v, want %v — it is being given another kind's backstop", withoutOwn, want)
				}
			}
		})
	}
}

func TestPendingCallKindNamesMatchTheEnum(t *testing.T) {
	for kind := range pendingCallKindNames {
		if kind < PendingCallPeer || kind > highestPendingCallKind {
			t.Errorf("pendingCallKindNames holds PendingCallKind(%d), outside the enum's range — either the range constant is stale or the entry is dead", int(kind))
		}
	}
	if got, want := len(pendingCallKindNames), int(highestPendingCallKind)+1; got != want {
		t.Errorf("pendingCallKindNames has %d entries for %d kinds", got, want)
	}
}

func TestBoundedResultContentStaysValidUTF8(t *testing.T) {
	msg := strings.Repeat("a", maxToolResultContentBytes-1) + "€" + strings.Repeat("b", 100)
	got := boundedResultContent(msg)

	if !utf8.ValidString(got) {
		t.Error("truncation produced invalid UTF-8 — Postgres rejects the insert, so the result is never recorded and the task cannot resume, while SQLite accepts it and the tests pass")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a truncated message does not say so, so a resumed turn reads a silently clipped tool answer as complete")
	}
	if len(got) > maxToolResultContentBytes+64 {
		t.Errorf("truncated length %d is not near the %d-byte bound", len(got), maxToolResultContentBytes)
	}

	short := "fits fine"
	if boundedResultContent(short) != short {
		t.Error("a message under the bound was altered")
	}
}
