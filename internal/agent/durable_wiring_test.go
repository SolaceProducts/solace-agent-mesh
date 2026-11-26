// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type subscriptionRecordingBroker struct {
	*mockBroker

	mu          sync.Mutex
	topics      []string
	queueTopics map[string][]string
}

func (b *subscriptionRecordingBroker) Subscribe(q broker.Queue, topic string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.topics = append(b.topics, topic)
	if b.queueTopics == nil {
		b.queueTopics = make(map[string][]string)
	}
	b.queueTopics[q.Name()] = append(b.queueTopics[q.Name()], topic)
	return b.mockBroker.Subscribe(q, topic)
}

func (b *subscriptionRecordingBroker) subscribedOn(queueName, topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Contains(b.queueTopics[queueName], topic)
}

func (b *subscriptionRecordingBroker) subscribed(topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Contains(b.topics, topic)
}

func progressPublisher(brk broker.Broker) *StatusPublisher {
	return NewStatusPublisher(brk, StatusPublisherConfig{
		StatusTopic: "test-ns/a2a/v1/gateway/status/gw/task-status",
		TaskID:      "task-status",
		AgentName:   "test-agent",
	}, slog.Default())
}

func TestResumedTurnLetsRemoteCallsPauseAgainOnlyWhenTheAgentCheckpoints(t *testing.T) {
	durable, _, _ := replicaOf(t, session.NewMemoryStore(), "durable")
	blocking, _, _ := newTestAgent()
	sp := progressPublisher(newMockBroker())

	if toolCtx := durable.buildToolCtxFromState(t.Context(), fullTaskState(), sp); !toolCtx.PauseRemoteTools {
		t.Error("a resumed turn of a checkpointing agent does not let remote calls pause")
	}
	if toolCtx := blocking.buildToolCtxFromState(t.Context(), fullTaskState(), sp); toolCtx.PauseRemoteTools {
		t.Error("a resumed turn of an agent that does not checkpoint lets remote calls pause")
	}
}

func startAgentOnBroker(t *testing.T, durable bool, brk broker.Broker, configure ...func(*Agent)) *Agent {
	t.Helper()
	agent := New(AgentConfig{Name: "test-agent", Model: "test-model", MaxLLMCalls: 5, Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), session.NewMemoryStore())
	svc.brk = brk
	svc.featureRegistry = durableResumeRegistry(durable)
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, c := range configure {
		c(agent)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	return agent
}

func TestAgentThatCheckpointsListensForAnswersOnTheNotifyResponseTopic(t *testing.T) {
	brk := &subscriptionRecordingBroker{mockBroker: newMockBroker()}

	startAgentOnBroker(t, true, brk)

	if !brk.subscribed(a2a.NotifyResponseSubscription("test-ns", "test-agent")) {
		t.Errorf("subscriptions = %q, want the notify-response subscription although notify_service is off", brk.topics)
	}
}

func TestAgentThatDoesNotCheckpointLeavesTheNotifyResponseTopicAlone(t *testing.T) {
	brk := &subscriptionRecordingBroker{mockBroker: newMockBroker()}

	startAgentOnBroker(t, false, brk)

	if brk.subscribed(a2a.NotifyResponseSubscription("test-ns", "test-agent")) {
		t.Errorf("subscriptions = %q, want no notify-response subscription", brk.topics)
	}
}

func TestAgentQueuesNotifyAnswersOnItsRequestQueueOnlyWhenItCheckpoints(t *testing.T) {
	requestQueue := a2a.QueueName("test-ns", "a2a", "test-agent")
	notifyAnswers := a2a.NotifyResponseSubscription("test-ns", "test-agent")
	for _, tc := range []struct {
		name       string
		durable    bool
		wantQueued bool
	}{
		{name: "checkpointing agent queues notify answers so one spools while no AWE is connected", durable: true, wantQueued: true},
		{name: "agent that does not checkpoint leaves its request queue off the notify topic", durable: false, wantQueued: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brk := &subscriptionRecordingBroker{mockBroker: newMockBroker()}

			startAgentOnBroker(t, tc.durable, brk)

			if got := brk.subscribedOn(requestQueue, notifyAnswers); got != tc.wantQueued {
				t.Errorf("request queue subscribed to %q = %v, want %v", notifyAnswers, got, tc.wantQueued)
			}
		})
	}
}

func TestMaintenanceTickCollectsAnAnswerAnotherReplicaRecorded(t *testing.T) {
	store := session.NewMemoryStore()
	faster := func(a *Agent) { a.checkpoints.interval = 10 * time.Millisecond }
	holder, holderLLM, holderBrk := replicaOf(t, store, "holder", faster)
	other, _, _ := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")

	if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("routeUserInputResponse() = %v, want delivered", got)
	}

	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-ask", "EMEA")
}

func TestStartingAnAgentWithARemoteToolTakesRepliesForTasksPausedElsewhere(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	strBrk := strBroker(t)
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor", func(a *Agent) { registerRemoteTool(t, a, strBrk) })
	corrID := idgen.New()
	pausedOnRemoteCallLostOwner(t, store, owner, "task-remote-start", corrID)

	publishSTRReply(t, strBrk, remotetool.ResponseTopic("test-ns", "test-agent", corrID), corrID, "report ready")

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, "task-remote-start", "report ready")
}

func TestAgentThatDoesNotCheckpointRunsARemoteToolCallToCompletionInline(t *testing.T) {
	agent, mock, _ := newTestAgent()
	rec := &toolAuditRecorder{}
	agent.svc.(*mockServices).auditLog = rec
	agent.svc.(*mockServices).brk = newMockBroker()
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool), {Content: "The report is ready."}}
	ids, release := heldSTR(t, strBrk, "report ready")
	finished := make(chan *a2a.Response, 1)
	go func() {
		resp, _ := agent.HandleTask(t.Context(), "task-inline", concUserMessage("build the report", idgen.New()), concurrentUser)
		finished <- resp
	}()
	waitForCorrID(t, ids)
	if agent.pausedTasks.Load("task-inline") != nil {
		t.Fatal("the task paused on a remote call although the agent does not checkpoint")
	}

	time.Sleep(callRunsFor)
	release()
	select {
	case resp := <-finished:
		if resp == nil {
			t.Fatal("HandleTask() returned no response: the call did not complete inline")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the task never finished")
	}

	events := rec.eventsFor(remoteReportTool)
	if len(events) != 1 || !events[0].Success || events[0].DurationMs < float64(callRunsFor.Milliseconds()) {
		t.Errorf("audit events = %+v, want one successful execution that lasted at least %v", events, callRunsFor)
	}
}
