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

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

type stubClient struct {
	id string
}

func (s *stubClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: s.id}, nil
}

func (s *stubClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{ContentDelta: s.id, FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func servedBy(t *testing.T, c llm.Client) string {
	t.Helper()
	resp, err := c.ChatCompletion(t.Context(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("call through binding: %v", err)
	}
	return resp.Content
}

func stubBuilder(build *atomic.Int32) ClientBuilder {
	return func(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
		if build != nil {
			build.Add(1)
		}
		id, _ := raw["id"].(string)
		if id == "" {
			id = "anon"
		}
		return &stubClient{id: id}, nil
	}
}

func TestDefaultClientBuilder_WrapsInInstrumentation(t *testing.T) {
	t.Parallel()
	c, err := DefaultClientBuilder(t.Context(), map[string]any{
		"model":   "openai/test-model",
		"api_key": "test-key",
	}, slog.Default())
	if err != nil {
		t.Fatalf("DefaultClientBuilder: %v", err)
	}
	if sd, ok := c.(interface{ Shutdown() }); ok {
		defer sd.Shutdown()
	}
	if _, ok := c.(*observability.InstrumentedLLMClient); !ok {
		t.Fatalf("DefaultClientBuilder must wrap the client in *observability.InstrumentedLLMClient; got %T", c)
	}
}

func TestModelClient_Unbound(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	_, err := r.ModelClient("planning")
	if err == nil {
		t.Fatal("expected error for unbound alias")
	}
	if !errors.Is(err, ErrAliasUnbound) {
		t.Errorf("want ErrAliasUnbound, got %v", err)
	}
}

func TestUpsertModel_NewAlias(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))

	if err := r.UpsertModel(context.Background(), "planning", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}

	c, err := r.ModelClient("planning")
	if err != nil {
		t.Fatal(err)
	}
	if got := servedBy(t, c); got != "v1" {
		t.Errorf("id = %q, want v1", got)
	}
}

func TestUpsertModel_ExistingAliasSwaps(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))

	_ = r.UpsertModel(context.Background(), "planning", map[string]any{"id": "v1"})
	_ = r.UpsertModel(context.Background(), "planning", map[string]any{"id": "v2"})

	c, _ := r.ModelClient("planning")
	if got := servedBy(t, c); got != "v2" {
		t.Errorf("id = %q, want v2 after re-upsert", got)
	}
}

func TestUpsertModel_RejectsEmptyAlias(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	if err := r.UpsertModel(context.Background(), "", map[string]any{}); err == nil {
		t.Fatal("expected error for empty alias")
	}
}

func TestUpsertModel_RejectsNilConfig(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	if err := r.UpsertModel(context.Background(), "planning", nil); err == nil {
		t.Fatal("expected error for nil raw config")
	}
}

func TestUpsertModel_BuilderFailure_LeavesPriorBindingIntact(t *testing.T) {
	t.Parallel()
	var builderErr atomic.Value
	builder := func(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
		if e, ok := builderErr.Load().(error); ok && e != nil {
			return nil, e
		}
		id, _ := raw["id"].(string)
		return &stubClient{id: id}, nil
	}

	r := New(WithClientBuilder(builder))
	if err := r.UpsertModel(context.Background(), "planning", map[string]any{"id": "good"}); err != nil {
		t.Fatal(err)
	}

	builderErr.Store(errors.New("boom"))
	if err := r.UpsertModel(context.Background(), "planning", map[string]any{"id": "bad"}); err == nil {
		t.Fatal("expected builder error to propagate")
	}

	c, err := r.ModelClient("planning")
	if err != nil {
		t.Fatal(err)
	}
	if got := servedBy(t, c); got != "good" {
		t.Errorf("id = %q, want good (prior binding preserved)", got)
	}
}

func TestUpsertModel_NewAliasVisibleAtomically(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	builderCalled := make(chan struct{})
	builder := func(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
		close(builderCalled)
		<-release
		id, _ := raw["id"].(string)
		return &stubClient{id: id}, nil
	}
	r := New(WithClientBuilder(builder))

	upsertDone := make(chan error, 1)
	go func() {
		upsertDone <- r.UpsertModel(context.Background(), "planning", map[string]any{"id": "v1"})
	}()

	<-builderCalled
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-readerDone:
				return
			default:
			}
			if c, err := r.ModelClient("planning"); err == nil {
				if c == nil {
					t.Error("reader saw binding present but client nil")
				}
				return
			}
		}
	}()

	close(release)
	if err := <-upsertDone; err != nil {
		t.Fatalf("upsert: %v", err)
	}
	<-readerDone
}

