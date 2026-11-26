// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func TestV1RunResultReadsAWorkflowTrialsTokens(t *testing.T) {
	raw := json.RawMessage(`{"id":"rr-1","type":"runResult","runId":"run-1","targetType":"workflow","cost":"0.0060000000",
		"tokenBreakdown":{"traceId":null,"incomplete":true,
			"byNode":[{"nodeId":"classify","models":["m1"],"tokens":{"inputTokens":10,"cachedInputTokens":2,"outputTokens":5,"reasoningTokens":1,"totalTokens":15}}],
			"unattributed":{"inputTokens":3,"cachedInputTokens":0,"outputTokens":1,"reasoningTokens":0,"totalTokens":4},
			"byModel":[{"model":null,"tokens":{"inputTokens":3,"cachedInputTokens":0,"outputTokens":1,"reasoningTokens":0,"totalTokens":4}},
				{"model":"m1","tokens":{"inputTokens":10,"cachedInputTokens":2,"outputTokens":5,"reasoningTokens":1,"totalTokens":15}}]},
		"scores":[],"createdTime":"2026-01-01T00:00:00.000Z"}`)

	var got evalplatform.RunResultResponse
	if err := v1RunResult(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	m1 := evalplatform.TokenCounts{InputTokens: 10, CachedInputTokens: 2, OutputTokens: 5, ReasoningTokens: 1, TotalTokens: 15}
	unattributed := evalplatform.TokenCounts{InputTokens: 3, OutputTokens: 1, TotalTokens: 4}
	want := &evalplatform.TokenBreakdown{
		Incomplete:   true,
		ByNode:       []evalplatform.NodeTokenUsage{{NodeID: "classify", Models: []string{"m1"}, TokenCounts: m1}},
		Unattributed: unattributed,
		ByModel:      map[string]evalplatform.TokenCounts{"": unattributed, "m1": m1},
	}
	if !reflect.DeepEqual(got.TokenBreakdown, want) {
		t.Errorf("tokenBreakdown = %+v, want %+v", got.TokenBreakdown, want)
	}
	if got.TargetType != "workflow" || got.Cost == nil || *got.Cost != "0.0060000000" {
		t.Errorf("targetType %q, cost %v, want the workflow trial's", got.TargetType, got.Cost)
	}
}

func TestV1RunCarriesTheRunsShape(t *testing.T) {
	run := apiv2.Run{ID: "run-1", ProgressTotal: 9, ConfigSnapshot: &apiv2.RunConfigSnapshot{Dataset: &apiv2.RunDatasetSnapshot{ExampleCount: 3}}}

	got := v1Run(run).ConfigSnapshot

	want := map[string]any{"dataset": map[string]any{"example_count": float64(3)}, "experiment": map[string]any{"runs_per_example": float64(3)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("configSnapshot = %v, want %v", got, want)
	}
	if snapshot := v1Run(apiv2.Run{ID: "run-2", ProgressTotal: 4}).ConfigSnapshot; snapshot != nil {
		t.Errorf("a run with no snapshot read as %v", snapshot)
	}
}
