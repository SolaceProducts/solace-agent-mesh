// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func spendServices(t *testing.T) (runtime.Services, *broker.DevBroker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	return runtime.NewRuntime(brk, config.FromMap(nil)).Services(), brk
}

func TestSpendPublishers_AgentsGetAPublisher(t *testing.T) {
	svc, _ := spendServices(t)
	pubs := newSpendPublishers(svc)
	t.Cleanup(func() { _ = pubs.close(context.Background()) })

	if pubs.forNamespace("ns") == nil {
		t.Error("no publisher for an agent in a process with a broker")
	}
}

func TestSpendPublishers_AgentsInOneNamespaceShareAPublisher(t *testing.T) {
	svc, _ := spendServices(t)
	pubs := newSpendPublishers(svc)
	t.Cleanup(func() { _ = pubs.close(context.Background()) })

	first, second, other := pubs.forNamespace("ns"), pubs.forNamespace("ns"), pubs.forNamespace("other")

	if first != second {
		t.Error("two agents in one namespace got different publishers")
	}
	if first == other {
		t.Error("two namespaces share a publisher")
	}
}

func TestSpendPublishers_CloseDeliversWhatWasRecorded(t *testing.T) {
	deliversRecordedEventWhenClosedWith(t.Context(), t)
}

func TestSpendPublishers_CloseStillDeliversWhenTheShutdownContextIsAlreadySpent(t *testing.T) {
	spent, cancel := context.WithCancel(t.Context())
	cancel()
	deliversRecordedEventWhenClosedWith(spent, t)
}

func deliversRecordedEventWhenClosedWith(closeCtx context.Context, t *testing.T) {
	t.Helper()
	svc, brk := spendServices(t)
	q := subscribedQueue(t, brk, "spend-test", a2a.SpendEventTopic("ns"))
	pubs := newSpendPublishers(svc)
	event := spend.Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "user-1",
		TriggerKind: spend.TriggerChat, Surface: spend.SurfaceAgent, ModelName: "m", TaskID: "task-1", TraceID: "trace-1",
	}
	pubs.forNamespace("ns").Record(event)

	if err := pubs.close(closeCtx); err != nil {
		t.Fatalf("close: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no event delivered by close: %v", err)
	}
	var got spend.Event
	if err := json.Unmarshal(msg.Payload, &got); err != nil || got.ID != event.ID {
		t.Errorf("delivered %+v (err %v), want event %s", got, err, event.ID)
	}
}

type slowPublishBroker struct {
	*broker.DevBroker
	release chan struct{}
}

func (b *slowPublishBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func TestNewAWEExecutor_StopFlushesSpendStillBufferedInThePublisher(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	q := subscribedQueue(t, dev, "spend-test", a2a.SpendEventTopic("ns"))
	brk := &slowPublishBroker{DevBroker: dev, release: make(chan struct{})}
	svc := runtime.NewRuntime(brk, config.FromMap(nil)).Services()
	exe, pubs := newAWEExecutor(svc)

	const recorded = 3
	pub := pubs.forNamespace("ns")
	for range recorded {
		pub.Record(spend.Event{
			ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "user-1",
			TriggerKind: spend.TriggerChat, Surface: spend.SurfaceAgent, ModelName: "m", TaskID: "task-1", TraceID: "trace-1",
		})
	}

	stopped := make(chan error, 1)
	go func() { stopped <- exe.Stop(t.Context()) }()
	select {
	case err := <-stopped:
		t.Fatalf("stop returned (%v) while spend was still held by the broker; it did not wait for the flush", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(brk.release)
	if err := <-stopped; err != nil {
		t.Fatalf("stop: %v", err)
	}

	for i := range recorded {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		_, err := dev.Receive(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("event %d of %d not delivered by stop: %v", i+1, recorded, err)
		}
	}
}

type usageLLM struct{}

func (usageLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "done", FinishReason: "stop", Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 2}}, nil
}

func (usageLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{ContentDelta: "done", FinishReason: "stop", Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 2}}
	close(ch)
	return ch, nil
}

