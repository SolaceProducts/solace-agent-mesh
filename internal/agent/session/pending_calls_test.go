// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type checkpointStore interface {
	SaveCheckpoint(ctx context.Context, cp *TaskCheckpoint) error
	GetCheckpoint(ctx context.Context, taskID string) (*TaskCheckpoint, error)
	PopCheckpoint(ctx context.Context, taskID string) (*TaskCheckpoint, error)
	RecordToolResult(ctx context.Context, taskID, corrID string, result *ToolCallResult) (int, error)
	UpdateCheckpoint(ctx context.Context, cp *TaskCheckpoint) error
	MarkAnswering(ctx context.Context, taskID, ownerID string, final *FinalMessage, until time.Time) (bool, error)
	AdoptCheckpoint(ctx context.Context, taskID, newOwnerID string, now time.Time, leaseFor, silentFor time.Duration, releasedOnly bool) (*TaskCheckpoint, error)
	CheckpointOwner(ctx context.Context, taskID string) (string, bool, error)
	RenewLeases(ctx context.Context, ownerID string, taskIDs []string, until time.Time) (int, error)
	ReleaseOwnership(ctx context.Context, taskID, ownerID string) error
	DeleteCheckpoint(ctx context.Context, taskID string) error
	DeleteCheckpointIfOwned(ctx context.Context, taskID, ownerID string) (bool, error)
	DeleteAbandonedCheckpoint(ctx context.Context, taskID string, lapsedBefore time.Time) (bool, error)
	RegisterPendingCalls(ctx context.Context, taskID, ownerID string, calls []PendingCall) error
	LookupPendingCall(ctx context.Context, agentName, corrID string) (*PendingCall, error)
	PendingCallTask(ctx context.Context, corrID string) (string, bool, error)
	DeletePendingCall(ctx context.Context, corrID string) error
	ListExpiredPendingCalls(ctx context.Context, agentName string, now time.Time, limit int) ([]PendingCall, error)
	ListResumableCheckpoints(ctx context.Context, agentName string, now time.Time, silentFor time.Duration, limit int, releasedOnly bool) ([]TaskCheckpoint, error)
	PurgeAbandonedCheckpoints(ctx context.Context, agentName string, lapsedBefore time.Time, limit int) (int, error)
}

func eachStore(t *testing.T, fn func(t *testing.T, store checkpointStore, seed func(appName, userID, sessionID string))) {
	t.Run("sql", func(t *testing.T) {
		store := newTestSQLStore(t)
		fn(t, store, func(appName, userID, sessionID string) {
			seedSession(t, store, appName, userID, sessionID)
		})
	})
	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		fn(t, NewMemoryStore(), func(string, string, string) {})
	})
}

func TestPendingCallRoundTrip(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID, corrID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)

		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, PendingCount: 1,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{
			{CorrID: corrID, TaskID: taskID, AgentName: "app",
				ToolCallID: "tc-round-trip", ToolName: "delegate_task_to_agent",
				TraceID: "trace-round-trip", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}

		got, err := store.LookupPendingCall(ctx, "app", corrID)
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if got == nil {
			t.Fatal("expected a pending call, got nil")
		}
		if got.TaskID != taskID || got.Kind != PendingKindPeer {
			t.Errorf("got taskID=%q kind=%q, want %q/%q", got.TaskID, got.Kind, taskID, PendingKindPeer)
		}
		if got.ToolCallID != "tc-round-trip" {
			t.Errorf("ToolCallID = %q, want %q — the resumed turn cannot match the recorded result to its tool call", got.ToolCallID, "tc-round-trip")
		}
		if got.ToolName != "delegate_task_to_agent" {
			t.Errorf("ToolName = %q, want %q — the recorded result would carry the pause kind instead of the tool", got.ToolName, "delegate_task_to_agent")
		}
		if got.TraceID != "trace-round-trip" {
			t.Errorf("TraceID = %q, want %q — a cross-pod resume cannot be correlated by trace", got.TraceID, "trace-round-trip")
		}

		if delErr := store.DeletePendingCall(ctx, corrID); delErr != nil {
			t.Fatalf("delete: %v", delErr)
		}
		got, err = store.LookupPendingCall(ctx, "app", corrID)
		if err != nil || got != nil {
			t.Errorf("after delete: got %v, %v; want nil, nil", got, err)
		}
	})
}

