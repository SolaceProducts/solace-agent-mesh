// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

const hilTestOwner = "u-test"

const (
	hilAlice = "alice@dev.local"
	hilBob   = "bob@dev.local"

	controlRefusedLogMsg  = "control response refused: sender does not own the paused task"
	noMatchingRequestMsg  = "received user_input_response with no matching pending request"
	hilAnswerSecretMarker = "answer-secret-marker"
)

func (a *Agent) deliverHILResponseAsOwner(resp *a2a.UserInputResponseData) bool {
	return a.deliverHILResponse(resp, hilSenderUser(hilTestOwner)) == hilDelivered
}

func (a *Agent) deliverHILResponseToPausedTaskAsOwner(resp *a2a.UserInputResponseData) bool {
	return a.deliverHILResponseToPausedTask(resp, hilSenderUser(hilTestOwner)) == hilDelivered
}

type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncLogBuffer) records(msg string) []map[string]any {
	var out []map[string]any
	for raw := range bytes.SplitSeq([]byte(b.String()), []byte("\n")) {
		var rec map[string]any
		if json.Unmarshal(raw, &rec) == nil && rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

func captureAgentLogs(agent *Agent) *syncLogBuffer {
	logs := &syncLogBuffer{}
	agent.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logs
}

func answerOnTheWire(t *testing.T, agent *Agent, taskID, userID string, response any) {
	t.Helper()
	msg := a2a.Message{
		Kind: "message", MessageID: idgen.New(), Role: a2a.RoleUser,
		Parts:  []a2a.Part{a2a.NewDataPart(response)},
		TaskID: taskID,
	}
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": msg}))
	if err != nil {
		t.Fatalf("marshal control message: %v", err)
	}
	var req a2a.Request
	if err = json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("decode control message: %v", err)
	}
	agent.processMessage(t.Context(), &broker.Message{
		Payload: payload,
		UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{
			Timestamp: time.Now().UnixMilli(), ClientID: "web-1", UserID: userID,
		}),
	}, &req)
}

func userInputResponse(requestID, status string, data map[string]any) a2a.UserInputResponseData {
	return a2a.UserInputResponseData{Type: "user_input_response", RequestID: requestID, Status: status, Data: data}
}

func TestHILSenderMayAnswer(t *testing.T) {
	const kelvin = "\u212a"
	for _, tc := range []struct {
		name   string
		sender hilSender
		owner  string
		want   bool
	}{
		{"the owner", hilSenderUser(hilAlice), hilAlice, true},
		{"the owner in another ASCII case", hilSenderUser("Alice@Dev.Local"), hilAlice, true},
		{"another user", hilSenderUser(hilBob), hilAlice, false},
		{"no user, owner known", hilSenderUser(""), hilAlice, false},
		{"a user, no owner recorded", hilSenderUser(hilBob), "", false},
		{"no user, no owner recorded", hilSenderUser(""), "", true},
		{"a look-alike rune", hilSenderUser(kelvin + "im@dev.local"), "kim@dev.local", false},
		{"the notification service", hilSenderNotifyService, hilAlice, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sender.mayAnswer(tc.owner); got != tc.want {
				t.Errorf("mayAnswer(%q) = %v, want %v", tc.owner, got, tc.want)
			}
		})
	}
}

func TestDeliverHILResponseOwnership(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	type fixture struct {
		agent     *Agent
		requestID string
		untouched func() bool
	}
	blockedTool := func(t *testing.T) fixture {
		agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())
		ch := agent.registerHILRequest("req-blocked", hilAlice, "")
		return fixture{agent, "req-blocked", func() bool { return len(ch) == 0 }}
	}
	pausedApproval := func(t *testing.T) fixture {
		agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())
		state := pausedStateWithHILEntry(t, agent, "task-paused", "call-paused", "req-paused")
		state.UserProps.UserID = hilAlice
		return fixture{agent, "req-paused", func() bool {
			state.mu.Lock()
			defer state.mu.Unlock()
			return state.PendingCalls["call-paused"] != nil && resumeCalls.Load() == 0
		}}
	}

	for _, where := range []struct {
		name  string
		build func(*testing.T) fixture
	}{{"blocked tool", blockedTool}, {"paused approval", pausedApproval}} {
		for _, tc := range []struct {
			name   string
			sender hilSender
			want   hilDelivery
		}{
			{"owner", hilSenderUser(hilAlice), hilDelivered},
			{"owner in another ASCII case", hilSenderUser("ALICE@dev.local"), hilDelivered},
			{"another user", hilSenderUser(hilBob), hilRefused},
			{"no user", hilSenderUser(""), hilRefused},
			{"notification service", hilSenderNotifyService, hilDelivered},
		} {
			t.Run(where.name+"/"+tc.name, func(t *testing.T) {
				resumeCalls.Store(0)
				fx := where.build(t)

				got := fx.agent.deliverHILResponse(&a2a.UserInputResponseData{RequestID: fx.requestID, Status: "answered"}, tc.sender)
				if got != tc.want {
					t.Fatalf("deliverHILResponse = %v, want %v", got, tc.want)
				}
				if tc.want == hilRefused {
					if !fx.untouched() {
						t.Error("a refused response reached the request")
					}
					if again := fx.agent.deliverHILResponse(&a2a.UserInputResponseData{RequestID: fx.requestID, Status: "answered"}, hilSenderUser(hilAlice)); again != hilDelivered {
						t.Errorf("the owner's answer after a refusal = %v, want delivered: the refusal must not consume the request", again)
					}
				}
			})
		}
	}
}

