// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnessboot_test

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harnessboot"
)

func TestStartFakeIDPs(t *testing.T) {
	specs := []harness.FakeIDPSpec{
		{Name: "a", ClientID: "ca", ClientSecret: "sa", Users: []harness.FakeOIDCUser{{Sub: "alice"}}},
		{Name: "b", ClientID: "cb", ClientSecret: "sb", Users: []harness.FakeOIDCUser{{Sub: "bob"}}},
	}
	idps := harnessboot.StartFakeIDPs(t, specs)
	if len(idps) != 2 {
		t.Fatalf("expected 2 IdPs, got %d", len(idps))
	}
	if idps["a"] == nil || idps["b"] == nil {
		t.Fatal("missing keyed IdPs")
	}
	if idps["a"].URL() == idps["b"].URL() {
		t.Error("IdPs should listen on different ports")
	}
}
