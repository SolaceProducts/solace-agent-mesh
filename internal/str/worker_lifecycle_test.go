// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type ackTrackingBroker struct {
	broker.Broker
	failReplies     atomic.Bool
	failNextReplies atomic.Int64
	failedPublishes atomic.Int64
	mu              sync.Mutex
	ackedIDs        map[string]bool
}

func newAckTrackingBroker(t *testing.T) *ackTrackingBroker {
	t.Helper()
	return &ackTrackingBroker{Broker: newTestBroker(t), ackedIDs: make(map[string]bool)}
}

func (b *ackTrackingBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	m, err := b.Broker.Receive(ctx, q)
	if m == nil || m.CorrelationID == "" {
		return m, err
	}
	id := m.CorrelationID
	tracked := broker.NewDeliveredMessage(m.ID, m.Topic, m.Payload, func() {
		b.mu.Lock()
		b.ackedIDs[id] = true
		b.mu.Unlock()
		m.Ack()
	}, m.Nack)
	tracked.UserProperties = m.UserProperties
	tracked.ReplyTo = m.ReplyTo
	tracked.CorrelationID = m.CorrelationID
	tracked.Redelivered = m.Redelivered
	return tracked, err
}

func (b *ackTrackingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if strings.HasPrefix(topic, "test-ns/reply/") && (b.failReplies.Load() || b.failNextReplies.Add(-1) >= 0) {
		b.failedPublishes.Add(1)
		return errors.New("simulated publish failure")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func (b *ackTrackingBroker) acked(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ackedIDs[id]
}

func newInProcessWorker(t *testing.T, brk broker.Broker, tools ...*samtoolsdk.ToolDef) *Worker {
	t.Helper()
	return NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:  "lifecycle-worker",
		Namespace: "test-ns",
		Sandbox: SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 10,
			MaxInFlight:             4,
		},
		InProcessTools: tools,
	})
}

func receiveReply(t *testing.T, brk broker.Broker, q broker.Queue, d time.Duration) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	msg, _ := brk.Receive(ctx, q)
	if msg != nil {
		msg.Ack()
	}
	return msg
}

func TestWorker_FailedReplyPublishIsRetriedThenAcked(t *testing.T) {
	const toolName = "ack_rule_tool"
	release := make(chan struct{})
	close(release)
	brk := newAckTrackingBroker(t)
	w := newInProcessWorker(t, brk, newBlockingDrainTool(toolName, make(chan struct{}, 4), release))
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	replyQ := subscribeReply(t, brk, toolName)

	publishDrainInvoke(t, t.Context(), brk, toolName, "req-published")
	if receiveReply(t, brk, replyQ, 2*time.Second) == nil {
		t.Fatal("no reply for a call whose publish works")
	}
	waitFor(t, func() bool { return brk.acked("req-published") }, "call acknowledged after its reply was published")

	brk.failReplies.Store(true)
	publishDrainInvoke(t, t.Context(), brk, toolName, "req-reply-fails")
	if err := brk.Publish(t.Context(), remotetool.InvokeTopic("test-ns", toolName), &broker.Message{
		Payload:        []byte(`{"jsonrpc":"2.0","id":"req-bad-params","method":"invoke","params":"not an object"}`),
		UserProperties: map[string]string{remotetool.PropReplyTo: "test-ns/reply/" + toolName},
		CorrelationID:  "req-bad-params",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return brk.acked("req-reply-fails") && brk.acked("req-bad-params") }, "both calls acknowledged after the retries")
	if got := brk.failedPublishes.Load(); got != 2*answerPublishAttempts {
		t.Errorf("failed publishes = %d, want %d attempts for each call", got, 2*answerPublishAttempts)
	}

	brk.failReplies.Store(false)
	brk.failNextReplies.Store(1)
	publishDrainInvoke(t, t.Context(), brk, toolName, "req-blip")
	if receiveReply(t, brk, replyQ, 3*time.Second) == nil {
		t.Fatal("an answer that failed once was not sent on the retry")
	}
	waitFor(t, func() bool { return brk.acked("req-blip") }, "call acknowledged after its retried answer was sent")
}

