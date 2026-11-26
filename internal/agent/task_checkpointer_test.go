// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func durableResumeRegistry(enabled bool) *features.Registry {
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyDurableTaskResume, Default: enabled})
	return reg
}

func TestNewTaskCheckpointerGate(t *testing.T) {
	t.Run("nil without a feature registry", func(t *testing.T) {
		svc := newMockServices(newMockLLM(), session.NewMemoryStore())
		if got := newTaskCheckpointer(svc, "agent", newTestAgentBackstops(), slog.Default()); got != nil {
			t.Error("expected nil when no feature registry is wired")
		}
	})

	t.Run("nil when the flag is off", func(t *testing.T) {
		svc := newMockServices(newMockLLM(), session.NewMemoryStore())
		svc.featureRegistry = durableResumeRegistry(false)
		if got := newTaskCheckpointer(svc, "agent", newTestAgentBackstops(), slog.Default()); got != nil {
			t.Error("expected nil when durable_task_resume is off")
		}
	})

	t.Run("nil without a session store", func(t *testing.T) {
		svc := newMockServices(newMockLLM(), nil)
		svc.featureRegistry = durableResumeRegistry(true)
		if got := newTaskCheckpointer(svc, "agent", newTestAgentBackstops(), slog.Default()); got != nil {
			t.Error("expected nil when no session store is configured — there is nowhere to persist")
		}
	})

	t.Run("enabled with both", func(t *testing.T) {
		svc := newMockServices(newMockLLM(), session.NewMemoryStore())
		svc.featureRegistry = durableResumeRegistry(true)
		if got := newTaskCheckpointer(svc, "agent", newTestAgentBackstops(), slog.Default()); got == nil {
			t.Error("expected a checkpointer when the flag is on and a store exists")
		}
	})
}

func TestNilCheckpointerIsNoOp(t *testing.T) {
	var c *taskCheckpointer
	ctx := context.Background()

	if err := c.save(ctx, &TaskExecutionState{TaskID: "t"}); err != nil {
		t.Errorf("save on nil: %v", err)
	}
	c.recordResult(ctx, "t", &PendingToolCall{ToolCallID: "tc"}, tool.OK("ok"))
	c.discardIfOwned(ctx, "t")
	got, err := c.lookup(ctx, "corr")
	if err != nil || got != nil {
		t.Errorf("lookup on nil = %v, %v; want nil, nil", got, err)
	}
}

func newEnabledCheckpointer(t *testing.T) (*taskCheckpointer, *session.MemoryStore) {
	t.Helper()
	store := session.NewMemoryStore()
	svc := newMockServices(newMockLLM(), store)
	svc.featureRegistry = durableResumeRegistry(true)
	c := newTaskCheckpointer(svc, "agent-1", newTestAgentBackstops(), slog.Default())
	if c == nil {
		t.Fatal("expected an enabled checkpointer")
	}
	databaseAnswering(c)
	return c, store
}

func TestCheckpointerSaveWritesStateAndCorrelations(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	ctx := context.Background()

	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-peer": {ToolCallID: "tc-peer", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-peer", Timeout: time.Minute},
		"tc-hil":  {ToolCallID: "tc-hil", ToolName: "approve", Kind: PendingCallHILApproval, HILRequestID: "hil-1", Timeout: time.Hour},
		"tc-auth": {ToolCallID: "tc-auth", ToolName: "gcal", Kind: PendingCallAuth, CredentialKey: "cred", Timeout: time.Minute},
	}
	state.TotalPending = 3

	if err := c.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	cp, err := store.AdoptCheckpoint(ctx, state.TaskID, "successor", time.Now().Add(2*checkpointLease), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if cp != nil {
		t.Fatal("a checkpoint with outstanding calls must not be adoptable yet")
	}

	for corrID, wantKind := range map[string]string{
		"sub-peer": session.PendingKindPeer,
		"hil-1":    session.PendingKindHILApproval,
		authCorrelationID(state.TaskID, "tc-auth"): session.PendingKindAuth,
	} {
		got, lookupErr := store.LookupPendingCall(ctx, c.agentName, corrID)
		if lookupErr != nil {
			t.Fatalf("lookup %s: %v", corrID, lookupErr)
		}
		if got == nil {
			t.Errorf("no correlation row for %s — a response arriving elsewhere could not find this task", corrID)
			continue
		}
		if got.TaskID != state.TaskID || got.Kind != wantKind {
			t.Errorf("correlation %s = %+v, want task %q kind %q", corrID, got, state.TaskID, wantKind)
		}
		if got.ExpiresAt == nil {
			t.Errorf("correlation %s has no expiry — the reaper could never resolve it", corrID)
		}
	}
}