func TestDeliverHILResponseUnknownIDIsUnmatchedForEveryone(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())
	for _, sender := range []hilSender{hilSenderUser(hilAlice), hilSenderUser(hilBob), hilSenderNotifyService} {
		if got := agent.deliverHILResponse(&a2a.UserInputResponseData{RequestID: "nobody-waits", Status: "answered"}, sender); got != hilUnmatched {
			t.Errorf("sender %+v: outcome = %v, want unmatched", sender, got)
		}
	}
}

type wiredHILAgent struct {
	agent *Agent
	logs  *syncLogBuffer
	asked chan string
}

const wiredRequestID = "req-wired"

func newWiredHILAgent(t *testing.T) *wiredHILAgent {
	t.Helper()
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	agent := New(AgentConfig{
		Name: "hil-agent", Namespace: "test-ns",
		Model: "test-model", MaxTokens: 1000, MaxLLMCalls: 5,
		SystemPrompt: "You are a test agent.",
	})
	svc := newMockServices(newMockLLM(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "ask", Arguments: "{}"}}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	), newMemSessionStore())
	svc.brk = brk
	agent.svc = svc
	if err := agent.Init(ctx, nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	logs := captureAgentLogs(agent)
	asked := make(chan string, 1)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "ask",
		description: "asks the user a question",
		execFn: func(ic context.Context, _ map[string]any, tCtx *tool.Context) (*tool.Result, error) {
			resp := tCtx.WaitForUserInput(ic, wiredRequestID, "ask_user_question", json.RawMessage(`{"surfaceId":"s"}`), time.Minute) //nolint:staticcheck // exercising the wired HIL waiter
			asked <- resp.Status
			return tool.OK(resp.Status), nil
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	return &wiredHILAgent{agent: agent, logs: logs, asked: asked}
}

func (w *wiredHILAgent) startTaskFor(t *testing.T, taskID, userID string) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	msg := a2a.Message{
		Kind: "message", MessageID: "m1", Role: a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewTextPart("ask me")}, ContextID: "ctx-1",
	}
	props := a2a.UserProperties{
		ClientID: "web-1", UserID: userID,
		A2AStatusTopic: "test-ns/a2a/v1/gateway/status/web-1/" + taskID,
	}
	go func() {
		defer close(done)
		_, _ = w.agent.HandleTask(t.Context(), taskID, msg, props)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !w.pending(wiredRequestID) {
		if time.Now().After(deadline) {
			t.Fatal("the task never blocked on the user")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return done
}

func (w *wiredHILAgent) pending(requestID string) bool {
	w.agent.pendingHILMu.Lock()
	defer w.agent.pendingHILMu.Unlock()
	_, ok := w.agent.pendingHIL[requestID]
	return ok
}

func TestControlResponseOnTheWire_BlockedToolIsAnsweredByItsOwnerOnly(t *testing.T) {
	w := newWiredHILAgent(t)
	done := w.startTaskFor(t, "task-alice", hilAlice)

	answerOnTheWire(t, w.agent, "task-bob", hilBob, userInputResponse(wiredRequestID, "answered", map[string]any{"leak": hilAnswerSecretMarker}))

	select {
	case status := <-w.asked:
		t.Fatalf("bob's response reached alice's tool (status %q)", status)
	case <-done:
		t.Fatal("bob's response ended alice's task")
	case <-time.After(300 * time.Millisecond):
	}
	if !w.pending(wiredRequestID) {
		t.Fatal("bob's refused response consumed alice's pending request")
	}
	refusals := w.logs.records(controlRefusedLogMsg)
	if len(refusals) != 1 {
		t.Fatalf("want exactly one refusal WARN, got %d:\n%s", len(refusals), w.logs.String())
	}
	if refusals[0]["level"] != "WARN" || refusals[0]["requestID"] != wiredRequestID || refusals[0]["userID"] != hilBob || refusals[0]["responseType"] != "user_input_response" {
		t.Errorf("refusal record = %+v, want a WARN naming the request, the sender and the type", refusals[0])
	}
	if got := w.logs.records(noMatchingRequestMsg); len(got) != 0 {
		t.Errorf("a refusal was also logged as an unmatched response: %+v", got)
	}
	if strings.Contains(w.logs.String(), hilAnswerSecretMarker) {
		t.Error("the refusal logged the answer's content")
	}

	answerOnTheWire(t, w.agent, "task-alice-answer", hilAlice, userInputResponse(wiredRequestID, "answered", nil))
	select {
	case status := <-w.asked:
		if status != "answered" {
			t.Errorf("alice's tool saw status %q, want answered", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alice's own answer did not reach her tool")
	}
	<-done
}

func TestControlResponseOnTheWire_UnknownIDGetsNoAnswerEither(t *testing.T) {
	agent, _, _ := newTestAgent()
	logs := captureAgentLogs(agent)

	answerOnTheWire(t, agent, "task-bob", hilBob, userInputResponse("no-such-request", "answered", nil))

	if got := logs.records(noMatchingRequestMsg); len(got) != 1 {
		t.Errorf("want one unmatched-response record, got %d:\n%s", len(got), logs.String())
	}
	if got := logs.records(controlRefusedLogMsg); len(got) != 0 {
		t.Errorf("an unknown id was logged as a refusal: %+v", got)
	}
}

func TestControlResponseOnTheWire_PausedApprovalIsAnsweredByItsOwnerOnly(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })
	agent := newTestAgentWithStubTool(t, "test_hil_tool", alwaysOKTool())
	logs := captureAgentLogs(agent)
	state := pausedStateWithHILEntry(t, agent, "task-alice", "call-1", "hil-approval-1")
	state.UserProps.UserID = hilAlice

	answerOnTheWire(t, agent, "task-bob", hilBob, userInputResponse("hil-approval-1", "answered", map[string]any{"decision": "approved"}))

	state.mu.Lock()
	claimed := state.PendingCalls["call-1"] == nil
	state.mu.Unlock()
	if claimed || resumeCalls.Load() != 0 {
		t.Fatalf("bob approved alice's tool call: claimed=%v resumes=%d", claimed, resumeCalls.Load())
	}
	if got := logs.records(controlRefusedLogMsg); len(got) != 1 || got[0]["taskID"] != "task-alice" || got[0]["requestID"] != "hil-approval-1" {
		t.Errorf("refusal records = %+v, want one naming the task and the request", got)
	}

	answerOnTheWire(t, agent, "task-alice-answer", hilAlice, userInputResponse("hil-approval-1", "answered", map[string]any{"decision": "approved"}))
	if resumeCalls.Load() != 1 {
		t.Errorf("alice's approval resumed the task %d times, want 1", resumeCalls.Load())
	}
}

func TestAuthResponseOnTheWire_NonOwnerIsRefused(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })
	agent := newTestAgentWithStubTool(t, "stubtool", alwaysOKTool())
	logs := captureAgentLogs(agent)
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&stubAuthHandler{key: "test-key", handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return nil
	}})
	state := pausedStateWithAuthEntry(t, agent, "task-alice", "call-1")
	state.UserProps.UserID = hilAlice
	resp := authResp("task-alice", "call-1", "test-key")

	answerOnTheWire(t, agent, "task-bob", hilBob, resp)

	state.mu.Lock()
	claimed := state.PendingCalls["call-1"] == nil
	state.mu.Unlock()
	if exchanges.Load() != 0 || claimed || resumeCalls.Load() != 0 {
		t.Fatalf("bob's auth_response was acted on: exchanges=%d claimed=%v resumes=%d", exchanges.Load(), claimed, resumeCalls.Load())
	}
	refusals := logs.records(controlRefusedLogMsg)
	if len(refusals) != 1 || refusals[0]["responseType"] != "auth_response" || refusals[0]["userID"] != hilBob || refusals[0]["functionCallID"] != "call-1" {
		t.Fatalf("refusal records = %+v, want one auth_response WARN naming the sender and the call", refusals)
	}
	if strings.Contains(logs.String(), resp.Code) {
		t.Error("the refusal logged the authorization code")
	}

	answerOnTheWire(t, agent, "task-alice-callback", "ALICE@dev.local", resp)
	if exchanges.Load() != 1 || resumeCalls.Load() != 1 {
		t.Errorf("alice's own sign-in: exchanges=%d resumes=%d, want 1 and 1", exchanges.Load(), resumeCalls.Load())
	}
}

