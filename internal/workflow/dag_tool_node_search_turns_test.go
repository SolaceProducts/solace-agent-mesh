// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func newMockSearchProviders(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var body struct {
				Query string `json:"query"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"title":   "Result for " + body.Query,
				"url":     "https://example.com/" + body.Query,
				"content": "snippet",
			}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{
			"title":   "Result for " + r.URL.Query().Get("q"),
			"link":    "https://example.com/" + r.URL.Query().Get("q"),
			"snippet": "snippet",
		}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func searchToolEntries(apiBase string) []any {
	googleCfg := map[string]any{
		"google_search_api_key": "fake-api-key",
		"google_cse_id":         "fake-cse-id",
		"api_base":              apiBase,
	}
	mirrorCfg := map[string]any{
		"provider": "tavily",
		"api_key":  "fake-tavily-key",
		"api_base": apiBase,
	}
	return []any{
		map[string]any{"tool_type": "builtin", "tool_name": "web_search_google", "tool_config": googleCfg},
		map[string]any{"tool_type": "builtin", "tool_name": "web_search", "tool_config": mirrorCfg},
	}
}

type ragUpdate struct {
	turn        int
	citationIDs []string
}

func ragUpdates(t *testing.T, brk *capturingBroker) []ragUpdate {
	t.Helper()
	var out []ragUpdate
	for _, m := range brk.snapshot() {
		if m.Topic != telemetryStatusTopic {
			continue
		}
		var envelope struct {
			Result struct {
				Status struct {
					Message struct {
						Parts []struct {
							Data a2a.RAGInfoUpdateData `json:"data"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(m.Payload, &envelope); err != nil {
			t.Fatalf("unmarshal status payload: %v\n%s", err, m.Payload)
		}
		for _, part := range envelope.Result.Status.Message.Parts {
			if part.Data.Type != string(a2a.SignalRAGInfoUpdate) {
				continue
			}
			u := ragUpdate{turn: part.Data.SearchTurn}
			for _, src := range part.Data.Sources {
				u.citationIDs = append(u.citationIDs, src.CitationID)
			}
			out = append(out, u)
		}
	}
	return out
}

func TestTwoWebSearchToolNodesNumberTheirCitationsApart(t *testing.T) {
	srv := newMockSearchProviders(t)
	nodes := []*NodeConfig{
		{ID: "google", Type: "tool", ToolName: "web_search_google", Input: map[string]any{"query": "first"}},
		{ID: "mirror", Type: "tool", ToolName: "web_search", Input: map[string]any{"query": "second"}, DependsOn: []string{"google"}},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("build DAG: %v", err)
	}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, Tools: searchToolEntries(srv.URL), OutputMapping: map[string]any{"done": "yes"}}
	state := NewExecutionState("wf", "exec-1")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	if err := e.instance.initToolsAndConnectors(t.Context(), e.instance.svc); err != nil {
		t.Fatalf("init tools: %v", err)
	}

	runToCompletion(t, e)

	for _, id := range []string{"google", "mirror"} {
		if !state.IsNodeCompleted(id) {
			t.Fatalf("node %s did not complete: %+v", id, state.ErrorState)
		}
	}
	updates := ragUpdates(t, brk)
	if len(updates) != 2 {
		t.Fatalf("got %d rag_info_update signals, want one per search: %+v", len(updates), updates)
	}
	turns := []int{updates[0].turn, updates[1].turn}
	slices.Sort(turns)
	if !slices.Equal(turns, []int{0, 1}) {
		t.Errorf("search turns = %v, want 0 and 1 from one shared counter", turns)
	}
	if slices.Equal(updates[0].citationIDs, updates[1].citationIDs) {
		t.Errorf("both searches cited %v", updates[0].citationIDs)
	}
}

func TestReconstructedExecutionContinuesTheSearchTurns(t *testing.T) {
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	w.ctx = t.Context()
	state := NewExecutionState("wf", "exec-1")
	for range 2 {
		state.SearchTurns.Next(tool.WebSearchTurnKey)
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	ecJSON, err := json.Marshal(persistedExecContext{TaskID: "task-1", ContextID: "ctx-1"})
	if err != nil {
		t.Fatalf("marshal exec context: %v", err)
	}

	exec, err := w.reconstructExecution(&wfstore.PausedWorkflow{StateSnapshot: string(stateJSON), ExecContext: string(ecJSON)})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	defer exec.Context.Cancel()

	tc := exec.Executor.buildToolContext(&NodeConfig{ID: "n1", Type: "tool", ToolName: "web_search_mirror"}, NodeCompletion{NodeID: "n1", SubTaskID: "sub-1"})
	if tc.SearchTurns == nil {
		t.Fatal("a reconstructed run's tool context carries no search counter")
	}
	if got := tc.SearchTurns.Next("web_search_mirror"); got != 2 {
		t.Errorf("next search turn after a resume = %d, want 2: the run would repeat a citation id it already sent", got)
	}
}
