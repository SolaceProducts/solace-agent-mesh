// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const adoptedReplySkippedLog = "recorded a remote tool's reply for a task paused on another replica; result signals, artifact notification and retry were skipped"

func strErrorReply(corrID string, code remotetool.ErrorCode) []byte {
	return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%q,"error":{"code":%q,"message":"the runtime reported a failure"}}`, corrID, code)
}

func retryableSTRReply(corrID string) []byte {
	return strErrorReply(corrID, remotetool.ErrToolNotAvailable)
}

func scriptedSTR(t *testing.T, brk *broker.DevBroker, firstReplyGate <-chan struct{}, replies ...func(corrID string) []byte) {
	t.Helper()
	q, err := brk.CreateQueue("str-"+idgen.New(), broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create STR queue: %v", err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic("test-ns", remoteReportTool)); err != nil {
		t.Fatalf("subscribe STR queue to %s invokes: %v", remoteReportTool, err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for i, reply := range replies {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil {
				return
			}
			msg.Ack()
			if i == 0 {
				select {
				case <-firstReplyGate:
				case <-ctx.Done():
					return
				}
			}
			if err := brk.Publish(context.Background(), msg.UserProperties[remotetool.PropReplyTo], &broker.Message{Payload: reply(msg.CorrelationID)}); err != nil {
				t.Errorf("publish STR reply: %v", err)
			}
		}
	}()
}

type recordedResultsSpy struct {
	session.Store
	mu      sync.Mutex
	results []session.ToolCallResult
}

func (s *recordedResultsSpy) RecordToolResult(ctx context.Context, taskID, corrID string, result *session.ToolCallResult) (int, error) {
	s.mu.Lock()
	s.results = append(s.results, *result)
	s.mu.Unlock()
	return s.Store.RecordToolResult(ctx, taskID, corrID, result)
}

func (s *recordedResultsSpy) attempts() []session.ToolCallResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.results)
}

type failingCheckpointReads struct {
	session.Store
}

func (failingCheckpointReads) GetCheckpoint(context.Context, string) (*session.TaskCheckpoint, error) {
	return nil, errors.New("store unavailable")
}

func TestAnAdoptedRemoteCallWhoseCallerCannotBeReadIsAuditedAndWarnedAbout(t *testing.T) {
	const unattributedCallLog = "could not read the paused task to attribute an adopted remote tool call; its audit event carries no caller"
	base := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, base, "owner")
	logs := &syncLogBuffer{}
	survivor, _, _ := replicaOf(t, failingCheckpointReads{Store: base}, "survivor", capturingLogs(logs))
	rec := &toolAuditRecorder{}
	survivor.svc.(*mockServices).auditLog = rec
	registerRemoteTool(t, survivor, strBroker(t))
	const taskID = "task-remote-unattributed"
	corrID := idgen.New()
	pausedOnRemoteCallLostOwner(t, base, owner, taskID, corrID)

	survivor.adoptOrphanReply(survivor.remoteToolReplyAdoption(corrID, failingSTRReply(corrID)))

	records := logs.records(unattributedCallLog)
	if len(records) != 1 {
		t.Fatalf("records = %v, want exactly one", records)
	}
	if record := records[0]; record["level"] != "WARN" || record["taskID"] != taskID || record["corrID"] != corrID || record["tool"] != remoteReportTool {
		t.Errorf("record = %v, want WARN with taskID %q, corrID %q, tool %q", record, taskID, corrID, remoteReportTool)
	}
	if got := len(rec.eventsFor(remoteReportTool)); got != 1 {
		t.Errorf("audit events = %d, want the call still audited once", got)
	}
}

func TestARetryableRemoteToolFailureIsLeftUnrecordedByAReplicaThatDoesNotHoldTheTask(t *testing.T) {
	tests := []struct {
		name         string
		code         remotetool.ErrorCode
		wantRecorded bool
	}{
		{"a retryable failure", remotetool.ErrToolNotAvailable, false},
		{"a final failure", remotetool.ErrExecutionError, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			owner, _, _ := replicaOf(t, store, "owner")
			survivor, _, _ := replicaOf(t, store, "survivor")
			rec := &toolAuditRecorder{}
			survivor.svc.(*mockServices).auditLog = rec
			registerRemoteTool(t, survivor, strBroker(t))
			const taskID = "task-remote-retryable"
			corrID := idgen.New()
			pausedOnRemoteCallLostOwner(t, store, owner, taskID, corrID)

			survivor.adoptOrphanReply(survivor.remoteToolReplyAdoption(corrID, strErrorReply(corrID, tt.code)))

			row, err := store.LookupPendingCall(t.Context(), "test-agent", corrID)
			if err != nil {
				t.Fatalf("look up the pending call: %v", err)
			}
			if gotRecorded := row == nil; gotRecorded != tt.wantRecorded {
				t.Errorf("the call was resolved by the adopting replica = %v, want %v", gotRecorded, tt.wantRecorded)
			}
			wantAudits := 0
			if tt.wantRecorded {
				wantAudits = 1
			}
			if got := len(rec.eventsFor(remoteReportTool)); got != wantAudits {
				t.Errorf("audit events from the adopting replica = %d, want %d", got, wantAudits)
			}
		})
	}
}

func TestAHoldersRetryOfARetryableRemoteFailureIsAuditedAndTheOtherReplicaStaysOut(t *testing.T) {
	store := &recordedResultsSpy{Store: session.NewMemoryStore()}
	holder, holderLLM, _ := replicaOf(t, store, "holder")
	other, _, _ := replicaOf(t, store, "other")
	holderAudit, otherAudit := &toolAuditRecorder{}, &toolAuditRecorder{}
	holder.svc.(*mockServices).auditLog = holderAudit
	other.svc.(*mockServices).auditLog = otherAudit
	strBrk := strBroker(t)
	registerRemoteTool(t, holder, strBrk)
	registerRemoteTool(t, other, strBrk)
	other.installRemoteToolOrphanHandlers()
	holderLLM.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool), {Content: "done"}}
	paused := make(chan struct{})
	scriptedSTR(t, strBrk, paused,
		retryableSTRReply,
		func(corrID string) []byte { return remoteToolReplyPayload(t, corrID, "report ready") })

	if resp, err := holder.HandleTask(t.Context(), "task-remote-retry", messageWithParentSession("build the report"), holderUser()); err != nil || resp != nil {
		t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
	}
	close(paused)
	if !waitUntilWithin(15*time.Second, func() bool {
		return len(holderLLM.getCalls()) == 2 && holder.pausedTasks.Load("task-remote-retry") == nil
	}) {
		t.Fatalf("the task did not resume and finish: %d LLM calls", len(holderLLM.getCalls()))
	}

	if !requestContains(holderLLM.getCalls()[1], "report ready") {
		t.Error("the resumed turn did not see the holder's successful retry")
	}
	stored := store.attempts()
	if len(stored) != 1 || stored[0].IsError || !strings.Contains(stored[0].Content, "report ready") {
		t.Errorf("results recorded against the checkpoint = %+v, want only the holder's successful retry", stored)
	}
	holderEvents := holderAudit.eventsFor(remoteReportTool)
	if len(holderEvents) != 1 || !holderEvents[0].Success {
		t.Errorf("holder audit events = %+v, want exactly one success: the retry is what the call finally did", holderEvents)
	}
	if otherEvents := otherAudit.eventsFor(remoteReportTool); len(otherEvents) != 0 {
		t.Errorf("the other replica's audit events = %+v, want none: it must not record the intermediate failure", otherEvents)
	}
}

func TestAReplyIsLoggedAsSkippingPostProcessingOnlyWhereItIsAdopted(t *testing.T) {
	tests := []struct {
		name            string
		alreadyResolved bool
		unknownCall     bool
		wantLogs        int
	}{
		{"a reply the replica adopts", false, false, 1},
		{"a reply for a call already resolved elsewhere", true, false, 0},
		{"a reply for a call nobody awaits", false, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			owner, _, _ := replicaOf(t, store, "owner")
			logs := &syncLogBuffer{}
			survivor, _, _ := replicaOf(t, store, "survivor", capturingLogs(logs))
			registerRemoteTool(t, survivor, strBroker(t))
			const taskID = "task-remote-adopt-log"
			corrID := idgen.New()
			pausedOnRemoteCallLostOwner(t, store, owner, taskID, corrID)
			replyFor := corrID
			if tt.unknownCall {
				replyFor = idgen.New()
			}
			if tt.alreadyResolved {
				if _, err := store.RecordToolResult(t.Context(), taskID, corrID, &session.ToolCallResult{ToolCallID: "tc-1", ToolName: remoteReportTool, Content: "resolved elsewhere"}); err != nil {
					t.Fatalf("resolve the call as another replica would: %v", err)
				}
			}

			survivor.adoptOrphanReply(survivor.remoteToolReplyAdoption(replyFor, remoteToolReplyPayload(t, replyFor, "report ready")))

			records := logs.records(adoptedReplySkippedLog)
			if len(records) != tt.wantLogs {
				t.Fatalf("records = %v, want %d", records, tt.wantLogs)
			}
			if tt.wantLogs == 0 {
				return
			}
			record := records[0]
			if record["level"] != "DEBUG" || record["taskID"] != taskID || record["corrID"] != corrID || record["tool"] != remoteReportTool {
				t.Errorf("record = %v, want DEBUG with taskID %q, corrID %q, tool %q", record, taskID, corrID, remoteReportTool)
			}
		})
	}
}
