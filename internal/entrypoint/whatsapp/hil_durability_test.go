// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const promptAgent = "Builder"

type promptEnv struct {
	brk  broker.Broker
	mock *rawMock
	dsn  string
}

func newPromptEnv(t *testing.T) *promptEnv {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return &promptEnv{brk: brk, mock: newRawMock(t), dsn: store.TestDBURL(t)}
}

func (e *promptEnv) start(t *testing.T) *Adapter {
	t.Helper()
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace": "test-ns", "gateway_id": "test-gw",
		"session_service": map[string]any{
			"type": "sql", "database_url": e.dsn, "default_behavior": "PERSISTENT",
		},
	})
	core.SetServices(runtime.NewRuntime(e.brk, nil).Services())
	initCoreForTest(t, core)
	if core.Store() == nil {
		t.Fatal("the test core has no database, so this would not exercise the durable path")
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: promptAgent})

	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: e.mock.server.URL, MaxMediaSizeMB: 7,
		DefaultAgentName: promptAgent, DisableRateLimiting: true,
	}
	log := discardLogger()
	a := &Adapter{
		core: core, cfg: cfg, logger: log,
		claims: newMemoryClaims(), claimLease: defaultClaimLease, hilDedup: runtime.NewMapCache(),
		client:   NewClient(cfg, log),
		sessKeys: store.NewGatewaySessionKeyRepo(core.Store()),
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	if err := a.initDurableState(); err != nil {
		t.Fatalf("initDurableState: %v", err)
	}
	a.sender.idleTimeout = 50 * time.Millisecond
	a.sender.Start(t.Context())
	t.Cleanup(func() {
		if !a.stopped.Load() && !a.sender.Stop(context.Background(), 2*time.Second) {
			t.Error("sender pool did not drain; this core's store closes next")
		}
		a.cancel()
	})
	e.mock.adapter = a
	return a
}

func (e *promptEnv) stop(t *testing.T, a *Adapter) {
	t.Helper()
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func askQuestion(t *testing.T, a *Adapter, taskID, phone, requestID string) *store.WhatsAppPendingPrompt {
	t.Helper()
	_ = a.core.Tasks().Register(taskID, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{SessionID: "sess-" + taskID})
	a.core.Tasks().SetMetadata(taskID, &taskMeta{FromPhone: phone, SessionID: "sess-" + taskID})
	if err := a.taskItems().writeDestination(t.Context(), taskID, phone, "sess-"+taskID); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	a.handleUserInputRequest(taskID, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: requestID,
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	waitQueueDrained(t, a)
	return waitPendingPrompt(t, a, phone)
}

func assertAttributedToTaskOwner(t *testing.T, msg *broker.Message, phone string) {
	t.Helper()
	if got, want := a2a.DecodeUserProperties(msg.UserProperties).UserID, "whatsapp:"+phone; got != want {
		t.Errorf("response attributed to %q, want the task's user %q", got, want)
	}
}

func subscribeAgentRequests(t *testing.T, brk broker.Broker, name string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", promptAgent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func receiveWithin(t *testing.T, brk broker.Broker, q broker.Queue, d time.Duration) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	msg, _ := brk.Receive(ctx, q)
	return msg
}

func TestAdapter_PendingPromptSurvivesARestart(t *testing.T) {
	const phone = "15551230100"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-restart-q")

	first := env.start(t)
	pending := askQuestion(t, first, taskT1, phone, "req-1")
	if pending.PromptWamid == "" {
		t.Fatal("the prompt's wamid must be stored for reply correlation")
	}
	env.stop(t, first)

	second := env.start(t)
	if second.core.Tasks().Metadata(taskT1) != nil {
		t.Fatal("precondition: the restarted instance must not hold the task's metadata")
	}

	value, msg := interactiveReply(phone, "wamid.tap", optionRefID(1))
	msg.Context = &messageContext{From: phone, ID: pending.PromptWamid}
	value.Messages[0].Context = msg.Context
	second.deliver(value, msg)

	resumed := receiveWithin(t, env.brk, q, 2*time.Second)
	if resumed == nil {
		t.Fatal("the button reply did not resume the task after the restart: the user's answer was lost")
	}
	if !strings.Contains(string(resumed.Payload), "req-1") {
		t.Errorf("resumed payload does not quote the original request id: %s", resumed.Payload)
	}
	if pendingPromptRow(t, second, phone) != nil {
		t.Error("an answered prompt must be cleared")
	}
}

func TestAdapter_PendingPromptLandsWithTheQuestionNotBeforeIt(t *testing.T) {
	t.Parallel()
	const phone = "15551230101"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, promptAgent)
	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.sender.claimCancel()
	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})

	pending, err := a.outbound.PendingCount(t.Context(), a.gatewayID())
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if pending == 0 {
		t.Fatal("precondition: the prompt should be queued")
	}
	if pendingPromptRow(t, a, phone) != nil {
		t.Fatal("a question the user cannot see must not be pending: their next message would be swallowed as its answer")
	}
}

