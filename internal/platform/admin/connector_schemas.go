// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func GetConnectorSchemas() []ConnectorSchemaResponse {
	schemas, err := LoadConnectorSchemas()
	if err != nil {
		log.Printf("ERROR: failed to load connector schemas from YAML: %v", err)
		return nil
	}
	return schemas
}

func VisibleConnectorSchemas(reg *features.Registry) []ConnectorSchemaResponse {
	all := GetConnectorSchemas()
	visible := make([]ConnectorSchemaResponse, 0, len(all))
	for _, s := range all {
		if !ConnectorTypeHidden(reg, s.Type) {
			visible = append(visible, s)
		}
	}
	return visible
}
