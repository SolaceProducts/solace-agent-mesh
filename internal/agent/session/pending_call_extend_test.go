// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestExtendPendingCallOnlyMovesExpiryForward(t *testing.T) {
	eachStore(t, func(t *testing.T, anyStore checkpointStore, seed func(string, string, string)) {
		store := anyStore.(interface {
			checkpointStore
			ExtendPendingCall(ctx context.Context, agentName, corrID string, until time.Time) error
		})
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		base := time.Now().Add(time.Minute).Truncate(time.Millisecond)
		extended, noExpiry := idgen.New(), idgen.New()
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{
			{CorrID: extended, TaskID: taskID, AgentName: "app", Kind: PendingKindPeer, ExpiresAt: &base},
			{CorrID: noExpiry, TaskID: taskID, AgentName: "app", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}

		expiryOf := func(agent, corrID string) *time.Time {
			t.Helper()
			row, err := store.LookupPendingCall(ctx, agent, corrID)
			if err != nil || row == nil {
				t.Fatalf("lookup %s: %v, %v", corrID, row, err)
			}
			return row.ExpiresAt
		}

		later := base.Add(10 * time.Minute)
		if err := store.ExtendPendingCall(ctx, "app", extended, later); err != nil {
			t.Fatalf("extend: %v", err)
		}
		if got := expiryOf("app", extended); got == nil || !got.Equal(later) {
			t.Errorf("expiry = %v, want it moved out to %v", got, later)
		}

		if err := store.ExtendPendingCall(ctx, "app", extended, base); err != nil {
			t.Fatalf("extend backwards: %v", err)
		}
		if got := expiryOf("app", extended); got == nil || !got.Equal(later) {
			t.Errorf("expiry = %v after an earlier extension, want %v kept — greatest must win", got, later)
		}

		if err := store.ExtendPendingCall(ctx, "other-agent", extended, later.Add(time.Hour)); err != nil {
			t.Fatalf("extend as another agent: %v", err)
		}
		if got := expiryOf("app", extended); got == nil || !got.Equal(later) {
			t.Errorf("expiry = %v after another agent's extension, want %v", got, later)
		}

		if err := store.ExtendPendingCall(ctx, "app", noExpiry, later); err != nil {
			t.Fatalf("extend a row with no expiry: %v", err)
		}
		if got := expiryOf("app", noExpiry); got != nil {
			t.Errorf("a row with no expiry was given %v", got)
		}

		if err := store.ExtendPendingCall(ctx, "app", "unknown", later); err != nil {
			t.Errorf("extending an unknown correlation = %v, want nil", err)
		}
	})
}