func TestAdapter_FailedPromptLeavesNoPendingRow(t *testing.T) {
	t.Parallel()
	const phone = "15551230102"
	mock := newWAMock(t)
	mock.rejectAll = true
	a := newAdapterRig(t, mock, promptAgent)
	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	waitQueueDrained(t, a)

	if row := pendingPromptRow(t, a, phone); row != nil {
		t.Fatalf("a question that never reached the user is pending (%+v): the user's next message would be read as its answer", row)
	}
}

func TestAdapter_ResumedTaskStillDeliversItsAnswer(t *testing.T) {
	const phone = "15551230103"
	env := newPromptEnv(t)
	first := env.start(t)
	pending := askQuestion(t, first, taskT1, phone, "req-1")
	env.stop(t, first)

	second := env.start(t)
	value, msg := interactiveReply(phone, "wamid.tap", optionRefID(1))
	msg.Context = &messageContext{From: phone, ID: pending.PromptWamid}
	value.Messages[0].Context = msg.Context
	second.deliver(value, msg)

	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if second.core.Tasks().Metadata(taskT1) == nil {
		t.Fatal("the resumed task's state was not loaded, so its final response has nowhere to go")
	}
	second.OnFinalResponse(t.Context(), taskT1, completedTask("deployment approved and done"), nil)
	waitQueueDrained(t, second)

	var told bool
	for _, b := range env.mock.recorded() {
		if strings.Contains(string(b), "deployment approved") {
			told = true
		}
	}
	if !told {
		t.Error("the agent acted on the answer and the user was never told")
	}
}

func TestAdapter_TwoConcurrentTasksLeaveNoOrphanedAgentWait(t *testing.T) {
	const phone = "15551230104"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-concurrent-q")
	a := env.start(t)

	askQuestion(t, a, taskTA, phone, "req-A")
	askQuestion(t, a, taskTB, phone, "req-B")

	live := pendingPromptRow(t, a, phone)
	if live == nil || live.TaskID != taskTB {
		t.Fatalf("live prompt = %+v, want the newer task tB", live)
	}

	cancelled := receiveWithin(t, env.brk, q, 2*time.Second)
	if cancelled == nil {
		t.Fatal("the displaced task was never told to stop waiting: its agent hangs to its own timeout")
	}
	if !strings.Contains(string(cancelled.Payload), "req-A") || !strings.Contains(string(cancelled.Payload), "cancelled") {
		t.Errorf("displaced task response = %s, want req-A cancelled", cancelled.Payload)
	}
	assertAttributedToTaskOwner(t, cancelled, phone)
	waitMockSent(t, env.mock, msgPromptSuperseded,
		"the user must be told which question is still live")

	value, msg := interactiveReply(phone, "wamid.tapB", optionRefID(0))
	msg.Context = &messageContext{From: phone, ID: live.PromptWamid}
	value.Messages[0].Context = msg.Context
	a.deliver(value, msg)

	answered := receiveWithin(t, env.brk, q, 2*time.Second)
	if answered == nil || !strings.Contains(string(answered.Payload), "req-B") {
		t.Fatalf("the live question did not resume its own task: %v", answered)
	}
	assertAttributedToTaskOwner(t, answered, phone)
}

