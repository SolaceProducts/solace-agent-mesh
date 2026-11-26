// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"bytes"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func saveOwned(t *testing.T, store checkpointStore, taskID, sessID, owner string, pending int) {
	t.Helper()
	lease := time.Now().Add(5 * time.Minute)
	if err := store.SaveCheckpoint(t.Context(), &TaskCheckpoint{
		TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
		PendingCount: pending, State: []byte(`{"turn":1}`), OwnerID: owner, LeaseExpiresAt: &lease,
	}); err != nil {
		t.Fatalf("save %s as %s: %v", taskID, owner, err)
	}
}

func TestCheckpointWritesRefuseARowAnotherOwnerHolds(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := t.Context()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		saveOwned(t, store, taskID, sessID, "owner-a", 1)
		silence(t, store, taskID, testTakeoverWindow)
		if err := store.RegisterPendingCalls(ctx, taskID, "owner-a", nil); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := store.UpdateCheckpoint(ctx, &TaskCheckpoint{TaskID: taskID, OwnerID: "owner-a", State: []byte(`{}`)}); err != nil {
			t.Fatalf("update: %v", err)
		}
		silence(t, store, taskID, testTakeoverWindow)
		if cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-b", time.Now(), time.Minute, testTakeoverWindow, false); err != nil || cp == nil {
			t.Fatalf("adopt by owner-b = %v, %v; want the row", cp, err)
		}

		late := &TaskCheckpoint{TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			State: []byte(`{"turn":"late"}`), OwnerID: "owner-a"}
		if err := store.SaveCheckpoint(ctx, late); !errors.Is(err, ErrNotOwner) {
			t.Errorf("late SaveCheckpoint = %v, want ErrNotOwner", err)
		}
		if err := store.UpdateCheckpoint(ctx, late); !errors.Is(err, ErrNotOwner) {
			t.Errorf("late UpdateCheckpoint = %v, want ErrNotOwner", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, "owner-a", []PendingCall{{
			CorrID: "late-corr", TaskID: taskID, AgentName: "app", ToolCallID: "tc", Kind: PendingKindPeer,
		}}); !errors.Is(err, ErrNotOwner) {
			t.Errorf("late RegisterPendingCalls = %v, want ErrNotOwner", err)
		}

		cp, err := store.GetCheckpoint(ctx, taskID)
		if err != nil || cp == nil {
			t.Fatalf("read back: %v, %v", cp, err)
		}
		if cp.OwnerID != "owner-b" || string(cp.State) == `{"turn":"late"}` {
			t.Errorf("row = owner %q state %s; the late process wrote over its successor", cp.OwnerID, cp.State)
		}
		if row, _ := store.LookupPendingCall(ctx, "app", "late-corr"); row != nil {
			t.Error("the late process registered a correlation on a task it no longer holds")
		}
	})
}

func TestUpdateCheckpointDoesNotRecreateAFinishedTask(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := t.Context()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		saveOwned(t, store, taskID, sessID, "owner-a", 0)
		if err := store.DeleteCheckpoint(ctx, taskID); err != nil {
			t.Fatalf("delete: %v", err)
		}

		err := store.UpdateCheckpoint(ctx, &TaskCheckpoint{TaskID: taskID, OwnerID: "owner-a", State: []byte(`{}`)})
		if !errors.Is(err, ErrNotOwner) {
			t.Errorf("UpdateCheckpoint on a deleted row = %v, want ErrNotOwner", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, "owner-a", nil); !errors.Is(err, ErrNotOwner) {
			t.Errorf("RegisterPendingCalls on a deleted row = %v, want ErrNotOwner", err)
		}
		if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
			t.Error("a late save recreated a task that finished elsewhere")
		}
	})
}

func TestTakeoverWaitsForTheOwnersSilenceOnTheStoreClock(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := t.Context()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		lapsed := time.Now().Add(-time.Hour)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			OwnerID: "owner-a", LeaseExpiresAt: &lapsed,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}

		farAhead := time.Now().Add(time.Hour)
		silence(t, store, taskID, testTakeoverWindow-5*time.Second)
		if cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-b", farAhead, time.Minute, testTakeoverWindow, false); err != nil || cp != nil {
			t.Fatalf("adopted before the owner was silent for the window: %v, %v", cp, err)
		}
		if listed, err := store.ListResumableCheckpoints(ctx, "app", farAhead, testTakeoverWindow, 10, false); err != nil || len(listed) != 0 {
			t.Fatalf("swept before the owner was silent for the window: %v, %v", listed, err)
		}

		silence(t, store, taskID, 10*time.Second)
		if listed, err := store.ListResumableCheckpoints(ctx, "app", time.Now(), testTakeoverWindow, 10, false); err != nil || len(listed) != 1 {
			t.Fatalf("sweep after the window = %v, %v; want the row", listed, err)
		}
		cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-b", time.Now(), time.Minute, testTakeoverWindow, false)
		if err != nil || cp == nil {
			t.Fatalf("adopt after the window = %v, %v; want the row", cp, err)
		}
		if cp.RenewedAt == nil || cp.ReadAt.Sub(*cp.RenewedAt) > 5*time.Second {
			t.Errorf("the adoption did not stamp a fresh renewal (renewed %v, read %v)", cp.RenewedAt, cp.ReadAt)
		}
		if again, err := store.AdoptCheckpoint(ctx, taskID, "owner-c", time.Now(), time.Minute, testTakeoverWindow, false); err != nil || again != nil {
			t.Errorf("a third process took the row its adopter just renewed: %v, %v", again, err)
		}
	})
}

