// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"sync/atomic"
)

type ReconfigurableClient struct {
	current    atomic.Pointer[configuredClient]
	generation atomic.Uint64
}

type configuredClient struct {
	client        Client
	modelConfigID string
	model         string
}

func NewReconfigurableClient(initial Client) *ReconfigurableClient {
	return NewReconfigurableClientFor(initial, "")
}

func NewReconfigurableClientFor(initial Client, model string) *ReconfigurableClient {
	rc := &ReconfigurableClient{}
	rc.current.Store(&configuredClient{client: initial, model: model})
	return rc
}

func (rc *ReconfigurableClient) Reconfigure(next Client) Client {
	return rc.ReconfigureFrom(next, "")
}

func (rc *ReconfigurableClient) ReconfigureFrom(next Client, modelConfigID string) Client {
	return rc.ReconfigureTo(next, modelConfigID, "")
}

func (rc *ReconfigurableClient) ReconfigureTo(next Client, modelConfigID, model string) Client {
	old := rc.current.Swap(&configuredClient{client: next, modelConfigID: modelConfigID, model: model})
	rc.generation.Add(1)
	if old == nil {
		return nil
	}
	return old.client
}

func (rc *ReconfigurableClient) ModelConfigID() string {
	return rc.current.Load().modelConfigID
}

func (rc *ReconfigurableClient) Model() string {
	return rc.current.Load().model
}

func (rc *ReconfigurableClient) Generation() uint64 {
	return rc.generation.Load()
}

func (rc *ReconfigurableClient) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return rc.current.Load().client.ChatCompletion(ctx, req)
}

func (rc *ReconfigurableClient) ChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	return rc.current.Load().client.ChatCompletionStream(ctx, req)
}
