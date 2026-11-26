// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestRecordToolResultConsumesTheCorrelationAndRefusesASecondCaller(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
			AgentName: "worker", PendingCount: 1,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
			CorrID: "corr-1", TaskID: taskID,
			AgentName: "worker", ToolCallID: "tc-a", Kind: PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register pending call: %v", err)
		}

		remaining, recErr := store.RecordToolResult(ctx, taskID, "corr-1", &ToolCallResult{ToolCallID: "tc-a", Content: "answer"})
		if recErr != nil {
			t.Fatalf("first record: %v", recErr)
		}
		if remaining != 0 {
			t.Fatalf("pending after the only result = %d, want 0", remaining)
		}
		if row, lookupErr := store.LookupPendingCall(ctx, "worker", "corr-1"); lookupErr != nil || row != nil {
			t.Errorf("correlation row still resolvable after its result was recorded (%v, %v) — a duplicate reply, or another replica holding the same fan-out, records it again", row, lookupErr)
		}

		if _, err := store.RecordToolResult(ctx, taskID, "corr-1", &ToolCallResult{ToolCallID: "tc-a", Content: "duplicate"}); !errors.Is(err, ErrCallNotOutstanding) {
			t.Errorf("second caller for the same correlation got err = %v, want ErrCallNotOutstanding", err)
		}

		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
			AgentName: "worker", PendingCount: 1,
		}); err != nil {
			t.Fatalf("re-save checkpoint: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{{
			CorrID: "corr-2", TaskID: taskID,
			AgentName: "worker", ToolCallID: "tc-b", Kind: PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register second pending call: %v", err)
		}

		if _, err := store.RecordToolResult(ctx, taskID, "corr-1", &ToolCallResult{ToolCallID: "tc-a", Content: "very late duplicate"}); !errors.Is(err, ErrCallNotOutstanding) {
			t.Fatalf("a duplicate from the previous turn got err = %v, want ErrCallNotOutstanding", err)
		}
		cp, readErr := store.GetCheckpoint(ctx, taskID)
		if readErr != nil {
			t.Fatalf("read back: %v", readErr)
		}
		if cp.PendingCount != 1 {
			t.Errorf("pending count = %d, want 1 — a duplicate from the previous turn decremented this turn's outstanding call, so it resumes without its answer", cp.PendingCount)
		}
		if len(cp.ToolResults) != 0 {
			t.Errorf("got %d banked results, want 0 — a stale tool_result for a tool_use id absent from this turn is rejected by the LLM APIs: %+v", len(cp.ToolResults), cp.ToolResults)
		}
	})
}

func TestRegisterPendingCallsNeverTakesACorrelationFromAnotherTask(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		victim, intruder, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)
		const (
			victimOwner   = "owner-victim"
			intruderOwner = "owner-intruder"
			shared        = "corr-shared"
			intruderOwn   = "corr-intruder-own"
		)
		saveOwned(t, store, victim, sessID, victimOwner, 1)
		saveOwned(t, store, intruder, sessID, intruderOwner, 2)
		if err := store.RegisterPendingCalls(ctx, victim, victimOwner, []PendingCall{
			{CorrID: shared, TaskID: victim, AgentName: "app", ToolCallID: "call-victim", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register the victim's call: %v", err)
		}

		err := store.RegisterPendingCalls(ctx, intruder, intruderOwner, []PendingCall{
			{CorrID: intruderOwn, TaskID: intruder, AgentName: "app", ToolCallID: "call-own", Kind: PendingKindPeer},
			{CorrID: shared, TaskID: intruder, AgentName: "app", ToolCallID: "call-intruder", Kind: PendingKindHILApproval},
		})

		var taken *CorrelationTakenError
		if !errors.As(err, &taken) || taken.CorrID != shared {
			t.Fatalf("registering a correlation another task holds = %v, want a CorrelationTakenError for %s", err, shared)
		}
		type row struct {
			TaskID     string
			ToolCallID string
			Kind       string
		}
		got := map[string]row{}
		for _, corrID := range []string{shared, intruderOwn} {
			found, lookupErr := store.LookupPendingCall(ctx, "app", corrID)
			if lookupErr != nil {
				t.Fatalf("lookup %s: %v", corrID, lookupErr)
			}
			if found != nil {
				got[corrID] = row{TaskID: found.TaskID, ToolCallID: found.ToolCallID, Kind: found.Kind}
			}
		}
		want := map[string]row{shared: {TaskID: victim, ToolCallID: "call-victim", Kind: PendingKindPeer}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("rows after the refused registration (-want +got):\n%s", diff)
		}

		if err := store.RegisterPendingCalls(ctx, victim, victimOwner, []PendingCall{
			{CorrID: shared, TaskID: victim, AgentName: "app", ToolCallID: "call-victim", Kind: PendingKindPeer},
		}); err != nil {
			t.Errorf("the owning task registering its own correlation again = %v, want it accepted", err)
		}
	})
}

func TestPendingCallTaskNamesTheTaskHoldingACorrelationWhateverAgentRegisteredIt(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		holder, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		saveOwned(t, store, holder, sessID, "owner-holder", 1)
		if err := store.RegisterPendingCalls(ctx, holder, "owner-holder", []PendingCall{
			{CorrID: "corr-held", TaskID: holder, AgentName: "another-agent", ToolCallID: "call-held", Kind: PendingKindHILApproval},
		}); err != nil {
			t.Fatalf("register the call: %v", err)
		}
		type answer struct {
			TaskID string
			Held   bool
		}

		tests := []struct {
			corrID string
			want   answer
		}{
			{corrID: "corr-held", want: answer{TaskID: holder, Held: true}},
			{corrID: "corr-nobody", want: answer{}},
		}
		for _, tt := range tests {
			taskID, held, err := store.PendingCallTask(ctx, tt.corrID)
			if err != nil {
				t.Fatalf("PendingCallTask(%s): %v", tt.corrID, err)
			}
			if diff := cmp.Diff(tt.want, answer{TaskID: taskID, Held: held}); diff != "" {
				t.Errorf("PendingCallTask(%s) (-want +got):\n%s", tt.corrID, diff)
			}
		}
	})
}