func TestAdapter_TapAfterTaskCompletionGetsTheStaleNotice(t *testing.T) {
	t.Parallel()
	const phone = "15551230105"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, promptAgent)
	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	waitQueueDrained(t, a)
	pending := waitPendingPrompt(t, a, phone)

	a.OnFinalResponse(t.Context(), taskT1, completedTask("done anyway"), nil)
	if pendingPromptRow(t, a, phone) != nil {
		t.Fatal("a terminal task must clear its own question")
	}

	value, msg := interactiveReply(phone, "wamid.late-tap", optionRefID(0))
	msg.Context = &messageContext{From: phone, ID: pending.PromptWamid}
	value.Messages[0].Context = msg.Context
	a.deliver(value, msg)
	waitQueueDrained(t, a)

	var sawNotice bool
	for _, sent := range mock.calls() {
		if strings.Contains(sent.Text.Body, "expired") {
			sawNotice = true
		}
		if strings.Contains(sent.Text.Body, "File type not supported") {
			t.Error("a stale tap must not be declined as an unsupported file type")
		}
	}
	if !sawNotice {
		t.Errorf("expected the expired-prompt notice, got %+v", mock.calls())
	}
}

func TestAdapter_ExpiredPromptOnReadSubmitsTimedOut(t *testing.T) {
	const phone = "15551230106"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-expiry-read-q")
	a := env.start(t)

	askQuestion(t, a, taskT1, phone, "req-1")
	expirePromptNow(t, a, phone)

	value, msg := textMessage("wamid.new", phone, "different question entirely")
	a.deliver(value, msg)

	timedOut := receiveWithin(t, env.brk, q, 2*time.Second)
	if timedOut == nil {
		t.Fatal("an expired question never told its agent to stop waiting")
	}
	if !strings.Contains(string(timedOut.Payload), "timed_out") {
		t.Errorf("response = %s, want timed_out", timedOut.Payload)
	}
	assertAttributedToTaskOwner(t, timedOut, phone)
	if pendingPromptRow(t, a, phone) != nil {
		t.Error("an expired prompt must be gone after the read that noticed it")
	}
}

func TestAdapter_SweepRetiresAndNotifies(t *testing.T) {
	const phone = "15551230107"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-expiry-sweep-q")
	a := env.start(t)

	askQuestion(t, a, taskT1, phone, "req-1")
	expirePromptNow(t, a, phone)

	a.sweepExpiredPrompts()
	waitQueueDrained(t, a)

	if timedOut := receiveWithin(t, env.brk, q, 2*time.Second); timedOut == nil {
		t.Fatal("the sweep did not release the waiting agent")
	}
	waitMockSent(t, env.mock, msgPromptTimedOut,
		"a question that dies with no inbound message must still be reported to the user")
	if pendingPromptRow(t, a, phone) != nil {
		t.Error("the swept prompt must be gone")
	}
}

func expirePromptNow(t *testing.T, a *Adapter, senderID string) {
	t.Helper()
	_, err := ledgerDB(a).ExecContext(context.Background(),
		`UPDATE whatsapp_pending_prompts SET expires_at = ? WHERE gateway_id = ? AND sender_id = ?`,
		time.Now().Add(-time.Minute).UnixMilli(), a.gatewayID(), senderID)
	if err != nil {
		t.Fatalf("expire prompt: %v", err)
	}
}

func TestAdapter_UnreadableSurfaceRetiresRatherThanWedges(t *testing.T) {
	const phone = "15551230108"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-unreadable-q")
	a := env.start(t)

	askQuestion(t, a, taskT1, phone, "req-1")
	if _, err := ledgerDB(a).ExecContext(context.Background(),
		`UPDATE whatsapp_pending_prompts SET surface_json = ? WHERE gateway_id = ? AND sender_id = ?`,
		"not a surface", a.gatewayID(), phone); err != nil {
		t.Fatalf("corrupt the stored surface: %v", err)
	}

	value, msg := textMessage("wamid.next", phone, "anything")
	a.deliver(value, msg)

	if cancelled := receiveWithin(t, env.brk, q, 2*time.Second); cancelled == nil {
		t.Fatal("an unanswerable question left its agent waiting")
	}
	if pendingPromptRow(t, a, phone) != nil {
		t.Error("the unreadable prompt must be cleared, or every later message wedges the same way")
	}
}

