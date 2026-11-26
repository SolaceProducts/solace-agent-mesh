// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type atomicSaveObserved struct {
	CheckpointState string
	RowTasks        map[string]string
}

func observeAtomicSave(t *testing.T, store checkpointStore, taskID string, corrIDs ...string) atomicSaveObserved {
	t.Helper()
	got := atomicSaveObserved{RowTasks: map[string]string{}}
	cp, err := store.GetCheckpoint(t.Context(), taskID)
	if err != nil {
		t.Fatalf("GetCheckpoint(%s): %v", taskID, err)
	}
	if cp != nil {
		got.CheckpointState = string(cp.State)
	}
	for _, corrID := range corrIDs {
		row, lookupErr := store.LookupPendingCall(t.Context(), "app", corrID)
		if lookupErr != nil {
			t.Fatalf("LookupPendingCall(%s): %v", corrID, lookupErr)
		}
		if row != nil {
			got.RowTasks[corrID] = row.TaskID
		}
	}
	return got
}

func TestSavingACheckpointWithItsCallsWritesBothTogether(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)

		err := store.SaveCheckpoint(t.Context(), &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			PendingCount: 1, State: []byte(`{"turn":1}`), OwnerID: "owner-a",
			ReplacePendingCalls: true,
			PendingCalls:        []PendingCall{{CorrID: "corr-one", TaskID: taskID, AgentName: "app", ToolCallID: "call-one", Kind: PendingKindPeer}},
		})

		want := atomicSaveObserved{CheckpointState: `{"turn":1}`, RowTasks: map[string]string{"corr-one": taskID}}
		if diff := cmp.Diff(want, observeAtomicSave(t, store, taskID, "corr-one")); err != nil || diff != "" {
			t.Errorf("SaveCheckpoint() = %v, stored (-want +got):\n%s", err, diff)
		}
	})
}

func TestASaveWhoseCallsAreRefusedLeavesNoCheckpoint(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		holder, caller, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)
		saveOwned(t, store, holder, sessID, "owner-holder", 1)
		if err := store.RegisterPendingCalls(t.Context(), holder, "owner-holder", []PendingCall{
			{CorrID: "corr-shared", TaskID: holder, AgentName: "app", ToolCallID: "call-holder", Kind: PendingKindHILApproval},
		}); err != nil {
			t.Fatalf("register the holder's call: %v", err)
		}

		err := store.SaveCheckpoint(t.Context(), &TaskCheckpoint{
			TaskID: caller, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			PendingCount: 2, State: []byte(`{"turn":1}`), OwnerID: "owner-caller",
			ReplacePendingCalls: true,
			PendingCalls: []PendingCall{
				{CorrID: "corr-fresh", TaskID: caller, AgentName: "app", ToolCallID: "call-fresh", Kind: PendingKindPeer},
				{CorrID: "corr-shared", TaskID: caller, AgentName: "app", ToolCallID: "call-clash", Kind: PendingKindHILApproval},
			},
		})

		var taken *CorrelationTakenError
		if !errors.As(err, &taken) || taken.CorrID != "corr-shared" {
			t.Errorf("SaveCheckpoint() = %v, want a CorrelationTakenError for corr-shared", err)
		}
		want := atomicSaveObserved{RowTasks: map[string]string{"corr-shared": holder}}
		if diff := cmp.Diff(want, observeAtomicSave(t, store, caller, "corr-fresh", "corr-shared")); diff != "" {
			t.Errorf("stored after the refused save (-want +got):\n%s", diff)
		}
	})
}

func TestAnUpdateWhoseCallsAreRefusedKeepsTheEarlierCheckpointAndRows(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		holder, caller, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)
		saveOwned(t, store, holder, sessID, "owner-holder", 1)
		saveOwned(t, store, caller, sessID, "owner-caller", 1)
		if err := store.RegisterPendingCalls(t.Context(), holder, "owner-holder", []PendingCall{
			{CorrID: "corr-shared", TaskID: holder, AgentName: "app", ToolCallID: "call-holder", Kind: PendingKindHILApproval},
		}); err != nil {
			t.Fatalf("register the holder's call: %v", err)
		}
		if err := store.RegisterPendingCalls(t.Context(), caller, "owner-caller", []PendingCall{
			{CorrID: "corr-earlier", TaskID: caller, AgentName: "app", ToolCallID: "call-earlier", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register the caller's earlier call: %v", err)
		}

		err := store.UpdateCheckpoint(t.Context(), &TaskCheckpoint{
			TaskID: caller, PendingCount: 1, State: []byte(`{"turn":2}`), OwnerID: "owner-caller",
			ReplacePendingCalls: true,
			PendingCalls:        []PendingCall{{CorrID: "corr-shared", TaskID: caller, AgentName: "app", ToolCallID: "call-clash", Kind: PendingKindHILApproval}},
		})

		var taken *CorrelationTakenError
		if !errors.As(err, &taken) || taken.CorrID != "corr-shared" {
			t.Errorf("UpdateCheckpoint() = %v, want a CorrelationTakenError for corr-shared", err)
		}
		want := atomicSaveObserved{CheckpointState: `{"turn":1}`, RowTasks: map[string]string{"corr-shared": holder, "corr-earlier": caller}}
		if diff := cmp.Diff(want, observeAtomicSave(t, store, caller, "corr-shared", "corr-earlier")); diff != "" {
			t.Errorf("stored after the refused update (-want +got):\n%s", diff)
		}
	})
}
