// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func llmResponseEvent(model string, input, output int, meta map[string]any) map[string]any {
	return llmCallEvent(model, input, output, 0, meta)
}

func llmCallEvent(model string, input, output, reasoning int, meta map[string]any) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "llm_response", "usage": map[string]any{
		"input_tokens": float64(input), "output_tokens": float64(output), "cached_input_tokens": float64(input / 2),
		"reasoning_tokens": float64(reasoning), "model": model,
	}}, meta)
}

func nodeStartEvent(node, nodeType, subTask string) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": node, "node_type": nodeType, "sub_task_id": subTask}, nil)
}

func nodeResultEvent(node string) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": node, "status": "success"}, nil)
}

func nodeErrorEvent(node string) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": node, "status": "error"}, nil)
}

func finalTaskEvent(byModel map[string]any) map[string]any {
	return map[string]any{"payload": map[string]any{"result": map[string]any{"kind": "task", "metadata": map[string]any{
		"token_usage": map[string]any{"by_model": byModel},
	}}}}
}

func fromSubTask(sub string) map[string]any {
	return map[string]any{"forwarded_from_peer": "order_flow", "original_peer_event_taskId": sub}
}

func int64OrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func tokenColumns(input, cached, output, reasoning, total *int64) string {
	return fmt.Sprintf("%d/%d/%d/%d/%d", int64OrZero(input), int64OrZero(cached), int64OrZero(output), int64OrZero(reasoning), int64OrZero(total))
}

func TestBuildWorkflowTokenBreakdown(t *testing.T) {
	messages := []map[string]any{
		nodeStartEvent("summarize", "agent", "sub-2"),
		nodeStartEvent("extract", "agent", "sub-1"),
		nodeStartEvent("lookup", "tool", "sub-3"),
		llmCallEvent("gpt", 200, 20, 0, fromSubTask("sub-2")),
		llmCallEvent("ada", 10, 1, 0, fromSubTask("sub-2")),
		llmCallEvent("claude", 100, 10, 4, fromSubTask("sub-1")),
		llmCallEvent("claude", 50, 5, 0, map[string]any{"peer_task_chain": []any{"sub-1", "sub-2"}}),
		llmCallEvent("", 7, 3, 0, map[string]any{"node_id": "classify"}),
		nodeResultEvent("summarize"),
		nodeResultEvent("extract"),
		nodeResultEvent("lookup"),
		finalTaskEvent(map[string]any{
			"jev":    map[string]any{"input_tokens": float64(40), "output_tokens": float64(6), "kind": "decision"},
			"claude": map[string]any{"input_tokens": float64(999), "output_tokens": float64(99), "kind": "agent"},
		}),
	}

	b := buildWorkflowTokenBreakdown(messages, "trace-1")

	if b == nil {
		t.Fatal("no breakdown for a trial that used tokens")
	}
	if b.TraceID != "trace-1" || b.Incomplete {
		t.Errorf("trace %q, incomplete %v; want trace-1 and a complete count", b.TraceID, b.Incomplete)
	}
	wantNodes := []NodeTokenUsage{
		{NodeID: "summarize", Models: []string{"gpt", "ada"}, TokenCounts: TokenCounts{InputTokens: 210, CachedInputTokens: 105, OutputTokens: 21, TotalTokens: 231}},
		{NodeID: "extract", Models: []string{"claude"}, TokenCounts: TokenCounts{InputTokens: 150, CachedInputTokens: 75, OutputTokens: 15, ReasoningTokens: 4, TotalTokens: 165}},
		{NodeID: "classify", Models: []string{}, TokenCounts: TokenCounts{InputTokens: 7, CachedInputTokens: 3, OutputTokens: 3, TotalTokens: 10}},
	}
	if !reflect.DeepEqual(b.ByNode, wantNodes) {
		t.Errorf("byNode = %+v\nwant     %+v (first-use order, the agent-kind usage on the final ignored)", b.ByNode, wantNodes)
	}
	decision := TokenCounts{InputTokens: 40, OutputTokens: 6, TotalTokens: 46}
	if b.Unattributed != decision {
		t.Errorf("unattributed = %+v, want the decision call %+v", b.Unattributed, decision)
	}
	wantModels := map[string]TokenCounts{
		"gpt":    {InputTokens: 200, CachedInputTokens: 100, OutputTokens: 20, TotalTokens: 220},
		"ada":    {InputTokens: 10, CachedInputTokens: 5, OutputTokens: 1, TotalTokens: 11},
		"claude": wantNodes[1].TokenCounts,
		"":       wantNodes[2].TokenCounts,
		"jev":    decision,
	}
	if !reflect.DeepEqual(b.ByModel, wantModels) {
		t.Errorf("byModel = %+v\nwant      %+v", b.ByModel, wantModels)
	}
	if total := b.total(); total != (TokenCounts{InputTokens: 407, CachedInputTokens: 183, OutputTokens: 45, ReasoningTokens: 4, TotalTokens: 452}) {
		t.Errorf("total = %+v", total)
	}
	if untraced := buildWorkflowTokenBreakdown(messages, ""); untraced == nil || untraced.TraceID != "" || untraced.total().TotalTokens != 452 {
		t.Errorf("tokens counted with no trace = %+v, want a breakdown without a trace", untraced)
	}

	noCalls := []map[string]any{nodeStartEvent("lookup", "tool", "s"), nodeResultEvent("lookup"), finalTaskEvent(nil)}
	empty := buildWorkflowTokenBreakdown(noCalls, "trace-2")
	if empty == nil || empty.TraceID != "trace-2" || len(empty.ByNode) != 0 || len(empty.ByModel) != 0 || empty.Incomplete {
		t.Errorf("a trial that called no model = %+v, want an empty, complete breakdown keeping its trace", empty)
	}
	if none := buildWorkflowTokenBreakdown(noCalls, ""); none != nil {
		t.Errorf("no tokens and no trace = %+v, want none", none)
	}
}