func TestAdapter_ExpiryNotifiesOnlyFromTheSweep(t *testing.T) {
	t.Parallel()
	const phone = "15551230109"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, promptAgent)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: promptAgent})

	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{
		TaskID: taskT1, RequestID: "req-1", AgentName: promptAgent,
		ExpiresAt: time.Now().Add(-time.Minute).UnixMilli()})

	if p := a.livePendingPrompt(phone); p != nil {
		t.Fatal("an expired prompt must not be returned as live")
	}
	waitQueueDrained(t, a)
	for _, c := range mock.calls() {
		if strings.Contains(c.Text.Body, "timed out") {
			t.Error("the read path must stay quiet; the message that triggered it produces its own answer")
		}
	}

	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{
		TaskID: taskT2, RequestID: "req-2", AgentName: promptAgent,
		ExpiresAt: time.Now().Add(-time.Minute).UnixMilli()})
	a.sweepExpiredPrompts()
	waitQueueDrained(t, a)

	var told bool
	for _, c := range mock.calls() {
		if strings.Contains(c.Text.Body, "timed out") {
			told = true
		}
	}
	if !told {
		t.Error("a question that dies with no inbound message must still be reported to the user")
	}
}

func TestAdapter_TerminalTaskWithdrawsAQueuedPrompt(t *testing.T) {
	t.Parallel()
	const phone = "15551230110"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, promptAgent)
	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.sender.claimCancel()
	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	a.clearPendingHIL(phone, taskT1)

	rows, err := dbutil.QueryRows[store.WhatsAppOutbound](t.Context(), ledgerDB(a),
		`SELECT id, gateway_id, recipient, task_id, trace_id, user_id, agent_name, session_id,
			kind, payload, artifact_ref, state, wamid, error_code, error_detail,
			attempts, not_before, parked_at, created_at, updated_at
		 FROM whatsapp_outbound WHERE gateway_id = ? AND kind = ?`, a.gatewayID(), kindInteractivePrompt)
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ledger holds %d prompt rows, want 1", len(rows))
	}
	if rows[0].State != store.WhatsAppOutboundSuperseded {
		t.Errorf("queued prompt = %q after its task ended, want superseded", rows[0].State)
	}
}

func TestAdapter_UndeliverablePromptAnswersTheAgent(t *testing.T) {
	const phone = "15551230111"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-abandon-q")
	a := env.start(t)
	env.mock.rejectAll(true)

	_ = a.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskT1, &taskMeta{FromPhone: phone, SessionID: "sess-" + taskT1})
	a.handleUserInputRequest(taskT1, &a2a.UserInputRequestData{
		Type: "user_input_request", RequestID: "req-1",
		Surface: json.RawMessage(askSurfaceSingleChoiceWA),
	})
	waitQueueDrained(t, a)

	cancelled := receiveWithin(t, env.brk, q, 2*time.Second)
	if cancelled == nil {
		t.Fatal("the agent was never told its question could not be delivered; it hangs to the task timeout")
	}
	if !strings.Contains(string(cancelled.Payload), "cancelled") ||
		!strings.Contains(string(cancelled.Payload), "req-1") {
		t.Errorf("agent response = %s, want req-1 cancelled", cancelled.Payload)
	}
	if pendingPromptRow(t, a, phone) != nil {
		t.Error("a question that never reached the user must not be pending")
	}
}

func TestAdapter_ExpiredPromptAfterARestartStillDeliversTheAgentsReply(t *testing.T) {
	const phone = "15551230112"
	env := newPromptEnv(t)
	first := env.start(t)
	askQuestion(t, first, taskT1, phone, "req-1")
	env.stop(t, first)

	second := env.start(t)
	expirePromptNow(t, second, phone)
	second.sweepExpiredPrompts()

	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if second.core.Tasks().Metadata(taskT1) == nil {
		t.Fatal("the task's state was not loaded; the agent's reply has nowhere to go")
	}
	second.OnFinalResponse(t.Context(), taskT1, completedTask("I've cancelled the deployment"), nil)
	waitQueueDrained(t, second)

	var told bool
	for _, b := range env.mock.recorded() {
		if strings.Contains(string(b), "cancelled the deployment") {
			told = true
		}
	}
	if !told {
		t.Error("the agent unwound after the timeout and the user was never told")
	}
}

