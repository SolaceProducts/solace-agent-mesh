// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestRejectImmutableEdits(t *testing.T) {
	t.Parallel()
	storedConnector := storedResource{raw: entplatform.ConnectorResponse{Name: "orders_db", Type: "sql", Subtype: "postgres"}}
	storedGateway := storedResource{raw: entplatform.GatewayResponse{Name: "OrderEvents", Type: "event_mesh"}}

	connector := func(connectorType, subtype string) desiredComponent {
		return desiredComponent{create: entplatform.CreateConnectorRequest{Name: "orders_db", Type: connectorType, Subtype: subtype}}
	}
	gateway := func(gatewayType string) desiredComponent {
		return desiredComponent{create: entplatform.CreateGatewayRequest{Name: "OrderEvents", Type: gatewayType}}
	}

	for _, tc := range []struct {
		name          string
		componentKind string
		desired       desiredComponent
		stored        storedResource
		wantRefused   string
	}{
		{"an unchanged connector passes", kindConnector, connector("sql", "postgres"), storedConnector, ""},
		{"retyping a connector is refused", kindConnector, connector("http", "postgres"), storedConnector, `connector "Orders DB" is stored with type "sql"`},
		{"re-subtyping a connector is refused", kindConnector, connector("sql", "mysql"), storedConnector, `connector "Orders DB" is stored with subtype "postgres"`},
		{"an unchanged entrypoint passes", kindGateway, gateway("event_mesh"), storedGateway, ""},
		{"retargeting an entrypoint's type is refused", kindGateway, gateway("slack"), storedGateway, `entrypoint "OrderEvents" is stored with type "event_mesh"`},

		{"a connector that states no subtype claims no change", kindConnector, connector("sql", ""), storedConnector, ""},
		{"a connector that states no type claims no change", kindConnector, connector("", "postgres"), storedConnector, ""},
		{"an entrypoint that states no type claims no change", kindGateway, gateway(""), storedGateway, ""},

		{"a create body of the wrong type does not panic", kindConnector, desiredComponent{create: entplatform.CreateGatewayRequest{Type: "slack"}}, storedConnector, ""},
		{"a stored body of the wrong type is not judged", kindConnector, connector("http", "postgres"), storedResource{raw: entplatform.GatewayResponse{Type: "event_mesh"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			componentName := map[string]string{kindConnector: "Orders DB", kindGateway: "OrderEvents"}[tc.componentKind]
			err := rejectImmutableEdits(tc.componentKind, componentName, tc.desired, tc.stored)

			if tc.wantRefused == "" {
				if err != nil {
					t.Fatalf("err = %v, want the write allowed", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("err = nil, want a refusal naming %q — the plan plans an Update the update body cannot deliver, so the edit would report success and change nothing", tc.wantRefused)
			}
			if !strings.Contains(err.Error(), tc.wantRefused) {
				t.Errorf("err = %q, want it to contain %q", err, tc.wantRefused)
			}
			if _, isValidation := err.(*entplatform.ValidationError); !isValidation {
				t.Errorf("err is %T, want *ValidationError — anything else is a 500 rather than a message the author can act on", err)
			}
		})
	}
}