func TestNewAWEExecutor_AnAgentItBuildsRecordsItsSpend(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	svc := runtime.NewRuntime(brk, config.FromMap(nil),
		runtime.WithDefaultLLM(usageLLM{}),
		runtime.WithSessions(session.NewMemoryStore())).Services()
	q := subscribedQueue(t, brk, "spend-test", a2a.SpendEventTopic("ns"))
	exe := NewAWEExecutor(svc)
	err := exe.LoadConfig(config.FromMap(map[string]any{
		"kind": "agent", "name": "spend-agent",
		config.KeyAppConfig: map[string]any{
			"agent_name": "spend-agent", "namespace": "ns", "model": "test-model", "instruction": "answer",
		},
	}))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	if err = exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err = exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ag, ok := exe.Instances()[0].(*agent.Agent)
	if !ok {
		t.Fatalf("instance is %T, want an agent", exe.Instances()[0])
	}
	if !ag.CountsContextSavings() {
		t.Error("the agent does not count Smart Context Management savings in a process that records spend")
	}

	if _, err = ag.HandleTaskToCompletion(t.Context(), idgen.New(), a2a.Message{
		Kind: "message", MessageID: idgen.New(), Role: a2a.RoleUser, Parts: []a2a.Part{{Kind: "text", Text: "hi"}},
	}, a2a.UserProperties{UserID: "user-1"}); err != nil {
		t.Fatalf("task: %v", err)
	}
	if err = exe.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no spend event published by the agent: %v", err)
	}
	var got spend.Event
	if err = json.Unmarshal(msg.Payload, &got); err != nil || got.UserSub != "user-1" || got.Tokens.Output != 2 {
		t.Errorf("published %+v (err %v), want user-1's call with 2 output tokens", got, err)
	}
	if got.AvoidedInputBytes == nil || *got.AvoidedInputBytes != 0 {
		t.Errorf("published avoided bytes %v, want 0: a counted call with nothing withheld, not an uncounted one", got.AvoidedInputBytes)
	}
}

const spendWorkflowReplyTopic = "test/spend-workflow/reply"

const (
	decisionAnswerWithUsage    = `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.9}},"usage":{"input_tokens":40,"output_tokens":3}}`
	decisionAnswerWithoutUsage = `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.9}}}`
)

func decisionProvider(t *testing.T, answer string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func subscribedQueue(t *testing.T, brk *broker.DevBroker, name, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func runDecisionWorkflow(t *testing.T, answer string) (*broker.DevBroker, broker.Queue) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: true})
	t.Setenv(features.EnvVarName(features.KeyWorkflowDecisionNode), "")
	svc := runtime.NewRuntime(brk, config.FromMap(nil), runtime.WithFeatureRegistry(reg)).Services()
	spendQueue := subscribedQueue(t, brk, "spend-test", a2a.SpendEventTopic("ns"))
	replyQueue := subscribedQueue(t, brk, "reply-test", spendWorkflowReplyTopic)

	exe := NewAWEExecutor(svc)
	if err := exe.LoadConfig(config.FromMap(map[string]any{
		"kind": "workflow", "name": "spend-workflow",
		config.KeyAppConfig: map[string]any{
			"name": "spend-workflow", "namespace": "ns",
			"workflow": map[string]any{
				"decision":       map[string]any{"api_base": decisionProvider(t, answer), "api_key": "k", "model": "jev-1.13.0"},
				"output_mapping": map[string]any{"urgent": "{{triage.output.answers.urgent.value}}"},
				"nodes": []any{map[string]any{
					"id": "triage", "type": "decision",
					"questions": map[string]any{"urgent": map[string]any{"type": "boolean", "instructions": "The ticket is urgent"}},
				}},
			},
		},
	})); err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	payload, err := json.Marshal(a2a.NewRequest(idgen.New(), "message/send", map[string]any{"message": a2a.Message{
		Kind: "message", MessageID: idgen.New(), Role: a2a.RoleUser, ContextID: "session-1",
		Parts:    []a2a.Part{{Kind: "data", Data: map[string]any{"ticket": "charged twice"}}},
		Metadata: map[string]any{"workflow_name": "parent-wf", "node_id": "child", "parentTaskId": "parent-task"},
	}}))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if err = brk.Publish(t.Context(), a2a.AgentRequestTopic("ns", "spend-workflow"), &broker.Message{
		Payload: payload,
		UserProperties: map[string]string{
			a2a.PropUserID: "user-1", a2a.PropReplyTo: spendWorkflowReplyTopic, a2a.PropTrigger: a2a.TriggerInteractive,
			a2a.PropDelegatingAgentName: "parent-wf",
		},
	}); err != nil {
		t.Fatalf("publish request: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err = brk.Receive(ctx, replyQueue); err != nil {
		t.Fatalf("the workflow did not answer: %v", err)
	}
	if err = exe.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	return brk, spendQueue
}

