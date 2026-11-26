// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	authzListenerTestNamespace = "default"
	authzListenerTestWait      = 10 * time.Second
	cachedLookupKey            = "cached-lookup"
)

type countingSnapshotSource struct {
	lookup *AuthzLookupHandler
	calls  chan bool

	mu       sync.Mutex
	failures int
}

func (s *countingSnapshotSource) Snapshot(context.Context) (*rbac.DBState, error) {
	_, cached := s.lookup.cache.Get(cachedLookupKey)
	s.mu.Lock()
	fail := s.failures > 0
	if fail {
		s.failures--
	}
	s.mu.Unlock()
	s.calls <- cached
	if fail {
		return nil, errors.New("database unavailable")
	}
	return &rbac.DBState{}, nil
}

type authzListenerFixture struct {
	brk    *broker.DevBroker
	source *countingSnapshotSource
	lookup *AuthzLookupHandler
	relay  *AuthzChangePublisher
}

func newAuthzListenerFixture(t *testing.T, failures int) *authzListenerFixture {
	t.Helper()
	brk := connectedDevBroker(t)
	source := &countingSnapshotSource{calls: make(chan bool, 16), failures: failures}
	composer := rbac.NewComposer(rbac.YAMLState{}, source, slog.Default())
	lookup := NewAuthzLookupHandler(composer, nil, brk, AuthzLookupConfig{Namespace: authzListenerTestNamespace}, slog.Default())
	source.lookup = lookup
	lookup.cache.Set(cachedLookupKey, true)

	listener := NewAuthzChangeListener(composer, lookup, brk, authzListenerTestNamespace, nil, slog.Default())
	listener.retryInitial = time.Millisecond
	listener.retryMax = time.Millisecond
	if err := listener.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = listener.Stop(context.Background()) }) //nolint:contextcheck
	return &authzListenerFixture{
		brk:    brk,
		source: source,
		lookup: lookup,
		relay:  NewAuthzChangePublisher(brk, authzListenerTestNamespace),
	}
}

func (f *authzListenerFixture) awaitSnapshot(t *testing.T, step string) bool {
	t.Helper()
	select {
	case cached := <-f.source.calls:
		return cached
	case <-time.After(authzListenerTestWait):
		t.Fatalf("%s: no role graph rebuild", step)
		return false
	}
}

func (f *authzListenerFixture) awaitLookupCleared(t *testing.T, step string) {
	t.Helper()
	if !waitForCondition(t, authzListenerTestWait, func() bool {
		_, cached := f.lookup.cache.Get(cachedLookupKey)
		return !cached
	}) {
		t.Fatalf("%s: the lookup cache was not cleared", step)
	}
}

func (f *authzListenerFixture) publish(t *testing.T, eventType string) {
	t.Helper()
	if err := f.relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: eventType, AggregateID: "id", Payload: "{}"}); err != nil {
		t.Fatalf("publish %s: %v", eventType, err)
	}
}

func TestAuthzChangeListener_StartRebuildsThenClearsTheCache(t *testing.T) {
	t.Parallel()
	f := newAuthzListenerFixture(t, 0)

	if cached := f.awaitSnapshot(t, "start"); !cached {
		t.Error("start: the cache was cleared before the rebuild")
	}
	f.awaitLookupCleared(t, "start")
}

func TestAuthzChangeListener_RolesChangeRebuildsThenClearsTheCache(t *testing.T) {
	t.Parallel()
	f := newAuthzListenerFixture(t, 0)
	f.awaitSnapshot(t, "start")
	f.awaitLookupCleared(t, "start")
	f.lookup.cache.Set(cachedLookupKey, true)

	f.publish(t, authzOutboxRolesChanged)

	if cached := f.awaitSnapshot(t, "roles changed"); !cached {
		t.Error("roles changed: the cache was cleared before the rebuild")
	}
	f.awaitLookupCleared(t, "roles changed")
}

func TestAuthzChangeListener_UsersChangeClearsTheCacheWithoutRebuilding(t *testing.T) {
	t.Parallel()
	f := newAuthzListenerFixture(t, 0)
	f.awaitSnapshot(t, "start")
	f.awaitLookupCleared(t, "start")
	f.lookup.cache.Set(cachedLookupKey, true)

	f.publish(t, authzOutboxUsersChanged)

	f.awaitLookupCleared(t, "users changed")
	if n := len(f.source.calls); n != 0 {
		t.Errorf("users changed: %d role graph rebuilds, want 0", n)
	}
}

