// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

func TestDescribeBuiltin_CarriesDeclaredEffect(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"artifact_grep": "read_only",
		"notify_user":   "side_effecting",
		"web_request":   "side_effecting",
	} {
		impl := toolinit.LookupBuiltinTool(name, nil, toolinit.Options{})
		if impl == nil {
			t.Fatalf("%s is not a built-in", name)
		}
		got := describeBuiltinImpl(DescribeToolRef{ToolName: name}, impl)
		if got.Effect != want {
			t.Errorf("%s: described effect %q, want %q", name, got.Effect, want)
		}
	}
}
