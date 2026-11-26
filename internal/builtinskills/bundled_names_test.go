// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builtinskills

import (
	"slices"
	"testing"
)

func TestBundledToolNamesAreASkillsManifestKeys(t *testing.T) {
	if got := BundledToolNames("sam-excel"); !slices.Equal(got, []string{"xlsx_read", "xlsx_write"}) {
		t.Errorf("sam-excel bundles %v, want xlsx_read and xlsx_write", got)
	}
	if got := BundledToolNames("sam-scheduling"); got != nil {
		t.Errorf("sam-scheduling, which has no tools manifest, bundles %v", got)
	}
	if got := BundledToolNames("no-such-skill"); got != nil {
		t.Errorf("an unknown skill bundles %v", got)
	}
}
