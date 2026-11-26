// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

const usageFactsReplyTopic = "test/reply"

func newUsageFactsExecutor(t *testing.T, nodes []*NodeConfig) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	return newUsageFactsExecutorFailingFast(t, nodes, false)
}

func newUsageFactsExecutorFailingFast(t *testing.T, nodes []*NodeConfig, failFast bool) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	cfg := &WorkflowConfig{Name: "PayrollWorkflow", Namespace: "test-ns", Nodes: nodes, FailFast: failFast}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("build DAG: %v", err)
	}
	state := NewExecutionState(cfg.Name, "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	e.execCtx.UserProps.ReplyTo = usageFactsReplyTopic
	exec := &WorkflowExecution{State: state, Context: e.execCtx, Executor: e}
	e.instance.executions[e.execCtx.TaskID] = exec
	e.instance.executionsByID[state.ExecutionID] = exec
	return e, brk
}

func agentNodes(count int) []*NodeConfig {
	nodes := make([]*NodeConfig, 0, count)
	for i := range count {
		nodes = append(nodes, &NodeConfig{
			ID:        fmt.Sprintf("node_%d", i),
			Type:      "agent",
			AgentName: fmt.Sprintf("Worker%d", i),
		})
	}
	return nodes
}

func dispatchedSubTasks(t *testing.T, e *DAGExecutor) []string {
	t.Helper()
	if err := e.Advance(context.Background(), &EventStart{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	byNode := e.state.SnapshotSubTasks()
	nodes := make([]string, 0, len(byNode))
	for _, nodeID := range byNode {
		nodes = append(nodes, nodeID)
	}
	slices.Sort(nodes)
	out := make([]string, 0, len(byNode))
	for _, nodeID := range nodes {
		for subTaskID, n := range byNode {
			if n == nodeID {
				out = append(out, subTaskID)
				break
			}
		}
	}
	return out
}

func deliverNodeTask(t *testing.T, e *DAGExecutor, subTaskID string, state a2a.TaskState, metadata map[string]any) {
	t.Helper()
	task := a2a.Task{
		ID:   subTaskID,
		Kind: "task",
		Status: a2a.TaskStatus{
			State:   state,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("done")}},
		},
		Metadata: metadata,
	}
	payload, err := json.Marshal(a2a.NewResponse(subTaskID, task))
	if err != nil {
		t.Fatalf("marshal node response: %v", err)
	}
	e.instance.handleAgentResponse(context.Background(),
		a2a.Request{ID: subTaskID}, &broker.Message{Payload: payload})
}

func deliverNodeError(t *testing.T, e *DAGExecutor, subTaskID string, data map[string]any) {
	t.Helper()
	resp := a2a.NewErrorResponse(subTaskID, -32603, "The AI model is currently rate limited.")
	resp.Error.Data = data
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal node error: %v", err)
	}
	e.instance.handleAgentResponse(context.Background(),
		a2a.Request{ID: subTaskID}, &broker.Message{Payload: payload})
}