func TestAuthzChangeListener_RetriesAFailedRebuildBeforeClearingTheCache(t *testing.T) {
	t.Parallel()
	f := newAuthzListenerFixture(t, 2)

	for attempt := range 3 {
		if cached := f.awaitSnapshot(t, "start"); !cached {
			t.Fatalf("attempt %d: the cache was cleared before a rebuild succeeded", attempt+1)
		}
	}
	f.awaitLookupCleared(t, "after the retries")
}

func TestAuthzChangeListener_StopReleasesTheSubscription(t *testing.T) {
	t.Parallel()
	brk := connectedDevBroker(t)
	composer := rbac.NewComposer(rbac.YAMLState{}, nil, slog.Default())
	listener := NewAuthzChangeListener(composer, nil, brk, authzListenerTestNamespace, nil, slog.Default())
	if err := listener.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := listener.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, topic := range []string{a2a.AuthzRolesChangedTopic(authzListenerTestNamespace), a2a.AuthzUsersChangedTopic(authzListenerTestNamespace)} {
		if queues := brk.MatchingQueues(topic); len(queues) != 0 {
			t.Errorf("%s still reaches %v after Stop", topic, queues)
		}
	}
}

type handledCountingBroker struct {
	*broker.DevBroker
	delivered atomic.Int64
	handled   atomic.Int64
}

func (b *handledCountingBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	b.handled.Store(b.delivered.Load())
	msg, err := b.DevBroker.Receive(ctx, q)
	if msg != nil {
		b.delivered.Add(1)
	}
	return msg, err
}

type gatedSnapshotSource struct {
	n       atomic.Int32
	entered chan int32
	release chan struct{}
}

func (s *gatedSnapshotSource) Snapshot(context.Context) (*rbac.DBState, error) {
	s.entered <- s.n.Add(1)
	<-s.release
	return &rbac.DBState{}, nil
}

type gatedListenerFixture struct {
	brk    *handledCountingBroker
	source *gatedSnapshotSource
	relay  *AuthzChangePublisher
}

func newGatedListenerFixture(t *testing.T) *gatedListenerFixture {
	t.Helper()
	brk := &handledCountingBroker{DevBroker: connectedDevBroker(t)}
	source := &gatedSnapshotSource{entered: make(chan int32, 8), release: make(chan struct{})}
	composer := rbac.NewComposer(rbac.YAMLState{}, source, slog.Default())
	listener := NewAuthzChangeListener(composer, nil, brk, authzListenerTestNamespace, nil, slog.Default())
	started := make(chan error, 1)
	go func() { started <- listener.Start(t.Context()) }()
	t.Cleanup(func() {
		close(source.release)
		if err := <-started; err == nil {
			_ = listener.Stop(context.Background()) //nolint:contextcheck
		}
	})
	return &gatedListenerFixture{brk: brk, source: source, relay: NewAuthzChangePublisher(brk, authzListenerTestNamespace)}
}

func (f *gatedListenerFixture) awaitSnapshot(t *testing.T, want int32) {
	t.Helper()
	select {
	case got := <-f.source.entered:
		if got != want {
			t.Fatalf("snapshot %d entered, want %d", got, want)
		}
	case <-time.After(authzListenerTestWait):
		t.Fatalf("snapshot %d never started", want)
	}
}

func (f *gatedListenerFixture) publishRoles(t *testing.T) {
	t.Helper()
	if err := f.relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: authzOutboxRolesChanged, AggregateID: "id", Payload: "{}"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestAuthzChangeListener_ChangeDuringTheStartRebuildIsApplied(t *testing.T) {
	t.Parallel()
	f := newGatedListenerFixture(t)
	f.awaitSnapshot(t, 1)

	f.publishRoles(t)
	f.source.release <- struct{}{}

	f.awaitSnapshot(t, 2)
	f.source.release <- struct{}{}
}

func TestAuthzChangeListener_ChangesDuringARebuildCollapseIntoOneMore(t *testing.T) {
	t.Parallel()
	f := newGatedListenerFixture(t)
	f.awaitSnapshot(t, 1)
	f.source.release <- struct{}{}

	f.publishRoles(t)
	f.awaitSnapshot(t, 2)
	for range 3 {
		f.publishRoles(t)
	}
	if !waitForCondition(t, authzListenerTestWait, func() bool { return f.brk.handled.Load() >= 4 }) {
		t.Fatalf("the listener handled %d of 4 messages", f.brk.handled.Load())
	}
	f.source.release <- struct{}{}

	f.awaitSnapshot(t, 3)
	f.source.release <- struct{}{}
	select {
	case n := <-f.source.entered:
		t.Fatalf("snapshot %d started; three messages during one rebuild should cause one more", n)
	case <-time.After(200 * time.Millisecond):
	}
}