func TestCheckpointerRecordResultDrainsPendingCountAndRemovesCorrelation(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	ctx := context.Background()

	state := fullTaskState()
	peer := &PendingToolCall{ToolCallID: "tc-peer", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-peer", Timeout: time.Minute}
	hil := &PendingToolCall{ToolCallID: "tc-hil", ToolName: "approve", Kind: PendingCallHILApproval, HILRequestID: "hil-1", Timeout: time.Hour}
	state.PendingCalls = map[string]*PendingToolCall{"tc-peer": peer, "tc-hil": hil}
	state.TotalPending = 2

	if err := c.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	c.recordResult(ctx, state.TaskID, peer, tool.OK("peer done"))
	if cp, err := store.AdoptCheckpoint(ctx, state.TaskID, "successor", time.Now().Add(2*checkpointLease), time.Minute, testTakeoverWindow, false); err != nil || cp != nil {
		t.Fatalf("claim after one of two results = %v, %v; want not-yet-adoptable", cp, err)
	}
	if got, err := store.LookupPendingCall(ctx, c.agentName, "sub-peer"); err != nil || got != nil {
		t.Errorf("correlation for a resolved call survived: %v, %v", got, err)
	}

	c.recordResult(ctx, state.TaskID, hil, tool.OK("approved"))
	ownerDies(t, store, state.TaskID)
	cp, err := store.AdoptCheckpoint(ctx, state.TaskID, "successor", time.Now().Add(2*checkpointLease), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint must be adoptable by a successor once every pending call is resolved")
	}
	if len(cp.ToolResults) != 2 {
		t.Errorf("got %d recorded tool results, want 2", len(cp.ToolResults))
	}
}

func TestCheckpointerDiscardRemovesEverything(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	ctx := context.Background()

	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-peer": {ToolCallID: "tc-peer", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-peer", Timeout: time.Minute},
	}
	state.TotalPending = 1
	if err := c.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	c.discardIfOwned(ctx, state.TaskID)

	if got, err := store.LookupPendingCall(ctx, c.agentName, "sub-peer"); err != nil || got != nil {
		t.Errorf("correlation survived discard: %v, %v — a late response could resurrect a finished task", got, err)
	}
}

type ctxHonoringStore struct {
	session.Store
}

func (s ctxHonoringStore) RecordToolResult(ctx context.Context, taskID, corrID string, result *session.ToolCallResult) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.Store.RecordToolResult(ctx, taskID, corrID, result)
}

func (s ctxHonoringStore) DeleteCheckpoint(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.DeleteCheckpoint(ctx, taskID)
}

func (s ctxHonoringStore) CheckpointOwner(ctx context.Context, taskID string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return s.Store.CheckpointOwner(ctx, taskID)
}

func (s ctxHonoringStore) DeletePendingCall(ctx context.Context, corrID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.DeletePendingCall(ctx, corrID)
}