func TestBuildWorkflowTokenBreakdownCreditsANestedWorkflowsCallsToItsNode(t *testing.T) {
	messages := []map[string]any{
		nodeStartEvent("enrich", "workflow", "sub-e"),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "extract", "sub_task_id": "nested-leaf"}, fromSubTask("sub-e")),
		llmResponseEvent("gpt", 30, 3, map[string]any{"original_peer_event_taskId": "nested-leaf", "peer_task_chain": []any{"nested-leaf", "nested-mid", "sub-e"}}),
		nodeResultEvent("enrich"),
		finalTaskEvent(nil),
	}

	b := buildWorkflowTokenBreakdown(messages, "trace-1")

	if len(b.ByNode) != 1 || b.ByNode[0].NodeID != "enrich" || b.ByNode[0].TotalTokens != 33 {
		t.Errorf("byNode = %+v, want the nested call credited to enrich", b.ByNode)
	}
	if !b.Incomplete {
		t.Error("a breakdown with a sub-workflow reads as complete; its decision calls are never reported")
	}
}

func TestAForwardedCallNoNodeDispatchedIsUnattributed(t *testing.T) {
	messages := []map[string]any{
		nodeStartEvent("extract", "agent", "sub-1"),
		llmResponseEvent("claude", 20, 2, map[string]any{"original_peer_event_taskId": "sub-x", "peer_task_chain": []any{"sub-x", "sub-y"}}),
		nodeStartEvent("route", "switch", ""),
		llmResponseEvent("claude", 5, 1, nil),
		nodeResultEvent("extract"),
		nodeResultEvent("route"),
		finalTaskEvent(nil),
	}

	b := buildWorkflowTokenBreakdown(messages, "trace-1")

	if len(b.ByNode) != 0 || b.Unattributed.TotalTokens != 28 || b.ByModel["claude"].TotalTokens != 28 {
		t.Errorf("byNode %+v, unattributed %+v, byModel %+v; want the call unattributed and counted by model", b.ByNode, b.Unattributed, b.ByModel)
	}
}

