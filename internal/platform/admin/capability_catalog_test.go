// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/capabilities"
)

const (
	gatewayCapabilitySuffix   = "_gateway"
	connectorCapabilitySuffix = "_connector"
)

var nonTypeCapabilities = map[string]bool{
	"workflow_decision_node": true,
}

func TestCapabilityCatalog_KeysNameRegisteredTypes(t *testing.T) {
	validKeys := make(map[string]bool)
	for _, s := range getAllGatewaySchemas() {
		validKeys[s.Type+gatewayCapabilitySuffix] = true
	}
	connectorDefs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("load connector types: %v", err)
	}
	for _, d := range connectorDefs {
		validKeys[d.Type+connectorCapabilitySuffix] = true
		for _, st := range d.Subtypes {
			validKeys[d.Type+"_"+st.Subtype+connectorCapabilitySuffix] = true
		}
	}

	for _, c := range capabilities.NewRegistryForPlatform("linux", nil).All() {
		if !validKeys[c.Key] && !nonTypeCapabilities[c.Key] {
			t.Errorf("capability %q is neither <registered gateway type>%s, <registered connector type>%s, nor listed in nonTypeCapabilities",
				c.Key, gatewayCapabilitySuffix, connectorCapabilitySuffix)
		}
	}
}
