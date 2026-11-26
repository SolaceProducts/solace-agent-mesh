// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestBuildConnectorToolsStampsTheCategoryOnEveryEntry(t *testing.T) {
	t.Parallel()
	subtypes := map[string]string{
		"document_db": "mongodb",
		"graph_db":    "neo4j",
		"search":      "elasticsearch",
		"web_search":  "tavily",
	}
	for _, category := range taxonomy.ConnectorCategories {
		t.Run(category, func(t *testing.T) {
			t.Parallel()
			conn := ConnectorConfig{
				ID:      "550e8400-e29b-41d4-a716-446655440000",
				Type:    category,
				Subtype: subtypes[category],
				Values:  map[string]any{},
			}
			entries := buildConnectorTools(conn)
			if len(entries) == 0 {
				t.Fatalf("connector type %q emitted no tool entries", category)
			}
			for i, entry := range entries {
				if got := entry["source_connector_category"]; got != category {
					t.Errorf("entry %d: source_connector_category = %v, want %q", i, got, category)
				}
			}
		})
	}
}

func TestConnectorTypesMatchTheCatalog(t *testing.T) {
	t.Parallel()
	var emitting []string
	candidates := append(slices.Clone(taxonomy.ConnectorCategories),
		"vector_db", "queue", "ftp", "warehouse", "acme_customer_ledger", "")
	for _, candidate := range candidates {
		conn := ConnectorConfig{ID: "550e8400-e29b-41d4-a716-446655440000", Type: candidate, Values: map[string]any{}}
		if len(buildConnectorTools(conn)) > 0 {
			emitting = append(emitting, candidate)
		}
	}
	approved := slices.Clone(taxonomy.ConnectorCategories)
	slices.Sort(approved)
	slices.Sort(emitting)
	if !slices.Equal(emitting, approved) {
		t.Errorf("the generator emits tools for %v, the catalog approves %v; keep them in step", emitting, approved)
	}
}
