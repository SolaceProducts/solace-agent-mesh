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

func TestConnectorCategoriesMatchTheConnectorTypeDefinitions(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("load connector type definitions: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("no connector type definitions loaded, so this guard would pass on an empty set")
	}
	declared := make([]string, 0, len(defs))
	for _, def := range defs {
		declared = append(declared, def.Type)
	}
	slices.Sort(declared)
	approved := slices.Clone(taxonomy.ConnectorCategories)
	slices.Sort(approved)
	if !slices.Equal(declared, approved) {
		t.Errorf("connector_types/*.yaml declare %v, taxonomy.ConnectorCategories approves %v; update the catalog", declared, approved)
	}
}

func TestGatewayTypesCoverEveryPlatformSchema(t *testing.T) {
	t.Parallel()
	schemas := getAllGatewaySchemas()
	if len(schemas) == 0 {
		t.Fatal("no gateway schemas registered, so this guard would pass on an empty set")
	}
	for _, schema := range schemas {
		if !slices.Contains(taxonomy.GatewayTypes, schema.Type) {
			t.Errorf("gateway type %q has a creation schema but is not in taxonomy.GatewayTypes; add it to the catalog", schema.Type)
		}
	}
}
