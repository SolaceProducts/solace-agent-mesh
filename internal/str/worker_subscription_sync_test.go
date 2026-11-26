// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type flakyBroker struct {
	*broker.DevBroker
	mu              sync.Mutex
	failSubscribe   map[string]struct{}
	failUnsubscribe map[string]struct{}
}

func newFlakyBroker(t *testing.T) *flakyBroker {
	t.Helper()
	inner := broker.NewDevBroker()
	if err := inner.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inner.Disconnect(context.Background()) }) //nolint:contextcheck
	return &flakyBroker{
		DevBroker:       inner,
		failSubscribe:   make(map[string]struct{}),
		failUnsubscribe: make(map[string]struct{}),
	}
}

func (b *flakyBroker) failSubscribeOn(topic string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failSubscribe[topic] = struct{}{}
}

func (b *flakyBroker) failUnsubscribeOn(topic string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failUnsubscribe[topic] = struct{}{}
}

func (b *flakyBroker) heal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.failSubscribe)
	clear(b.failUnsubscribe)
}

func (b *flakyBroker) fails(set map[string]struct{}, topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := set[topic]
	return ok
}

func (b *flakyBroker) Subscribe(q broker.Queue, topic string) error {
	if b.fails(b.failSubscribe, topic) {
		return errors.New("subscribe denied")
	}
	return b.DevBroker.Subscribe(q, topic)
}

func (b *flakyBroker) Unsubscribe(q broker.Queue, topic string) error {
	if b.fails(b.failUnsubscribe, topic) {
		return errors.New("unsubscribe denied")
	}
	return b.DevBroker.Unsubscribe(q, topic)
}

func (b *flakyBroker) bound(q broker.Queue, topic string) bool {
	return slices.Contains(b.MatchingQueues(topic), q.Name())
}

func writeAlphaBetaManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"alpha_tool", "beta_tool"} {
		writeSchemaStubForManifestTest(t, filepath.Join(dir, name), name, "test")
	}
	manifest := `version: 1
tools:
  alpha_tool:
    executable: ./alpha_tool
    description: "Alpha"
    parameters:
      message:
        type: string
  beta_tool:
    executable: ./beta_tool
    description: "Beta"
    parameters:
      message:
        type: string
`
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSyncSubscriptions_FailedSubscribeDoesNotStopThePass(t *testing.T) {
	brk := newFlakyBroker(t)
	w := newTestWorker(t, &testServices{brk: brk}, writeAlphaBetaManifest(t))
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	alphaTopic := remotetool.InvokeTopic("test-ns", "alpha_tool")
	betaTopic := remotetool.InvokeTopic("test-ns", "beta_tool")
	brk.failSubscribeOn(alphaTopic)

	if err := w.syncSubscriptions(); err == nil {
		t.Fatal("syncSubscriptions reported success although the subscribe for alpha_tool failed")
	}
	if !brk.bound(w.queue, betaTopic) {
		t.Error("beta_tool is not on the queue: the failed subscribe for alpha_tool stopped the pass")
	}
	if !brk.bound(w.queue, remotetool.InitRequestTopic("test-ns", "beta_tool")) {
		t.Error("init_request is not on the queue: the failed subscribe for alpha_tool stopped the pass")
	}

	brk.heal()
	w.retrySubscriptions()
	if !brk.bound(w.queue, alphaTopic) {
		t.Error("alpha_tool is still not on the queue after the broker recovered, so nothing " +
			"retried the failed subscribe")
	}
}

func TestSyncSubscriptions_FailedUnsubscribeIsRetried(t *testing.T) {
	brk := newFlakyBroker(t)
	w := newTestWorker(t, &testServices{brk: brk}, writeAlphaBetaManifest(t))
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	const bundled = "skill__bundled_tool"
	topic := remotetool.InvokeTopic("test-ns", bundled)
	w.manifest.Register(bundled, &ManifestEntry{ToolName: "bundled_tool"})
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("syncSubscriptions: %v", err)
	}
	if !brk.bound(w.queue, topic) {
		t.Fatalf("precondition: %q must be on the queue before it is removed", bundled)
	}

	brk.failUnsubscribeOn(topic)
	w.manifest.Unregister(bundled)
	if err := w.syncSubscriptions(); err == nil {
		t.Fatal("syncSubscriptions reported success although the unsubscribe failed")
	}

	brk.heal()
	w.retrySubscriptions()
	if brk.bound(w.queue, topic) {
		t.Errorf("%q is still on the durable queue after the broker recovered, so the failed "+
			"unsubscribe was forgotten instead of retried", bundled)
	}
}

