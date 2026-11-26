// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type twoInstanceRig struct {
	t                *testing.T
	brk              *broker.DevBroker
	dbURL            string
	mock             *waMock
	agent            broker.Queue
	recoveryInterval time.Duration
	reprobeInterval  time.Duration
	wrap             func(broker.Broker) broker.Broker
}

type instance struct {
	client  *broker.DevBrokerClient
	adapter *Adapter
	core    *entrypoint.GatewayCore
	handler http.Handler
}

func newTwoInstanceRig(t *testing.T) *twoInstanceRig {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	_, dbURL := newIngressDB(t)
	agentQ, err := brk.CreateQueue("two-instance-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if subErr := brk.Subscribe(agentQ, a2a.AgentRequestTopic("test-ns", "Builder")); subErr != nil {
		t.Fatalf("subscribe agent topic: %v", subErr)
	}
	return &twoInstanceRig{t: t, brk: brk, dbURL: dbURL, mock: newWAMock(t), agent: agentQ, recoveryInterval: 50 * time.Millisecond}
}

func (r *twoInstanceRig) start() *instance {
	r.t.Helper()
	client := r.brk.NewClient()
	if err := client.Connect(r.t.Context()); err != nil {
		r.t.Fatalf("connect client: %v", err)
	}
	adapterCfg := map[string]any{
		"access_token":          "tok-test",
		"phone_number_id":       "555111",
		"app_secret":            ingressTestAppSecret,
		"verify_token":          "verify",
		"default_agent_name":    "Builder",
		"graph_api_base_url":    r.mock.server.URL,
		"graph_api_version":     "v26.0",
		"disable_rate_limiting": true,
	}
	adapter, err := NewAdapter(adapterCfg)
	if err != nil {
		r.t.Fatalf("NewAdapter: %v", err)
	}
	inst := &instance{client: client, adapter: adapter}
	adapter.SetOnHandlerReady(func(h http.Handler) { inst.handler = h })
	adapter.senderTune = func(p *senderPool) {
		p.idleTimeout = 50 * time.Millisecond
		p.recoveryInterval = r.recoveryInterval
	}
	adapter.reprobeInterval = r.reprobeInterval
	var brk broker.Broker = client
	if r.wrap != nil {
		brk = r.wrap(client)
	}
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"gateway_adapter": map[string]any{
			"type":           "whatsapp",
			"adapter_config": adapterCfg,
		},
		"session_service": map[string]any{
			"type":             "sql",
			"database_url":     r.dbURL,
			"default_behavior": "PERSISTENT",
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(r.t.Context(), nil); err != nil {
		r.t.Fatalf("core.Init: %v", err)
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	if err := core.Start(r.t.Context()); err != nil {
		r.t.Fatalf("core.Start: %v", err)
	}
	inst.core = core
	r.t.Cleanup(inst.stop)
	return inst
}

func (i *instance) stop() {
	if i.core == nil {
		return
	}
	_ = i.core.Stop(context.Background())
	i.core = nil
}

func (i *instance) disconnect() {
	_ = i.client.Disconnect(context.Background())
}

func (r *twoInstanceRig) post(i *instance, body string) int {
	r.t.Helper()
	return postSigned(i.handler, body)
}

func (r *twoInstanceRig) agentRequests(within time.Duration) []string {
	r.t.Helper()
	deadline := time.Now().Add(within)
	var got []string
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return got
		}
		ctx, cancel := context.WithTimeout(r.t.Context(), remaining)
		msg, err := r.brk.Receive(ctx, r.agent)
		cancel()
		if err != nil || msg == nil {
			return got
		}
		msg.Ack()
		got = append(got, inboundTextOf(r.t, msg.Payload))
	}
}