func TestNewAWEExecutor_AWorkflowItBuildsRecordsItsDecisionSpend(t *testing.T) {
	brk, spendQueue := runDecisionWorkflow(t, decisionAnswerWithUsage)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, spendQueue)
	if err != nil {
		t.Fatalf("no spend event published by the workflow: %v", err)
	}
	var got spend.Event
	if err = json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got.UserSub != "user-1" || got.Surface != spend.SurfaceDecision || got.WorkflowName != "spend-workflow" ||
		got.WorkflowNodeID != "triage" || got.Tokens != (spend.Tokens{UncachedInput: 40, Output: 3}) {
		t.Errorf("published %+v, want user-1's decision call at spend-workflow/triage with 40 input and 3 output tokens", got)
	}
	if got.TriggerKind != spend.TriggerWorkflow || got.TriggerID != "parent-wf" || got.ParentTaskID != "parent-task" {
		t.Errorf("published trigger %s/%q parent %q, want the dispatching workflow parent-wf and its task", got.TriggerKind, got.TriggerID, got.ParentTaskID)
	}
}

func TestNewAWEExecutor_AWorkflowRecordsNoSpendForADecisionThatReportsNoUsage(t *testing.T) {
	brk, spendQueue := runDecisionWorkflow(t, decisionAnswerWithoutUsage)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if msg, err := brk.Receive(ctx, spendQueue); err == nil {
		t.Errorf("published %s for a call that reported no tokens, want nothing", msg.Payload)
	}
}

type scriptedLLM struct {
	mu        sync.Mutex
	responses []*llm.ChatResponse
}

func (s *scriptedLLM) next() *llm.ChatResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.responses) == 0 {
		return &llm.ChatResponse{Content: "done", FinishReason: "stop", Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 2}}
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp
}

func (s *scriptedLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return s.next(), nil
}

func (s *scriptedLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	resp := s.next()
	ch := make(chan llm.StreamChunk, 3)
	if resp.Content != "" {
		ch <- llm.StreamChunk{ContentDelta: resp.Content}
	}
	if len(resp.ToolCalls) > 0 {
		deltas := make([]llm.ToolCallDelta, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			deltas[i] = llm.ToolCallDelta{Index: i, ID: tc.ID, Name: tc.Name, ArgumentsDelta: tc.Arguments}
		}
		ch <- llm.StreamChunk{ToolCallDeltas: deltas}
	}
	ch <- llm.StreamChunk{FinishReason: resp.FinishReason, Usage: resp.Usage}
	close(ch)
	return ch, nil
}