func TestLookupUnknownPendingCallReturnsNil(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, _ func(string, string, string)) {
		got, err := store.LookupPendingCall(context.Background(), "app", idgen.New())
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil for an unknown correlation ID", got)
		}
	})
}

func TestDeleteCheckpointCascadesToPendingCalls(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		corrA, corrB := idgen.New(), idgen.New()
		seed("app", "user", sessID)

		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, PendingCount: 2,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{
			{CorrID: corrA, TaskID: taskID, Kind: PendingKindPeer},
			{CorrID: corrB, TaskID: taskID, Kind: PendingKindRemoteTool},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}

		if err := store.DeleteCheckpoint(ctx, taskID); err != nil {
			t.Fatalf("delete checkpoint: %v", err)
		}
		for _, corrID := range []string{corrA, corrB} {
			got, err := store.LookupPendingCall(ctx, "app", corrID)
			if err != nil {
				t.Fatalf("lookup %s: %v", corrID, err)
			}
			if got != nil {
				t.Errorf("correlation %s survived checkpoint deletion", corrID)
			}
		}
	})
}

func TestListExpiredPendingCallsOnlyReturnsPastExpiry(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		now := time.Now()
		past := now.Add(-time.Minute)
		future := now.Add(time.Hour)
		expired, live, noExpiry := idgen.New(), idgen.New(), idgen.New()
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{
			{CorrID: expired, TaskID: taskID, AgentName: "app", ToolCallID: "tc-expired", ToolName: "peer_slow",
				TraceID: "trace-expired", Kind: PendingKindPeer, ExpiresAt: &past},
			{CorrID: live, TaskID: taskID, AgentName: "app", Kind: PendingKindPeer, ExpiresAt: &future},
			{CorrID: noExpiry, TaskID: taskID, AgentName: "app", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}

		got, err := store.ListExpiredPendingCalls(ctx, "app", now, 10)
		if err != nil {
			t.Fatalf("list expired: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d expired calls, want 1: %+v", len(got), got)
		}
		if got[0].CorrID != expired {
			t.Errorf("got %q, want the past-expiry entry %q", got[0].CorrID, expired)
		}
		if got[0].ToolCallID != "tc-expired" {
			t.Errorf("ToolCallID = %q, want %q — the reaper cannot key its timeout result to the tool call", got[0].ToolCallID, "tc-expired")
		}
		if got[0].ToolName != "peer_slow" {
			t.Errorf("ToolName = %q, want %q — the synthesised timeout would name the pause kind instead of the tool", got[0].ToolName, "peer_slow")
		}
		if got[0].TraceID != "trace-expired" {
			t.Errorf("TraceID = %q, want %q — the reaper's timeout cannot be correlated to the task's trace", got[0].TraceID, "trace-expired")
		}
	})
}

func TestLiveOwnerKeepsItsCheckpoint(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)

		lease := time.Now().Add(5 * time.Minute)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			PendingCount: 0, OwnerID: "owner-alive", LeaseExpiresAt: &lease,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		got, err := store.AdoptCheckpoint(ctx, taskID, "bystander", time.Now(), time.Minute, testTakeoverWindow, false)
		if err != nil {
			t.Fatalf("adopt: %v", err)
		}
		if got != nil {
			t.Error("a bystander adopted a task whose owner still holds the lease — that owner is still running the turn")
		}

		resumable, err := store.ListResumableCheckpoints(ctx, "app", time.Now(), testTakeoverWindow, 10, false)
		if err != nil {
			t.Fatalf("list resumable: %v", err)
		}
		if len(resumable) != 0 {
			t.Errorf("got %d resumable, want 0 while the lease is held", len(resumable))
		}
	})
}

