// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const holderTimeoutResult = "SANDBOX_TIMEOUT: the remote call timed out"

func TestHoldersTimeoutDefersToAReplyAnotherReplicaAlreadyRecorded(t *testing.T) {
	const corrID = "corr-timeout-race"
	store := session.NewMemoryStore()
	holder, holderLLM, holderBrk := replicaOf(t, store, "holder")
	survivor, _, _ := replicaOf(t, store, "survivor")
	registerRemoteTool(t, survivor, strBroker(t))
	survivor.installRemoteToolOrphanHandlers()
	deliveries := make(chan func(*tool.Result), 1)
	if err := holder.Tools().Register(&testBuiltinTool{
		name:        remoteReportTool,
		description: "a remote call whose wait window the holder times out",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			pending := tool.Pending("waiting for the STR", tool.WithData(map[string]any{
				"pending_kind": "remote_tool", "sub_task_id": corrID, "timeout_ms": float64(time.Minute.Milliseconds()),
			}))
			pending.OnDelivery = func(deliver func(*tool.Result)) { deliveries <- deliver }
			return pending, nil
		},
	}); err != nil {
		t.Fatalf("register %s: %v", remoteReportTool, err)
	}
	holderLLM.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool), {Content: "The report is ready."}}
	user := approvalUser("task-timeout-race")
	if resp, err := holder.HandleTask(t.Context(), "task-timeout-race", concUserMessage("build the report", idgen.New()), user); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	if !survivor.resumeOrphanRemoteToolReply(corrID, remoteToolReplyPayload(t, corrID, "report ready")) {
		t.Fatal("resumeOrphanRemoteToolReply() = false, want the reply queued")
	}
	if !waitUntil(func() bool { return len(recordedAnswers(t, store, "task-timeout-race")) == 1 }) {
		t.Fatal("the survivor never recorded the STR's reply")
	}

	(<-deliveries)(tool.Error(holderTimeoutResult))

	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-timeout-race", "report ready")
	assertNoTimeoutSeen(t, holderLLM)
}

type gatedCheckpointRead struct {
	session.Store

	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *gatedCheckpointRead) GetCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	if s.armed.Load() {
		s.once.Do(func() {
			close(s.entered)
			<-s.release
		})
	}
	return s.Store.GetCheckpoint(ctx, taskID)
}

func TestResumedTurnWaitsForABatchSiblingsRecordedResultToBeRead(t *testing.T) {
	const corrA, corrB = "corr-batch-a", "corr-batch-b"
	base := session.NewMemoryStore()
	gated := &gatedCheckpointRead{Store: base, entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gated.release) }) }
	t.Cleanup(release)
	holder, holderLLM, holderBrk := replicaOf(t, gated, "holder")
	survivor, _, _ := replicaOf(t, base, "survivor")
	registerRemoteTool(t, survivor, strBroker(t))
	survivor.installRemoteToolOrphanHandlers()
	deliveries := make(map[string]func(*tool.Result))
	var deliveriesMu sync.Mutex
	pendingRemote := func(corrID string) stubExecFn {
		return func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			pending := tool.Pending("waiting for the STR", tool.WithData(map[string]any{
				"pending_kind": "remote_tool", "sub_task_id": corrID, "timeout_ms": float64(time.Minute.Milliseconds()),
			}))
			pending.OnDelivery = func(deliver func(*tool.Result)) {
				deliveriesMu.Lock()
				defer deliveriesMu.Unlock()
				deliveries[corrID] = deliver
			}
			return pending, nil
		}
	}
	for name, corrID := range map[string]string{remoteReportTool: corrA, "other_report": corrB} {
		if err := holder.Tools().Register(&testBuiltinTool{name: name, description: "a remote call", execFn: pendingRemote(corrID)}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	holderLLM.responses = []*llm.ChatResponse{{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{
			{ID: "call-a", Name: remoteReportTool, Arguments: `{}`},
			{ID: "call-b", Name: "other_report", Arguments: `{}`},
		},
	}, {Content: "Both reports are ready."}}
	if resp, err := holder.HandleTask(t.Context(), "task-batch", concUserMessage("build the reports", idgen.New()), approvalUser("task-batch")); err != nil || resp != nil {
		t.Fatalf("the task should pause on both calls: resp=%v err=%v", resp, err)
	}
	if !survivor.resumeOrphanRemoteToolReply(corrA, remoteToolReplyPayload(t, corrA, "report ready")) {
		t.Fatal("resumeOrphanRemoteToolReply() = false, want the reply queued")
	}
	if !waitUntil(func() bool { return len(recordedAnswers(t, base, "task-batch")) == 1 }) {
		t.Fatal("the survivor never recorded the STR's reply")
	}
	deliveriesMu.Lock()
	deliverA, deliverB := deliveries[corrA], deliveries[corrB]
	deliveriesMu.Unlock()
	gated.armed.Store(true)
	go deliverA(tool.Error(holderTimeoutResult))
	<-gated.entered

	deliverB(tool.OK("other ready"))

	if brieflyBecomes(func() bool { return len(holderLLM.getCalls()) > 1 }) {
		t.Fatal("the turn resumed before the recorded result of the timed-out call was read")
	}
	release()
	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-batch", "report ready")
	assertNoTimeoutSeen(t, holderLLM)
}

func assertNoTimeoutSeen(t *testing.T, mock *mockLLM) {
	t.Helper()
	for _, msg := range mock.getCalls()[1].Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "SANDBOX_TIMEOUT") {
			t.Errorf("the resumed turn was told the call timed out although the STR's reply was recorded: %q", msg.Content)
		}
	}
}
