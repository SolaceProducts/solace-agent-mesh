// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type platformStub struct {
	mu       sync.Mutex
	rows     map[string]map[string]any
	failing  map[string]bool
	gate     chan struct{}
	calls    atomic.Int32
	inFlight atomic.Int32
	maxInFly atomic.Int32
}

func newPlatformStub() *platformStub {
	return &platformStub{rows: map[string]map[string]any{}, failing: map[string]bool{}}
}

func (p *platformStub) set(key, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" {
		delete(p.rows, key)
		return
	}
	p.rows[key] = map[string]any{"id": id}
}

func (p *platformStub) fail(key string, failing bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failing[key] = failing
}

func (p *platformStub) fetch(_ context.Context, key string) (map[string]any, error) {
	n := p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for {
		m := p.maxInFly.Load()
		if n <= m || p.maxInFly.CompareAndSwap(m, n) {
			break
		}
	}
	p.mu.Lock()
	row, failing, gate := p.rows[key], p.failing[key], p.gate
	p.mu.Unlock()
	p.calls.Add(1)
	if gate != nil {
		<-gate
	}
	if failing {
		return nil, errors.New("platform unreachable")
	}
	return row, nil
}

func newPullRegistry(t *testing.T, p *platformStub) *BindingsRegistry {
	t.Helper()
	r := New(WithFetcher(p.fetch), WithClientBuilder(stubBuilder(nil)), WithRetryBackoff(5*time.Millisecond, 20*time.Millisecond))
	t.Cleanup(r.Close)
	return r
}

func boundID(r *BindingsRegistry, key string) string {
	c, err := r.ModelClient(key)
	if err != nil {
		return ""
	}
	resp, err := c.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		return ""
	}
	return resp.Content
}

func waitBound(t *testing.T, r *BindingsRegistry, key, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if boundID(r, key) == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("binding %q = %q, want %q", key, boundID(r, key), want)
}

func waitIdle(t *testing.T, r *BindingsRegistry) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.lineMu.Lock()
		n := len(r.lines)
		r.lineMu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("pull lines still running")
}

func TestNotify_HeldKeyIsPulledAndSwapped(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	if err := r.UpsertModel(t.Context(), "general", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}
	p.set("general", "v2")

	r.Notify("general")

	waitBound(t, r, "general", "v2")
}

func TestNotify_KeyNotHeldNorWantedIsLeftAlone(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	p.set("research", "v1")
	r := newPullRegistry(t, p)

	r.Notify("research")
	waitIdle(t, r)

	if got := p.calls.Load(); got != 0 {
		t.Fatalf("platform pulled %d times, want 0", got)
	}
	if boundID(r, "research") != "" {
		t.Fatal("unheld key was installed")
	}
}

func TestNotify_WantedKeyIsInstalled(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	p.set("research", "v1")
	r := newPullRegistry(t, p)
	r.Want(func(key string) bool { return key == "research" })

	r.Notify("research")

	waitBound(t, r, "research", "v1")
}

func TestNotify_FailedPullKeepsBindingUntilARetrySucceeds(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	if err := r.UpsertModel(t.Context(), "general", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}
	p.set("general", "v2")
	p.fail("general", true)

	r.Notify("general")
	deadline := time.Now().Add(2 * time.Second)
	for p.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := boundID(r, "general"); got != "v1" {
		t.Fatalf("binding during failed pulls = %q, want v1 kept", got)
	}

	p.fail("general", false)
	waitBound(t, r, "general", "v2")
	waitIdle(t, r)
}

func TestNotify_NotFoundDropsTheBinding(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	if err := r.UpsertModel(t.Context(), "research", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}

	r.Notify("research")

	waitBound(t, r, "research", "")
}

func TestNotify_NotFoundKeepsASystemAlias(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	if err := r.UpsertModel(t.Context(), "general", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}

	r.Notify("general")
	waitIdle(t, r)

	if got := boundID(r, "general"); got != "v1" {
		t.Fatalf("general = %q, want v1 kept", got)
	}
}

func TestNotify_PullsForOneKeyRunInOrder(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	if err := r.UpsertModel(t.Context(), "general", map[string]any{"id": "v0"}); err != nil {
		t.Fatal(err)
	}
	p.set("general", "v1")
	gate := make(chan struct{})
	p.mu.Lock()
	p.gate = gate
	p.mu.Unlock()

	r.Notify("general")
	waitCalls(t, p, 1)
	p.set("general", "v2")
	r.Notify("general")
	p.mu.Lock()
	p.gate = nil
	p.mu.Unlock()
	close(gate)

	waitIdle(t, r)
	if got := boundID(r, "general"); got != "v2" {
		t.Fatalf("general = %q, want v2", got)
	}
	if got := p.maxInFly.Load(); got != 1 {
		t.Fatalf("pulls of one key ran %d at a time, want 1", got)
	}
}

func TestLazyFetchOverlappingAChangeEndsOnTheNewRow(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	p.set("research", "v1")
	gate := make(chan struct{})
	p.mu.Lock()
	p.gate = gate
	p.mu.Unlock()

	lazy := make(chan error, 1)
	go func() {
		_, err := r.ResolveOrFetch(t.Context(), "research")
		lazy <- err
	}()
	waitCalls(t, p, 1)
	p.set("research", "v2")
	r.Notify("research")
	p.mu.Lock()
	p.gate = nil
	p.mu.Unlock()
	close(gate)

	if err := <-lazy; err != nil {
		t.Fatalf("lazy fetch: %v", err)
	}
	waitIdle(t, r)
	if got := boundID(r, "research"); got != "v2" {
		t.Fatalf("research = %q, want v2", got)
	}
}