func TestTokenBreakdownSaysWhenItMayLeaveCallsOut(t *testing.T) {
	final := finalTaskEvent(nil)
	truncated := map[string]any{"payload": map[string]any{"type": taskMessageBufferTruncated}}
	forwardedStart := func(signal string) map[string]any {
		return workflowStatusEvent(map[string]any{"type": signal, "node_id": "decide"}, fromSubTask("sub-m"))
	}
	cases := []struct {
		name     string
		messages []map[string]any
		want     bool
	}{
		{"complete", []map[string]any{final}, false},
		{"an agent node that finished", []map[string]any{nodeStartEvent("extract", "agent", "sub-1"), nodeResultEvent("extract"), final}, false},
		{"a skipped node", []map[string]any{workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "audit", "status": "skipped"}, nil), final}, false},
		{"a node of a type the tally does not know", []map[string]any{nodeStartEvent("x", "join_v2", ""), nodeResultEvent("x"), final}, true},
		{"a tool, a decision and a switch node", []map[string]any{
			nodeStartEvent("lookup", "tool", "sub-1"), nodeStartEvent("decide", "decision", "sub-2"), nodeStartEvent("route", "switch", ""),
			nodeResultEvent("lookup"), nodeResultEvent("decide"), nodeResultEvent("route"), final,
		}, false},
		{"a node started again before its result, as a retry is", []map[string]any{nodeStartEvent("extract", "agent", "sub-1"), nodeStartEvent("extract", "agent", "sub-2"), nodeResultEvent("extract"), final}, true},
		{"a node of an unknown type", []map[string]any{nodeStartEvent("x", "", ""), nodeResultEvent("x"), final}, true},
		{"a node start with no id", []map[string]any{nodeStartEvent("", "agent", "sub-1"), final}, true},
		{"a call that reported no tokens", []map[string]any{llmResponseEvent("claude", 0, 0, nil), final}, true},
		{"a call with no usage", []map[string]any{workflowStatusEvent(map[string]any{"type": "llm_response"}, nil), final}, true},
		{"a call reporting a negative count", []map[string]any{llmResponseEvent("claude", -5, 10, nil), final}, true},
		{"a call reporting a count as a string", []map[string]any{workflowStatusEvent(map[string]any{"type": "llm_response", "usage": map[string]any{"input_tokens": "5", "output_tokens": float64(1)}}, nil), final}, true},
		{"capture cut short before the final", []map[string]any{truncated}, true},
		{"capture cut short after the final", []map[string]any{final, truncated}, true},
		{"no final response", nil, true},
		{"a node ran a sub-workflow", []map[string]any{nodeStartEvent("enrich", "workflow", "sub-e"), nodeResultEvent("enrich"), final}, true},
		{"a nested workflow's node start forwarded", []map[string]any{nodeStartEvent("delegate", "agent", "sub-d"), forwardedStart("workflow_node_execution_start"), nodeResultEvent("delegate"), final}, true},
		{"a nested workflow's start forwarded", []map[string]any{nodeStartEvent("delegate", "agent", "sub-d"), forwardedStart("workflow_execution_start"), nodeResultEvent("delegate"), final}, true},
		{"a map node ran", []map[string]any{nodeStartEvent("each", "map", ""), nodeResultEvent("each"), final}, true},
		{"a loop node ran", []map[string]any{nodeStartEvent("retry", "loop", ""), nodeResultEvent("retry"), final}, true},
		{"a node ended in error", []map[string]any{nodeStartEvent("extract", "agent", "sub-1"), nodeErrorEvent("extract"), final}, true},
		{"a sibling still running at the final", []map[string]any{nodeStartEvent("a", "agent", "sub-a"), nodeStartEvent("b", "agent", "sub-b"), nodeResultEvent("a"), final}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildWorkflowTokenBreakdown(tc.messages, "trace-1").Incomplete; got != tc.want {
				t.Errorf("incomplete = %v, want %v", got, tc.want)
			}
		})
	}
	ownStart := workflowStatusEvent(map[string]any{"type": "workflow_execution_start"}, nil)
	if buildWorkflowTokenBreakdown([]map[string]any{ownStart, final}, "trace-1").Incomplete {
		t.Error("the workflow's own start reads as a sub-workflow")
	}
}

type fakeTraceCosts map[string]store.TraceCost

