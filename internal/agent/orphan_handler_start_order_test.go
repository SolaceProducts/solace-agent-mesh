// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

const unknownReplyLog = "response for unknown correlation ID"

type replyWhileStarting struct {
	t      *testing.T
	strBrk *broker.DevBroker
	corrID string
	logs   *syncLogBuffer
}

func (r *replyWhileStarting) Start(context.Context) error {
	publishSTRReply(r.t, r.strBrk, remotetool.ResponseTopic("test-ns", "test-agent", r.corrID), r.corrID, "report ready")
	waitUntilWithin(300*time.Millisecond, func() bool { return len(r.logs.records(unknownReplyLog)) > 0 })
	return nil
}

func (r *replyWhileStarting) Stop() {}

func TestAReplyThatArrivesWhileTheAgentIsStartingIsRecordedNotDropped(t *testing.T) {
	const taskID, corrID = "task-start-race", "corr-start-race"
	strBrk := strBroker(t)
	logs := &syncLogBuffer{}
	exe := remote.NewExecutor(strBrk, "test-ns", "test-agent", remote.WithLogger(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	var mock *mockLLM
	startAgentOnBroker(t, true, newMockBroker(), func(a *Agent) {
		mock = a.svc.(*mockServices).llmMap[""].(*mockLLM)
		mock.responses = []*llm.ChatResponse{{Content: "The report is ready."}}
		owner, _, _ := replicaOf(t, a.svc.Sessions(), "owner")
		pausedOnRemoteCallLostOwner(t, a.svc.Sessions(), owner, taskID, corrID)
		registerRemoteToolOn(t, a, exe, remoteReportTool)
		a.AddSubComponent(exe)
		a.AddSubComponent(&replyWhileStarting{t: t, strBrk: strBrk, corrID: corrID, logs: logs})
	})

	if !waitUntil(func() bool { return len(mock.getCalls()) > 0 }) {
		t.Fatalf("the task was never resumed; %d replies were dropped as unknown because the agent had not yet installed its handler for them", len(logs.records(unknownReplyLog)))
	}
	if !requestContains(mock.getCalls()[0], "report ready") {
		t.Error("the resumed turn did not see the STR's reply")
	}
}