func publishedToPeer(brk *mockBroker, namespace, peer string) int {
	n := 0
	for _, m := range brk.getPublished() {
		if m.topic == a2a.AgentRequestTopic(namespace, peer) {
			n++
		}
	}
	return n
}

func TestPeerRouterRefusesAForwardFromAnotherUser(t *testing.T) {
	brk := newMockBroker()
	router := NewPeerRouter(brk, "test-ns", testLogger())
	router.Register("sub-1", "gateway/status", "parent-1", "Orchestrator", "Peer", "", "client-1", hilAlice, "", 5*time.Second)
	router.HandleStatus("sub-1", &broker.Message{Payload: uirPayload("sub-1", "req-peer")})
	resp := &a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-peer", Status: "answered"}

	if got := router.ForwardUserInputResponse(t.Context(), "ns", resp, hilSenderUser(hilBob)); got != hilRefused {
		t.Fatalf("bob's forward = %v, want refused", got)
	}
	if n := publishedToPeer(brk, "ns", "Peer"); n != 0 {
		t.Fatalf("a refused response was published to the peer (%d messages)", n)
	}
	if got := router.ForwardUserInputResponse(t.Context(), "ns", resp, hilSenderUser("ALICE@dev.local")); got != hilDelivered {
		t.Errorf("alice's forward after the refusal = %v, want delivered", got)
	}
}

