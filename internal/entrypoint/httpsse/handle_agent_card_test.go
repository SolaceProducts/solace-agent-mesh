// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type scopeResolverFunc func(userID string, roles []string) []string

func (f scopeResolverFunc) ResolveScopes(userID string, roles []string) []string {
	return f(userID, roles)
}

func TestHandleAgentCards_Empty(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	ah := &agentCardHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agentCards", ah.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agentCards", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var cardsResp PaginatedResponse[any]
	if err := json.NewDecoder(rec.Body).Decode(&cardsResp); err != nil {
		t.Fatal(err)
	}

	if len(cardsResp.Data) != 0 {
		t.Errorf("expected empty array, got %d items", len(cardsResp.Data))
	}
}

func TestHandleAgentCards_WithAgents(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "zebra-agent",
		URL:  "solace:zebra",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{"display_name": "Zebra Agent"},
				},
			},
		},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "alpha-agent",
		URL:  "solace:alpha",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{"display_name": "Alpha Agent"},
				},
			},
		},
	})

	ah := &agentCardHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agentCards", ah.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agentCards", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var cardsResp PaginatedResponse[a2a.AgentCard]
	if err := json.NewDecoder(rec.Body).Decode(&cardsResp); err != nil {
		t.Fatal(err)
	}
	cards := cardsResp.Data

	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}

	if cards[0].Name != "alpha-agent" {
		t.Errorf("expected first card to be alpha-agent, got %s", cards[0].Name)
	}
	if cards[1].Name != "zebra-agent" {
		t.Errorf("expected second card to be zebra-agent, got %s", cards[1].Name)
	}
}

func TestHandleAgentCards_FiltersByDelegateScope(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "allowed-agent", URL: "solace:allowed"})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "restricted-agent", URL: "solace:restricted"})

	ah := &agentCardHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agentCards", ah.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/agentCards", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID:        "sam_dev_user",
		Authenticated: true,
		Scopes:        []string{"agent:allowed-agent:invoke"},
	})))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[a2a.AgentCard]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Name != "allowed-agent" {
		t.Fatalf("expected only allowed-agent, got %+v", resp.Data)
	}
}

func TestHandleAgentCards_FiltersByRequiredScopes(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "open-agent", URL: "solace:open"})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "observability-agent",
		URL:  "solace:obs",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIRequiredScopes,
				Params: map[string]any{"scopes": []any{"analytics:_:read"}},
			}},
		},
	})

	ah := &agentCardHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agentCards", ah.handleList)

	names := func(scopes []string) []string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/agentCards", nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID:        "sam_dev_user",
			Authenticated: true,
			Scopes:        scopes,
		})))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp PaginatedResponse[a2a.AgentCard]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(resp.Data))
		for i, c := range resp.Data {
			got[i] = c.Name
		}
		return got
	}

	if got := names([]string{"agent:*:invoke"}); len(got) != 1 || got[0] != "open-agent" {
		t.Fatalf("required_scopes leak: expected only open-agent, got %v", got)
	}

	if got := names([]string{"agent:*:invoke", "analytics:_:read"}); len(got) != 2 {
		t.Fatalf("expected both agents for a scoped caller, got %v", got)
	}
}

func TestHandleAgentCards_ResolvesEmptyIdentityThenFilters(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "orchestrator", URL: "solace:orch"})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "observability-agent",
		URL:  "solace:obs",
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
			URI:    a2a.ExtensionURIRequiredScopes,
			Params: map[string]any{"scopes": []any{"analytics:_:read"}},
		}}},
	})

	ah := &agentCardHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agentCards", ah.handleList)

	names := func() []string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/agentCards", nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID:        "alice@example.com",
			Authenticated: true,
		})))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp PaginatedResponse[a2a.AgentCard]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(resp.Data))
		for i, c := range resp.Data {
			got[i] = c.Name
		}
		return got
	}

	core.SetScopeResolver(scopeResolverFunc(func(string, []string) []string {
		return []string{"agent_builder:*:read"}
	}))
	if got := names(); len(got) != 0 {
		t.Fatalf("no-invoke role must hide all agents, got %v", got)
	}

	core.SetScopeResolver(scopeResolverFunc(func(string, []string) []string {
		return []string{"agent:*:invoke"}
	}))
	if got := names(); !slices.Contains(got, "orchestrator") || slices.Contains(got, "observability-agent") {
		t.Fatalf("scenario 2: want orchestrator without observability-agent, got %v", got)
	}

	core.SetScopeResolver(scopeResolverFunc(func(string, []string) []string {
		return []string{"agent:*:invoke", "analytics:_:read"}
	}))
	if got := names(); !slices.Contains(got, "observability-agent") {
		t.Fatalf("scenario 3: want observability-agent visible, got %v", got)
	}
}

func TestHandleAgentCards_FiltersActivityMonitorByFeatureFlag(t *testing.T) {
	adapter, core, brk := setupTestAdapter(t)

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyActivityMonitor, Name: "activity", Default: false})
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(reg),
	)
	core.SetServices(rt.Services())

	activityCard := a2a.AgentCard{
		Name: "activity-monitor-instance",
		URL:  "solace:activity",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": "Activity Monitor"},
			}},
		},
	}
	orchCard := a2a.AgentCard{Name: "orchestrator", URL: "solace:orch"}
	core.AgentRegistry().AddOrUpdate(activityCard)
	core.AgentRegistry().AddOrUpdate(orchCard)

	ah := &agentCardHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agentCards", ah.handleList)

	names := func() []string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/agentCards", nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID:        "sam_dev_user",
			Authenticated: true,
			Scopes:        []string{"agent:*:invoke"},
		})))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var resp PaginatedResponse[a2a.AgentCard]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(resp.Data))
		for i, c := range resp.Data {
			got[i] = c.Name
		}
		return got
	}

	got := names()
	if slices.Contains(got, "activity-monitor-instance") {
		t.Errorf("flag=off leaks Activity Monitor into dropdown: got %v", got)
	}
	if !slices.Contains(got, "orchestrator") {
		t.Errorf("flag=off must not affect unrelated agents; got %v", got)
	}

	t.Setenv("SAM_FEATURE_ACTIVITY_MONITOR", "true")
	reg2 := features.NewRegistry()
	reg2.Register(features.Definition{Key: features.KeyActivityMonitor, Name: "activity", Default: false})
	rt2 := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(reg2),
	)
	core.SetServices(rt2.Services())
	got = names()
	if !slices.Contains(got, "activity-monitor-instance") {
		t.Errorf("flag=on must reveal Activity Monitor: got %v", got)
	}
	if !slices.Contains(got, "orchestrator") {
		t.Errorf("flag=on must keep unrelated agents visible; got %v", got)
	}

	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
	got = names()
	if slices.Contains(got, "activity-monitor-instance") {
		t.Errorf("nil registry must fail closed and hide Activity Monitor: got %v", got)
	}
	if !slices.Contains(got, "orchestrator") {
		t.Errorf("nil registry must not affect unrelated agents; got %v", got)
	}
}
