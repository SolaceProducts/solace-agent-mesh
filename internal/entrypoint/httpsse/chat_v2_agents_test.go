// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

const orchestratorPriority = 1000

func agentCard(name, displayName string, extensions ...a2a.AgentExtension) a2a.AgentCard {
	exts := append([]a2a.AgentExtension{{
		URI: "https://solace.com/a2a/extensions/display-name", Params: map[string]any{"display_name": displayName},
	}}, extensions...)
	return a2a.AgentCard{Name: name, URL: "solace:" + name, Capabilities: a2a.AgentCapabilities{Extensions: exts}}
}

func priorityExt(priority int) a2a.AgentExtension {
	return a2a.AgentExtension{URI: a2a.ExtensionURIPriority, Params: map[string]any{"priority": priority}}
}

func workflowExt() a2a.AgentExtension {
	return a2a.AgentExtension{URI: a2a.ExtensionURIAgentType, Params: map[string]any{"type": chatv2.AgentTypeWorkflow}}
}

func internalExt() a2a.AgentExtension {
	return a2a.AgentExtension{URI: a2a.ExtensionURIInternal, Params: map[string]any{"internal": true}}
}

func listChatAgents(t *testing.T, f *v2Session, query string) apicontract.PaginatedResponse[chatv2.ChatAgent] {
	t.Helper()
	req := withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/agents"+query, nil), v2Alice)
	rec := httptest.NewRecorder()
	f.a.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list agents = %d %s, want 200", rec.Code, rec.Body)
	}
	return decodeBody[apicontract.PaginatedResponse[chatv2.ChatAgent]](t, rec)
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var body T
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return body
}

func requiredScopesExt(scopes ...string) a2a.AgentExtension {
	return a2a.AgentExtension{URI: a2a.ExtensionURIRequiredScopes, Params: map[string]any{"scopes": scopes}}
}

func TestChatV2AgentsCarryTypedPriorityTypeAndInternal(t *testing.T) {
	f := newV2Session(t)
	f.core.AgentRegistry().AddOrUpdate(agentCard("builder", "Builder", internalExt()))
	f.core.AgentRegistry().AddOrUpdate(agentCard("flow", "Flow", workflowExt(), priorityExt(5)))
	f.core.AgentRegistry().AddOrUpdate(agentCard("plain", "Plain"))

	byName := map[string]chatv2.ChatAgent{}
	for _, a := range listChatAgents(t, f, "").Data {
		byName[a.ID] = a
	}
	if a := byName["builder"]; !a.Internal || a.AgentType != chatv2.AgentTypeStandard || a.Type != "agent" {
		t.Errorf("builder = %+v, want internal standard agent", a)
	}
	if a := byName["flow"]; a.AgentType != chatv2.AgentTypeWorkflow || a.Priority != 5 || a.Internal {
		t.Errorf("flow = %+v, want workflow with priority 5", a)
	}
	if a := byName["plain"]; a.Priority != 0 || a.Internal || a.DisplayName != "Plain" {
		t.Errorf("plain = %+v, want priority 0, not internal", a)
	}
}

func TestChatV2AgentsPageSortedByDisplayName(t *testing.T) {
	f := newV2Session(t)
	for _, n := range []string{"d", "b", "a", "c", "e"} {
		f.core.AgentRegistry().AddOrUpdate(agentCard("agent-"+n, strings.ToUpper(n)))
	}
	page := listChatAgents(t, f, "?pageSize=2&pageNumber=2")
	if len(page.Data) != 2 || page.Data[0].DisplayName != "C" || page.Data[1].DisplayName != "D" {
		t.Fatalf("page 2 = %+v, want C then D", page.Data)
	}
	if got := page.Meta.Pagination.Count; got != 5 {
		t.Errorf("total = %d, want 5", got)
	}
}

func TestChatV2AgentsEmptyListIsAnArray(t *testing.T) {
	f := newV2Session(t)
	req := withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/agents", nil), v2Alice)
	rec := httptest.NewRecorder()
	f.a.Mux().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("body = %s, want data to be []", rec.Body)
	}
}

