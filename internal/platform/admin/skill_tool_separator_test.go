// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

func TestSkillBundledToolSepMatchesSTR(t *testing.T) {
	if skillBundledToolSep != str.SkillToolSep {
		t.Errorf("admin skillBundledToolSep %q != str.SkillToolSep %q",
			skillBundledToolSep, str.SkillToolSep)
	}
	if store.PackageToolSeparator != str.PackageToolSep {
		t.Errorf("store.PackageToolSeparator %q != str.PackageToolSep %q",
			store.PackageToolSeparator, str.PackageToolSep)
	}
}
