// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type stubClient struct {
	id    string
	calls atomic.Int64
}

func (s *stubClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	s.calls.Add(1)
	return &llm.ChatResponse{}, nil
}

func (s *stubClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func TestReconfigurableClient_Delegates(t *testing.T) {
	a := &stubClient{id: "a"}
	rc := llm.NewReconfigurableClient(a)

	_, err := rc.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	_, err = rc.ChatCompletionStream(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
}

func TestReconfigurableClient_Reconfigure_ReturnsOld(t *testing.T) {
	a := &stubClient{id: "a"}
	b := &stubClient{id: "b"}
	rc := llm.NewReconfigurableClient(a)

	old := rc.Reconfigure(b)
	if old == nil {
		t.Fatal("Reconfigure must return old client")
	}
	if old.(*stubClient).id != "a" {
		t.Errorf("returned old client id = %q, want %q", old.(*stubClient).id, "a")
	}

	if _, err := rc.ChatCompletion(context.Background(), &llm.ChatRequest{}); err != nil {
		t.Fatalf("ChatCompletion after reconfigure: %v", err)
	}
	if got := b.calls.Load(); got != 1 {
		t.Errorf("post-reconfigure call hit new client %d times, want 1", got)
	}
	if got := a.calls.Load(); got != 0 {
		t.Errorf("post-reconfigure call wrongly hit old client %d times, want 0", got)
	}
}

func TestReconfigurableClient_ConcurrentRace(t *testing.T) {
	initial := &stubClient{id: "initial"}
	rc := llm.NewReconfigurableClient(initial)

	const workers = 20
	const iters = 100
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iters {
				if _, err := rc.ChatCompletion(context.Background(), &llm.ChatRequest{}); err != nil {
					t.Errorf("ChatCompletion: %v", err)
					return
				}
			}
		}()
	}

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc.Reconfigure(&stubClient{id: "replacement"})
		}()
	}

	wg.Wait()
}

func TestReconfigureAdvancesTheGeneration(t *testing.T) {
	rc := llm.NewReconfigurableClient(&stubClient{id: "a"})
	if rc.Generation() != 0 {
		t.Fatalf("generation %d before any reconfiguration, want 0", rc.Generation())
	}
	rc.Reconfigure(&stubClient{id: "b"})
	rc.Reconfigure(&stubClient{id: "c"})
	if rc.Generation() != 2 {
		t.Errorf("generation %d after two reconfigurations, want 2", rc.Generation())
	}
}

func TestTheModelNameFollowsTheClientItWasSwappedWith(t *testing.T) {
	rc := llm.NewReconfigurableClientFor(&stubClient{id: "boot"}, "claude-sonnet-5-5")
	if model := rc.Model(); model != "claude-sonnet-5-5" {
		t.Fatalf("model %q at startup, want the one the client was built for", model)
	}
	if model := llm.NewReconfigurableClient(&stubClient{id: "unnamed"}).Model(); model != "" {
		t.Errorf("model %q for a client built without one, want none", model)
	}

	rc.ReconfigureTo(&stubClient{id: "next"}, "config-2", "claude-sonnet-4-6")
	if model, id := rc.Model(), rc.ModelConfigID(); model != "claude-sonnet-4-6" || id != "config-2" {
		t.Errorf("model %q and configuration %q after a swap, want claude-sonnet-4-6 and config-2", model, id)
	}

	rc.ReconfigureFrom(&stubClient{id: "from-config"}, "config-3")
	if model := rc.Model(); model != "" {
		t.Errorf("model %q after a swap that named none, want none rather than the old model's", model)
	}
	rc.ReconfigureTo(&stubClient{id: "named"}, "", "claude-opus-5-5")
	rc.Reconfigure(&stubClient{id: "plain"})
	if model := rc.Model(); model != "" {
		t.Errorf("model %q after a plain reconfiguration, want none", model)
	}
}

func TestReconfigureFromSwapsTheClientAndItsConfigurationTogether(t *testing.T) {
	rc := llm.NewReconfigurableClient(&stubClient{id: "boot"})
	if id := rc.ModelConfigID(); id != "" {
		t.Fatalf("startup default came from configuration %q, want none", id)
	}

	seeded := &stubClient{id: "seeded"}
	rc.ReconfigureFrom(seeded, "config-1")
	if id := rc.ModelConfigID(); id != "config-1" {
		t.Errorf("configuration %q after seeding, want config-1", id)
	}
	if _, err := rc.ChatCompletion(context.Background(), &llm.ChatRequest{}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if seeded.calls.Load() != 1 {
		t.Error("call did not reach the client configuration config-1 was seeded with")
	}

	rc.Reconfigure(&stubClient{id: "unconfigured"})
	if id := rc.ModelConfigID(); id != "" {
		t.Errorf("configuration %q after a reconfiguration from none, want none", id)
	}
	if rc.Generation() != 2 {
		t.Errorf("generation %d after two reconfigurations, want 2", rc.Generation())
	}
}