func TestControlResponseOnTheWire_PeerDelegatedPromptIsAnsweredByItsOwnerOnly(t *testing.T) {
	agent, _, _ := newTestAgent()
	logs := captureAgentLogs(agent)
	brk := newMockBroker()
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)
	agent.peerRouter.Register("sub-1", "gateway/status", "parent-1", "test-agent", "Peer", "", "client-1", hilAlice, "", 5*time.Second)
	agent.peerRouter.HandleStatus("sub-1", &broker.Message{Payload: uirPayload("sub-1", "req-peer")})

	answerOnTheWire(t, agent, "task-bob", hilBob, userInputResponse("req-peer", "answered", nil))
	if n := publishedToPeer(brk, "test-ns", "Peer"); n != 0 {
		t.Fatalf("bob's response was forwarded to the peer (%d messages)", n)
	}
	if got := logs.records(controlRefusedLogMsg); len(got) != 1 {
		t.Errorf("want one refusal record, got %d:\n%s", len(got), logs.String())
	}
	if got := logs.records(noMatchingRequestMsg); len(got) != 0 {
		t.Errorf("a refusal was also logged as unmatched: %+v", got)
	}

	answerOnTheWire(t, agent, "task-alice-answer", "ALICE@dev.local", userInputResponse("req-peer", "answered", nil))
	if n := publishedToPeer(brk, "test-ns", "Peer"); n != 1 {
		t.Errorf("alice's response published %d messages to the peer, want 1", n)
	}
}

