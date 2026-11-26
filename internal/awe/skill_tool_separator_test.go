// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

func TestSkillBundledToolSepMatchesSTR(t *testing.T) {
	if skillBundledToolSep != str.SkillToolSep {
		t.Errorf("awe skillBundledToolSep %q != str.SkillToolSep %q; the prefixed names AWE "+
			"produces would no longer split on the STR side",
			skillBundledToolSep, str.SkillToolSep)
	}
}