func terminalTaskMetadata(t *testing.T, brk *capturingBroker) map[string]any {
	t.Helper()
	for _, m := range brk.snapshot() {
		if m.Topic != usageFactsReplyTopic {
			continue
		}
		var envelope struct {
			Result struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"result"`
		}
		if err := json.Unmarshal(m.Payload, &envelope); err != nil {
			t.Fatalf("unmarshal terminal task: %v", err)
		}
		return envelope.Result.Metadata
	}
	t.Fatalf("the workflow published no terminal task to %s", usageFactsReplyTopic)
	return nil
}

func metadataStrings(t *testing.T, metadata map[string]any, key string) []string {
	t.Helper()
	raw, present := metadata[key]
	if !present {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a list", key, raw)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("%s carries a non-string %#v", key, item)
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func TestWorkflowTerminalTaskCarriesItsNodesUsageFacts(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(2))
	subTasks := dispatchedSubTasks(t, e)
	if len(subTasks) != 2 {
		t.Fatalf("dispatched %d nodes, want 2", len(subTasks))
	}

	deliverNodeTask(t, e, subTasks[0], a2a.TaskStateCompleted, map[string]any{
		"agent_name":                    "acme-payroll-assistant",
		a2a.MetadataToolCategories:      []string{taxonomy.ToolCategoryConnector},
		a2a.MetadataConnectorCategories: []string{"sql"},
		a2a.MetadataModelFamily:         modelfamily.GPT4o,
		a2a.MetadataModelProvider:       "openai",
	})
	deliverNodeTask(t, e, subTasks[1], a2a.TaskStateCompleted, map[string]any{
		"agent_name":                  "acme-ledger-reader",
		a2a.MetadataToolCategories:    []string{taxonomy.ToolCategoryBuiltin},
		a2a.MetadataPeerDelegated:     true,
		a2a.MetadataToolModelFamilies: []string{modelfamily.ClaudeSonnet},
	})

	metadata := terminalTaskMetadata(t, brk)
	if got, want := metadataStrings(t, metadata, a2a.MetadataToolCategories),
		[]string{taxonomy.ToolCategoryBuiltin, taxonomy.ToolCategoryConnector}; !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataToolCategories, got, want)
	}
	if got, want := metadataStrings(t, metadata, a2a.MetadataConnectorCategories), []string{"sql"}; !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataConnectorCategories, got, want)
	}
	if metadata[a2a.MetadataPeerDelegated] != true {
		t.Errorf("%s = %v, want true", a2a.MetadataPeerDelegated, metadata[a2a.MetadataPeerDelegated])
	}
	if got, want := metadataStrings(t, metadata, a2a.MetadataToolModelFamilies), []string{modelfamily.ClaudeSonnet}; !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataToolModelFamilies, got, want)
	}
	if metadata[a2a.MetadataModelFamily] != modelfamily.GPT4o {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, metadata[a2a.MetadataModelFamily], modelfamily.GPT4o)
	}
	if metadata[a2a.MetadataModelProvider] != "openai" {
		t.Errorf("%s = %v, want openai", a2a.MetadataModelProvider, metadata[a2a.MetadataModelProvider])
	}
}

func TestWorkflowFailureCarriesTheFactsOfBothFailureShapes(t *testing.T) {
	cases := []struct {
		name    string
		deliver func(t *testing.T, e *DAGExecutor, subTaskID string)
	}{
		{"a node that reported a failed task", func(t *testing.T, e *DAGExecutor, subTaskID string) {
			deliverNodeTask(t, e, subTaskID, a2a.TaskStateFailed, map[string]any{
				a2a.MetadataToolCategories: []string{taxonomy.ToolCategoryCustom},
				a2a.MetadataModelFamily:    modelfamily.Llama,
				a2a.MetadataModelProvider:  "groq",
			})
		}},
		{"a node that produced no task at all", func(t *testing.T, e *DAGExecutor, subTaskID string) {
			deliverNodeError(t, e, subTaskID, map[string]any{
				"token_usage":              map[string]any{"prompt_tokens": 120},
				a2a.MetadataToolCategories: []string{taxonomy.ToolCategoryCustom},
				a2a.MetadataModelFamily:    modelfamily.Llama,
				a2a.MetadataModelProvider:  "groq",
			})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, brk := newUsageFactsExecutorFailingFast(t, agentNodes(1), true)
			subTasks := dispatchedSubTasks(t, e)
			c.deliver(t, e, subTasks[0])

			metadata := terminalTaskMetadata(t, brk)
			if metadata["error"] == nil {
				t.Fatalf("the workflow did not publish a failure task: %#v", metadata)
			}
			if got, want := metadataStrings(t, metadata, a2a.MetadataToolCategories),
				[]string{taxonomy.ToolCategoryCustom}; !slices.Equal(got, want) {
				t.Errorf("%s = %v, want %v", a2a.MetadataToolCategories, got, want)
			}
			if metadata[a2a.MetadataModelFamily] != modelfamily.Llama {
				t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, metadata[a2a.MetadataModelFamily], modelfamily.Llama)
			}
			if metadata[a2a.MetadataModelProvider] != "groq" {
				t.Errorf("%s = %v, want groq", a2a.MetadataModelProvider, metadata[a2a.MetadataModelProvider])
			}
		})
	}
}

func TestWorkflowCancellationCarriesTheFactsSoFar(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(2))
	subTasks := dispatchedSubTasks(t, e)

	deliverNodeTask(t, e, subTasks[0], a2a.TaskStateCompleted, map[string]any{
		a2a.MetadataToolCategories: []string{taxonomy.ToolCategoryBuiltin},
		a2a.MetadataModelFamily:    modelfamily.GPT4,
		a2a.MetadataModelProvider:  "openai",
	})
	if err := e.Advance(context.Background(), &EventCancel{Reason: "user cancelled"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	metadata := terminalTaskMetadata(t, brk)
	if metadata["error"] != nil || metadata["duration_seconds"] != nil {
		t.Fatalf("the workflow did not publish a cancelled task: %#v", metadata)
	}
	if got, want := metadataStrings(t, metadata, a2a.MetadataToolCategories),
		[]string{taxonomy.ToolCategoryBuiltin}; !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataToolCategories, got, want)
	}
	if metadata[a2a.MetadataModelFamily] != modelfamily.GPT4 {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, metadata[a2a.MetadataModelFamily], modelfamily.GPT4)
	}
}

func TestWorkflowUsageFactsStayBoundedAcrossManyNodes(t *testing.T) {
	const nodeCount = 100
	const wantMaxElements = 32
	e, brk := newUsageFactsExecutor(t, agentNodes(nodeCount))
	subTasks := dispatchedSubTasks(t, e)
	if len(subTasks) != nodeCount {
		t.Fatalf("dispatched %d nodes, want %d", len(subTasks), nodeCount)
	}
	for i, subTaskID := range subTasks {
		deliverNodeTask(t, e, subTaskID, a2a.TaskStateCompleted, map[string]any{
			a2a.MetadataToolCategories:      []string{fmt.Sprintf("category_%d", i), taxonomy.ToolCategoryBuiltin},
			a2a.MetadataConnectorCategories: []string{fmt.Sprintf("connector_%d", i), "sql"},
			a2a.MetadataToolModelFamilies:   []string{fmt.Sprintf("family_%d", i), modelfamily.Mistral},
		})
	}

	metadata := terminalTaskMetadata(t, brk)
	for _, key := range []string{
		a2a.MetadataToolCategories, a2a.MetadataConnectorCategories, a2a.MetadataToolModelFamilies,
	} {
		if got := len(metadataStrings(t, metadata, key)); got > wantMaxElements {
			t.Errorf("%d nodes produced %d elements in %s, want at most %d",
				nodeCount, got, key, wantMaxElements)
		}
	}
}

func TestWorkflowRefusesWhatTheCatalogDoesNotApprove(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(2))
	subTasks := dispatchedSubTasks(t, e)

	flood := make([]string, usagefacts.MaxArrivingSetElements)
	for i := range flood {
		flood[i] = fmt.Sprintf("acme_ledger_tool_%d", i)
	}
	deliverNodeTask(t, e, subTasks[0], a2a.TaskStateCompleted, map[string]any{
		a2a.MetadataToolCategories:      flood,
		a2a.MetadataConnectorCategories: []string{"acme_customer_ledger"},
		a2a.MetadataModelFamily:         "acme-internal-vllm-cluster",
		a2a.MetadataModelProvider:       "acme-internal",
		a2a.MetadataToolModelFamilies:   []string{"ft:gpt-4o:acme-corp:legal-triage:AbCd1234"},
	})
	deliverNodeTask(t, e, subTasks[1], a2a.TaskStateCompleted, map[string]any{
		a2a.MetadataToolCategories: []string{taxonomy.ToolCategoryBuiltin},
		a2a.MetadataModelFamily:    modelfamily.GPT5,
		a2a.MetadataModelProvider:  "openai",
	})

	metadata := terminalTaskMetadata(t, brk)
	rendered, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal terminal metadata: %v", err)
	}
	for _, secret := range []string{"acme_ledger_tool", "acme_customer_ledger", "acme-internal", "AbCd1234"} {
		if strings.Contains(string(rendered), secret) {
			t.Errorf("%q reached the workflow's terminal metadata %s", secret, rendered)
		}
	}
	if got, want := metadataStrings(t, metadata, a2a.MetadataToolCategories),
		[]string{taxonomy.ToolCategoryBuiltin}; !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataToolCategories, got, want)
	}
	if metadata[a2a.MetadataModelFamily] != modelfamily.GPT5 {
		t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, metadata[a2a.MetadataModelFamily], modelfamily.GPT5)
	}
	if metadata[a2a.MetadataModelProvider] != "openai" {
		t.Errorf("%s = %v, want openai", a2a.MetadataModelProvider, metadata[a2a.MetadataModelProvider])
	}
}

var multiModelNodeFacts = []map[string]any{
	{a2a.MetadataModelFamily: modelfamily.GPT4o, a2a.MetadataModelProvider: "openai"},
	{a2a.MetadataModelFamily: modelfamily.ClaudeSonnet, a2a.MetadataModelProvider: "anthropic"},
	{a2a.MetadataModelFamily: modelfamily.GeminiFlash, a2a.MetadataModelProvider: "gemini"},
}

func runMultiModelWorkflow(t *testing.T, completionOrder []int) map[string]any {
	t.Helper()
	e, brk := newUsageFactsExecutor(t, agentNodes(len(multiModelNodeFacts)))
	subTasks := dispatchedSubTasks(t, e)
	if len(subTasks) != len(multiModelNodeFacts) {
		t.Fatalf("dispatched %d nodes, want %d", len(subTasks), len(multiModelNodeFacts))
	}
	for _, node := range completionOrder {
		deliverNodeTask(t, e, subTasks[node], a2a.TaskStateCompleted, multiModelNodeFacts[node])
	}
	return terminalTaskMetadata(t, brk)
}

func TestWorkflowReportsTheSameModelHoweverItsNodesInterleave(t *testing.T) {
	first := runMultiModelWorkflow(t, []int{0, 1, 2})
	if first[a2a.MetadataModelFamily] == nil {
		t.Fatal("the workflow reported no model, so this test asserts nothing")
	}
	for _, order := range [][]int{{2, 1, 0}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}} {
		again := runMultiModelWorkflow(t, order)
		if again[a2a.MetadataModelFamily] != first[a2a.MetadataModelFamily] ||
			again[a2a.MetadataModelProvider] != first[a2a.MetadataModelProvider] {
			t.Errorf("nodes finishing in order %v reported (%v, %v), where order [0 1 2] reported (%v, %v) — the same workflow must report the same model",
				order, again[a2a.MetadataModelFamily], again[a2a.MetadataModelProvider],
				first[a2a.MetadataModelFamily], first[a2a.MetadataModelProvider])
		}
	}
}

func TestWorkflowReportsAMatchedModelPair(t *testing.T) {
	metadata := runMultiModelWorkflow(t, []int{0, 1, 2})
	pairs := map[string]string{}
	for _, node := range multiModelNodeFacts {
		pairs[node[a2a.MetadataModelFamily].(string)] = node[a2a.MetadataModelProvider].(string)
	}
	family, _ := metadata[a2a.MetadataModelFamily].(string)
	wantProvider, known := pairs[family]
	if !known {
		t.Fatalf("%s = %q, want one of the families a node reported", a2a.MetadataModelFamily, family)
	}
	if got := metadata[a2a.MetadataModelProvider]; got != wantProvider {
		t.Errorf("%s = %v beside family %q, want %q — the pair must be one model's",
			a2a.MetadataModelProvider, got, family, wantProvider)
	}
}

func dispatchToolNodeForUsage(t *testing.T, toolName string, provenance *toolinit.Provenance) map[string]any {
	t.Helper()
	node := &NodeConfig{ID: "lookup", Type: "tool", ToolName: toolName}
	e, _ := newUsageFactsExecutor(t, []*NodeConfig{node})
	tools := tool.NewSet()
	if err := tools.Register(&schemaTool{name: toolName}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	e.instance.tools = tools
	if provenance != nil {
		e.instance.recordToolProvenance(toolName, *provenance)
	}

	e.dispatchToolNode(context.Background(), node, "sub-1")
	e.instance.wg.Wait()

	metadata := map[string]any{}
	e.state.UsageFacts.AddTo(metadata)
	return metadata
}

func TestWorkflowToolNodeReportsItsCategory(t *testing.T) {
	metadata := dispatchToolNodeForUsage(t, "acme_ledger_sql_query", &toolinit.Provenance{
		Category:          taxonomy.ToolCategoryConnector,
		ConnectorCategory: "sql",
	})

	for key, want := range map[string][]string{
		a2a.MetadataToolCategories:      {taxonomy.ToolCategoryConnector},
		a2a.MetadataConnectorCategories: {"sql"},
	} {
		got, _ := metadata[key].([]string)
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", key, metadata[key], want)
		}
	}
}

func TestWorkflowToolNodeReportsItsBuiltinCategory(t *testing.T) {
	metadata := dispatchToolNodeForUsage(t, "query_data_with_sql", &toolinit.Provenance{
		Category:        taxonomy.ToolCategoryBuiltin,
		BuiltinCategory: "data_analysis",
	})

	for key, want := range map[string][]string{
		a2a.MetadataToolCategories:    {taxonomy.ToolCategoryBuiltin},
		a2a.MetadataBuiltinCategories: {"data_analysis"},
	} {
		got, _ := metadata[key].([]string)
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", key, metadata[key], want)
		}
	}
}

func TestWorkflowUnclassifiedToolNodeReportsNothing(t *testing.T) {
	if metadata := dispatchToolNodeForUsage(t, "mystery", nil); len(metadata) != 0 {
		t.Errorf("metadata = %#v, want nothing reported", metadata)
	}
}

func TestWorkflowUsageFactsSurviveTheHILSnapshot(t *testing.T) {
	state := NewExecutionState("PayrollWorkflow", "exec-1")
	state.UsageFacts.Merge(map[string]any{
		a2a.MetadataToolCategories:      []string{taxonomy.ToolCategoryConnector},
		a2a.MetadataConnectorCategories: []string{"slack"},
		a2a.MetadataPeerDelegated:       true,
		a2a.MetadataModelFamily:         modelfamily.GPT41,
		a2a.MetadataModelProvider:       "azure",
	})

	snapshot, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	var restored WorkflowExecutionState
	if err := json.Unmarshal(snapshot, &restored); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}

	before := map[string]any{}
	state.UsageFacts.AddTo(before)
	after := map[string]any{}
	restored.UsageFacts.AddTo(after)

	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Errorf("facts after the snapshot = %s, want %s", afterJSON, beforeJSON)
	}
	if len(before) == 0 {
		t.Fatal("the test asserted nothing: no facts were recorded before the snapshot")
	}
}

func TestWorkflowUsageFactsFoldConcurrently(t *testing.T) {
	var facts usagefacts.Facts
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				facts.Merge(map[string]any{
					a2a.MetadataToolCategories:      []any{taxonomy.ToolCategoryBuiltin},
					a2a.MetadataConnectorCategories: []any{"sql", "slack"},
					a2a.MetadataPeerDelegated:       true,
					a2a.MetadataModelFamily:         modelfamily.Families[i%len(modelfamily.Families)],
					a2a.MetadataModelProvider:       "openai",
					a2a.MetadataToolModelFamilies:   []any{modelfamily.Mistral},
				})
				facts.AddTo(map[string]any{})
			}
		}()
	}
	wg.Wait()

	metadata := map[string]any{}
	facts.AddTo(metadata)
	if metadata[a2a.MetadataPeerDelegated] != true {
		t.Errorf("%s = %v, want true", a2a.MetadataPeerDelegated, metadata[a2a.MetadataPeerDelegated])
	}
	if got, ok := metadata[a2a.MetadataModelFamily].(string); !ok || !slices.Contains(modelfamily.Families, got) {
		t.Errorf("%s = %v, want a family the catalog approves", a2a.MetadataModelFamily, metadata[a2a.MetadataModelFamily])
	}
}
