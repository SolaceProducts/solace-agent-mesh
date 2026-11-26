// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"
)

func TestBuiltinToolNamesAreTheFactories(t *testing.T) {
	got := BuiltinToolNames()
	if len(got) != len(builtinToolFactories) || !slices.IsSorted(got) {
		t.Fatalf("BuiltinToolNames() has %d names, sorted %t; want the %d factories, sorted",
			len(got), slices.IsSorted(got), len(builtinToolFactories))
	}
	for _, name := range got {
		if _, ok := builtinToolFactories[name]; !ok {
			t.Errorf("%q is not a factory", name)
		}
	}
}
