// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestBuiltinAgentToolsets_FullParity(t *testing.T) {
	t.Parallel()
	want := slices.Sorted(maps.Keys(schema.ValidBuiltinGroups))
	got := slices.Clone(builtinAgentToolsets)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Errorf("builtinAgentToolsets out of sync with schema.ValidBuiltinGroups\n want: %v\n  got: %v", want, got)
	}
}