func TestNewAWEExecutor_ADataToolsWithheldResultReachesTheNextCallsSpendEvent(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	usage := &llm.Usage{PromptTokens: 10, CompletionTokens: 2}
	model := &scriptedLLM{responses: []*llm.ChatResponse{
		{FinishReason: "tool_calls", Usage: usage, ToolCalls: []llm.ToolCall{{
			ID: "c1", Name: "transform_data_with_jmespath",
			Arguments: `{"input_filename":"accounts.json","jmespath_expression":"[*]"}`,
		}}},
		{Content: "done", FinishReason: "stop", Usage: usage},
	}}
	svc := runtime.NewRuntime(brk, config.FromMap(nil),
		runtime.WithDefaultLLM(model),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithArtifacts(&artifact.ScopedService{Inner: artmem.New()})).Services()
	q := subscribedQueue(t, brk, "spend-savings-test", a2a.SpendEventTopic("ns"))
	exe := NewAWEExecutor(svc)
	err := exe.LoadConfig(config.FromMap(map[string]any{
		"kind": "agent", "name": "savings_agent",
		config.KeyAppConfig: map[string]any{
			"agent_name": "savings_agent", "namespace": "ns", "model": "test-model", "instruction": "answer",
			"tools": []any{map[string]any{
				"tool_type": "builtin", "tool_name": "transform_data_with_jmespath",
				"tool_config": map[string]any{"max_result_preview_bytes": savingsTestPreviewBytes},
			}},
		},
	}))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	if err = exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err = exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ag, ok := exe.Instances()[0].(*agent.Agent)
	if !ok {
		t.Fatalf("instance is %T, want an agent", exe.Instances()[0])
	}

	accounts := make([]map[string]any, 80)
	for i := range accounts {
		accounts[i] = map[string]any{"id": i, "name": fmt.Sprintf("Account %02d", i), "notes": strings.Repeat("n", 200)}
	}
	compact, err := json.Marshal(accounts)
	if err != nil {
		t.Fatal(err)
	}
	toolCtx := &tool.Context{ArtifactService: svc.Artifacts(), AgentName: "savings_agent", UserID: "user-1", SessionID: "sess-1"}
	if _, err = toolCtx.Facade().SaveArtifact(t.Context(), "accounts.json", compact, "application/json"); err != nil {
		t.Fatalf("save the input artifact: %v", err)
	}

	if _, err = ag.HandleTaskToCompletion(t.Context(), idgen.New(), a2a.Message{
		Kind: "message", MessageID: idgen.New(), ContextID: "sess-1", Role: a2a.RoleUser,
		Parts: []a2a.Part{{Kind: "text", Text: "transform the accounts"}},
	}, a2a.UserProperties{UserID: "user-1"}); err != nil {
		t.Fatalf("task: %v", err)
	}
	if err = exe.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var events []spend.Event
	for len(events) < 2 {
		msg, recvErr := brk.Receive(ctx, q)
		if recvErr != nil {
			t.Fatalf("spend event %d: %v", len(events)+1, recvErr)
		}
		var e spend.Event
		if err = json.Unmarshal(msg.Payload, &e); err != nil {
			t.Fatalf("decode spend event: %v", err)
		}
		events = append(events, e)
	}
	if events[0].AvoidedInputBytes == nil || *events[0].AvoidedInputBytes != 0 {
		t.Errorf("the call that asked for the tool avoided %v bytes, want 0", events[0].AvoidedInputBytes)
	}
	if events[1].AvoidedInputBytes == nil {
		t.Fatal("the call after the tool carries no avoided bytes")
	}
	preview := longestPrefixWithin(t, accounts, savingsTestPreviewBytes)
	if want := int64(len(compact) - len(preview)); *events[1].AvoidedInputBytes != want {
		t.Errorf("the call after the tool avoided %d bytes, want %d: the %d-byte compact result net of its %d-byte preview", *events[1].AvoidedInputBytes, want, len(compact), len(preview))
	}
}

const savingsTestPreviewBytes = 1024

func longestPrefixWithin(t *testing.T, items []map[string]any, maxBytes int) []byte {
	t.Helper()
	preview, err := json.Marshal(items[:1])
	if err != nil {
		t.Fatal(err)
	}
	for n := 2; n <= len(items); n++ {
		encoded, err := json.Marshal(items[:n])
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > maxBytes {
			break
		}
		preview = encoded
	}
	return preview
}
