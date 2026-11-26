// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type gatedClient struct {
	inner llm.Client
	gate  *budget.Gate
}

func NewGatedClient(inner llm.Client, gate *budget.Gate) llm.Client {
	if gate == nil {
		return inner
	}
	return &gatedClient{inner: inner, gate: gate}
}

func (c *gatedClient) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if err := c.gate.CheckKnownBlock(requestUser(req)); err != nil {
		return nil, err
	}
	return c.inner.ChatCompletion(ctx, req)
}

func (c *gatedClient) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if err := c.gate.CheckKnownBlock(requestUser(req)); err != nil {
		return nil, err
	}
	return c.inner.ChatCompletionStream(ctx, req)
}

func requestUser(req *llm.ChatRequest) string {
	if req == nil {
		return ""
	}
	return attributedUser(req.Spend)
}

func attributedUser(a *llm.SpendAttribution) string {
	if a == nil {
		return ""
	}
	return budget.UserKey(a.UserID, a.UserSub)
}
