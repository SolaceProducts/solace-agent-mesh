// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type modelBinding struct {
	alias     string
	current   atomic.Pointer[liveClient]
	rawConfig map[string]any
	decision  bool
}

var _ llm.Client = (*modelBinding)(nil)

func newModelBinding(alias string, live *liveClient, rawConfig map[string]any) *modelBinding {
	b := &modelBinding{alias: alias, rawConfig: rawConfig}
	b.current.Store(live)
	return b
}

func (b *modelBinding) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	live, err := b.acquire()
	if err != nil {
		return nil, err
	}
	defer live.leave()
	return live.client.ChatCompletion(ctx, req)
}

func (b *modelBinding) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	live, err := b.acquire()
	if err != nil {
		return nil, err
	}
	in, err := live.client.ChatCompletionStream(ctx, req)
	if err != nil {
		live.leave()
		return nil, err
	}
	out := make(chan llm.StreamChunk, cap(in))
	go func() {
		defer live.leave()
		defer close(out)
		for chunk := range in {
			select {
			case out <- chunk:
			case <-ctx.Done():
				for range in {
				}
				return
			}
		}
	}()
	return out, nil
}

func (b *modelBinding) acquire() (*liveClient, error) {
	for {
		live := b.current.Load()
		if live == nil {
			return nil, fmt.Errorf("%w: %q (binding removed)", ErrAliasUnbound, b.alias)
		}
		if live.enter() {
			return live, nil
		}
	}
}

func (b *modelBinding) replace(next *liveClient) {
	if old := b.current.Swap(next); old != nil {
		old.retire()
	}
}

type liveClient struct {
	client llm.Client

	mu      sync.Mutex
	calls   int
	retired bool
	closed  bool
}

func newLiveClient(client llm.Client) *liveClient {
	return &liveClient{client: client}
}

func (c *liveClient) enter() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.calls++
	return true
}

func (c *liveClient) leave() {
	c.mu.Lock()
	c.calls--
	shut := c.closeIfIdleLocked()
	c.mu.Unlock()
	if shut {
		go shutdownClient(c.client)
	}
}

func (c *liveClient) retire() {
	c.mu.Lock()
	c.retired = true
	shut := c.closeIfIdleLocked()
	c.mu.Unlock()
	if shut {
		go shutdownClient(c.client)
	}
}

func (c *liveClient) closeIfIdleLocked() bool {
	if !c.retired || c.calls > 0 || c.closed {
		return false
	}
	c.closed = true
	return true
}

func shutdownClient(client llm.Client) {
	if sd, ok := client.(interface{ Shutdown() }); ok {
		sd.Shutdown()
	}
}