func TestTakeoverOfARowWithoutARenewalFollowsItsLease(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := t.Context()
		for _, tc := range []struct {
			name    string
			lease   time.Duration
			adopted bool
		}{
			{"live lease", time.Hour, false},
			{"lapsed lease", -time.Hour, true},
		} {
			taskID, sessID := idgen.New(), idgen.New()
			seed("app", "user", sessID)
			lease := time.Now().Add(tc.lease)
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
				OwnerID: "owner-old-release", LeaseExpiresAt: &lease,
			}); err != nil {
				t.Fatalf("%s: save: %v", tc.name, err)
			}
			clearRenewal(t, store, taskID)

			listed, err := store.ListResumableCheckpoints(ctx, "app", time.Now(), testTakeoverWindow, 10, false)
			if err != nil {
				t.Fatalf("%s: list: %v", tc.name, err)
			}
			if got := slices.ContainsFunc(listed, func(c TaskCheckpoint) bool { return c.TaskID == taskID }); got != tc.adopted {
				t.Errorf("%s: listed for takeover = %v, want %v", tc.name, got, tc.adopted)
			}
			cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-new", time.Now(), time.Minute, testTakeoverWindow, false)
			if err != nil {
				t.Fatalf("%s: adopt: %v", tc.name, err)
			}
			if got := cp != nil; got != tc.adopted {
				t.Errorf("%s: adopted = %v, want %v", tc.name, got, tc.adopted)
			}
		}
	})
}

func TestRenewalStampsTheStoreClock(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := t.Context()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		saveOwned(t, store, taskID, sessID, "owner-a", 0)
		silence(t, store, taskID, time.Hour)

		if n, err := store.RenewLeases(ctx, "owner-a", []string{taskID}, time.Now().Add(-time.Hour)); err != nil || n != 1 {
			t.Fatalf("renew = %d, %v", n, err)
		}
		if cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-b", time.Now(), time.Minute, testTakeoverWindow, false); err != nil || cp != nil {
			t.Errorf("a row renewed a moment ago was taken: %v, %v", cp, err)
		}
		if _, err := store.RenewLeases(ctx, "owner-a", nil, time.Now()); err != nil {
			t.Errorf("a renewal with nothing to renew failed against a reachable store: %v", err)
		}
	})
}

func TestMarkAnsweringStoresTheFinalMessageForATakeover(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := t.Context()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		saveOwned(t, store, taskID, sessID, "owner-a", 1)
		if err := store.RegisterPendingCalls(ctx, taskID, "owner-a", []PendingCall{{
			CorrID: "corr-live", TaskID: taskID, AgentName: "app", ToolCallID: "tc", Kind: PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register: %v", err)
		}
		final := &FinalMessage{
			Topic:          "ns/a2a/v1/gateway/response/gw-1/task",
			Payload:        []byte(`{"jsonrpc":"2.0","id":"t","result":{"status":{"state":"completed"}}}`),
			UserProperties: map[string]string{"userId": "u1", "eventSeq": "7", "publisherId": "app"},
		}
		until := time.Now().Add(5 * time.Minute)

		if marked, err := store.MarkAnswering(ctx, taskID, "owner-b", final, until); err != nil || marked {
			t.Fatalf("a process not holding the row marked it answering: %v, %v", marked, err)
		}
		if marked, err := store.MarkAnswering(ctx, taskID, "owner-a", final, until); err != nil || !marked {
			t.Fatalf("the holder could not mark its row answering: %v, %v", marked, err)
		}
		if row, _ := store.LookupPendingCall(ctx, "app", "corr-live"); row != nil {
			t.Error("the answering mark left a correlation a late reply could still record against")
		}
		if popped, err := store.PopCheckpoint(ctx, taskID); err != nil || popped != nil {
			t.Errorf("a cancel took a row being answered: %v, %v", popped, err)
		}
		if err := store.UpdateCheckpoint(ctx, &TaskCheckpoint{TaskID: taskID, OwnerID: "owner-a", State: []byte(`{}`)}); !errors.Is(err, ErrNotOwner) {
			t.Errorf("a save overwrote a row being answered: %v", err)
		}

		silence(t, store, taskID, testTakeoverWindow)
		cp, err := store.AdoptCheckpoint(ctx, taskID, "owner-b", time.Now(), time.Minute, testTakeoverWindow, false)
		if err != nil || cp == nil {
			t.Fatalf("adopt the answering row = %v, %v", cp, err)
		}
		if cp.Final == nil {
			t.Fatal("the adopted row lost its final message")
		}
		if cp.Final.Topic != final.Topic || !bytes.Equal(cp.Final.Payload, final.Payload) || !maps.Equal(cp.Final.UserProperties, final.UserProperties) {
			t.Errorf("stored final = %+v, want %+v", cp.Final, final)
		}
		if deleted, err := store.DeleteCheckpointIfOwned(ctx, taskID, "owner-b"); err != nil || !deleted {
			t.Errorf("the adopter could not delete the answered row: %v, %v", deleted, err)
		}
	})
}

func TestPurgeAbandonedCheckpointsStopsAtItsLimit(t *testing.T) {
	for name, store := range purgeStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			old := time.Now().Add(-48 * time.Hour)
			for range 5 {
				savePurgeCheckpoint(t, store, "agent", old)
			}
			cutoff := time.Now().Add(-24 * time.Hour)
			var removed []int
			for range 4 {
				n, err := store.PurgeAbandonedCheckpoints(ctx, "", cutoff, 2)
				if err != nil {
					t.Fatalf("purge: %v", err)
				}
				removed = append(removed, n)
			}
			if want := []int{2, 2, 1, 0}; !slices.Equal(removed, want) {
				t.Errorf("batches removed %v, want %v", removed, want)
			}
		})
	}
}