func TestHandleInvocation_ResultAfterContextEndIsPublished(t *testing.T) {
	const toolName = "late_result_tool"
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	lateTool := samtoolsdk.NewTool(toolName, "Ignores cancellation and answers when released.",
		func(_ context.Context, _ struct {
			N string `json:"n"`
		}, _ *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			started <- struct{}{}
			<-release
			return samtoolsdk.OK("late"), nil
		})

	brk := newAckTrackingBroker(t)
	w := newInProcessWorker(t, brk, lateTool)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.syncSubscriptions(); err != nil {
		t.Fatal(err)
	}
	replyQ := subscribeReply(t, brk, toolName)

	const reqID = "req-late"
	publishDrainInvoke(t, t.Context(), brk, toolName, reqID)
	ctx := t.Context()
	recvCtx, cancelRecv := context.WithTimeout(ctx, 2*time.Second)
	defer cancelRecv()
	call, err := brk.Receive(recvCtx, w.queue)
	if err != nil || call == nil {
		t.Fatalf("no call on the worker queue: %v", err)
	}

	invokeCtx, cancelInvoke := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.handleInvocation(invokeCtx, call, w.manifest.GetTool(toolName))
	}()
	<-started
	cancelInvoke()
	close(release)
	<-done

	reply := receiveReply(t, brk, replyQ, 2*time.Second)
	if reply == nil {
		t.Fatal("the result of a tool finishing after its invocation context ended was never published")
	}
	if !strings.Contains(string(reply.Payload), "late") {
		t.Errorf("reply = %s, want the tool's result", reply.Payload)
	}
	if !brk.acked(reqID) {
		t.Error("the call was not acknowledged after its result was published")
	}
}

type blockingFetchAllMirror struct {
	recordingMirror
	entered      chan struct{}
	release      chan struct{}
	failFetchAll atomic.Int64
}