func TestRebroadcast_AdvertisesToolOnlyOnceItsSubscriptionExists(t *testing.T) {
	brk := newFlakyBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:            "test-worker",
		Namespace:           "test-ns",
		ManifestPaths:       []string{writeAlphaBetaManifest(t)},
		Sandbox:             SandboxConfig{Mode: "direct"},
		RebroadcastInterval: 50 * time.Millisecond,
	})
	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	alphaTopic := remotetool.InvokeTopic("test-ns", "alpha_tool")
	brk.failSubscribeOn(alphaTopic)
	if err := w.syncSubscriptions(); err == nil {
		t.Fatal("precondition: the subscribe for alpha_tool must fail")
	}

	initQueue := func(name, tool string) broker.Queue {
		t.Helper()
		q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
		if err != nil {
			t.Fatal(err)
		}
		if subErr := brk.Subscribe(q, remotetool.InitTopic("test-ns", tool)); subErr != nil {
			t.Fatal(subErr)
		}
		return q
	}
	alphaQ := initQueue("alpha-init-q", "alpha_tool")
	betaQ := initQueue("beta-init-q", "beta_tool")
	advertised := func(q broker.Queue, within time.Duration) bool {
		t.Helper()
		recvCtx, cancel := context.WithTimeout(ctx, within)
		defer cancel()
		msg, err := brk.Receive(recvCtx, q)
		if err != nil || msg == nil {
			return false
		}
		msg.Ack()
		return true
	}

	loopCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.rebroadcastLoop(loopCtx)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})

	if !advertised(betaQ, 2*time.Second) {
		t.Fatal("no init broadcast for beta_tool, so this test cannot tell a working gate apart " +
			"from a broken publisher")
	}
	if advertised(alphaQ, 500*time.Millisecond) {
		t.Error("alpha_tool was advertised by broadcast while its invoke subscription does not exist")
	}
	if reply := requestInitDirect(t, w, brk, "alpha_tool", nil); reply != nil {
		t.Errorf("alpha_tool answered init_request while its invoke subscription does not exist: %s", reply)
	}

	brk.heal()
	if !advertised(alphaQ, 2*time.Second) {
		t.Fatal("alpha_tool was never advertised after the broker recovered")
	}
	if !brk.bound(w.queue, alphaTopic) {
		t.Error("alpha_tool was advertised although its invoke subscription is still not on the queue")
	}
	if reply := requestInitDirect(t, w, brk, "alpha_tool", nil); reply == nil {
		t.Error("alpha_tool still does not answer init_request after its subscription was bound")
	}
}

func TestInitRequest_AliasThatCannotSubscribeIsNotAnswered(t *testing.T) {
	brk := newFlakyBroker(t)
	dir := t.TempDir()
	manifest := `version: 1
tools:
  slack:
    executable: /bin/echo
    description: "Slack connector"
    parameters:
      channel:
        type: string
`
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	const alias = "slack_send_message_abc12345"
	aliasTopic := remotetool.InvokeTopic("test-ns", alias)
	toolConfig := map[string]any{"_str_binary": "slack"}
	brk.failSubscribeOn(aliasTopic)

	if reply := requestInitDirect(t, w, brk, alias, toolConfig); reply != nil {
		t.Fatalf("the alias answered init_request although its invoke subscribe failed: %s", reply)
	}

	brk.heal()
	w.retrySubscriptions()
	if !brk.bound(w.queue, aliasTopic) {
		t.Fatal("the alias is still not on the queue after the broker recovered, so nothing " +
			"retried the failed subscribe")
	}
	if reply := requestInitDirect(t, w, brk, alias, toolConfig); reply == nil {
		t.Error("the alias still does not answer init_request after its subscription was bound")
	}
}