func (f fakeTraceCosts) CostByTrace(_ context.Context, traceIDs []string, exclude []spend.Surface) (map[string]store.TraceCost, error) {
	if len(exclude) != 1 || exclude[0] != spend.SurfaceEvalJudge {
		return nil, errors.New("the judge's calls were not excluded")
	}
	out := map[string]store.TraceCost{}
	for _, id := range traceIDs {
		if c, ok := f[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

type failingTraceCosts struct{ err error }

func (f failingTraceCosts) CostByTrace(context.Context, []string, []spend.Surface) (map[string]store.TraceCost, error) {
	return nil, f.err
}

func TestAttachTrialCostsOnlyReportsACostTheLedgerAccountsFor(t *testing.T) {
	ledger := fakeTraceCosts{
		"priced":    {TraceID: "priced", Cost: 1_234_500_000, Tokens: 10},
		"unpriced":  {TraceID: "unpriced", Cost: 1_000, Unpriced: 1, Tokens: 10},
		"filing":    {TraceID: "filing", Cost: 1_000, Tokens: 7},
		"tool-only": {TraceID: "tool-only", Cost: 5_000, Tokens: 30},
		"free":      {TraceID: "free", Cost: 0, Tokens: 10},
	}
	usedTokens := func(trace string) *TokenBreakdown {
		return &TokenBreakdown{TraceID: trace, ByModel: map[string]TokenCounts{"claude": {InputTokens: 5, OutputTokens: 5, TotalTokens: 10}}}
	}
	noCalls := func(trace string) *TokenBreakdown {
		return &TokenBreakdown{TraceID: trace, ByModel: map[string]TokenCounts{}}
	}
	incomplete := usedTokens("priced")
	incomplete.Incomplete = true
	kind := func(k string) *string { return &k }
	const workflow = store.EvalTargetTypeWorkflow
	cases := []struct {
		name           string
		result         RunResultResponse
		want           string
		wantIncomplete bool
	}{
		{"priced", RunResultResponse{TargetType: workflow, TokenBreakdown: usedTokens("priced")}, "0.1234500000", false},
		{"priced at a zero rate", RunResultResponse{TargetType: workflow, TokenBreakdown: usedTokens("free")}, "0.0000000000", false},
		{"a failed node whose count the ledger matches", RunResultResponse{TargetType: workflow, FailureKind: kind(FailureKindNodeFailed), TokenBreakdown: usedTokens("priced")}, "0.1234500000", false},
		{"a call the ledger priced that the stream did not see", RunResultResponse{TargetType: workflow, TokenBreakdown: noCalls("tool-only")}, "0.0000005000", true},
		{"never dispatched", RunResultResponse{TargetType: workflow, FailureKind: kind(FailureKindInvalidInput)}, "0.0000000000", false},
		{"no calls and nothing filed", RunResultResponse{TargetType: workflow, FailureKind: kind(FailureKindNodeFailed), TokenBreakdown: noCalls("no-calls")}, "", false},
		{"an unpriced call", RunResultResponse{TargetType: workflow, TokenBreakdown: usedTokens("unpriced")}, "", false},
		{"calls not yet all filed", RunResultResponse{TargetType: workflow, TokenBreakdown: usedTokens("filing")}, "", false},
		{"tokens but nothing filed", RunResultResponse{TargetType: workflow, TokenBreakdown: usedTokens("unfiled")}, "", false},
		{"an incomplete count", RunResultResponse{TargetType: workflow, TokenBreakdown: incomplete}, "", true},
		{"timed out", RunResultResponse{TargetType: workflow, FailureKind: kind(FailureKindTimeout), TokenBreakdown: usedTokens("priced")}, "", false},
		{"cancelled", RunResultResponse{TargetType: workflow, FailureKind: kind(FailureKindCancelled), TokenBreakdown: noCalls("tool-only")}, "", true},
		{"no trace", RunResultResponse{TargetType: workflow, TokenBreakdown: noCalls("")}, "", false},
		{"a legacy row without a breakdown", RunResultResponse{TargetType: workflow, FailureKind: kind(FailureKindNodeFailed)}, "", false},
		{"an agent trial", RunResultResponse{TargetType: store.EvalTargetTypeAgent, FailureKind: kind(FailureKindInvalidInput)}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewRunService(RunServiceParams{SpendLedger: ledger})
			results := []RunResultResponse{tc.result}

			svc.attachTrialCosts(t.Context(), results)

			if got := derefString(results[0].Cost); got != tc.want || (results[0].Cost == nil) != (tc.want == "") {
				t.Errorf("cost = %v, want %q", results[0].Cost, tc.want)
			}
			if b := results[0].TokenBreakdown; b != nil && b.Incomplete != tc.wantIncomplete {
				t.Errorf("incomplete = %v, want %v", b.Incomplete, tc.wantIncomplete)
			}
		})
	}
}

func TestAttachTrialCostsMarksACountTheLedgerExceedsIncomplete(t *testing.T) {
	svc := NewRunService(RunServiceParams{
		SpendLedger: fakeTraceCosts{
			"exact":    {TraceID: "exact", Cost: 10, Tokens: 10},
			"one-over": {TraceID: "one-over", Cost: 11, Tokens: 11},
			"more":     {TraceID: "more", Cost: 30, Tokens: 30},
			"unpriced": {TraceID: "unpriced", Cost: 30, Unpriced: 1, Tokens: 30},
		},
	})
	counted := func(trace string) *TokenBreakdown {
		return &TokenBreakdown{TraceID: trace, ByModel: map[string]TokenCounts{"claude": {InputTokens: 5, OutputTokens: 5, TotalTokens: 10}}}
	}
	alreadyIncomplete := counted("exact")
	alreadyIncomplete.Incomplete = true
	timeout, cancelled := FailureKindTimeout, FailureKindCancelled
	cases := []struct {
		name           string
		result         RunResultResponse
		wantCost       string
		wantIncomplete bool
	}{
		{"ledger matching the count", RunResultResponse{TokenBreakdown: counted("exact")}, "0.0000000010", false},
		{"ledger matching a count already incomplete", RunResultResponse{TokenBreakdown: alreadyIncomplete}, "", true},
		{"ledger one token over", RunResultResponse{TokenBreakdown: counted("one-over")}, "0.0000000011", true},
		{"ledger well over", RunResultResponse{TokenBreakdown: counted("more")}, "0.0000000030", true},
		{"ledger over with an unpriced call", RunResultResponse{TokenBreakdown: counted("unpriced")}, "", true},
		{"ledger over a timed-out trial", RunResultResponse{FailureKind: &timeout, TokenBreakdown: counted("more")}, "", true},
		{"ledger over a cancelled trial", RunResultResponse{FailureKind: &cancelled, TokenBreakdown: counted("more")}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.result.TargetType = store.EvalTargetTypeWorkflow
			results := []RunResultResponse{tc.result}

			svc.attachTrialCosts(t.Context(), results)

			r := results[0]
			if derefString(r.Cost) != tc.wantCost || (r.Cost == nil) != (tc.wantCost == "") || r.TokenBreakdown.Incomplete != tc.wantIncomplete {
				t.Errorf("cost %v, incomplete %v; want %q and %v", r.Cost, r.TokenBreakdown.Incomplete, tc.wantCost, tc.wantIncomplete)
			}
		})
	}
}

type recordingTraceCosts struct {
	fakeTraceCosts
	lookups [][]string
}

func (r *recordingTraceCosts) CostByTrace(ctx context.Context, traceIDs []string, exclude []spend.Surface) (map[string]store.TraceCost, error) {
	r.lookups = append(r.lookups, slices.Clone(traceIDs))
	return r.fakeTraceCosts.CostByTrace(ctx, traceIDs, exclude)
}

func TestAttachTrialCostsCoversEveryGroupInOneLookup(t *testing.T) {
	ledger := &recordingTraceCosts{fakeTraceCosts: fakeTraceCosts{
		"trace-a": {TraceID: "trace-a", Cost: 10, Tokens: 1},
		"trace-b": {TraceID: "trace-b", Cost: 20, Tokens: 1},
		"trace-c": {TraceID: "trace-c", Cost: 30, Tokens: 30},
	}}
	svc := NewRunService(RunServiceParams{SpendLedger: ledger})
	trial := func(trace string, counted int64) RunResultResponse {
		return RunResultResponse{TargetType: store.EvalTargetTypeWorkflow, TokenBreakdown: &TokenBreakdown{
			TraceID: trace, ByModel: map[string]TokenCounts{"claude": {InputTokens: counted, TotalTokens: counted}},
		}}
	}
	modelA := []RunResultResponse{trial("trace-a", 1), trial("trace-c", 10), trial("", 0)}
	modelB := []RunResultResponse{trial("trace-b", 1)}

	svc.attachTrialCosts(t.Context(), modelA, modelB)

	if derefString(modelA[0].Cost) != "0.0000000010" || derefString(modelB[0].Cost) != "0.0000000020" {
		t.Errorf("costs = %v, %v; want each group's trial priced", modelA[0].Cost, modelB[0].Cost)
	}
	if second := modelA[1]; derefString(second.Cost) != "0.0000000030" || !second.TokenBreakdown.Incomplete {
		t.Errorf("second trial of a group: cost %v, incomplete %v; want the ledger's cost and an incomplete count", second.Cost, second.TokenBreakdown.Incomplete)
	}
	if len(ledger.lookups) != 1 || !slices.Equal(slices.Sorted(slices.Values(ledger.lookups[0])), []string{"trace-a", "trace-b", "trace-c"}) {
		t.Errorf("lookups = %v, want one covering every trace and no empty one", ledger.lookups)
	}
}

func TestAttachTrialCostsReportsNothingWithoutTheLedger(t *testing.T) {
	invalidInput := FailureKindInvalidInput
	cases := map[string]RunServiceParams{
		"no ledger":     {},
		"lookup failed": {SpendLedger: failingTraceCosts{errors.New("ledger unreachable")}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			results := []RunResultResponse{
				{TargetType: store.EvalTargetTypeWorkflow, TokenBreakdown: &TokenBreakdown{TraceID: "t", ByModel: map[string]TokenCounts{}}},
				{TargetType: store.EvalTargetTypeWorkflow, FailureKind: &invalidInput},
			}

			NewRunService(params).attachTrialCosts(t.Context(), results)

			for i, r := range results {
				if r.Cost != nil {
					t.Errorf("trial %d cost = %q, want unknown", i, *r.Cost)
				}
			}
			if results[0].TokenBreakdown.Incomplete {
				t.Error("a breakdown the unread ledger cannot judge was marked incomplete")
			}
		})
	}
}

