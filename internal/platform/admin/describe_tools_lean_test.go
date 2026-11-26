// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build sam_lean

package platform

import (
	"testing"
)

func TestToolsDescribe_SamLeanBuildCannotDescribeSTRBackedConnectors(t *testing.T) {
	for _, c := range strConnectorCases {
		t.Run(c.name, func(t *testing.T) {
			f := newDescribeFixture(t, describeConnectorFlags...)
			conn := createHydrationConnector(t, f.connectors, "the-"+c.typ, c.typ, c.subtype, c.values)

			tools := f.describe(t, connectorRef(conn.Name, "")).Tools
			if len(tools) != len(c.binaries) {
				t.Fatalf("tools = %v, want every tool the connector deploys, named", toolNames(tools))
			}
			for _, got := range tools {
				if got.Status != describeStatusUnavailable || got.Reason != reasonUnsupportedInBuild || got.ToolNode.Usable || !isNullSchema(got.Parameters) {
					t.Errorf("%s: got %+v, want unavailable with %q and no parameters", got.Name, got, reasonUnsupportedInBuild)
				}
			}
		})
	}
}

func TestToolsDescribe_SamLeanBuildStillDescribesAPIMCPAndBuiltins(t *testing.T) {
	f := newDescribeFixture(t)
	f.shipmentConnector(t, "carrier-api", "")
	f.mcpConnector(t, "acme-crm", crmManifest())

	for _, ref := range []DescribeToolRef{toolRef("get_current_time"), connectorRef("carrier-api", "get_shipment"), connectorRef("acme-crm", "find_customer")} {
		if got := f.one(t, ref); got.Status != describeStatusOK || isNullSchema(got.Parameters) {
			t.Errorf("%+v: got %+v, want ok with parameters", ref, got)
		}
	}
}
