// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

func TestRequiredScopesFromCard(t *testing.T) {
	want := []string{"analytics:_:read", "schedule:task:create"}

	inProcess := AgentCard{Capabilities: AgentCapabilities{Extensions: []AgentExtension{{
		URI:    ExtensionURIRequiredScopes,
		Params: map[string]any{"scopes": want},
	}}}}
	if got := RequiredScopesFromCard(inProcess); !slices.Equal(got, want) {
		t.Errorf("in-process []string: got %v, want %v", got, want)
	}

	data, err := json.Marshal(inProcess)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped AgentCard
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if got := RequiredScopesFromCard(roundTripped); !slices.Equal(got, want) {
		t.Errorf("json []any: got %v, want %v", got, want)
	}

	if got := RequiredScopesFromCard(AgentCard{Name: "no-extension"}); got != nil {
		t.Errorf("no extension: got %v, want nil", got)
	}
}

func TestIsWorkflowHandoffArtifactName(t *testing.T) {
	cases := []struct {
		filename string
		want     bool
	}{
		{"wf_nodeA_input_abc123.json", true},
		{"wf_nodeA_output_abc123.json", true},
		{"wf_nodeA_map_0_input_abc123.json", true},
		{"draft.md", false},
		{"wi_workflow_abc123.json", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsWorkflowHandoffArtifactName(c.filename); got != c.want {
			t.Errorf("IsWorkflowHandoffArtifactName(%q) = %v, want %v", c.filename, got, c.want)
		}
	}
}

func TestIsInternalCardScansEveryExtension(t *testing.T) {
	cases := []struct {
		name       string
		extensions []AgentExtension
		internal   bool
	}{
		{"no extensions", nil, false},
		{"the marker alone", []AgentExtension{{
			URI: ExtensionURIInternal, Params: map[string]any{"internal": true},
		}}, true},
		{"a malformed marker ahead of the real one", []AgentExtension{
			{URI: ExtensionURIInternal, Params: map[string]any{"internal": "yes"}},
			{URI: ExtensionURIInternal, Params: map[string]any{"internal": true}},
		}, true},
		{"the marker explicitly false", []AgentExtension{{
			URI: ExtensionURIInternal, Params: map[string]any{"internal": false},
		}}, false},
		{"another extension ahead of the marker", []AgentExtension{
			{URI: ExtensionURIDisplayName, Params: map[string]any{"display_name": "Builder"}},
			{URI: ExtensionURIInternal, Params: map[string]any{"internal": true}},
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			card := AgentCard{Capabilities: AgentCapabilities{Extensions: c.extensions}}
			if got := IsInternalCard(card); got != c.internal {
				t.Errorf("IsInternalCard = %v, want %v", got, c.internal)
			}
		})
	}
}

func TestHealthProblemsFromCard(t *testing.T) {
	since := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	want := []health.Problem{
		{ID: "queue:a2a/myAgent", Severity: health.Degraded, Detail: "provision cooldown", Since: since},
		{ID: "db:session", Severity: health.Fatal, Detail: "connection refused", Since: since},
	}

	inProcess := AgentCard{Capabilities: AgentCapabilities{Extensions: []AgentExtension{
		HealthProblemsExtension(want),
	}}}
	if got := HealthProblemsFromCard(inProcess); !slices.EqualFunc(got, want, problemsEqual) {
		t.Errorf("in-process []map[string]any: got %+v, want %+v", got, want)
	}

	data, err := json.Marshal(inProcess)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped AgentCard
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if got := HealthProblemsFromCard(roundTripped); !slices.EqualFunc(got, want, problemsEqual) {
		t.Errorf("json []any: got %+v, want %+v", got, want)
	}

	if got := HealthProblemsFromCard(AgentCard{Name: "no-extension"}); got != nil {
		t.Errorf("no extension: got %v, want nil", got)
	}
	if got := HealthProblemsExtension(nil); len(got.Params["problems"].([]map[string]any)) != 0 {
		t.Errorf("HealthProblemsExtension(nil) = %+v, want empty problems", got)
	}
}

func problemsEqual(a, b health.Problem) bool {
	return a.ID == b.ID && a.Severity == b.Severity && a.Detail == b.Detail && a.Since.Equal(b.Since)
}