func TestATrialCostLookupWarnsUnlessTheReadWasCancelled(t *testing.T) {
	for _, tc := range []struct {
		err      error
		wantWarn bool
	}{
		{errors.New("ledger unreachable"), true},
		{context.Canceled, false},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			var logs bytes.Buffer
			svc := NewRunService(RunServiceParams{
				SpendLedger: failingTraceCosts{tc.err},
				Logger:      slog.New(slog.NewTextHandler(&logs, nil)),
			})
			results := []RunResultResponse{{TargetType: store.EvalTargetTypeWorkflow, TokenBreakdown: &TokenBreakdown{TraceID: "t"}}}

			svc.attachTrialCosts(t.Context(), results)

			if warned := bytes.Contains(logs.Bytes(), []byte("trial cost lookup failed")); warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v: %s", warned, tc.wantWarn, logs.String())
			}
		})
	}
}

func TestTrialTokenUsageCountsAWorkflowsNodeAgents(t *testing.T) {
	reportedTotal := int64(7)
	reported := &evalTokenUsage{Total: &reportedTotal}
	messages := []map[string]any{
		nodeStartEvent("extract", "agent", "sub-1"),
		llmCallEvent("claude", 100, 10, 3, map[string]any{"original_peer_event_taskId": "sub-1"}),
		nodeResultEvent("extract"),
		finalTaskEvent(nil),
	}
	agent := &store.EvalExperiment{TargetType: store.EvalTargetTypeAgent}
	workflow := &store.EvalExperiment{TargetType: store.EvalTargetTypeWorkflow}
	usageOf := func(u *evalTokenUsage) string {
		if u == nil {
			return "none"
		}
		return tokenColumns(u.Input, u.Cached, u.Output, u.Reasoning, u.Total)
	}
	breakdownOf := func(t *testing.T, raw *string) *TokenBreakdown {
		t.Helper()
		b := decodeTokenBreakdown(raw)
		if b == nil {
			t.Fatal("no breakdown stored")
		}
		return b
	}
	const counted = "100/50/10/3/110"

	usage, breakdown := trialTokenUsage(agent, &pendingTask{messages: messages, traceID: "trace-1"}, reported)
	if usageOf(usage) != usageOf(reported) || breakdown != nil {
		t.Errorf("agent trial = %s, %v; want its reported usage and no breakdown", usageOf(usage), breakdown)
	}

	usage, breakdown = trialTokenUsage(workflow, &pendingTask{messages: messages, traceID: "trace-1"}, reported)
	if got := usageOf(usage); got != counted {
		t.Errorf("workflow trial = %s, want the node agent's counted tokens %s", got, counted)
	}
	if b := breakdownOf(t, breakdown); b.TraceID != "trace-1" || b.Incomplete {
		t.Errorf("stored breakdown = %+v, want trace-1 and complete", b)
	}

	usage, breakdown = trialTokenUsage(workflow, &pendingTask{}, reported)
	if usageOf(usage) != usageOf(reported) || breakdown != nil {
		t.Errorf("workflow trial with no trace and no calls = %s, %v; want the reported usage", usageOf(usage), breakdown)
	}

	usage, breakdown = trialTokenUsage(workflow, &pendingTask{messages: []map[string]any{finalTaskEvent(nil)}, traceID: "trace-1"}, nil)
	if usage != nil || breakdown == nil {
		t.Errorf("workflow trial that counted nothing = %s, %v; want no usage and a breakdown", usageOf(usage), breakdown)
	}

	usage, breakdown = trialTokenUsage(workflow, &pendingTask{messages: messages[:2], traceID: "trace-1"}, nil)
	if usage != nil || !breakdownOf(t, breakdown).Incomplete {
		t.Errorf("workflow trial with no final response = %s; want no usage and an incomplete breakdown", usageOf(usage))
	}

	truncated := append(slices.Clone(messages), map[string]any{"payload": map[string]any{"type": taskMessageBufferTruncated}})
	usage, breakdown = trialTokenUsage(workflow, &pendingTask{messages: truncated, traceID: "trace-1"}, reported)
	if usageOf(usage) != counted || !breakdownOf(t, breakdown).Incomplete {
		t.Errorf("workflow trial cut short = %s; want the larger counted usage and an incomplete breakdown", usageOf(usage))
	}

	decisionOnly := []map[string]any{finalTaskEvent(map[string]any{"jev": map[string]any{"input_tokens": float64(40), "output_tokens": float64(6), "kind": "decision"}})}
	in, out, total := int64(40), int64(6), int64(46)
	usage, breakdown = trialTokenUsage(workflow, &pendingTask{messages: decisionOnly, traceID: "trace-1"}, &evalTokenUsage{Input: &in, Output: &out, Total: &total})
	if usageOf(usage) != "40/0/6/0/46" || breakdownOf(t, breakdown).Incomplete {
		t.Errorf("decision-only workflow reporting what was counted = %s; want the counted usage and a complete breakdown", usageOf(usage))
	}

	usage, _ = trialTokenUsage(workflow, &pendingTask{messages: messages, traceID: "trace-1"}, &evalTokenUsage{Input: &in})
	if usageOf(usage) != counted {
		t.Errorf("workflow trial whose report has no total = %s, want the counted usage %s", usageOf(usage), counted)
	}

	moreReported := int64(500)
	usage, breakdown = trialTokenUsage(workflow, &pendingTask{messages: messages, traceID: "trace-1"}, &evalTokenUsage{Total: &moreReported})
	if usageOf(usage) != "0/0/0/0/500" || !breakdownOf(t, breakdown).Incomplete {
		t.Errorf("workflow trial reporting more than was counted = %s; want the reported usage and an incomplete breakdown", usageOf(usage))
	}
}