func TestChatV2AgentsLeaveOutEphemeralTestAgents(t *testing.T) {
	f := newV2Session(t)
	f.core.AgentRegistry().AddOrUpdate(agentCard("temp", "Temp", a2a.AgentExtension{URI: a2a.EphemeralAgentExtensionURI}))
	f.core.AgentRegistry().AddOrUpdate(agentCard("real", "Real"))
	got := listChatAgents(t, f, "").Data
	if len(got) != 1 || got[0].ID != "real" {
		t.Fatalf("agents = %+v, want only real", got)
	}
}

func TestChatV2AgentsListInTheOrderTheDefaultAgentRuleReads(t *testing.T) {
	f := newV2Session(t)
	f.core.AgentRegistry().AddOrUpdate(agentCard("orchestrator", "Orchestrator", priorityExt(orchestratorPriority)))
	f.core.AgentRegistry().AddOrUpdate(agentCard("builder", "Builder", internalExt(), priorityExt(orchestratorPriority+500)))
	f.core.AgentRegistry().AddOrUpdate(agentCard("flow", "A Workflow", workflowExt(), priorityExt(orchestratorPriority+500)))
	f.core.AgentRegistry().AddOrUpdate(agentCard("alpha", "Alpha"))

	var names []string
	for _, a := range listChatAgents(t, f, "").Data {
		names = append(names, fmt.Sprintf("%s internal=%t type=%s priority=%d", a.ID, a.Internal, a.AgentType, a.Priority))
	}
	want := []string{
		"flow internal=false type=workflow priority=1500",
		"alpha internal=false type=standard priority=0",
		"builder internal=true type=standard priority=1500",
		"orchestrator internal=false type=standard priority=1000",
	}
	if !slices.Equal(names, want) {
		t.Errorf("agents = %q, want %q", names, want)
	}
}

func TestChatV2AgentsLeaveOutAgentsTheCallerMayNotInvoke(t *testing.T) {
	f := newV2Session(t)
	f.core.AgentRegistry().AddOrUpdate(agentCard("open", "Open"))
	f.core.AgentRegistry().AddOrUpdate(agentCard("locked", "Locked", requiredScopesExt("agent:locked:invoke")))

	got := listChatAgents(t, f, "").Data
	if len(got) != 1 || got[0].ID != "open" {
		t.Fatalf("agents = %+v, want only open", got)
	}
}

func TestChatV2AgentsReadAPriorityThatArrivedAsJSON(t *testing.T) {
	var card a2a.AgentCard
	raw := `{"name":"x","capabilities":{"extensions":[{"uri":"` + a2a.ExtensionURIPriority + `","params":{"priority":42}}]}}`
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		t.Fatal(err)
	}
	if got := a2a.PriorityFromCard(card); got != 42 {
		t.Errorf("priority = %d, want 42", got)
	}
}

func TestChatV2AgentsMatchTheV1ListForTheSameCaller(t *testing.T) {
	f := newV2Session(t)
	for i := range 3 {
		f.core.AgentRegistry().AddOrUpdate(agentCard(fmt.Sprintf("a%d", i), fmt.Sprintf("Agent %d", i)))
	}
	v1 := httptest.NewRecorder()
	f.a.Mux().ServeHTTP(v1, withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/agentCards", nil), v2Alice))
	v1Cards := decodeBody[PaginatedResponse[a2a.AgentCard]](t, v1).Data
	v2Agents := listChatAgents(t, f, "").Data
	if len(v1Cards) != len(v2Agents) {
		t.Fatalf("v1 lists %d, v2 lists %d", len(v1Cards), len(v2Agents))
	}
	for i := range v1Cards {
		if v1Cards[i].Name != v2Agents[i].ID {
			t.Errorf("position %d: v1 %s, v2 %s", i, v1Cards[i].Name, v2Agents[i].ID)
		}
	}
}
