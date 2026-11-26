// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type closingClient struct {
	id       string
	shutdown atomic.Int32
	lateCall atomic.Int32
	hold     chan struct{}
}

func (c *closingClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if c.shutdown.Load() > 0 {
		c.lateCall.Add(1)
		return nil, errors.New("provider is shutting down")
	}
	if c.hold != nil {
		<-c.hold
	}
	return &llm.ChatResponse{Content: c.id}, nil
}

func (c *closingClient) ChatCompletionStream(ctx context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if c.shutdown.Load() > 0 {
		c.lateCall.Add(1)
		return nil, errors.New("provider is shutting down")
	}
	out := make(chan llm.StreamChunk)
	go func() {
		defer close(out)
		for _, part := range []string{c.id + "-1", c.id + "-2"} {
			if c.hold != nil {
				select {
				case <-c.hold:
				case <-ctx.Done():
					return
				}
			}
			select {
			case out <- llm.StreamChunk{ContentDelta: part}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (c *closingClient) Shutdown() { c.shutdown.Add(1) }

func closingRegistry(t *testing.T, hold chan struct{}) (*BindingsRegistry, *sync.Map) {
	t.Helper()
	var clients sync.Map
	r := New(WithClientBuilder(func(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
		id, _ := raw["id"].(string)
		c := &closingClient{id: id, hold: hold}
		clients.Store(id, c)
		return c, nil
	}))
	return r, &clients
}

func upsert(t *testing.T, r *BindingsRegistry, key, id string) {
	t.Helper()
	if err := r.UpsertModel(t.Context(), key, map[string]any{"id": id}); err != nil {
		t.Fatal(err)
	}
}

func client(clients *sync.Map, id string) *closingClient {
	c, _ := clients.Load(id)
	return c.(*closingClient)
}

func waitShutdown(t *testing.T, c *closingClient) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for c.shutdown.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("client %s was never shut down", c.id)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBinding_ReplacedIdleClientIsShutDown(t *testing.T) {
	t.Parallel()
	r, clients := closingRegistry(t, nil)
	upsert(t, r, "general", "v1")
	upsert(t, r, "general", "v2")

	waitShutdown(t, client(clients, "v1"))
	if client(clients, "v2").shutdown.Load() != 0 {
		t.Fatal("the current client was shut down")
	}
}

func TestBinding_DeletedClientIsShutDownAndAKeptBindingFails(t *testing.T) {
	t.Parallel()
	r, clients := closingRegistry(t, nil)
	upsert(t, r, "research", "v1")
	kept, err := r.ModelClient("research")
	if err != nil {
		t.Fatal(err)
	}

	r.DeleteModel("research")

	waitShutdown(t, client(clients, "v1"))
	if _, err := kept.ChatCompletion(t.Context(), &llm.ChatRequest{}); !errors.Is(err, ErrAliasUnbound) {
		t.Fatalf("call through a removed binding: err = %v, want ErrAliasUnbound", err)
	}
	if got := client(clients, "v1").lateCall.Load(); got != 0 {
		t.Fatalf("%d calls reached the removed client", got)
	}
}

func TestBinding_RunningCallKeepsItsClientUntilItEnds(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	r, clients := closingRegistry(t, hold)
	upsert(t, r, "general", "v1")
	c, _ := r.ModelClient("general")

	done := make(chan string, 1)
	go func() {
		resp, err := c.ChatCompletion(t.Context(), &llm.ChatRequest{})
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		done <- resp.Content
	}()
	time.Sleep(20 * time.Millisecond)
	upsert(t, r, "general", "v2")
	time.Sleep(20 * time.Millisecond)
	if client(clients, "v1").shutdown.Load() != 0 {
		t.Fatal("client shut down while a call was running on it")
	}

	close(hold)
	if got := <-done; got != "v1" {
		t.Fatalf("running call answered %q, want v1", got)
	}
	waitShutdown(t, client(clients, "v1"))
}

func TestBinding_KeptBindingUsesTheNewClient(t *testing.T) {
	t.Parallel()
	r, _ := closingRegistry(t, nil)
	upsert(t, r, "general", "v1")
	kept, _ := r.ModelClient("general")
	if got := servedBy(t, kept); got != "v1" {
		t.Fatalf("first call = %q, want v1", got)
	}

	upsert(t, r, "general", "v2")

	if got := servedBy(t, kept); got != "v2" {
		t.Fatalf("second call through the kept binding = %q, want v2", got)
	}
}

func TestBinding_StreamKeepsItsClientUntilItEnds(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	r, clients := closingRegistry(t, hold)
	upsert(t, r, "general", "v1")
	c, _ := r.ModelClient("general")

	stream, err := c.ChatCompletionStream(t.Context(), &llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	hold <- struct{}{}
	first := <-stream
	upsert(t, r, "general", "v2")
	time.Sleep(20 * time.Millisecond)
	if client(clients, "v1").shutdown.Load() != 0 {
		t.Fatal("client shut down while its stream was open")
	}
	hold <- struct{}{}
	second := <-stream
	if _, open := <-stream; open {
		t.Fatal("stream not closed after its last chunk")
	}

	if first.ContentDelta != "v1-1" || second.ContentDelta != "v1-2" {
		t.Fatalf("chunks = %q, %q; want v1-1, v1-2", first.ContentDelta, second.ContentDelta)
	}
	waitShutdown(t, client(clients, "v1"))
}

func TestBinding_CancelledStreamReleasesItsClient(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	r, clients := closingRegistry(t, hold)
	upsert(t, r, "general", "v1")
	c, _ := r.ModelClient("general")
	ctx, cancel := context.WithCancel(t.Context())

	if _, err := c.ChatCompletionStream(ctx, &llm.ChatRequest{}); err != nil {
		t.Fatal(err)
	}
	upsert(t, r, "general", "v2")
	cancel()

	waitShutdown(t, client(clients, "v1"))
}

func TestBinding_NoCallReachesAShutDownClient(t *testing.T) {
	t.Parallel()
	r, clients := closingRegistry(t, nil)
	upsert(t, r, "general", "v0")
	c, _ := r.ModelClient("general")

	const replacements = 200
	stop := make(chan struct{})
	var failed atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := c.ChatCompletion(t.Context(), &llm.ChatRequest{}); err != nil {
					failed.Add(1)
				}
			}
		})
	}
	for i := 1; i <= replacements; i++ {
		upsert(t, r, "general", fmt.Sprintf("v%d", i))
	}
	close(stop)
	wg.Wait()

	if got := failed.Load(); got != 0 {
		t.Fatalf("%d calls failed during replacements", got)
	}
	for i := range replacements {
		old := client(clients, fmt.Sprintf("v%d", i))
		waitShutdown(t, old)
		if n := old.shutdown.Load(); n != 1 {
			t.Fatalf("client %s shut down %d times, want 1", old.id, n)
		}
		if n := old.lateCall.Load(); n != 0 {
			t.Fatalf("%d calls reached client %s after its shutdown", n, old.id)
		}
	}
	if client(clients, fmt.Sprintf("v%d", replacements)).shutdown.Load() != 0 {
		t.Fatal("the current client was shut down")
	}
}

type stubbornStreamClient struct {
	ended chan struct{}
}

func (c *stubbornStreamClient) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (c *stubbornStreamClient) ChatCompletionStream(ctx context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	out := make(chan llm.StreamChunk)
	go func() {
		defer close(c.ended)
		defer close(out)
		<-ctx.Done()
		out <- llm.StreamChunk{ContentDelta: "late-1"}
		out <- llm.StreamChunk{ContentDelta: "late-2"}
	}()
	return out, nil
}

func TestBinding_CancelledStreamLetsTheInnerStreamFinish(t *testing.T) {
	t.Parallel()
	inner := &stubbornStreamClient{ended: make(chan struct{})}
	r := New(WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
		return inner, nil
	}))
	upsert(t, r, "general", "v1")
	c, _ := r.ModelClient("general")
	ctx, cancel := context.WithCancel(t.Context())
	if _, err := c.ChatCompletionStream(ctx, &llm.ChatRequest{}); err != nil {
		t.Fatal(err)
	}

	cancel()

	select {
	case <-inner.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("inner stream goroutine still blocked after the cancel")
	}
}
