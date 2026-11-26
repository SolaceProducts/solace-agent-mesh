// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"slices"
	"testing"
)

func mgWithSlug(name, slug string) *managedGateway {
	mg := &managedGateway{name: name, core: &GatewayCore{}}
	if slug != "" {
		mg.appConfig = map[string]any{"gateway_id": slug}
	}
	return mg
}

func TestListGatewayNames_ReportsSlugNotInstanceKey(t *testing.T) {
	exe := &GatewayExecutor{gws: []*managedGateway{
		mgWithSlug("0192f8a1-1111-7000-8000-000000000001", "my-slack"),
		mgWithSlug("0192f8a1-2222-7000-8000-000000000002", "my-teams"),
	}}
	got := exe.ListGatewayNames()
	slices.Sort(got)
	want := []string{"my-slack", "my-teams"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestListGatewayNames_FallsBackToInstanceKeyWithoutSlug(t *testing.T) {
	exe := &GatewayExecutor{gws: []*managedGateway{
		mgWithSlug("0192f8a1-3333-7000-8000-000000000003", ""),
	}}
	want := []string{"0192f8a1-3333-7000-8000-000000000003"}
	if got := exe.ListGatewayNames(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestListGatewayNames_Empty(t *testing.T) {
	exe := &GatewayExecutor{}
	if got := exe.ListGatewayNames(); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
