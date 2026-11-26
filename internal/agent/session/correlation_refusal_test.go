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

func TestARefusedRegistrationLeavesTheCallersEarlierRowsAndTheHoldersRowIntact(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		holder, caller, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)
		const (
			holderOwner = "owner-holder"
			callerOwner = "owner-caller"
			shared      = "corr-shared"
			earlier     = "corr-caller-earlier"
			fresh       = "corr-caller-fresh"
		)
		saveOwned(t, store, holder, sessID, holderOwner, 1)
		saveOwned(t, store, caller, sessID, callerOwner, 2)
		if err := store.RegisterPendingCalls(ctx, holder, holderOwner, []PendingCall{
			{CorrID: shared, TaskID: holder, AgentName: "app", ToolCallID: "call-holder", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register the holder's call: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, caller, callerOwner, []PendingCall{
			{CorrID: earlier, TaskID: caller, AgentName: "app", ToolCallID: "call-earlier", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register the caller's earlier call: %v", err)
		}

		err := store.RegisterPendingCalls(ctx, caller, callerOwner, []PendingCall{
			{CorrID: fresh, TaskID: caller, AgentName: "app", ToolCallID: "call-fresh", Kind: PendingKindPeer},
			{CorrID: shared, TaskID: caller, AgentName: "app", ToolCallID: "call-clash", Kind: PendingKindHILApproval},
		})

		var taken *CorrelationTakenError
		if !errors.As(err, &taken) || taken.CorrID != shared {
			t.Fatalf("RegisterPendingCalls() = %v, want a CorrelationTakenError for %s", err, shared)
		}
		got := map[string]string{}
		for _, corrID := range []string{shared, earlier, fresh} {
			found, lookupErr := store.LookupPendingCall(ctx, "app", corrID)
			if lookupErr != nil {
				t.Fatalf("lookup %s: %v", corrID, lookupErr)
			}
			if found != nil {
				got[corrID] = found.TaskID + "/" + found.ToolCallID
			}
		}
		want := map[string]string{
			shared:  holder + "/call-holder",
			earlier: caller + "/call-earlier",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("rows after the refused registration (-want +got):\n%s", diff)
		}
	})
}

func TestRegisteringACorrelationAnotherAgentsTaskHoldsIsRefused(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		holder, caller, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)
		const shared = "corr-held-by-another-agent"
		saveOwned(t, store, holder, sessID, "owner-holder", 1)
		saveOwned(t, store, caller, sessID, "owner-caller", 1)
		if err := store.RegisterPendingCalls(ctx, holder, "owner-holder", []PendingCall{
			{CorrID: shared, TaskID: holder, AgentName: "another-agent", ToolCallID: "call-holder", Kind: PendingKindHILApproval},
		}); err != nil {
			t.Fatalf("register the holder's call: %v", err)
		}

		err := store.RegisterPendingCalls(ctx, caller, "owner-caller", []PendingCall{
			{CorrID: shared, TaskID: caller, AgentName: "app", ToolCallID: "call-clash", Kind: PendingKindHILApproval},
		})

		var taken *CorrelationTakenError
		if !errors.As(err, &taken) || taken.CorrID != shared {
			t.Errorf("RegisterPendingCalls() = %v, want a CorrelationTakenError for %s", err, shared)
		}
		if taskID, held, lookupErr := store.PendingCallTask(ctx, shared); lookupErr != nil || !held || taskID != holder {
			t.Errorf("PendingCallTask(%s) = %q, %v, %v, want the holder %q still holding it", shared, taskID, held, lookupErr, holder)
		}
	})
}