func waitOwnership(t *testing.T, a *Adapter, want bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for a.ownsIngress() != want {
		if time.Now().After(deadline) {
			t.Fatalf("ownsIngress = %v, want %v", !want, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOwner_UserSideFollowsTheIngressBind(t *testing.T) {
	const phone = "15551230000"
	r := newTwoInstanceRig(t)
	first := r.start()
	waitOwnership(t, first.adapter, true)
	second := r.start()

	if got := r.post(second, textWebhook("wamid.STANDBY-1", phone, "via standby")); got != http.StatusOK {
		t.Fatalf("webhook on the standby instance = %d, want 200", got)
	}
	if got := r.agentRequests(2 * time.Second); !slices.Equal(got, []string{"via standby"}) {
		t.Fatalf("agent requests = %v, want [via standby]", got)
	}
	if second.adapter.ownsIngress() {
		t.Fatal("the second instance owns the ingress while the first holds the bind")
	}
	if second.adapter.runningSender() != nil {
		t.Fatal("the standby instance runs a sender pool")
	}

	second.adapter.reply("", phone, "note from the standby instance")
	waitMockSentWA(t, r.mock, "note from the standby instance", "the active instance did not send the standby's row")
	if second.adapter.runningSender() != nil {
		t.Fatal("writing a row started a sender pool on the standby instance")
	}

	first.disconnect()
	waitOwnership(t, second.adapter, true)
	waitOwnership(t, first.adapter, false)
	if got := r.post(second, textWebhook("wamid.TAKEOVER-1", phone, "after takeover")); got != http.StatusOK {
		t.Fatalf("webhook after the takeover = %d, want 200", got)
	}
	if got := r.agentRequests(2 * time.Second); !slices.Equal(got, []string{"after takeover"}) {
		t.Fatalf("agent requests after the takeover = %v, want [after takeover]", got)
	}
	second.adapter.reply("", phone, "sent after takeover")
	waitMockSentWA(t, r.mock, "sent after takeover", "the promoted instance did not send")
}

type solaceBindBroker struct {
	bindFailingBroker

	mu       sync.Mutex
	held     map[string]func()
	bound    map[string]bool
	receives map[string]int
}

func newSolaceBindBroker() *solaceBindBroker {
	return &solaceBindBroker{held: map[string]func(){}, bound: map[string]bool{}, receives: map[string]int{}}
}

func (b *solaceBindBroker) CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error) {
	if report := opts.OnBindState; report != nil {
		opts.OnBindState = func(state broker.BindState) {
			b.mu.Lock()
			if !b.bound[name] {
				b.held[name] = func() { report(state) }
				b.mu.Unlock()
				return
			}
			b.mu.Unlock()
			report(state)
		}
	}
	return b.Broker.CreateQueue(name, opts)
}

func (b *solaceBindBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	b.receives[q.Name()]++
	b.mu.Unlock()
	msg, err := b.bindFailingBroker.Receive(ctx, q)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return msg, err
	}
	b.mu.Lock()
	report := b.held[q.Name()]
	delete(b.held, q.Name())
	b.bound[q.Name()] = true
	b.mu.Unlock()
	if report != nil {
		report()
	}
	return msg, err
}

func (b *solaceBindBroker) receivesOn(queue string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.receives[queue]
}

func TestOwner_ReprobesUntilTheIngressQueueBindsAndHandsOnWhatTheProbePulled(t *testing.T) {
	const phone = "15551230000"
	r := newTwoInstanceRig(t)
	failing := newSolaceBindBroker()
	failing.setFailure(errors.New("queue provision cooldown active"))
	r.wrap = func(b broker.Broker) broker.Broker {
		failing.Broker = b
		return failing
	}
	r.reprobeInterval = 20 * time.Millisecond
	unbound := r.start()
	queue := unbound.adapter.ingressQueueName()

	if !unbound.adapter.ingressDown.Load() {
		t.Fatal("the instance came up unlatched with an unbindable queue")
	}
	if got := r.post(unbound, textWebhook("wamid.UNBOUND-1", phone, "while unbound")); got != http.StatusServiceUnavailable {
		t.Fatalf("webhook while unbound = %d, want 503", got)
	}
	probesAtStart := failing.receivesOn(queue)
	if !waitFor(2*time.Second, func() bool { return failing.receivesOn(queue) >= probesAtStart+2 }) {
		t.Fatal("the owner loop stopped re-probing the bind after a failed attempt")
	}
	if !unbound.adapter.ingressDown.Load() {
		t.Fatal("the latch cleared while every probe failed")
	}

	r.wrap = nil
	standby := r.start()
	if got := r.post(standby, textWebhook("wamid.HELD-1", phone, "held on the queue")); got != http.StatusOK {
		t.Fatalf("webhook on the standby instance = %d, want 200", got)
	}
	if got := r.agentRequests(200 * time.Millisecond); len(got) != 0 {
		t.Fatalf("agent requests before the bind = %v, want none", got)
	}

	failing.setFailure(nil)
	waitOwnership(t, unbound.adapter, true)
	if !waitFor(2*time.Second, func() bool { return !unbound.adapter.ingressDown.Load() }) {
		t.Fatal("the latch never cleared after the bind succeeded")
	}
	if got := r.agentRequests(2 * time.Second); !slices.Equal(got, []string{"held on the queue"}) {
		t.Fatalf("agent requests after the bind = %v, want [held on the queue]", got)
	}
	if got := r.post(unbound, textWebhook("wamid.BOUND-1", phone, "after the bind")); got != http.StatusOK {
		t.Fatalf("webhook after the bind = %d, want 200", got)
	}
	if got := r.agentRequests(2 * time.Second); !slices.Equal(got, []string{"after the bind"}) {
		t.Fatalf("agent requests = %v, want [after the bind]", got)
	}
}

func TestOwner_DoesNotReprobeWhileItOwnsTheUserSide(t *testing.T) {
	r := newTwoInstanceRig(t)
	counting := newSolaceBindBroker()
	r.wrap = func(b broker.Broker) broker.Broker {
		counting.Broker = b
		return counting
	}
	r.reprobeInterval = 20 * time.Millisecond
	inst := r.start()
	waitOwnership(t, inst.adapter, true)
	queue := inst.adapter.ingressQueueName()
	const probeThenDispatch = 2
	if !waitFor(2*time.Second, func() bool { return counting.receivesOn(queue) == probeThenDispatch }) {
		t.Fatalf("receives on the ingress queue = %d, want %d (the probe, then the dispatch loop)", counting.receivesOn(queue), probeThenDispatch)
	}

	inst.adapter.ingressDown.Store(true)
	inst.adapter.kickOwner()
	time.Sleep(10 * r.reprobeInterval)
	if got := counting.receivesOn(queue); got != probeThenDispatch {
		t.Fatalf("receives on the ingress queue = %d, want %d: the owner loop re-probed while the dispatch loop was consuming", got, probeThenDispatch)
	}
}

func waitMockSentWA(t *testing.T, m *waMock, want, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, b := range sentSoFar(m) {
			if strings.Contains(string(b), want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: no send containing %q arrived", why, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func sentSoFar(m *waMock) [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.raw)
}

func TestSender_RowMarksDriveTypingAndTheTaskSlot(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	const phone, task = "15551230000", "task-marks"
	a.senders.taskStarted(phone, task)
	a.typingTrack(phone, task, "wamid.in")

	a.enqueue(kindInteractivePrompt, a.targetFor(task, phone), outboundPayload{
		Text: "Which one?",
		Prompt: &promptPayload{Kind: promptKindHIL, Mode: promptModeText, RequestID: "req-1",
			Surface: json.RawMessage(askSurfaceSingleChoiceWA)},
	}, "")
	waitQueueDrained(t, a)
	if got := typingStateOf(a, phone, task); got != typingAwaitingUser {
		t.Fatalf("after the prompt row: typing state = %v, want awaiting the user", got)
	}

	a.replyResumed(task, phone, authSuccessMessage)
	waitQueueDrained(t, a)
	if got := typingStateOf(a, phone, task); got != typingWorking {
		t.Fatalf("after the authorized row: typing state = %v, want working", got)
	}

	a.endingTasks.Store(task, struct{}{})
	a.reply(task, phone, "all done")
	a.endingTasks.Delete(task)
	waitQueueDrained(t, a)
	if a.typingTracked(phone, task) {
		t.Error("after the final row the task still holds the typing indicator")
	}
	if a.senders.admitTask(phone) != true || a.senders.runningTasks(phone) != 0 {
		t.Errorf("after the final row the sender still has %d running tasks", a.senders.runningTasks(phone))
	}
}

func typingStateOf(a *Adapter, sender, taskID string) typingTaskState {
	a.typing.mu.Lock()
	defer a.typing.mu.Unlock()
	if conv := a.typing.convs[sender]; conv != nil {
		if task := conv.tasks[taskID]; task != nil {
			return task.state
		}
	}
	return -1
}

func (a *Adapter) typingTracked(sender, taskID string) bool {
	return typingStateOf(a, sender, taskID) >= 0
}

func TestAdapter_RowsWaitForTheSenderWhenNoneRuns(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	const phone = "15551230000"
	if !a.stopSender(t.Context()) {
		t.Fatal("the sender did not stop")
	}
	if a.runningSender() != nil {
		t.Fatal("a stopped sender reads as running")
	}

	a.reply("", phone, "waits for a sender")
	time.Sleep(100 * time.Millisecond)
	for _, b := range sentSoFar(mock) {
		if strings.Contains(string(b), "waits for a sender") {
			t.Fatal("a row was sent with no sender running on this instance")
		}
	}
	pending, err := a.outbound.PendingCount(t.Context(), a.gatewayID())
	if err != nil || pending != 1 {
		t.Fatalf("pending rows = %d (err %v), want 1", pending, err)
	}

	a.startSender()
	waitMockSentWA(t, mock, "waits for a sender", "the started sender did not pick the row up")
	if !a.stopSender(t.Context()) {
		t.Fatal("the sender did not stop")
	}
}

func TestOwner_DoorbellWakesTheActiveSender(t *testing.T) {
	const phone = "15551230001"
	r := newTwoInstanceRig(t)
	r.recoveryInterval = senderRecoveryInterval
	first := r.start()
	waitOwnership(t, first.adapter, true)
	second := r.start()
	if second.adapter.runningSender() != nil {
		t.Fatal("the standby instance runs a sender pool, so this would not exercise the doorbell")
	}

	second.adapter.reply("", phone, "rung through")
	deadline := time.Now().Add(3 * time.Second)
	for countSent(sentSoFar(r.mock), "rung through") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the doorbell did not wake the active instance's sender")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAdapter_TaskLimitCountsTasksWithAWrittenAnswerAsFinished(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	const phone = "15551230300"
	if !a.stopSender(t.Context()) {
		t.Fatal("the sender did not stop")
	}
	for _, task := range []string{"t-1", "t-2", "t-3"} {
		a.senders.taskStarted(phone, task)
	}
	if a.admitTask(phone) {
		t.Fatal("a fourth task was admitted with three running")
	}

	a.endingTasks.Store("t-2", struct{}{})
	a.reply("t-2", phone, "answer for t-2")
	a.endingTasks.Delete("t-2")

	if !a.admitTask(phone) {
		t.Fatal("a task was refused although one of the three has its answer written")
	}
	if n := a.senders.runningTasks(phone); n != 2 {
		t.Fatalf("running tasks = %d, want 2", n)
	}
}

func TestOwner_ReleaseAndRetakeOnTheSameInstance(t *testing.T) {
	const phone = "15551230002"
	r := newIngressRig(t)
	a := r.adapter
	waitOwnership(t, a, true)

	a.onIngressBindState(broker.BindStandby)
	waitOwnership(t, a, false)
	if a.runningSender() != nil {
		t.Fatal("the sender still runs after the bind went to standby")
	}

	a.onIngressBindState(broker.BindActive)
	waitOwnership(t, a, true)
	if got := r.post(textWebhook("wamid.RETAKE-1", phone, "after the retake")); got != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", got)
	}
	if got := r.awaitAgentRequests(1, 100*time.Millisecond); !slices.Equal(got, []string{"after the retake"}) {
		t.Fatalf("agent requests = %v, want [after the retake]", got)
	}
	a.reply("", phone, "sent after the retake")
	waitMockSentWA(t, r.mock, "sent after the retake", "the retaken sender did not send")
}

type failingItemRepo struct {
	taskItemRepo
	prefix string
}

func (f failingItemRepo) Put(ctx context.Context, taskID, item, value string) error {
	if strings.HasPrefix(item, f.prefix) {
		return errors.New("database unavailable")
	}
	return f.taskItemRepo.Put(ctx, taskID, item, value)
}

func (f failingItemRepo) Delete(ctx context.Context, taskID, item string) error {
	if strings.HasPrefix(item, f.prefix) {
		return errors.New("database unavailable")
	}
	return f.taskItemRepo.Delete(ctx, taskID, item)
}

func TestAdapter_SavedFileWhoseRowFailedIsStillDelivered(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.items = taskItems{repo: failingItemRepo{store.NewEntrypointTaskItemRepo(a.core.Store()), itemSavedFilePrefix}, logger: a.logger}
	a.itemsOnce.Do(func() {})
	registerReplyTask(a, taskT1, "15551230400")
	if err := a.taskItems().writeDestination(t.Context(), taskT1, "15551230400", "sess-"+taskT1); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}

	a.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: "chart.png", Version: 1, MIMEType: "image/png"}, nil), nil)

	saves := a.recordedSaves(t.Context(), taskT1, a.getTaskMeta(taskT1))
	if len(saves.files) != 1 || saves.files[0].Filename != "chart.png" {
		t.Fatalf("saved files = %+v, want chart.png from this instance's record", saves.files)
	}
}
