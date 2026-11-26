// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

func benchAttribution() *llm.SpendAttribution {
	return &llm.SpendAttribution{
		UserID:         idgen.New(),
		AgentID:        idgen.New(),
		RootKind:       string(RootAgent),
		RootID:         idgen.New(),
		EntrypointName: "webui",
		Channel:        "webui",
		TriggerKind:    string(TriggerChat),
		Surface:        string(SurfaceAgent),
		ModelKey:       "default-model",
		ModelConfigID:  idgen.New(),
		TaskID:         idgen.New(),
		TraceID:        idgen.New(),
		SessionID:      idgen.New(),
	}
}

func benchUsage() llm.Usage {
	return llm.Usage{PromptTokens: 72_000, CompletionTokens: 1_200, CachedTokens: 68_000, CacheWriteTokens: 8_000, CacheWritesReported: true}
}

func BenchmarkTokensFromUsage(b *testing.B) {
	usage := benchUsage()
	b.ReportAllocs()
	for b.Loop() {
		_ = TokensFromUsage(usage)
	}
}

func BenchmarkCostOf(b *testing.B) {
	cacheRead, cacheWrite := pricing.Amount(300_000), pricing.Amount(3_750_000)
	price := pricing.ModelPrice{Input: 3_000_000, Output: 15_000_000, CacheRead: &cacheRead, CacheWrite: &cacheWrite}
	tokens := TokensFromUsage(benchUsage())
	b.ReportAllocs()
	for b.Loop() {
		_ = CostOf(tokens, price)
	}
}

func BenchmarkNewEventAndSanitize(b *testing.B) {
	attribution := benchAttribution()
	usage := benchUsage()
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		event := NewEvent(attribution, "openai/gpt-4o", TokensFromUsage(usage), 1)
		event.CacheWritesReported = new(usage.CacheWritesReported)
		if _, err := event.Sanitize(now); err != nil {
			b.Fatal(err)
		}
	}
}

type discardingBroker struct{ *broker.DevBroker }

func (discardingBroker) PublishGuaranteed(context.Context, string, *broker.Message) error {
	return nil
}

func BenchmarkPublisher_Record(b *testing.B) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	pub := NewPublisher(discardingBroker{dev}, testNamespace, discardLogger)
	b.Cleanup(func() { _ = pub.Close(context.Background()) })
	event := NewEvent(benchAttribution(), "openai/gpt-4o", TokensFromUsage(benchUsage()), 1)

	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		dropsBefore := pub.drops.count()
		for b.Loop() {
			pub.Record(event)
		}
		b.ReportMetric(float64(pub.drops.count()-dropsBefore)/float64(b.N), "drops/op")
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		dropsBefore := pub.drops.count()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				pub.Record(event)
			}
		})
		b.ReportMetric(float64(pub.drops.count()-dropsBefore)/float64(b.N), "drops/op")
	})
}