func TestResolveOrFetch_JoinsThePullInProgress(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	p.set("research", "v1")
	gate := make(chan struct{})
	p.mu.Lock()
	p.gate = gate
	p.mu.Unlock()

	const callers = 5
	errs := make(chan error, callers)
	for range callers {
		go func() {
			_, err := r.ResolveOrFetch(t.Context(), "research")
			errs <- err
		}()
	}
	waitCalls(t, p, 1)
	time.Sleep(20 * time.Millisecond)
	close(gate)
	for range callers {
		if err := <-errs; err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("platform pulled %d times, want 1", got)
	}
}

func TestResolveOrFetch_FailureIsNotRetried(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	p.fail("research", true)
	r := newPullRegistry(t, p)

	if _, err := r.ResolveOrFetch(t.Context(), "research"); err == nil {
		t.Fatal("want an error from a failed lazy fetch")
	}
	waitIdle(t, r)
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("platform pulled %d times, want 1", got)
	}
}

func TestResolveOrFetch_CutsARetryWaitShort(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := New(WithFetcher(p.fetch), WithClientBuilder(stubBuilder(nil)), WithRetryBackoff(time.Hour, time.Hour))
	t.Cleanup(r.Close)
	r.Want(func(string) bool { return true })
	p.set("research", "v1")
	p.fail("research", true)

	r.Notify("research")
	waitRetryWait(t, r, p, "research")
	p.fail("research", false)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := r.ResolveOrFetch(ctx, "research"); err != nil {
		t.Fatalf("resolve during retry wait: %v", err)
	}
}

func TestOnChange_RunsOnStoreAndDelete(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	var mu sync.Mutex
	var got []string
	r.OnChange(func(key string) {
		mu.Lock()
		got = append(got, key)
		mu.Unlock()
	})

	if err := r.UpsertModel(t.Context(), "general", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}
	r.DeleteModel("general")
	r.DeleteModel("general")

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "general" || got[1] != "general" {
		t.Fatalf("hook calls = %v, want [general general]", got)
	}
}

func TestClose_StopsRetries(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := New(WithFetcher(p.fetch), WithClientBuilder(stubBuilder(nil)), WithRetryBackoff(5*time.Millisecond, 5*time.Millisecond))
	r.Want(func(string) bool { return true })
	p.fail("general", true)

	r.Notify("general")
	waitCalls(t, p, 2)
	r.Close()
	waitIdle(t, r)
	calls := p.calls.Load()
	time.Sleep(30 * time.Millisecond)

	if got := p.calls.Load(); got != calls {
		t.Fatalf("pulls continued after Close: %d then %d", calls, got)
	}
}

func TestRefresh_PullsEvenAKeyNotHeld(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	p.set("research", "v1")
	r := newPullRegistry(t, p)

	r.Refresh("research")

	waitBound(t, r, "research", "v1")
}

func waitRetryWait(t *testing.T, r *BindingsRegistry, p *platformStub, key string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.lineMu.Lock()
		l := r.lines[key]
		parked := p.calls.Load() > 0 && l != nil && l.current == nil && len(l.queue) > 0
		r.lineMu.Unlock()
		if parked {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pull of %q is not waiting to retry", key)
}

func waitCalls(t *testing.T, p *platformStub, n int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for p.calls.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("platform pulled %d times, want at least %d", p.calls.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNotify_UnchangedConfigIsNotRebuilt(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	var builds atomic.Int32
	r := New(WithFetcher(p.fetch), WithClientBuilder(stubBuilder(&builds)), WithRetryBackoff(5*time.Millisecond, 20*time.Millisecond))
	t.Cleanup(r.Close)
	if err := r.UpsertModel(t.Context(), "general", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	r.OnChange(func(string) { changes.Add(1) })
	p.set("general", "v1")

	r.Notify("general")
	r.Notify("general")
	waitIdle(t, r)

	if got := builds.Load(); got != 1 {
		t.Fatalf("clients built = %d, want 1", got)
	}
	if got := changes.Load(); got != 0 {
		t.Fatalf("change hook ran %d times for an unchanged config", got)
	}
}

func TestNotify_ConfigThatDoesNotBuildIsNotRetried(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	build := stubBuilder(nil)
	r := New(WithFetcher(p.fetch), WithRetryBackoff(5*time.Millisecond, 20*time.Millisecond),
		WithClientBuilder(func(ctx context.Context, raw map[string]any, l *slog.Logger) (llm.Client, error) {
			if raw["id"] == "bad" {
				return nil, errors.New("config does not build")
			}
			return build(ctx, raw, l)
		}))
	t.Cleanup(r.Close)
	if err := r.UpsertModel(t.Context(), "general", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}
	p.set("general", "bad")

	r.Notify("general")
	waitIdle(t, r)
	time.Sleep(50 * time.Millisecond)

	if got := p.calls.Load(); got != 1 {
		t.Fatalf("platform pulled %d times, want 1", got)
	}
	if got := boundID(r, "general"); got != "v1" {
		t.Fatalf("general = %q, want v1 kept", got)
	}

	p.set("general", "v2")
	r.Notify("general")
	waitBound(t, r, "general", "v2")
}
