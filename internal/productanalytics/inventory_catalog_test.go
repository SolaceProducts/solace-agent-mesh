// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"slices"
	"testing"
)

func scrubInventory(t *testing.T, props map[string]any) map[string]any {
	t.Helper()
	out, ok := scrub(SurfaceServer, RawEvent{Name: EventInstallInventory, Properties: props})
	if !ok {
		t.Fatal("install:inventory should be an approved server event")
	}
	return out
}

func setField(t *testing.T, out map[string]any, key string) []string {
	t.Helper()
	v, present := out[key]
	if !present {
		t.Fatalf("%s was dropped entirely", key)
	}
	set, ok := v.([]string)
	if !ok {
		t.Fatalf("%s = %T, want []string", key, v)
	}
	return set
}

func TestInventoryIsNotReachableFromAClient(t *testing.T) {
	for _, surface := range []string{SurfaceUI, SurfaceCLI} {
		if _, ok := scrub(surface, RawEvent{Name: EventInstallInventory}); ok {
			t.Errorf("surface %q must not be able to emit install:inventory", surface)
		}
	}
}

func TestInventoryCountsAreExact(t *testing.T) {
	counts := map[string]any{
		PropAgentCount: 3, PropWorkflowCount: 2, PropConnectorCount: 1,
		PropEntrypointCount: 40, PropToolsetCount: 900,
		PropAgentsWithCustomToolsets: 12, PropWorkflowsWithCustomToolsets: 0,
	}
	out := scrubInventory(t, counts)
	for key, want := range counts {
		if got := out[key]; got != int64(want.(int)) {
			t.Errorf("%s = %v (%T), want the exact count %v", key, got, got, want)
		}
	}
}

func TestCountRejectsANonCount(t *testing.T) {
	for _, bad := range []any{"3", 1.5, -1, nil, []int{1}, maxReportableCount + 1} {
		out := scrubInventory(t, map[string]any{PropAgentCount: bad})
		if got, present := out[PropAgentCount]; present {
			t.Errorf("value %#v was kept as %v, want it dropped", bad, got)
		}
	}
}

func TestCountIsBoundedFromAbove(t *testing.T) {
	out := scrubInventory(t, map[string]any{PropAgentCount: maxReportableCount})
	if out[PropAgentCount] != int64(maxReportableCount) {
		t.Errorf("a count at the ceiling was dropped: %v", out[PropAgentCount])
	}
	out = scrubInventory(t, map[string]any{PropAgentCount: maxReportableCount + 1})
	if got, present := out[PropAgentCount]; present {
		t.Errorf("a count above the ceiling was kept as %v, want it dropped", got)
	}
}

func TestInventorySetsAreDeduplicatedAndSorted(t *testing.T) {
	out := scrubInventory(t, map[string]any{
		PropConnectorCategories: []string{"sql", "api", "sql", "knowledge_base", "api"},
		PropEntrypointTypes:     []string{"webhook", "mcp", "webhook"},
	})
	if got := setField(t, out, PropConnectorCategories); !slices.Equal(got, []string{"api", "knowledge_base", "sql"}) {
		t.Errorf("connectorCategories = %v, want [api knowledge_base sql]", got)
	}
	if got := setField(t, out, PropEntrypointTypes); !slices.Equal(got, []string{"mcp", "webhook"}) {
		t.Errorf("entrypointTypes = %v, want [mcp webhook]", got)
	}
}

func TestInventorySetsRejectAnUnknownValue(t *testing.T) {
	out := scrubInventory(t, map[string]any{
		PropConnectorCategories: []string{"sql", "acme-payroll-prod", "knowledge_base"},
		PropEntrypointTypes:     []string{"slack", "carrier-pigeon"},
	})
	if got := setField(t, out, PropConnectorCategories); !slices.Equal(got, []string{"knowledge_base", "sql"}) {
		t.Errorf("connectorCategories = %v, want the unknown value dropped", got)
	}
	if got := setField(t, out, PropEntrypointTypes); !slices.Equal(got, []string{"slack"}) {
		t.Errorf("entrypointTypes = %v, want the unknown value dropped", got)
	}
}

func TestInventoryEmptySetIsKept(t *testing.T) {
	out := scrubInventory(t, map[string]any{
		PropConnectorCategories: []string{},
		PropEntrypointTypes:     []string{"nonsense-only"},
	})
	if got := setField(t, out, PropConnectorCategories); len(got) != 0 {
		t.Errorf("connectorCategories = %v, want empty", got)
	}
	if got := setField(t, out, PropEntrypointTypes); len(got) != 0 {
		t.Errorf("entrypointTypes = %v, want empty after every value was rejected", got)
	}
}

func TestInventorySetRejectsANonSet(t *testing.T) {
	for _, bad := range []any{"sql", 7, map[string]any{"sql": true}, []any{"sql", 3}} {
		out := scrubInventory(t, map[string]any{PropConnectorCategories: bad})
		if _, present := out[PropConnectorCategories]; present {
			t.Errorf("value %#v should have been dropped, got %#v", bad, out[PropConnectorCategories])
		}
	}
}

func TestInventorySetAcceptsAJSONArray(t *testing.T) {
	out := scrubInventory(t, map[string]any{PropConnectorCategories: []any{"sql", "api"}})
	if got := setField(t, out, PropConnectorCategories); !slices.Equal(got, []string{"api", "sql"}) {
		t.Errorf("connectorCategories = %v, want [api sql]", got)
	}
}

func TestEnumSetShapeBoundsElementCount(t *testing.T) {
	spec := enumSetShape(2, "a", "b", "c", "d")
	got, keep := spec.validate([]string{"d", "c", "b", "a"})
	if !keep {
		t.Fatal("value should be kept")
	}
	if !slices.Equal(got.([]string), []string{"a", "b"}) {
		t.Errorf("got %v, want the first two of the sorted set", got)
	}
}

func TestInventoryCarriesNoNameField(t *testing.T) {
	for field, spec := range serverCatalog[EventInstallInventory] {
		if _, keep := spec.validate("acme-payroll-agent"); keep {
			t.Errorf("%s accepted a free-text value; every inventory field must be a count or a closed set", field)
		}
	}
}