func TestVolumePromptIsOwnedAndItsIDIsPerPrompt(t *testing.T) {
	volSvc := &mockVolumeService{volumes: []*volume.Volume{}, getVolume: &volume.Volume{ID: "vol-alice", Name: "Alice's", UserID: "alice"}}
	agent := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name:        "test-agent",
		VolumeSlots: []VolumeSlot{{Name: "dataset", Provision: "prompt_user"}},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	}, volSvc)
	logs := captureAgentLogs(agent)

	type outcome struct {
		bindings []tool.VolumeBinding
		err      error
	}
	prompt := func(userID string) <-chan outcome {
		ch := make(chan outcome, 1)
		go func() {
			b, err := agent.resolveVolumeBindings("my-tool", &volumeResolveCtx{
				ctx: t.Context(), sp: &StatusPublisher{}, userID: userID, sessionID: "session-" + userID, canPrompt: true,
			})
			ch <- outcome{b, err}
		}()
		return ch
	}
	pendingVolumePrompts := func() []string {
		agent.pendingHILMu.Lock()
		defer agent.pendingHILMu.Unlock()
		var ids []string
		for id := range agent.pendingHIL {
			if strings.HasPrefix(id, "volume-prompt-dataset") {
				ids = append(ids, id)
			}
		}
		return ids
	}

	aliceDone := prompt("alice")
	bobDone := prompt("bob")
	var ids []string
	for deadline := time.Now().Add(5 * time.Second); len(ids) < 2 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		ids = pendingVolumePrompts()
	}
	if len(ids) != 2 {
		t.Fatalf("two users prompted for one slot left %d pending prompts, want 2: %v", len(ids), ids)
	}
	if got := agent.deliverHILResponse(&a2a.UserInputResponseData{RequestID: "volume-prompt-dataset", Status: "cancelled"}, hilSenderUser("mallory")); got != hilUnmatched {
		t.Errorf("the slot-only id still matched a prompt: %v", got)
	}

	var aliceID string
	for _, id := range ids {
		agent.pendingHILMu.Lock()
		owner := agent.pendingHIL[id].ownerUserID
		agent.pendingHILMu.Unlock()
		if owner == "alice" {
			aliceID = id
		}
	}
	if aliceID == "" {
		t.Fatalf("no pending prompt belongs to alice: %v", ids)
	}

	if got := agent.deliverHILResponse(&a2a.UserInputResponseData{RequestID: aliceID, Status: "cancelled"}, hilSenderUser("bob")); got != hilRefused {
		t.Fatalf("bob cancelling alice's volume prompt = %v, want refused", got)
	}
	select {
	case o := <-aliceDone:
		t.Fatalf("bob ended alice's volume prompt: %+v", o)
	case <-time.After(200 * time.Millisecond):
	}
	if got := logs.records(controlRefusedLogMsg); len(got) != 1 {
		t.Errorf("want one refusal record, got %d", len(got))
	}

	if got := agent.deliverHILResponse(&a2a.UserInputResponseData{RequestID: aliceID, Status: "answered", Data: map[string]any{"selected_volume": "vol-alice"}}, hilSenderUser("alice")); got != hilDelivered {
		t.Fatalf("alice answering her own prompt = %v, want delivered", got)
	}
	if o := <-aliceDone; o.err != nil || len(o.bindings) != 1 || o.bindings[0].VolumeID != "vol-alice" {
		t.Errorf("alice's prompt = %+v, want her volume bound", o)
	}
	for _, id := range ids {
		if id != aliceID {
			_ = agent.deliverHILResponse(&a2a.UserInputResponseData{RequestID: id, Status: "cancelled"}, hilSenderUser("bob"))
		}
	}
	<-bobDone
}

func TestControlResponseParts_GatewayAndAgentAgree(t *testing.T) {
	agent := New(AgentConfig{Name: "test-agent"})
	for _, tc := range []struct {
		name string
		part a2a.Part
	}{
		{"user_input_response", a2a.Part{Kind: "data", Data: map[string]any{"type": "user_input_response", "request_id": "r"}}},
		{"auth_response", a2a.Part{Kind: "data", Data: map[string]any{"type": "auth_response", "target_agent_task_id": "t"}}},
		{"another signal", a2a.Part{Kind: "data", Data: map[string]any{"type": "agent_progress_update"}}},
		{"no type", a2a.Part{Kind: "data", Data: map[string]any{"request_id": "r"}}},
		{"type in another case", a2a.Part{Kind: "data", Data: map[string]any{"type": "User_Input_Response"}}},
		{"a text part naming the type", a2a.Part{Kind: "text", Text: "user_input_response", Data: map[string]any{"type": "user_input_response"}}},
		{"data that is not an object", a2a.Part{Kind: "data", Data: "user_input_response"}},
		{"no data", a2a.Part{Kind: "data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := a2a.Message{Parts: []a2a.Part{tc.part}}
			agentActs := agent.extractUserInputResponse(msg) != nil || agent.extractAuthResponse(msg) != nil
			_, gatewayRefuses := a2a.ControlResponseType(tc.part.Kind, tc.part.Data)
			if gatewayRefuses != agentActs {
				t.Errorf("gateway predicate = %v, agent acts on it = %v", gatewayRefuses, agentActs)
			}
		})
	}
}
