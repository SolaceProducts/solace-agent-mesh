// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type InstrumentedLLMClient struct {
	inner   llm.Client
	model   string
	metrics runtime.Metrics
}

func NewInstrumentedLLMClient(inner llm.Client, model string, metrics ...runtime.Metrics) *InstrumentedLLMClient {
	c := &InstrumentedLLMClient{inner: inner, model: model}
	if len(metrics) > 0 {
		c.metrics = metrics[0]
	}
	return c
}

func (c *InstrumentedLLMClient) Shutdown() {
	if sd, ok := c.inner.(interface{ Shutdown() }); ok {
		sd.Shutdown()
	}
}

func (c *InstrumentedLLMClient) mp() runtime.Metrics {
	if c.metrics != nil {
		return c.metrics
	}
	return runtime.MetricProvider()
}

func (c *InstrumentedLLMClient) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (resp *llm.ChatResponse, err error) {
	model := req.EffectiveModel(c.model)
	defer NewGenAIMonitor(c.mp(), model).Stop(&err)

	resp, err = c.inner.ChatCompletion(ctx, req)
	if resp != nil && resp.Usage != nil {
		c.recordTokenUsage(model, req.Observability, *resp.Usage)
	}
	return resp, err
}

func (c *InstrumentedLLMClient) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	model := req.EffectiveModel(c.model)
	durationMonitor := NewGenAIMonitor(c.mp(), model)
	ttftMonitor := NewGenAITTFTMonitor(c.mp(), model)

	stream, err := c.inner.ChatCompletionStream(ctx, req)
	if err != nil {
		durationMonitor.Stop(&err)
		return nil, err
	}

	out := make(chan llm.StreamChunk, 16)
	go func() {
		var streamErr error
		var lastUsage *llm.Usage
		defer close(out)
		defer durationMonitor.Stop(&streamErr)
		defer func() {
			if lastUsage != nil {
				c.recordTokenUsage(model, req.Observability, *lastUsage)
			}
		}()

		firstToken := true

		for chunk := range stream {
			if firstToken && chunk.Err == nil {
				firstToken = false
				ttftMonitor.Stop(nil)
			}
			if chunk.Err != nil {
				streamErr = chunk.Err
			}
			if chunk.Usage != nil && hasBucketedTokens(*chunk.Usage) {
				lastUsage = chunk.Usage
			}

			select {
			case out <- chunk:
			case <-ctx.Done():
				streamErr = ctx.Err()
				return
			}
		}
	}()

	return out, nil
}

const (
	tokenTypeInput      = "input"
	tokenTypeCacheRead  = "cache_read"
	tokenTypeCacheWrite = "cache_write"
	tokenTypeOutput     = "output"
)

type tokenBucket struct {
	tokenType string
	count     int
}

func tokenBuckets(usage llm.Usage) []tokenBucket {
	b := usage.Buckets()
	return []tokenBucket{
		{tokenTypeInput, b.Input},
		{tokenTypeCacheRead, b.CacheRead},
		{tokenTypeCacheWrite, b.CacheWrite},
		{tokenTypeOutput, b.Output},
	}
}

func hasBucketedTokens(usage llm.Usage) bool {
	return slices.ContainsFunc(tokenBuckets(usage), func(b tokenBucket) bool { return b.count > 0 })
}

func (c *InstrumentedLLMClient) recordTokenUsage(model string, obs llm.ObservabilityContext, usage llm.Usage) {
	recordTokenBuckets(c.mp(), model, obs.ComponentName, obs.OwnerID, tokenBuckets(usage))
}

func recordTokenBuckets(m runtime.Metrics, model, component, owner string, buckets []tokenBucket) {
	for _, bucket := range buckets {
		if bucket.count <= 0 {
			continue
		}
		m.Counter("gen_ai.tokens.used", int64(bucket.count),
			"gen_ai.request.model", model,
			"gen_ai.token.type", bucket.tokenType,
			"component.name", component,
			"owner.id", owner,
		)
	}
}