func TestCheckpointerWritesAndDiscardsThroughCancelledContext(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	c.store = ctxHonoringStore{Store: store}

	state := fullTaskState()
	peer := &PendingToolCall{ToolCallID: "tc-peer", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-peer", Timeout: time.Minute}
	state.PendingCalls = map[string]*PendingToolCall{"tc-peer": peer}
	state.TotalPending = 1
	if err := c.save(context.Background(), state); err != nil {
		t.Fatalf("save: %v", err)
	}

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	c.recordResult(dead, state.TaskID, peer, tool.OK("late but valid"))

	ownerDies(t, store, state.TaskID)
	cp, err := store.AdoptCheckpoint(context.Background(), state.TaskID, "successor", time.Now().Add(2*checkpointLease), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if cp == nil {
		t.Fatal("the result was dropped because the originating context was cancelled")
	}
	if len(cp.ToolResults) != 1 {
		t.Errorf("got %d tool results, want 1", len(cp.ToolResults))
	}

	ownerDies(t, store, state.TaskID)
	c.discardIfOwned(dead, state.TaskID)
	if got, err := store.GetCheckpoint(context.Background(), state.TaskID); err != nil || got != nil {
		t.Errorf("discard on a cancelled context left the checkpoint behind (present=%v, err=%v) — a sweep can resume a finished task", got != nil, err)
	}

	owned := fullTaskState()
	owned.TaskID = "task-owned-discard"
	owned.PendingCalls = map[string]*PendingToolCall{}
	owned.TotalPending = 0
	if err := c.save(context.Background(), owned); err != nil {
		t.Fatalf("save owned: %v", err)
	}
	c.discardIfOwned(dead, owned.TaskID)
	if got, err := store.GetCheckpoint(context.Background(), owned.TaskID); err != nil || got != nil {
		t.Errorf("discardIfOwned on a cancelled context left the checkpoint behind (present=%v, err=%v) — the completed task stays resumable", got != nil, err)
	}
}

func enableDurableResume(t *testing.T, agent *Agent) {
	t.Helper()
	store := session.NewMemoryStore()
	svc := newMockServices(newMockLLM(), store)
	svc.featureRegistry = durableResumeRegistry(true)
	agent.svc = svc
	agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, newTestAgentBackstops(), agent.logger)
	if agent.checkpoints == nil {
		t.Fatal("expected durable resume to be enabled")
	}
}

func TestPeerPauseGetsADurableExpiryTheReaperCanSee(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "owner")
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-peer-expiry"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-peer": {ToolCallID: "tc-peer", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-peer"},
	}
	state.TotalPending = 1
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	backstop := agent.delegationBackstops().peer
	if backstop <= 0 {
		t.Fatal("the agent resolved no peer backstop, so this test cannot distinguish anything")
	}

	early, err := store.ListExpiredPendingCalls(ctx, agent.cfg.Name, time.Now().Add(backstop), 10)
	if err != nil {
		t.Fatalf("list early: %v", err)
	}
	for _, pc := range early {
		if pc.ToolCallID == "tc-peer" {
			t.Error("the peer call was reapable before its own deadline — another replica can time out a call this one is still waiting on")
		}
	}

	late, err := store.ListExpiredPendingCalls(ctx, agent.cfg.Name, time.Now().Add(backstop+checkpointExpiryGrace+time.Second), 10)
	if err != nil {
		t.Fatalf("list late: %v", err)
	}
	for _, pc := range late {
		if pc.ToolCallID == "tc-peer" {
			return
		}
	}
	t.Errorf("the peer call never became reapable (%d expired rows) — its checkpoint is held forever if the peer never answers", len(late))
}

func newTestAgentBackstops() delegationBackstops {
	agent, _, _ := newTestAgent()
	return agent.delegationBackstops()
}