func TestConcurrentReadDuringUpsert(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	if err := r.UpsertModel(context.Background(), "planning", map[string]any{"id": "v0"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				c, err := r.ModelClient("planning")
				if err != nil {
					t.Errorf("reader %d: unexpected error %v", i, err)
					return
				}
				if resp, err := c.ChatCompletion(t.Context(), &llm.ChatRequest{}); err != nil || resp.Content == "" {
					t.Errorf("reader %d: call through binding failed: %v", i, err)
					return
				}
			}
		})
	}

	for i := range 100 {
		id := "v" + itoa(i)
		if err := r.UpsertModel(ctx, "planning", map[string]any{"id": id}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	cancel()
	wg.Wait()
}

func TestSnapshot_Isolation(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	raw := map[string]any{"id": "v1", "nested": map[string]any{"k": "v"}}
	if err := r.UpsertModel(context.Background(), "planning", raw); err != nil {
		t.Fatal(err)
	}

	snap1 := r.Snapshot()
	models, ok := snap1["models"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot models type = %T", snap1["models"])
	}
	if _, ok := models["planning"]; !ok {
		t.Fatal("snapshot missing planning alias")
	}

	models["planning"] = "tampered"
	raw["id"] = "mutated"

	snap2 := r.Snapshot()
	again := snap2["models"].(map[string]any)["planning"].(map[string]any)
	if again["id"] != "v1" {
		t.Errorf("id = %v, want v1 (registry took a defensive copy)", again["id"])
	}
}

func TestDeleteModel_RemovesBinding(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	if err := r.UpsertModel(context.Background(), "planning", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}
	if got := r.DeleteModel("planning"); !got {
		t.Fatal("DeleteModel returned false for present alias")
	}
	if _, err := r.ModelClient("planning"); !errors.Is(err, ErrAliasUnbound) {
		t.Errorf("after delete, want ErrAliasUnbound, got %v", err)
	}
	models := r.Snapshot()["models"].(map[string]any)
	if _, ok := models["planning"]; ok {
		t.Errorf("snapshot still contains deleted alias: %v", models)
	}
}

func TestDeleteModel_MissingAliasReturnsFalse(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	if got := r.DeleteModel("never-set"); got {
		t.Errorf("DeleteModel of missing alias returned true")
	}
	if got := r.DeleteModel(""); got {
		t.Errorf("DeleteModel of empty alias returned true")
	}
}

func TestResolveOrFetch_ServesCached(t *testing.T) {
	t.Parallel()
	var builds atomic.Int32
	r := New(WithClientBuilder(stubBuilder(&builds)))
	if err := r.UpsertModel(context.Background(), "planning", map[string]any{"id": "cached"}); err != nil {
		t.Fatal(err)
	}
	builds.Store(0)

	c, err := r.ResolveOrFetch(context.Background(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	if got := servedBy(t, c); got != "cached" {
		t.Errorf("id = %q, want cached", got)
	}
	if builds.Load() != 0 {
		t.Errorf("builder invoked %d times, expected 0 for cache hit", builds.Load())
	}
}

func TestResolveOrFetch_FetchesAndCaches(t *testing.T) {
	t.Parallel()
	var fetches atomic.Int32
	fetcher := func(_ context.Context, alias string) (map[string]any, error) {
		fetches.Add(1)
		return map[string]any{"id": alias + "-fetched"}, nil
	}
	var builds atomic.Int32
	r := New(WithFetcher(fetcher), WithClientBuilder(stubBuilder(&builds)))

	c, err := r.ResolveOrFetch(context.Background(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	if got := servedBy(t, c); got != "planning-fetched" {
		t.Errorf("id = %q, want planning-fetched", got)
	}

	_, err = r.ResolveOrFetch(context.Background(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("fetches = %d, want 1", n)
	}
	if n := builds.Load(); n != 1 {
		t.Errorf("builds = %d, want 1", n)
	}
}

func TestResolveOrFetch_NoFetcher_ReturnsUnbound(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	_, err := r.ResolveOrFetch(context.Background(), "planning")
	if err == nil {
		t.Fatal("expected ErrAliasUnbound when no fetcher is configured")
	}
	if !errors.Is(err, ErrAliasUnbound) {
		t.Errorf("want ErrAliasUnbound, got %v", err)
	}
}

func TestResolveOrFetch_FetcherReturnsNilConfig(t *testing.T) {
	t.Parallel()
	fetcher := func(_ context.Context, _ string) (map[string]any, error) { return nil, nil }
	r := New(WithFetcher(fetcher), WithClientBuilder(stubBuilder(nil)))
	_, err := r.ResolveOrFetch(context.Background(), "planning")
	if err == nil || !errors.Is(err, ErrAliasUnbound) {
		t.Fatalf("want ErrAliasUnbound, got %v", err)
	}
}

func TestResolveOrFetch_DetachesFetchFromFirstCallerCtx(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	fetcher := func(ctx context.Context, _ string) (map[string]any, error) {
		close(started)
		select {
		case <-release:
			return map[string]any{"id": "ok"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r := New(WithFetcher(fetcher), WithClientBuilder(stubBuilder(nil)))

	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := r.ResolveOrFetch(ctxA, "planning")
		errA <- err
	}()

	<-started

	errB := make(chan error, 1)
	go func() {
		_, err := r.ResolveOrFetch(context.Background(), "planning")
		errB <- err
	}()

	cancelA()
	close(release)

	if err := <-errB; err != nil {
		t.Fatalf("coalesced caller observed first caller's cancellation: %v", err)
	}
	<-errA
}

func TestResolveRawOrFetch_SharesCacheWithResolveOrFetch(t *testing.T) {
	t.Parallel()
	var fetches atomic.Int32
	var builds atomic.Int32
	fetcher := func(_ context.Context, alias string) (map[string]any, error) {
		fetches.Add(1)
		return map[string]any{"id": alias + "-fetched", "extra": "keep-me"}, nil
	}
	r := New(WithFetcher(fetcher), WithClientBuilder(stubBuilder(&builds)))

	raw, err := r.ResolveRawOrFetch(context.Background(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	if raw["id"] != "planning-fetched" || raw["extra"] != "keep-me" {
		t.Errorf("raw = %v, want {id: planning-fetched, extra: keep-me}", raw)
	}

	raw["id"] = "mutated"
	raw2, _ := r.ResolveRawOrFetch(context.Background(), "planning")
	if raw2["id"] != "planning-fetched" {
		t.Errorf("second Raw = %v, registry must return a defensive copy", raw2)
	}

	c, err := r.ResolveOrFetch(context.Background(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	if got := servedBy(t, c); got != "planning-fetched" {
		t.Errorf("client id = %q, want planning-fetched", got)
	}

	if n := fetches.Load(); n != 1 {
		t.Errorf("fetches = %d, want 1 (one fetch should cover both raw and client paths)", n)
	}
	if n := builds.Load(); n != 1 {
		t.Errorf("builds = %d, want 1", n)
	}
}

func TestResolveRawOrFetch_NoFetcher_ReturnsUnbound(t *testing.T) {
	t.Parallel()
	r := New(WithClientBuilder(stubBuilder(nil)))
	_, err := r.ResolveRawOrFetch(context.Background(), "planning")
	if err == nil || !errors.Is(err, ErrAliasUnbound) {
		t.Fatalf("want ErrAliasUnbound, got %v", err)
	}
}

func TestResolveOrFetch_SingleflightCoalesces(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var fetches atomic.Int32
	fetcher := func(ctx context.Context, _ string) (map[string]any, error) {
		fetches.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string]any{"id": "one"}, nil
	}
	r := New(WithFetcher(fetcher), WithClientBuilder(stubBuilder(nil)))

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := r.ResolveOrFetch(context.Background(), "planning"); err != nil {
				t.Errorf("resolve: %v", err)
			}
		})
	}

	close(release)
	wg.Wait()

	if n := fetches.Load(); n != 1 {
		t.Errorf("fetcher invoked %d times; want 1 (singleflight coalescing broken)", n)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