func TestExpiredLeaseIsAdoptable(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)

		expired := time.Now().Add(-time.Minute)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			PendingCount: 0, OwnerID: "owner-dead", LeaseExpiresAt: &expired,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		silence(t, store, taskID, testTakeoverWindow)

		resumable, err := store.ListResumableCheckpoints(ctx, "app", time.Now(), testTakeoverWindow, 10, false)
		if err != nil {
			t.Fatalf("list resumable: %v", err)
		}
		if len(resumable) != 1 || resumable[0].TaskID != taskID {
			t.Fatalf("got %+v, want the expired-lease checkpoint %q", resumable, taskID)
		}

		got, err := store.AdoptCheckpoint(ctx, taskID, "successor", time.Now(), time.Minute, testTakeoverWindow, false)
		if err != nil {
			t.Fatalf("adopt: %v", err)
		}
		if got == nil {
			t.Fatal("an expired lease must be adoptable, or the task is stuck forever")
		}
		if got.OwnerID != "successor" {
			t.Errorf("OwnerID = %q, want the adopting replica", got.OwnerID)
		}

		if second, secondErr := store.AdoptCheckpoint(ctx, taskID, "other", time.Now(), time.Minute, testTakeoverWindow, false); secondErr != nil || second != nil {
			t.Errorf("a second replica adopted an already-transferred checkpoint: %v, %v", second, secondErr)
		}
	})
}

func TestRenewLeaseKeepsOwnershipAndOnlyOwnMinesTasks(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		mine, theirs, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)

		nearlyUp := time.Now().Add(time.Second)
		for taskID, owner := range map[string]string{mine: "owner-me", theirs: "owner-them"} {
			lease := nearlyUp
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
				PendingCount: 0, OwnerID: owner, LeaseExpiresAt: &lease,
			}); err != nil {
				t.Fatalf("save %s: %v", taskID, err)
			}
			silence(t, store, taskID, testTakeoverWindow)
		}

		renewed, err := store.RenewLeases(ctx, "owner-me", []string{mine, theirs}, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("renew: %v", err)
		}
		if renewed != 1 {
			t.Errorf("renewed %d rows, want 1 — a replica must not extend another's lease", renewed)
		}

		if got, adoptErr := store.AdoptCheckpoint(ctx, mine, "bystander", time.Now().Add(time.Minute), time.Minute, testTakeoverWindow, false); adoptErr != nil || got != nil {
			t.Errorf("renewed task was adopted anyway: %v, %v", got, adoptErr)
		}
		if got, adoptErr := store.AdoptCheckpoint(ctx, theirs, "bystander", time.Now().Add(time.Minute), time.Minute, testTakeoverWindow, false); adoptErr != nil || got == nil {
			t.Errorf("un-renewed task should have lapsed and been adoptable: %v, %v", got, adoptErr)
		}
	})
}

func TestReleaseOwnershipMakesCheckpointImmediatelyAdoptable(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)

		lease := time.Now().Add(time.Hour)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			PendingCount: 0, OwnerID: "owner-leaving", LeaseExpiresAt: &lease,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		if err := store.ReleaseOwnership(ctx, taskID, "someone-else"); err != nil {
			t.Fatalf("release by non-owner: %v", err)
		}
		if got, err := store.AdoptCheckpoint(ctx, taskID, "successor", time.Now(), time.Minute, testTakeoverWindow, false); err != nil || got != nil {
			t.Errorf("a non-owner's release freed the lease: %v, %v", got, err)
		}

		if err := store.ReleaseOwnership(ctx, taskID, "owner-leaving"); err != nil {
			t.Fatalf("release: %v", err)
		}
		got, err := store.AdoptCheckpoint(ctx, taskID, "successor", time.Now(), time.Minute, testTakeoverWindow, false)
		if err != nil {
			t.Fatalf("adopt after release: %v", err)
		}
		if got == nil {
			t.Error("a released checkpoint must be adoptable at once")
		}
	})
}