func TestRecordedResultSurvivesResumeWithFullFidelity(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "owner")
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-fidelity"
	pc := &PendingToolCall{ToolCallID: "tc-rich", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-rich"}
	state.PendingCalls = map[string]*PendingToolCall{"tc-rich": pc}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	rich := &tool.Result{
		Status:       tool.StatusFailed,
		Message:      "the peer reported a partial answer",
		Data:         map[string]any{"rows": float64(3)},
		FailedDetail: &tool.ToolFailedDetail{Code: "PEER_PARTIAL", Message: "partial"},
		ImageBlocks:  []tool.ImageBlock{{MIMEType: "image/png", Data: []byte{1, 2, 3}}},
		DataObjects: []tool.DataObject{
			{Name: "report.csv", MIMEType: "text/csv", Content: []byte("a,b\n1,2\n")},
		},
	}
	agent.checkpoints.recordResult(ctx, state.TaskID, pc, rich)

	ownerDies(t, store, state.TaskID)
	cp, err := store.AdoptCheckpoint(ctx, state.TaskID, "successor", time.Now().Add(2*checkpointLease), time.Minute, testTakeoverWindow, false)
	if err != nil || cp == nil {
		t.Fatalf("adopt: %v, %v", cp, err)
	}

	resumed, err := unmarshalTaskState(cp.State)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agent.applyCheckpointResults(resumed, cp.ToolResults)

	got := resumed.SyncResults[0]
	if got == nil {
		t.Fatal("the resumed turn has no result for the call it was waiting on")
	}
	if got.Status != tool.StatusFailed {
		t.Errorf("Status = %q, want %q — a Failed result rebuilt as Error is classified as a different failure", got.Status, tool.StatusFailed)
	}
	if got.Message != rich.Message {
		t.Errorf("Message = %q, want %q", got.Message, rich.Message)
	}
	if got.Data == nil || got.Data["rows"] != float64(3) {
		t.Errorf("Data = %+v, want the structured payload — the tool_result event loses its body", got.Data)
	}
	if got.FailedDetail == nil || got.FailedDetail.Code != "PEER_PARTIAL" {
		t.Errorf("FailedDetail = %+v, want the classification the next turn branches on", got.FailedDetail)
	}
	if len(got.ImageBlocks) != 1 || string(got.ImageBlocks[0].Data) != "\x01\x02\x03" {
		t.Errorf("ImageBlocks = %+v, want the block the peer returned", got.ImageBlocks)
	}
	if len(got.DataObjects) != 1 {
		t.Fatalf("DataObjects = %+v, want the artifact the call produced", got.DataObjects)
	}
	if string(got.DataObjects[0].Content) != "a,b\n1,2\n" {
		t.Errorf("artifact content = %q, want the bytes the peer produced — the artifact arrives empty", got.DataObjects[0].Content)
	}
}

func TestNewerCheckpointVersionIsRefusedAndOlderStillReadable(t *testing.T) {
	blob, err := marshalTaskState(peerOnlyState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(blob), `"version":`) {
		t.Fatal("the blob carries no version, so a newer build's state is indistinguishable from this build's")
	}

	forged := strings.Replace(string(blob),
		`"version":2`,
		fmt.Sprintf(`"version":%d`, checkpointStateVersion+1), 1)
	if forged == string(blob) {
		t.Fatal("could not forge a newer version, so this test proves nothing")
	}
	if _, err := unmarshalTaskState([]byte(forged)); err == nil {
		t.Error("a checkpoint from a newer build was decoded — the resumed task runs on whatever fields happened to match")
	}

	older := strings.Replace(string(blob),
		`"version":2`, `"version":0`, 1)
	if _, err := unmarshalTaskState([]byte(older)); err != nil {
		t.Errorf("a checkpoint from an older build was refused (%v) — a rollback would strand every paused task", err)
	}
}

func TestOversizedStateIsRefusedRatherThanWritten(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	state := fullTaskState()
	state.TaskID = "task-huge"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	state.Question = llm.ChatMessage{
		Role:    "user",
		Content: strings.Repeat("x", maxCheckpointBlobBytes+1),
	}

	if err := c.save(context.Background(), state); err == nil {
		t.Error("an oversized state was checkpointed silently — every pause rewrites it")
	}
	got, err := store.GetCheckpoint(context.Background(), state.TaskID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Error("the oversized state reached the store despite the refusal")
	}

	ordinary := fullTaskState()
	ordinary.TaskID = "task-ordinary"
	ordinary.PendingCalls = map[string]*PendingToolCall{}
	ordinary.TotalPending = 0
	if err := c.save(context.Background(), ordinary); err != nil {
		t.Errorf("an ordinary task was refused by the size bound: %v", err)
	}
}