func (m *blockingFetchAllMirror) FetchAll(ctx context.Context) error {
	m.fetchAll.Add(1)
	select {
	case m.entered <- struct{}{}:
	default:
	}
	select {
	case <-m.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if m.failFetchAll.Add(-1) >= 0 {
		return errors.New("simulated object store outage")
	}
	return nil
}

func TestStart_DispatchWaitsForBootReconcile(t *testing.T) {
	const toolName = "boot_order_tool"
	release := make(chan struct{})
	close(release)
	brk := newAckTrackingBroker(t)
	w := newInProcessWorker(t, brk, newBlockingDrainTool(toolName, make(chan struct{}, 1), release))
	w.cfg.ToolsetsDir = t.TempDir()
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	mirror := &blockingFetchAllMirror{entered: make(chan struct{}, 1), release: make(chan struct{})}
	w.packageStore = mirror
	replyQ := subscribeReply(t, brk, toolName)

	startErr := make(chan error, 1)
	go func() { startErr <- w.Start(t.Context()) }()
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	select {
	case <-mirror.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("boot reconcile never called FetchAll")
	}

	publishDrainInvoke(t, t.Context(), brk, toolName, "req-during-boot")
	if msg := receiveReply(t, brk, replyQ, 300*time.Millisecond); msg != nil {
		t.Fatalf("a call was answered before the boot reconcile finished: %s", msg.Payload)
	}
	if w.ReadinessOpen() {
		t.Error("worker reported ready before the boot reconcile finished")
	}

	close(mirror.release)
	if err := <-startErr; err != nil {
		t.Fatal(err)
	}
	if receiveReply(t, brk, replyQ, 2*time.Second) == nil {
		t.Fatal("the call queued during boot was never answered")
	}
	if !w.ReadinessOpen() {
		t.Error("worker not ready after the boot reconcile with the queue bound and nothing pending")
	}
}

func TestStart_FailedBootFetchAllIsPending(t *testing.T) {
	prevRetry := mirrorRetryInterval
	mirrorRetryInterval = 20 * time.Millisecond
	t.Cleanup(func() { mirrorRetryInterval = prevRetry })

	brk := newTestBroker(t)
	w := newInProcessWorker(t, brk)
	w.cfg.ToolsetsDir = t.TempDir()
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	close(released)
	mirror := &blockingFetchAllMirror{entered: make(chan struct{}, 1), release: released}
	mirror.failFetchAll.Store(1 << 30)
	w.packageStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	waitFor(t, func() bool { return mirror.fetchAll.Load() >= 2 }, "the retry loop ran FetchAll again")

	err := w.Health()
	if !errors.Is(err, runtime.ErrDegraded) {
		t.Fatalf("Health() = %v after a failed boot FetchAll, want ErrDegraded", err)
	}
	if w.ReadinessOpen() {
		t.Error("worker reported ready while the boot FetchAll had failed")
	}

	mirror.failFetchAll.Store(0)
	waitFor(t, func() bool { return w.Health() == nil && w.ReadinessOpen() },
		"worker healthy and ready once FetchAll succeeds")
}

func TestRuntime_PendingFetchKeepsLivenessAndFailsReadiness(t *testing.T) {
	brk := newTestBroker(t)
	rt := runtime.NewRuntime(brk, nil, runtime.WithGracePeriod(2*time.Second))
	w := NewWorker(rt.Services(), WorkerConfig{
		WorkerID:  "probe-worker",
		Namespace: "test-ns",
		Sandbox:   SandboxConfig{Mode: "direct"},
	})
	rt.Register("str", w)
	if err := rt.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	mirror := newFlakyMirror(9999)
	w.packageStore = mirror
	if err := rt.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, rt.Ready, "runtime ready with nothing pending")

	if err := brk.PublishGuaranteed(t.Context(), a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.uploaded","name":"outage_pkg"}`)},
	); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return w.fetchesPending() }, "fetch pending after the object store failed")

	if err := rt.Health(); err != nil {
		t.Errorf("runtime Health() = %v with a fetch pending, want healthy so liveness keeps STR", err)
	}
	if rt.Ready() {
		t.Error("runtime Ready() with a fetch pending, want not ready")
	}
}

func TestHealth_DispatchLoopDeathIsUnhealthy(t *testing.T) {
	brk := &queueGoneBroker{Broker: newTestBroker(t)}
	w := newInProcessWorker(t, brk)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	brk.queueName = w.queue.Name()
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	waitFor(t, w.dispatchExited.Load, "dispatch loop exited on a deleted queue")
	err := w.Health()
	if err == nil || errors.Is(err, runtime.ErrDegraded) {
		t.Fatalf("Health() = %v with the dispatch loop dead, want a non-degraded error", err)
	}
	if w.ReadinessOpen() {
		t.Error("worker reported ready with the dispatch loop dead")
	}

	if err := w.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.Health(); err != nil {
		t.Errorf("Health() = %v after Stop, want the stopped loop not to count", err)
	}
}

type queueGoneBroker struct {
	broker.Broker
	queueName string
}

func (b *queueGoneBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	if q.Name() == b.queueName {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, broker.ErrQueueNotFound
	}
	return b.Broker.Receive(ctx, q)
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestStop_CallRetryingItsAnswerAtTheDeadlineIsLeftForRedelivery(t *testing.T) {
	const toolName = "retrying_answer_tool"
	release := make(chan struct{})
	close(release)
	brk := newAckTrackingBroker(t)
	brk.failReplies.Store(true)
	w := newInProcessWorker(t, brk, newBlockingDrainTool(toolName, make(chan struct{}, 4), release))
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	subscribeReply(t, brk, toolName)
	publishDrainInvoke(t, t.Context(), brk, toolName, "req-retrying")
	waitFor(t, func() bool { return brk.failedPublishes.Load() >= 1 }, "first answer attempt failed")

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	time.Sleep(answerPublishAttempts * answerPublishRetryDelay)

	if brk.acked("req-retrying") {
		t.Error("a call still retrying its answer when the stop budget ended was acknowledged; it is not redelivered")
	}
}
