// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"
)

func eventMeshBroker() map[string]any {
	return map[string]any{
		"broker_url": "tcps://broker.example.com:55443", "broker_vpn": "default",
		"broker_username": "svc", "broker_password": "real-password",
	}
}

func eventMeshOperation() map[string]any {
	return map[string]any{
		"tool_name": "create_order", "tool_description": "Create an order for fulfilment",
		"topic": "retail/orders/create", "wait_for_response": false, "payload_format": "json",
		"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func eventMeshFixture() connectorFixture {
	return connectorFixture{connType: "event_mesh", subtype: "solace", gate: "event_mesh_connector"}
}

func eventMeshValues(extra map[string]any) map[string]any {
	values := eventMeshBroker()
	for k, v := range extra {
		values[k] = v
	}
	return values
}

func canonicalEventMeshValues() map[string]any {
	return eventMeshValues(map[string]any{"operations": []any{eventMeshOperation()}})
}

func flatEventMeshValues() map[string]any {
	return eventMeshValues(eventMeshOperation())
}

type eventMeshKeyCase struct {
	name         string
	stored       map[string]any
	authored     map[string]any
	wantRejected []string
	wantHint     map[string]string
}

func TestApply_EventMeshConnectorKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []eventMeshKeyCase{
		{name: "create, canonical form", authored: canonicalEventMeshValues()},
		{name: "create, flat form keeps its top-level operation keys", authored: flatEventMeshValues()},
		{
			name:         "create, a mistyped operations key is not taken for the flat form",
			authored:     eventMeshValues(map[string]any{"operatons": []any{eventMeshOperation()}}),
			wantRejected: []string{"operatons"}, wantHint: map[string]string{"operatons": "operations"},
		},
		{
			name:         "create, a mistyped broker key in the flat form",
			authored:     eventMeshValues(map[string]any{"broker_urll": "x", "topic": "a/b"}),
			wantRejected: []string{"broker_urll"}, wantHint: map[string]string{"broker_urll": "broker_url"},
		},
		{
			name:         "create, a flat-form key that is not an operation key",
			authored:     eventMeshValues(map[string]any{"topic": "a/b", "bogusKey": 1}),
			wantRejected: []string{"bogusKey"},
		},
		{
			name: "update of a canonical connector, canonical authored", stored: canonicalEventMeshValues(),
			authored: eventMeshValues(map[string]any{"operations": []any{eventMeshOperation()}, "broker_vpn": "other"}),
		},
		{
			name: "update of a canonical connector, a mistyped operations key", stored: canonicalEventMeshValues(),
			authored:     eventMeshValues(map[string]any{"operatons": []any{eventMeshOperation()}}),
			wantRejected: []string{"operatons"}, wantHint: map[string]string{"operatons": "operations"},
		},
		{
			name: "update of a canonical connector, flat keys authored without operations", stored: canonicalEventMeshValues(),
			authored:     eventMeshValues(map[string]any{"topic": "a/b", "tool_name": "x"}),
			wantRejected: []string{"tool_name", "topic"},
		},
		{
			name: "update of a flat connector, flat authored", stored: flatEventMeshValues(),
			authored: eventMeshValues(map[string]any{"topic": "retail/orders/updated", "tool_name": "create_order", "tool_description": "Create an order for fulfilment"}),
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func (tc eventMeshKeyCase) run(t *testing.T) {
	f := eventMeshFixture()
	svc := serviceFor(t, f, true)
	const name = "event_mesh_keys"
	if tc.stored != nil {
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorArtifactFor(name, f, tc.stored)}}, testActor); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	dry, applied := applyBoth(t, svc, connectorArtifactFor(name, f, tc.authored))
	dryOp, appliedOp := opFor(t, dry, kindConnector, name), opFor(t, applied, kindConnector, name)
	if len(tc.wantRejected) == 0 {
		requireAcceptedWithoutMissingWarnings(t, dryOp, appliedOp)
		return
	}
	requireRejectedKeys(t, dryOp, keyCase{wantRejected: tc.wantRejected, wantHint: tc.wantHint})
	requireSameFailure(t, dryOp, appliedOp, "does not declare")
	if tc.stored == nil {
		if len(storedConnectors(t, svc)) != 0 {
			t.Error("a rejected create stored a connector")
		}
		return
	}
	for _, key := range tc.wantRejected {
		if _, wrote := storedValues(t, svc, name)[key]; wrote {
			t.Errorf("a rejected update stored %q", key)
		}
	}
}

func requireAcceptedWithoutMissingWarnings(t *testing.T, dryOp, appliedOp BuildOperationResult) {
	t.Helper()
	if dryOp.Status != statusPlanned || appliedOp.Status != statusSucceeded {
		t.Fatalf("dry = %+v, applied = %+v; errors %+v / %+v, want accepted", dryOp, appliedOp, dryOp.Error, appliedOp.Error)
	}
	for _, op := range []BuildOperationResult{dryOp, appliedOp} {
		if warnings := strings.Join(op.Warnings, "\n"); strings.Contains(warnings, "which the") {
			t.Errorf("a connector with every required field was warned of missing ones: %q", warnings)
		}
	}
}

func TestApply_EventMeshUpdateSilentAboutValuesKeepsTheStoredOperations(t *testing.T) {
	t.Parallel()
	for _, shape := range valuesShapes() {
		if shape.notAnObj {
			continue
		}
		t.Run(shape.name, func(t *testing.T) {
			f := eventMeshFixture()
			svc := serviceFor(t, f, true)
			const name = "event_mesh_silent"
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorArtifactFor(name, f, canonicalEventMeshValues())}}, testActor); err != nil {
				t.Fatalf("seed: %v", err)
			}
			dry, applied := applyBoth(t, svc, artifactWithValuesJSON(name, f, shape.json))
			requireAcceptedWithoutMissingWarnings(t, opFor(t, dry, kindConnector, name), opFor(t, applied, kindConnector, name))
			if operations, _ := storedValues(t, svc, name)["operations"].([]any); len(operations) != 1 {
				t.Errorf("stored operations = %v, want the one operation kept", storedValues(t, svc, name)["operations"])
			}
		})
	}
}
