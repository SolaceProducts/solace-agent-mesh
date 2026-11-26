// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func webSearchConnector(t *testing.T, name, subtype string, values map[string]any) BuildComponentInput {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name": name, "description": "Searches the web for the research agent and cites the results.",
		"type": "web_search", "subtype": subtype, "icon": "web_search", "values": values,
	})
	if err != nil {
		t.Fatal(err)
	}
	return BuildComponentInput{Kind: kindConnector, Name: name, Content: string(body)}
}

func TestApply_WebSearchConnectorMovedWithoutItsKeyIsRefused(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.features = registryEnabling(t, "web_search_connector")
	saved := webSearchConnector(t, "web-move", "tavily", map[string]any{"api_key": "${TAVILY_API_KEY}"})
	created, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{saved}}, testActor)
	if err != nil || created.Summary.Failed != 0 {
		t.Fatalf("create = %+v, %v", created.Operations, err)
	}

	moved := webSearchConnector(t, "web-move", "tavily", map[string]any{
		"api_key": "<<__SAM_REQUIRED__>>", "api_base": "https://collector.example",
	})
	result, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{moved}}, testActor)
	if err == nil && result.Summary.Failed == 0 {
		t.Fatalf("apply = %+v, want the move refused", result.Operations)
	}
	reported, _ := json.Marshal(result.Operations)
	if !strings.Contains(fmt.Sprint(err)+string(reported), "Enter the API key again") {
		t.Fatalf("apply = %s, %v; want the refusal to ask for the key again", reported, err)
	}

	row, err := store.NewConnectorRepo(svc.deps.DB).GetByID(t.Context(), connectorNamed(t, svc, "web-move").ID)
	if err != nil || row == nil || row.Values == nil {
		t.Fatalf("stored connector = %v, %v", row, err)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*row.Values), &stored); err != nil {
		t.Fatal(err)
	}
	if base, _ := stored["api_base"].(string); base == "https://collector.example" {
		t.Fatalf("stored api_base = %v: the stored key now goes to the new host", base)
	}
}

func TestApply_WebSearchConnectorWithAPlaceholderKeyIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, subtype := range []string{"tavily", "exa"} {
		t.Run(subtype, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			svc.features = registryEnabling(t, "web_search_connector")
			comp := webSearchConnector(t, "web-"+subtype, subtype, map[string]any{"api_key": "<<__SAM_REQUIRED__>>"})

			first, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
			if err != nil {
				t.Fatalf("first apply: %v", err)
			}
			if first.Summary.Failed != 0 || first.Operations[0].Action != actionCreate {
				t.Fatalf("first apply = %+v, want the connector created", first.Operations)
			}
			for _, warning := range first.Operations[0].Warnings {
				t.Errorf("first apply warned %q; api_base is defaulted and the key is a placeholder", warning)
			}

			second, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
			if err != nil {
				t.Fatalf("second apply: %v", err)
			}
			if op := second.Operations[0]; op.Action != actionUnchanged {
				t.Errorf("re-applying the same connector planned %q (changed %v)", op.Action, op.ChangedFields)
			}
			row, err := store.NewConnectorRepo(svc.deps.DB).GetByID(t.Context(), connectorNamed(t, svc, "web-"+subtype).ID)
			if err != nil || row == nil || row.Values == nil {
				t.Fatalf("stored connector = %v, %v", row, err)
			}
			var stored map[string]any
			if err := json.Unmarshal([]byte(*row.Values), &stored); err != nil {
				t.Fatal(err)
			}
			if stored["api_key"] != "<<__SAM_REQUIRED__>>" {
				t.Errorf("stored api_key = %v, want the placeholder kept for the user to fill in", stored["api_key"])
			}
		})
	}
}

func TestApply_WebSearchConnectorIsRefusedWhileItsTypeIsOff(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	comp := webSearchConnector(t, "web-off", "tavily", map[string]any{"api_key": "<<__SAM_REQUIRED__>>"})

	result, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := result.Operations[0]; op.Status != statusFailed {
		t.Errorf("op = %+v, want the connector refused while web_search_connector is off", op)
	}
}
