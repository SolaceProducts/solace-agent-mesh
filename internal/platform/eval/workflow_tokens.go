// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type TokenCounts struct {
	InputTokens       int64 `json:"inputTokens"`
	CachedInputTokens int64 `json:"cachedInputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	ReasoningTokens   int64 `json:"reasoningTokens"`
	TotalTokens       int64 `json:"totalTokens"`
}

func (c *TokenCounts) add(o TokenCounts) {
	c.InputTokens += o.InputTokens
	c.CachedInputTokens += o.CachedInputTokens
	c.OutputTokens += o.OutputTokens
	c.ReasoningTokens += o.ReasoningTokens
	c.TotalTokens += o.TotalTokens
}

type NodeTokenUsage struct {
	NodeID string   `json:"nodeId"`
	Models []string `json:"models"`
	TokenCounts
}

type TokenBreakdown struct {
	TraceID      string                 `json:"traceId"`
	Incomplete   bool                   `json:"incomplete"`
	ByNode       []NodeTokenUsage       `json:"byNode"`
	Unattributed TokenCounts            `json:"unattributed"`
	ByModel      map[string]TokenCounts `json:"byModel"`

	sawFinal bool
}

func (b *TokenBreakdown) total() TokenCounts {
	var t TokenCounts
	for _, c := range b.ByModel {
		t.add(c)
	}
	return t
}

func buildWorkflowTokenBreakdown(messages []map[string]any, traceID string) *TokenBreakdown {
	t := tokenTally{
		breakdown:     &TokenBreakdown{TraceID: traceID, ByModel: map[string]TokenCounts{}},
		byNode:        map[string]*NodeTokenUsage{},
		nodeBySubTask: map[string]string{},
		running:       map[string]bool{},
	}
	for _, m := range messages {
		t.read(m)
	}
	return t.result()
}

type tokenTally struct {
	breakdown     *TokenBreakdown
	byNode        map[string]*NodeTokenUsage
	order         []string
	nodeBySubTask map[string]string
	running       map[string]bool
	sawFinal      bool
	truncated     bool
	unseen        bool
}

var streamedNodeTypes = []string{"agent", "tool", "decision", "switch"}

func (t *tokenTally) read(m map[string]any) {
	if isCaptureTruncatedMarker(m) {
		t.truncated = true
		return
	}
	if isFinalTask(m) {
		t.sawFinal = true
		for model, c := range decisionUsageByModel(finalTokenUsage(m)) {
			t.count("", model, c)
		}
		return
	}
	parts, ev := workflowEventParts(m)
	for _, root := range parts {
		data, _ := root["data"].(map[string]any)
		switch data["type"] {
		case string(a2a.SignalWorkflowNodeExecStart):
			t.nodeStarted(data, ev)
		case string(a2a.SignalWorkflowNodeExecResult):
			if !ev.forwarded() {
				delete(t.running, stringField(data, "node_id"))
				t.unseen = t.unseen || stringField(data, "status") == "error"
			}
		case string(a2a.SignalWorkflowExecutionStart):
			t.unseen = t.unseen || ev.forwarded()
		case string(a2a.SignalLLMResponse):
			usage, _ := data["usage"].(map[string]any)
			model, c, ok := llmUsageCounts(usage)
			if !ok {
				t.unseen = true
				continue
			}
			t.count(nodeOfEvent(ev.chain(), t.nodeBySubTask), model, c)
		}
	}
}

func (t *tokenTally) nodeStarted(data map[string]any, ev eventMeta) {
	if ev.forwarded() {
		t.unseen = true
		return
	}
	node := stringField(data, "node_id")
	if node == "" || t.running[node] || !slices.Contains(streamedNodeTypes, stringField(data, "node_type")) {
		t.unseen = true
	}
	if node == "" {
		return
	}
	t.running[node] = true
	if sub := stringField(data, "sub_task_id"); sub != "" {
		t.nodeBySubTask[sub] = node
	}
}

func (t *tokenTally) count(node, model string, c TokenCounts) {
	m := t.breakdown.ByModel[model]
	m.add(c)
	t.breakdown.ByModel[model] = m
	if node == "" {
		t.breakdown.Unattributed.add(c)
		return
	}
	n, ok := t.byNode[node]
	if !ok {
		n = &NodeTokenUsage{NodeID: node}
		t.byNode[node] = n
		t.order = append(t.order, node)
	}
	n.add(c)
	if model != "" && !slices.Contains(n.Models, model) {
		n.Models = append(n.Models, model)
	}
}

func (t *tokenTally) result() *TokenBreakdown {
	b := t.breakdown
	if len(b.ByModel) == 0 && b.TraceID == "" {
		return nil
	}
	b.sawFinal = t.sawFinal
	b.Incomplete = t.truncated || !t.sawFinal || t.unseen || len(t.running) > 0
	b.ByNode = make([]NodeTokenUsage, 0, len(t.order))
	for _, node := range t.order {
		n := t.byNode[node]
		if n.Models == nil {
			n.Models = []string{}
		}
		b.ByNode = append(b.ByNode, *n)
	}
	return b
}

func nodeOfEvent(meta map[string]any, nodeBySubTask map[string]string) string {
	if node := stringField(meta, "node_id"); node != "" {
		return node
	}
	if node := nodeBySubTask[stringField(meta, "original_peer_event_taskId")]; node != "" {
		return node
	}
	chain, _ := meta["peer_task_chain"].([]any)
	for _, hop := range chain {
		if sub, _ := hop.(string); nodeBySubTask[sub] != "" {
			return nodeBySubTask[sub]
		}
	}
	return ""
}

func llmUsageCounts(usage map[string]any) (string, TokenCounts, bool) {
	input, output := tokenCount(usage, "input_tokens"), tokenCount(usage, "output_tokens")
	if input == nil || output == nil || *input+*output == 0 {
		return "", TokenCounts{}, false
	}
	model, _ := usage["model"].(string)
	c := TokenCounts{
		InputTokens:       *input,
		CachedInputTokens: countOrZero(usage, "cached_input_tokens"),
		OutputTokens:      *output,
		ReasoningTokens:   countOrZero(usage, "reasoning_tokens"),
		TotalTokens:       *input + *output,
	}
	return model, c, true
}

func isFinalTask(m map[string]any) bool {
	payload, _ := m["payload"].(map[string]any)
	result, _ := payload["result"].(map[string]any)
	return result != nil && result["kind"] == "task"
}

func finalTokenUsage(m map[string]any) map[string]any {
	payload, _ := m["payload"].(map[string]any)
	result, _ := payload["result"].(map[string]any)
	metadata, _ := result["metadata"].(map[string]any)
	usage, _ := metadata["token_usage"].(map[string]any)
	return usage
}

func decisionUsageByModel(usage map[string]any) map[string]TokenCounts {
	byModel, _ := usage["by_model"].(map[string]any)
	out := map[string]TokenCounts{}
	for model, v := range byModel {
		entry, _ := v.(map[string]any)
		if entry["kind"] != "decision" {
			continue
		}
		c := TokenCounts{InputTokens: countOrZero(entry, "input_tokens"), OutputTokens: countOrZero(entry, "output_tokens")}
		c.TotalTokens = c.InputTokens + c.OutputTokens
		out[model] = c
	}
	return out
}

func countOrZero(m map[string]any, key string) int64 {
	if n := tokenCount(m, key); n != nil {
		return *n
	}
	return 0
}

func trialCost(r *RunResultResponse, ledger map[string]store.TraceCost, ledgerRead bool) (int64, bool) {
	if r.TargetType != store.EvalTargetTypeWorkflow || !ledgerRead {
		return 0, false
	}
	kind := derefString(r.FailureKind)
	b := r.TokenBreakdown
	switch {
	case b == nil:
		return 0, kind == FailureKindInvalidInput
	case b.TraceID == "", b.Incomplete, kind == FailureKindTimeout, kind == FailureKindCancelled:
		return 0, false
	}
	filed, ok := ledger[b.TraceID]
	if !ok || filed.Unpriced > 0 || filed.Tokens < b.total().TotalTokens {
		return 0, false
	}
	return filed.Cost, true
}

func markCountShortOfLedger(b *TokenBreakdown, ledger map[string]store.TraceCost) {
	if b != nil && b.TraceID != "" && ledger[b.TraceID].Tokens > b.total().TotalTokens {
		b.Incomplete = true
	}
}

func trialTokenUsage(exp *store.EvalExperiment, task *pendingTask, reported *evalTokenUsage) (*evalTokenUsage, *string) {
	if !exp.IsWorkflowTarget() {
		return reported, nil
	}
	breakdown := buildWorkflowTokenBreakdown(task.messages, task.traceID)
	if breakdown == nil {
		return reported, nil
	}
	counted := breakdown.total().TotalTokens
	reportedMore := reported != nil && reported.Total != nil && *reported.Total > counted
	breakdown.Incomplete = breakdown.Incomplete || reportedMore
	if !breakdown.sawFinal || counted == 0 || reportedMore {
		return reported, encodeTokenBreakdown(breakdown)
	}
	return evalTokenUsageOf(breakdown), encodeTokenBreakdown(breakdown)
}

func evalTokenUsageOf(b *TokenBreakdown) *evalTokenUsage {
	t := b.total()
	return &evalTokenUsage{
		Input: &t.InputTokens, Output: &t.OutputTokens, Cached: &t.CachedInputTokens,
		Reasoning: &t.ReasoningTokens, Total: &t.TotalTokens,
	}
}

func encodeTokenBreakdown(b *TokenBreakdown) *string {
	if b == nil {
		return nil
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		return nil
	}
	s := string(encoded)
	return &s
}

func decodeTokenBreakdown(raw *string) *TokenBreakdown {
	if raw == nil || *raw == "" {
		return nil
	}
	var b TokenBreakdown
	if json.Unmarshal([]byte(*raw), &b) != nil {
		return nil
	}
	if b.ByNode == nil {
		b.ByNode = []NodeTokenUsage{}
	}
	for i := range b.ByNode {
		if b.ByNode[i].Models == nil {
			b.ByNode[i].Models = []string{}
		}
	}
	if b.ByModel == nil {
		b.ByModel = map[string]TokenCounts{}
	}
	return &b
}
