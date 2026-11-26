// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"testing"
)

func TestSchedulingToolsRegistered(t *testing.T) {
	if !IsValidBuiltinGroup("scheduling_tools") {
		t.Fatal("scheduling_tools must be a valid built-in group")
	}
}