func TestAdapter_ResumedPromptCarriesItsSession(t *testing.T) {
	const phone = "15551230113"
	env := newPromptEnv(t)
	first := env.start(t)
	pending := askQuestion(t, first, taskT1, phone, "req-1")
	if pending.SessionID != "sess-"+taskT1 {
		t.Fatalf("stored sessionID = %q, want the task's session", pending.SessionID)
	}
	env.stop(t, first)

	second := env.start(t)
	value, msg := interactiveReply(phone, "wamid.tap", optionRefID(1))
	msg.Context = &messageContext{From: phone, ID: pending.PromptWamid}
	value.Messages[0].Context = msg.Context
	second.deliver(value, msg)

	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	meta := second.getTaskMeta(taskT1)
	if meta == nil {
		t.Fatal("the resumed task's state was not loaded")
	}
	if meta.SessionID != "sess-"+taskT1 {
		t.Errorf("loaded session = %q, want the stored one; an artifact reply resolves through it", meta.SessionID)
	}
}

func TestAdapter_TerminalTaskClearDuringShutdown(t *testing.T) {
	const phone = "15551230114"
	env := newPromptEnv(t)
	a := env.start(t)
	askQuestion(t, a, taskT1, phone, "req-1")

	a.cancel()
	if a.ctx.Err() == nil {
		t.Fatal("a.cancel did not cancel a.ctx; the test models nothing")
	}

	a.clearPendingHIL(phone, taskT1)

	if row := pendingPromptRow(t, a, phone); row != nil {
		t.Errorf("the question survived its task's end during shutdown (%+v); the user's next message answers a dead task", row)
	}
}

func TestAdapter_AnswerAfterTheTaskRecordWasReapedStillDeliversTheReply(t *testing.T) {
	const phone = "15551230116"
	env := newPromptEnv(t)
	first := env.start(t)
	pending := askQuestion(t, first, taskT1, phone, "req-1")
	first.core.Tasks().Remove(taskT1)
	env.stop(t, first)

	second := env.start(t)
	value, msg := interactiveReply(phone, "wamid.tap", optionRefID(1))
	msg.Context = &messageContext{From: phone, ID: pending.PromptWamid}
	value.Messages[0].Context = msg.Context
	second.deliver(value, msg)

	info := second.core.Tasks().Get(taskT1)
	if info == nil || info.SessionID != "sess-"+taskT1 {
		t.Fatalf("restored record = %+v, want one carrying the question's session", info)
	}
	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	second.OnFinalResponse(t.Context(), taskT1, completedTask("deployment approved after the outage"), nil)
	waitQueueDrained(t, second)
	var told bool
	for _, b := range env.mock.recorded() {
		if strings.Contains(string(b), "deployment approved after the outage") {
			told = true
		}
	}
	if !told {
		t.Error("the agent's reply to the answer was dropped")
	}
}

func TestAdapter_SweepLeavesAPromptAnsweredAtTheDeadline(t *testing.T) {
	const phone = "15551230110"
	env := newPromptEnv(t)
	q := subscribeAgentRequests(t, env.brk, "wa-expiry-answered-q")
	a := env.start(t)

	askQuestion(t, a, taskT1, phone, "req-1")
	expirePromptNow(t, a, phone)
	a.hilDedup.Set("req-1", struct{}{})

	a.sweepExpiredPrompts()
	waitQueueDrained(t, a)

	if msg := receiveWithin(t, env.brk, q, 300*time.Millisecond); msg != nil {
		t.Errorf("the sweep sent %s for a prompt already answered", msg.Payload)
	}
	for _, body := range env.mock.recorded() {
		if strings.Contains(string(body), msgPromptTimedOut) {
			t.Error("the user was told an answered prompt expired")
		}
	}
	if pendingPromptRow(t, a, phone) == nil {
		t.Error("the sweep took the row the answer path clears")
	}
}