func TestListResumableCheckpointsIsScopedToOneAgent(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		mine, theirs, sessID := idgen.New(), idgen.New(), idgen.New()
		seed("app", "user", sessID)

		lapsed := time.Now().Add(-time.Hour)
		for taskID, agentName := range map[string]string{mine: "app", theirs: "some-other-agent"} {
			expiry := lapsed
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: agentName, PendingCount: 0,
				OwnerID: "owner-dead", LeaseExpiresAt: &expiry,
			}); err != nil {
				t.Fatalf("save %s: %v", taskID, err)
			}
			silence(t, store, taskID, testTakeoverWindow)
		}

		resumable, err := store.ListResumableCheckpoints(ctx, "app", time.Now(), testTakeoverWindow, 10, false)
		if err != nil {
			t.Fatalf("list resumable: %v", err)
		}
		var sawMine, sawTheirs bool
		for _, row := range resumable {
			switch row.TaskID {
			case mine:
				sawMine = true
			case theirs:
				sawTheirs = true
			}
		}
		if !sawMine {
			t.Error("the sweep did not offer this agent its own abandoned checkpoint")
		}
		if sawTheirs {
			t.Error("the sweep offered a checkpoint belonging to a different agent — resuming it would run that task with the wrong tools and config")
		}
	})
}

func TestCheckpointOwnerDistinguishesMissingUnownedAndOwned(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		sessID := idgen.New()
		seed("app", "user", sessID)

		if _, exists, err := store.CheckpointOwner(ctx, idgen.New()); err != nil || exists {
			t.Errorf("unknown task: exists=%v err=%v; want false, nil", exists, err)
		}

		owned := idgen.New()
		lease := time.Now().Add(time.Hour)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: owned, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			OwnerID: "owner-A", LeaseExpiresAt: &lease,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
		gotOwner, exists, err := store.CheckpointOwner(ctx, owned)
		if err != nil {
			t.Fatalf("owner: %v", err)
		}
		if !exists {
			t.Fatal("an existing checkpoint reported as missing — a renewal shortfall would be read as a takeover")
		}
		if gotOwner != "owner-A" {
			t.Errorf("owner = %q, want %q — the wrong column is being read", gotOwner, "owner-A")
		}

		if relErr := store.ReleaseOwnership(ctx, owned, "owner-A"); relErr != nil {
			t.Fatalf("release: %v", err)
		}
		gotOwner, exists, err = store.CheckpointOwner(ctx, owned)
		if err != nil {
			t.Fatalf("owner after release: %v", err)
		}
		if !exists {
			t.Error("a released checkpoint reported as missing")
		}
		if gotOwner != "" {
			t.Errorf("owner = %q after release, want empty so callers can tell nobody holds it", gotOwner)
		}
	})
}

func TestReleasedCheckpointStaysVisibleToTheSweep(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)

		held := time.Now().Add(time.Hour)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, AgentName: "app",
			PendingCount: 0, OwnerID: "owner-leaving", LeaseExpiresAt: &held,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		if err := store.ReleaseOwnership(ctx, taskID, "owner-leaving"); err != nil {
			t.Fatalf("release: %v", err)
		}

		resumable, err := store.ListResumableCheckpoints(ctx, "app", time.Now(), testTakeoverWindow, 10, false)
		if err != nil {
			t.Fatalf("list resumable: %v", err)
		}
		for _, row := range resumable {
			if row.TaskID == taskID {
				return
			}
		}
		t.Errorf("a gracefully released checkpoint is invisible to the sweep (%d other rows listed) — SIGTERM strands the task while kill -9 recovers it", len(resumable))
	})
}