func TestStoreLostWithTheProcessTurnsDurableResumeOff(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	svc := newMockServices(newMockLLM(), session.NewMemoryStore())
	svc.sessionsDieWithProcess = true
	svc.featureRegistry = durableResumeRegistry(true)
	if c := newTaskCheckpointer(svc, "agent-1", newTestAgentBackstops(), logger); c != nil {
		t.Fatal("durable resume is on over a store that is lost when the process stops")
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "local to this process") ||
		!strings.Contains(buf.String(), "sessionStore=memory") {
		t.Errorf("an in-memory session store drew no warning; operators cannot tell why tasks do not resume (log: %q)", buf.String())
	}
}

func TestStoreThatOutlivesTheProcessKeepsDurableResumeOn(t *testing.T) {
	var buf strings.Builder
	svc := newMockServices(newMockLLM(), session.NewMemoryStore())
	svc.featureRegistry = durableResumeRegistry(true)
	if c := newTaskCheckpointer(svc, "agent-1", newTestAgentBackstops(), slog.New(slog.NewTextHandler(&buf, nil))); c == nil {
		t.Fatal("durable resume is off over a store that outlives the process")
	}
	if strings.Contains(buf.String(), "local to this process") {
		t.Errorf("a store that outlives the process was warned about: %q", buf.String())
	}
}

func TestArrivingPeerResultKeepsItsFullFidelity(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-arriving"
	pc := &PendingToolCall{ToolCallID: "tc-arriving", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-arriving"}
	state.PendingCalls = map[string]*PendingToolCall{"tc-arriving": pc}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	arriving := &tool.Result{
		Status:       tool.StatusFailed,
		Message:      "the peer reported a partial answer",
		Data:         map[string]any{"rows": float64(7)},
		FailedDetail: &tool.ToolFailedDetail{Code: "PEER_PARTIAL"},
		ImageBlocks:  []tool.ImageBlock{{MIMEType: "image/png", Data: []byte{9, 8, 7}}},
		DataObjects:  []tool.DataObject{{Name: "out.csv", MIMEType: "text/csv", Content: []byte("x,y\n")}},
	}

	pending, err := agent.checkpoints.lookup(ctx, "sub-arriving")
	if err != nil || pending == nil {
		t.Fatalf("lookup: %v, %v", pending, err)
	}
	agent.adoptCheckpointedTask(ctx, pending, "sub-arriving", arriving, agent.logger)

	cp, err := store.GetCheckpoint(ctx, state.TaskID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cp == nil || len(cp.ToolResults) != 1 {
		t.Fatalf("expected one recorded result, got %+v", cp)
	}
	got := resultFromPayload(cp.ToolResults[0].Payload)
	if got == nil {
		t.Fatal("the arriving result was recorded as text only — the resumed turn rebuilds it as bare message and error flag")
	}
	if got.Status != tool.StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, tool.StatusFailed)
	}
	if got.Data == nil || got.Data["rows"] != float64(7) {
		t.Errorf("Data = %+v, want the structured payload", got.Data)
	}
	if got.FailedDetail == nil || got.FailedDetail.Code != "PEER_PARTIAL" {
		t.Errorf("FailedDetail = %+v, want the classification", got.FailedDetail)
	}
	if len(got.ImageBlocks) != 1 {
		t.Errorf("ImageBlocks = %+v, want the block the peer returned", got.ImageBlocks)
	}
	if len(got.DataObjects) != 1 || string(got.DataObjects[0].Content) != "x,y\n" {
		t.Errorf("DataObjects = %+v, want the artifact bytes the peer produced", got.DataObjects)
	}
}

