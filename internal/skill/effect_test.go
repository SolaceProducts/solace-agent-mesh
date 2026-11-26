// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSkillToolsHaveTheirReviewedEffect_LoadRegistersToolsUnloadOnlyChangesTheRunsOwnSession(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tool   tool.Tool
		want   tool.Effect
		reason string
	}{
		{&LoadSkillTool{}, tool.EffectSideEffecting, "registers the tools a skill names, which connect to URLs the skill's content chooses"},
		{&UnloadSkillTool{}, tool.EffectReadOnly, "removes a skill from the run's own session state and writes no artifact"},
	} {
		if got := tool.EffectClassOf(tc.tool); got != tool.DeclaredEffect(tc.want) {
			t.Errorf("%T: EffectClassOf = %+v, want declared %s because it %s", tc.tool, got, tc.want, tc.reason)
		}
	}
}
