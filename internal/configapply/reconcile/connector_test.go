// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func connectorFixture(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"type":    "messaging",
		"subtype": "slack",
		"values": map[string]any{
			"slack_bot_token": "xoxb-real-secret",
			"slack_channel":   "#ops-alerts",
		},
	}
	if f != nil {
		f(m)
	}
	return m
}

func resolvedConnector(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:        "connector",
		Name:        name,
		SourceFile:  "connectors/" + name + ".yaml",
		Spec:        spec,
		Description: "fixture",
	}
}

func TestPlanConnectors_CreateAndDelete(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(nil))}
	actual := []wire.ConnectorResponse{{ID: "id-old", Name: "obsolete"}}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "ops-slack" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "id-old" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 0 {
		t.Errorf("expected no updates/unchanged, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
}

func TestPlanConnectors_NoOpWhenSecretRedacted(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(nil))}

	actual := []wire.ConnectorResponse{{
		ID:               "id-1",
		Name:             "ops-slack",
		Description:      sp("fixture"),
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values: map[string]any{
			"slack_bot_token": "<REDACTED>",
			"slack_channel":   "#ops-alerts",
		},
	}}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("expected no diff, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanConnectors_UpdateOnNonSecretChange(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(func(m map[string]any) {
		m["values"] = map[string]any{
			"slack_bot_token": "xoxb-real-secret",
			"slack_channel":   "#different-channel",
		}
	}))}
	actual := []wire.ConnectorResponse{{
		ID:               "id-1",
		Name:             "ops-slack",
		Description:      sp("fixture"),
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values: map[string]any{
			"slack_bot_token": "<REDACTED>",
			"slack_channel":   "#ops-alerts",
		},
	}}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Errorf("Updates = %+v", plan.Updates)
	}
}

func TestPlanConnectors_RejectsTypeMutation(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("ops-slack", connectorFixture(func(m map[string]any) {
		m["type"] = "email"
		m["subtype"] = "smtp"
	}))}
	actual := []wire.ConnectorResponse{{
		ID:               "id-1",
		Name:             "ops-slack",
		ConnectorType:    "slack",
		ConnectorSubtype: "bot",
	}}
	if _, err := planConnectors(t.Context(), nil, desired, actual); err == nil {
		t.Fatal("expected error when type changes between desired and actual")
	}
}

func TestPlanConnectors_DuplicateNameInDesired(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedConnector("dup", connectorFixture(nil)),
		resolvedConnector("dup", connectorFixture(nil)),
	}
	if _, err := planConnectors(t.Context(), nil, desired, nil); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestRedactedKeySet(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "non-map", in: "string", want: nil},
		{name: "no secrets", in: map[string]any{"a": "b"}, want: nil},
		{
			name: "one secret",
			in:   map[string]any{"a": "<REDACTED>", "b": "c"},
			want: []string{"a"},
		},
		{
			name: "non-string redacted-looking value is not a secret",
			in:   map[string]any{"a": 42, "b": "<REDACTED>"},
			want: []string{"b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactedKeySet(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d keys, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for _, k := range tc.want {
				if _, ok := got[k]; !ok {
					t.Errorf("expected key %q in result", k)
				}
			}
		})
	}
}
