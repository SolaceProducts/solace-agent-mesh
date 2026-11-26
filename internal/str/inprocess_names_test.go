// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"slices"
	"testing"
)

func TestInProcessToolNamesListsTheRegisteredTools(t *testing.T) {
	withGlobalInProcessTool(t, newBridgeFixtureTool("zz_fixture_b"))
	withGlobalInProcessTool(t, newBridgeFixtureTool("zz_fixture_a"))
	got := InProcessToolNames()
	if !slices.Contains(got, "zz_fixture_a") || !slices.Contains(got, "zz_fixture_b") || !slices.IsSorted(got) {
		t.Errorf("InProcessToolNames() = %v, want both registered fixtures, sorted", got)
	}
}

func TestInProcessToolNamesDeDuplicates(t *testing.T) {
	withGlobalInProcessTool(t, newBridgeFixtureTool("zz_fixture_dup"))
	withGlobalInProcessTool(t, newBridgeFixtureTool("zz_fixture_dup"))
	got := InProcessToolNames()
	n := 0
	for _, name := range got {
		if name == "zz_fixture_dup" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("zz_fixture_dup appears %d times in %v, want once", n, got)
	}
}