type failOnceStore struct {
	session.Store
	failed bool
}

func (s *failOnceStore) DeleteCheckpointIfOwned(ctx context.Context, taskID, ownerID string) (bool, error) {
	if !s.failed {
		s.failed = true
		return false, errors.New("transient failure")
	}
	return s.Store.DeleteCheckpointIfOwned(ctx, taskID, ownerID)
}

func TestFailedDiscardIsRetriedOnTheMaintenanceTick(t *testing.T) {
	c, inner := newEnabledCheckpointer(t)
	store := &failOnceStore{Store: inner}
	c.store = store
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-stuck-row"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	if err := c.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	c.discardIfOwned(ctx, state.TaskID)

	got, err := inner.GetCheckpoint(ctx, state.TaskID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("the store did not actually fail the delete, so this test proves nothing")
	}

	c.retryFailedDiscards(ctx)

	got, err = inner.GetCheckpoint(ctx, state.TaskID)
	if err != nil {
		t.Fatalf("get after retry: %v", err)
	}
	if got != nil {
		t.Error("a checkpoint whose delete failed was never retried — the finished task stays resumable and answers twice")
	}
}

func TestCheckpointWritesTheTaskTraceIDOntoItsRows(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-traced"
	state.UserProps.TraceID = "trace-from-the-task"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-traced": {ToolCallID: "tc-traced", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-traced"},
	}
	state.TotalPending = 1
	if err := c.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	cp, err := store.GetCheckpoint(ctx, state.TaskID)
	if err != nil || cp == nil {
		t.Fatalf("get checkpoint: %v, %v", cp, err)
	}
	if cp.TraceID != "trace-from-the-task" {
		t.Errorf("checkpoint TraceID = %q, want %q — an adoption that fails before decoding the blob can only name a taskID", cp.TraceID, "trace-from-the-task")
	}

	pending, err := store.LookupPendingCall(ctx, c.agentName, "sub-traced")
	if err != nil || pending == nil {
		t.Fatalf("lookup: %v, %v", pending, err)
	}
	if pending.TraceID != "trace-from-the-task" {
		t.Errorf("correlation TraceID = %q, want %q — the reaper's synthesised timeout cannot be correlated to the task", pending.TraceID, "trace-from-the-task")
	}
}

type failRecordStore struct {
	session.Store
}

func (s *failRecordStore) RecordToolResult(context.Context, string, string, *session.ToolCallResult) (int, error) {
	return 0, errors.New("transient failure")
}

func TestFailedRecordKeepsTheCorrelationForTheReaper(t *testing.T) {
	inner := session.NewMemoryStore()
	c, _ := newEnabledCheckpointer(t)
	c.store = inner
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-record-failed"
	pc := &PendingToolCall{ToolCallID: "tc-fail", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-fail"}
	state.PendingCalls = map[string]*PendingToolCall{"tc-fail": pc}
	state.TotalPending = 1
	if err := c.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	c.store = &failRecordStore{Store: inner}
	c.recordResult(ctx, state.TaskID, pc, tool.OK("peer finished the work"))

	got, err := inner.LookupPendingCall(ctx, c.agentName, "sub-fail")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil {
		t.Error("the correlation was consumed after the record failed — the checkpoint now has an outstanding call, no result and no row, so neither the reaper nor the sweep can ever resolve it")
	}
}

func TestFencedReplicaWritesNoTurnTailToTheSession(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "fenced")
	ctx := context.Background()

	sess := &session.Session{
		AppName: agent.cfg.Name, UserID: "user-1", SessionID: "sess-tail", ContextID: "sess-tail",
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := fullTaskState()
	state.TaskID = "task-tail"
	state.EffectiveSessionID = "sess-tail"
	state.HistoryOffset = 0
	state.Fenced.Store(true)

	rc := &taskRunContext{sess: sess, logger: agent.logger}
	if err := agent.flushSessionMessages(ctx, state, rc); err != nil {
		t.Fatalf("flush: %v", err)
	}

	after, err := store.Get(ctx, agent.cfg.Name, "user-1", "sess-tail")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(after.Messages) != 0 {
		t.Errorf("a fenced replica appended %d messages to the session its successor is completing — the turn appears twice in history", len(after.Messages))
	}
}

func TestSaveWritesToolNameOntoTheCorrelationRow(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-toolname"
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-named": {ToolCallID: "tc-named", ToolName: "delegate_task_to_agent",
			Kind: PendingCallPeer, SubTaskID: "sub-named"},
	}
	state.TotalPending = 1
	if err := c.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.LookupPendingCall(ctx, c.agentName, "sub-named")
	if err != nil || got == nil {
		t.Fatalf("lookup: %v, %v", got, err)
	}
	if got.ToolName != "delegate_task_to_agent" {
		t.Errorf("ToolName on the correlation row = %q, want the tool — the adopting replica and the reaper record the result under the pause kind instead", got.ToolName)
	}
}