func TestDecodeTokenBreakdownReadsMissingListsAsEmpty(t *testing.T) {
	for _, raw := range []string{
		`{"traceId":"t","incomplete":false,"byNode":null,"unattributed":{},"byModel":null}`,
		`{"traceId":"t","byNode":[{"nodeId":"extract","models":null}]}`,
		`{"traceId":"t"}`,
	} {
		b := decodeTokenBreakdown(&raw)
		if b == nil || b.ByNode == nil || b.ByModel == nil {
			t.Errorf("%s decoded to %+v, want empty lists", raw, b)
			continue
		}
		for _, n := range b.ByNode {
			if n.Models == nil {
				t.Errorf("%s: node %q models are nil", raw, n.NodeID)
			}
		}
	}
	for _, raw := range []string{"", "not json", `{"byNode":"x"}`} {
		if b := decodeTokenBreakdown(&raw); b != nil {
			t.Errorf("%q decoded to %+v, want none", raw, b)
		}
	}
	if decodeTokenBreakdown(nil) != nil {
		t.Error("a missing breakdown decoded to one")
	}
}

func TestEvaluateAndSaveStoresAWorkflowTrialsBreakdownAndCountedTokens(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	ds := &store.EvalDataset{Name: "orders", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewEvalDatasetRepo(db).Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	input := `{"order_id":"O-1"}`
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, InputData: &input}
	if err := store.NewEvalExampleRepo(db).Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	expRepo := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{
		Name: "order-flow", TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow, Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	runs := store.NewEvalRunRepo(db)
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, ProgressTotal: 1, Source: "manual", StartedBy: "alice"}
	if err := runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	results := store.NewEvalRunResultRepo(db)
	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "ns", Runs: runs, Results: results, Scores: store.NewEvalRunScoreRepo(db), Experiments: expRepo,
	})
	task := &pendingTask{exampleID: ex.ID, example: ex, startedAt: time.Now(), traceID: "trace-1", messages: []map[string]any{
		nodeStartEvent("extract", "agent", "sub-1"),
		llmCallEvent("claude", 100, 10, 3, fromSubTask("sub-1")),
		nodeResultEvent("extract"),
		finalTaskEvent(map[string]any{"jev": map[string]any{"input_tokens": float64(40), "output_tokens": float64(6), "kind": "decision"}}),
	}}
	reportedIn, reportedOut, reportedTotal := int64(40), int64(6), int64(46)
	response := `{"priority":"urgent"}`

	svc.evaluateAndSave(ctx, evaluateAndSaveArgs{
		RunID: run.ID, Experiment: exp, Task: task, ResponseText: &response,
		TokenUsage: &evalTokenUsage{Input: &reportedIn, Output: &reportedOut, Total: &reportedTotal},
		State:      &runState{pending: map[string]*pendingTask{}},
	})

	rows, err := results.ListByRun(ctx, run.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("results = %d, %v; want one", len(rows), err)
	}
	row := rows[0]
	b := decodeTokenBreakdown(row.TokenBreakdown)
	if b == nil || b.TraceID != "trace-1" || b.Incomplete || len(b.ByNode) != 1 {
		t.Fatalf("stored breakdown = %+v, want trace-1, complete, with extract", b)
	}
	got := tokenColumns(row.InputTokens, row.CachedInputTokens, row.OutputTokens, row.ReasoningTokens, row.TotalTokens)
	if want := "140/50/16/3/156"; got != want {
		t.Errorf("row tokens in/cached/out/reasoning/total = %s, want the counted %s", got, want)
	}
}

