// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "github.com/SolaceProducts/solace-agent-mesh/internal/features"

type BuilderConnectorType struct {
	Type     string                    `json:"type"`
	Subtypes []BuilderConnectorSubtype `json:"subtypes"`
}

type BuilderConnectorSubtype struct {
	Subtype     string `json:"subtype"`
	Description string `json:"description,omitempty"`
}

func builderConnectorTypes(reg *features.Registry) []BuilderConnectorType {
	schemas := VisibleConnectorSchemas(reg)
	types := make([]BuilderConnectorType, 0, len(schemas))
	for _, schema := range schemas {
		subtypes := make([]BuilderConnectorSubtype, 0, len(schema.Subtypes))
		for _, st := range schema.Subtypes {
			subtypes = append(subtypes, BuilderConnectorSubtype{Subtype: st.Subtype, Description: st.Description})
		}
		types = append(types, BuilderConnectorType{Type: schema.Type, Subtypes: subtypes})
	}
	return types
}
