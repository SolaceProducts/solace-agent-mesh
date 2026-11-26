// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"errors"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type wrapperCapture struct {
	statuses []string
	signals  []a2a.RAGInfoUpdateData
}

func wrapperContext(cfg map[string]any, turns *tool.SearchTurns, c *wrapperCapture) *tool.Context {
	ctx := &tool.Context{ParentTaskID: "task", ToolConfig: cfg, SearchTurns: turns}
	if c != nil {
		ctx.SendStatus = func(s string) error { c.statuses = append(c.statuses, s); return nil }
		ctx.SendSignal = func(s any) error {
			c.signals = append(c.signals, s.(a2a.RAGInfoUpdateData))
			return nil
		}
	}
	return ctx
}

func TestWebSearchTool_PublishesCitationsNumberedByTheTask(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
	cfg := map[string]any{"provider": "tavily", "api_key": "K", "api_base": fake.server.URL}
	turns := &tool.SearchTurns{}
	turns.Next(tool.WebSearchTurnKey)
	c := &wrapperCapture{}

	result, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "solace"}, wrapperContext(cfg, turns, c))
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("Execute = %+v, %v", result, err)
	}
	if result.Data["search_turn"] != 1 {
		t.Fatalf("search_turn = %v, want 1 from the task's counter", result.Data["search_turn"])
	}
	if len(c.signals) != 1 || c.signals[0].SearchTurn != 1 || c.signals[0].Sources[0].CitationID != "s1r0" {
		t.Fatalf("signals = %+v, want one rag_info_update for turn 1", c.signals)
	}
	if len(c.statuses) != 1 || c.statuses[0] != "Searching the web for: solace" {
		t.Fatalf("statuses = %v", c.statuses)
	}
}

func TestWebSearchTool_RefusedCallDrawsNoTurn(t *testing.T) {
	turns := &tool.SearchTurns{}
	c := &wrapperCapture{}
	result, _ := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "solace"}, wrapperContext(map[string]any{}, turns, c))
	if result.Status != tool.StatusError || len(c.signals) != 0 || len(c.statuses) != 0 {
		t.Fatalf("result = %+v, signals = %d, statuses = %d", result, len(c.signals), len(c.statuses))
	}
	if next := turns.Next(tool.WebSearchTurnKey); next != 0 {
		t.Fatalf("next turn = %d, want 0: a call with no provider must not use a turn", next)
	}
}

func TestWebSearchTool_WithoutCallbacksStillReturnsTheResult(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
	cfg := map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL}
	result, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, wrapperContext(cfg, nil, nil))
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("Execute = %+v, %v", result, err)
	}
}

func TestGoogleWebSearchTool_PublishesCitationsNumberedPerTask(t *testing.T) {
	cse := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	cfg := map[string]any{"google_search_api_key": "G", "google_cse_id": "CX", "api_base": cse.baseURL()}
	c := &wrapperCapture{}
	search := NewGoogleWebSearchTool(nil)
	for want := range 2 {
		result, err := search.Execute(t.Context(), map[string]any{"query": "q"}, wrapperContext(cfg, nil, c))
		if err != nil || result.Status != tool.StatusSuccess {
			t.Fatalf("Execute = %+v, %v", result, err)
		}
		if result.Data["search_turn"] != want {
			t.Fatalf("search_turn = %v, want %d", result.Data["search_turn"], want)
		}
	}
	if len(c.signals) != 2 || c.signals[1].Sources[0].CitationID != "s1r0" {
		t.Fatalf("signals = %+v, want s0 then s1", c.signals)
	}
}

func TestWebSearchTool_CallConfigOverridesStaticConfig(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
	static := map[string]any{"provider": "tavily", "api_key": "STATIC", "api_base": "http://127.0.0.1:1"}
	call := map[string]any{"api_key": "CALL", "api_base": fake.server.URL}

	result, err := NewWebSearchTool(static).Execute(t.Context(), map[string]any{"query": "q"}, wrapperContext(call, &tool.SearchTurns{}, nil))
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("Execute = %+v, %v", result, err)
	}
	if got := fake.recorded(); len(got) != 1 || got[0].header.Get("Authorization") != "Bearer CALL" {
		t.Fatalf("requests = %+v, want one with the call's key", got)
	}
}

func TestWebSearchTool_ASignalErrorDoesNotFailTheSearch(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
	cfg := map[string]any{"provider": "tavily", "api_key": "K", "api_base": fake.server.URL}
	toolCtx := wrapperContext(cfg, &tool.SearchTurns{}, nil)
	toolCtx.SendSignal = func(any) error { return errors.New("stream closed") }

	result, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, toolCtx)
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("Execute = %+v, %v; want success despite the signal error", result, err)
	}
}

func TestWebSearchTool_AFailedSearchDrawsNoTurn(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusInternalServerError, `{"error": "down"}`})
	cfg := map[string]any{"provider": "tavily", "api_key": "K", "api_base": fake.server.URL}
	turns := &tool.SearchTurns{}
	c := &wrapperCapture{}

	result, _ := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, wrapperContext(cfg, turns, c))
	if result.Status != tool.StatusError || len(c.signals) != 0 {
		t.Fatalf("result = %+v, signals = %d; want an error and no citations", result, len(c.signals))
	}
	if next := turns.Next(tool.WebSearchTurnKey); next != 0 {
		t.Fatalf("next turn = %d, want 0", next)
	}
}