func TestSetModelOverrideRecordsTheAliasForResume(t *testing.T) {
	state := &TaskExecutionState{}
	state.setModelOverride(&ResolvedModelOverride{
		Alias:                 "fast-model",
		Config:                &llm.BifrostConfig{Model: "anthropic/claude-haiku-4-5"},
		CacheStrategy:         "rolling",
		ReasoningBudgetTokens: 4096,
	})

	if state.ModelOverrideAlias != "fast-model" {
		t.Errorf("ModelOverrideAlias = %q, want %q — the checkpoint persists the alias, so without it a resumed task reverts to the agent default model",
			state.ModelOverrideAlias, "fast-model")
	}
	if state.ModelOverride == nil || state.ModelOverride.Model != "anthropic/claude-haiku-4-5" {
		t.Errorf("the resolved config did not reach the state: %+v", state.ModelOverride)
	}
}

func TestASlowDurableWriteDoesNotBlockTheStateLock(t *testing.T) {
	inner := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, inner, "owner", func(a *Agent) {
		a.checkpoints.store = &slowRecordStore{
			Store:   inner,
			delay:   2 * time.Second,
			started: make(chan struct{}),
		}
	})
	slow := agent.checkpoints.store.(*slowRecordStore)

	state := fullTaskState()
	state.TaskID = "task-lock-contention"
	pc := &PendingToolCall{ToolCallID: "tc-hil", ToolName: "approve",
		Kind: PendingCallHILApproval, HILRequestID: "hil-1", Timeout: time.Minute}
	state.PendingCalls = map[string]*PendingToolCall{"tc-hil": pc}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)

	go agent.handlePendingHILTimeout(state.TaskID, "tc-hil")

	<-slow.started
	acquired := make(chan struct{})
	go func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		close(acquired)
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Error("state.mu was held across a durable write — every sibling delivery and every cancel of this task waits on the database")
	}
}

func TestOversizedStateRemovesTheStaleRowItCannotReplace(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	ctx := context.Background()

	state := fullTaskState()
	state.TaskID = "task-outgrew-itself"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0

	if err := c.save(ctx, state); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if got, err := store.GetCheckpoint(ctx, state.TaskID); err != nil || got == nil {
		t.Fatalf("the first save did not land: %v, %v", got, err)
	}

	state.Question = llm.ChatMessage{
		Role:    "user",
		Content: strings.Repeat("x", maxCheckpointBlobBytes+1),
	}
	if err := c.save(ctx, state); err == nil {
		t.Fatal("the oversized save was accepted")
	}

	got, err := store.GetCheckpoint(ctx, state.TaskID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Error("the stale row survived a refused checkpoint — it is still owned and adoptable, so a successor resumes from state this task has already moved past")
	}
	if state.checkpointed.Load() {
		t.Error("the task still believes it has a durable row, so the fence will read a missing row as 'somebody completed it' rather than 'never saved'")
	}
}