func TestRunResultReadsCarryTheTargetTypeBreakdownAndCost(t *testing.T) {
	f := newRunFixture(t, 3)
	f.svc.spendLedger = fakeTraceCosts{
		"trace-wf": {TraceID: "trace-wf", Cost: 1_234_500_000, Tokens: 110},
		"trace-m2": {TraceID: "trace-m2", Cost: 20, Tokens: 1},
	}
	run := &store.EvalRun{ExperimentID: f.experimentID, Status: StatusCompleted, ProgressTotal: 3, Source: "manual", StartedBy: "alice", Model: "m1"}
	if err := f.runRepo.Create(f.ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	breakdown := encodeTokenBreakdown(buildWorkflowTokenBreakdown([]map[string]any{
		nodeStartEvent("extract", "agent", "sub-1"),
		llmResponseEvent("claude", 100, 10, map[string]any{"original_peer_event_taskId": "sub-1"}),
		nodeResultEvent("extract"),
		finalTaskEvent(nil),
	}, "trace-wf"))
	workflow, nodeFailed := store.EvalTargetTypeWorkflow, FailureKindNodeFailed
	rows := []*store.EvalRunResult{
		{RunID: run.ID, ExampleID: f.exampleIDs[0], TargetType: &workflow, TokenBreakdown: breakdown},
		{RunID: run.ID, ExampleID: f.exampleIDs[1], TargetType: &workflow, FailureKind: &nodeFailed},
		{RunID: run.ID, ExampleID: f.exampleIDs[2]},
	}
	if err := f.resultRepo.BulkCreate(f.ctx, rows); err != nil {
		t.Fatalf("create results: %v", err)
	}
	runM2 := &store.EvalRun{ExperimentID: f.experimentID, RunGroupID: run.RunGroupID, Status: StatusCompleted, ProgressTotal: 1, Source: "manual", StartedBy: "alice", Model: "m2"}
	if err := f.runRepo.Create(f.ctx, runM2); err != nil {
		t.Fatalf("create second run: %v", err)
	}
	m2Breakdown := encodeTokenBreakdown(&TokenBreakdown{TraceID: "trace-m2", ByModel: map[string]TokenCounts{}})
	if err := f.resultRepo.BulkCreate(f.ctx, []*store.EvalRunResult{{RunID: runM2.ID, ExampleID: f.exampleIDs[0], TargetType: &workflow, TokenBreakdown: m2Breakdown}}); err != nil {
		t.Fatalf("create second run's result: %v", err)
	}
	check := func(t *testing.T, results []RunResultResponse) {
		t.Helper()
		byExample := map[string]RunResultResponse{}
		for _, r := range results {
			byExample[r.ExampleID] = r
		}
		wf := byExample[f.exampleIDs[0]]
		if wf.TargetType != workflow || wf.TokenBreakdown == nil || wf.TokenBreakdown.TraceID != "trace-wf" || derefString(wf.Cost) != "0.1234500000" {
			t.Errorf("workflow trial = %+v, cost %v; want its breakdown and the ledger's cost", wf.TokenBreakdown, wf.Cost)
		}
		if legacy := byExample[f.exampleIDs[1]]; legacy.TargetType != workflow || legacy.TokenBreakdown != nil || legacy.Cost != nil {
			t.Errorf("workflow trial without a breakdown = %+v; want no breakdown and no cost", legacy)
		}
		if agent := byExample[f.exampleIDs[2]]; agent.TargetType != store.EvalTargetTypeAgent || agent.TokenBreakdown != nil || agent.Cost != nil {
			t.Errorf("agent trial = %+v; want targetType agent, no breakdown and no cost", agent)
		}
	}

	t.Run("by run", func(t *testing.T) {
		results, err := f.svc.GetRunResultsForRun(f.ctx, run.ID)
		if err != nil {
			t.Fatalf("results: %v", err)
		}
		check(t, results)
	})
	t.Run("paginated", func(t *testing.T) {
		results, _, err := f.svc.ListRunResultsPaginated(f.ctx, run.ID, 1, 10)
		if err != nil {
			t.Fatalf("results: %v", err)
		}
		check(t, results)
	})
	t.Run("by id", func(t *testing.T) {
		got, err := f.svc.GetRunResultByID(f.ctx, rows[0].ID)
		if err != nil {
			t.Fatalf("result: %v", err)
		}
		if got.TokenBreakdown == nil || derefString(got.Cost) != "0.1234500000" {
			t.Errorf("result = %+v, cost %v; want its breakdown and the ledger's cost", got.TokenBreakdown, got.Cost)
		}
	})
	t.Run("by model in the run's group", func(t *testing.T) {
		stored, err := f.runRepo.GetByID(f.ctx, run.ID)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		group, err := f.svc.GetRunGroupResultsByModel(f.ctx, stored.RunGroupID)
		if err != nil {
			t.Fatalf("group: %v", err)
		}
		check(t, group.ResultsByModel["m1"])
		if m2 := group.ResultsByModel["m2"]; len(m2) != 1 || derefString(m2[0].Cost) != "0.0000000020" {
			t.Errorf("second model's results = %+v, want its trial priced", m2)
		}
	})
}
