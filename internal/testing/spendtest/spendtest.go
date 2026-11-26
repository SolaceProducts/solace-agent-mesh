// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spendtest

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	flushInterval = 10 * time.Millisecond
	fileTimeout   = 30 * time.Second
	pollInterval  = 20 * time.Millisecond
	settle        = 200 * time.Millisecond
)

func StartGovernor(t testing.TB, brk broker.Broker, namespace string, db *store.DB) {
	t.Helper()
	gov := governor.New(governor.Config{
		Namespace:     namespace,
		Broker:        brk,
		Ledger:        store.NewSpendLedgerRepo(db),
		Prices:        governor.NewModelConfigPrices(store.NewModelConfigRepo(db), slog.Default()),
		Logger:        slog.Default(),
		FlushInterval: flushInterval,
	})
	if err := gov.Start(t.Context()); err != nil {
		t.Fatalf("start governor: %v", err)
	}
	t.Cleanup(func() { _ = gov.Stop(context.Background()) })
}

func FiledRows[T any](t testing.TB, db dbutil.Querier, what string, want int, query string, args ...any) []T {
	t.Helper()
	fetch := func() []T {
		found, err := dbutil.QueryRows[T](t.Context(), db, query, args...)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return found
	}
	var rows []T
	for deadline := time.Now().Add(fileTimeout); time.Now().Before(deadline); time.Sleep(pollInterval) {
		if rows = fetch(); len(rows) >= want {
			break
		}
	}
	time.Sleep(settle)
	if later := fetch(); len(rows) != want || len(later) != want {
		t.Fatalf("%s: %d then %d rows filed, want %d", what, len(rows), len(later), want)
	}
	return rows
}

type UsageLLM struct{}

func (UsageLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content: "a summary",
		Usage:   &llm.Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150},
	}, nil
}

func (UsageLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("not streamed")
}