func TestRecordToolResultIsIdempotentOnToolCallID(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, PendingCount: 2,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		first, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{ToolCallID: "tc-a", Content: "answer"})
		if err != nil {
			t.Fatalf("first record: %v", err)
		}
		if first != 1 {
			t.Fatalf("pending after first result = %d, want 1", first)
		}

		second, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{ToolCallID: "tc-a", Content: "a later write that must not land"})
		if err != nil {
			t.Fatalf("redelivery: %v", err)
		}
		if second != 1 {
			t.Errorf("pending after a redelivery = %d, want 1 — the duplicate decremented, so the turn resumes before its other call answers", second)
		}

		if _, recErr := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{ToolCallID: "tc-b", Content: "other"}); recErr != nil {
			t.Fatalf("second call: %v", recErr)
		}
		cp, err := store.AdoptCheckpoint(ctx, taskID, "reader", time.Now(), time.Minute, testTakeoverWindow, false)
		if err != nil || cp == nil {
			t.Fatalf("adopt: %v, %v", cp, err)
		}
		if len(cp.ToolResults) != 2 {
			t.Errorf("got %d recorded results, want 2 — a duplicate tool_use id is rejected by the LLM APIs: %+v", len(cp.ToolResults), cp.ToolResults)
		}
		for _, r := range cp.ToolResults {
			if r.ToolCallID == "tc-a" && r.Content != "answer" {
				t.Errorf("tc-a content = %q, want the first recorded value — a later write overwrote a terminal result, which is how a real answer becomes a timeout", r.Content)
			}
		}
	})
}

func TestRecordToolResultRejectsAMissingToolCallID(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, PendingCount: 1,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		if _, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{Content: "orphan"}); err == nil {
			t.Error("an id-less result was accepted — it is unreadable on resume and collides with every other id-less result")
		}
	})
}

func TestCheckpointTraceIDIsReadableWithoutDecodingState(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		lapsed := time.Now().Add(-time.Hour)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", AgentName: "app", UserID: "user", SessionID: sessID,
			PendingCount: 0, TraceID: "trace-visible",
			OwnerID: "owner-gone", LeaseExpiresAt: &lapsed,
			State: []byte("{not json"),
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		silence(t, store, taskID, testTakeoverWindow)

		got, err := store.GetCheckpoint(ctx, taskID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got == nil {
			t.Fatal("expected the checkpoint")
		}
		if got.TraceID != "trace-visible" {
			t.Errorf("TraceID = %q, want %q — a failed adoption or an undecodable blob can only name a taskID", got.TraceID, "trace-visible")
		}

		listed, err := store.ListResumableCheckpoints(ctx, "app", time.Now(), testTakeoverWindow, 10, false)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, cp := range listed {
			if cp.TaskID != taskID {
				continue
			}
			if cp.TraceID != "trace-visible" {
				t.Errorf("the sweep's row has TraceID %q, want %q", cp.TraceID, "trace-visible")
			}
			return
		}
		t.Error("the sweep did not list the checkpoint, so the trace assertion above never ran")
	})
}

func TestPopCheckpointYieldsTheRowToExactlyOneCaller(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", AgentName: "app", UserID: "user", SessionID: sessID,
			State: []byte(`{"task_id":"x"}`),
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []PendingCall{
			{CorrID: idgen.New(), TaskID: taskID, ToolCallID: "tc-1", Kind: PendingKindPeer},
		}); err != nil {
			t.Fatalf("register: %v", err)
		}

		const racers = 16
		var wg sync.WaitGroup
		won := make([]bool, racers)
		errs := make([]error, racers)
		for i := range racers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				cp, err := store.PopCheckpoint(ctx, taskID)
				won[i], errs[i] = cp != nil, err
			}(i)
		}
		wg.Wait()

		winners := 0
		for i := range racers {
			if errs[i] != nil {
				t.Errorf("racer %d: %v", i, errs[i])
			}
			if won[i] {
				winners++
			}
		}
		if winners != 1 {
			t.Errorf("%d callers took the row, want exactly 1 — each one publishes a terminal response", winners)
		}
		if got, err := store.GetCheckpoint(ctx, taskID); err != nil || got != nil {
			t.Errorf("the checkpoint outlived the take: %v, %v", got, err)
		}
	})
}
