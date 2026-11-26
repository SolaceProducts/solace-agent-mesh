// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"strings"
	"testing"
)

func TestOnlyALowercaseSHA256IsASkillContentHash(t *testing.T) {
	digest := strings.Repeat("0f", 32)
	for value, want := range map[string]bool{
		digest:                  true,
		strings.ToUpper(digest): false,
		strings.Repeat("g", 64): false,
		digest[:63]:             false,
		digest + "0":            false,
		"sha256:" + digest[:57]: false,
		"":                      false,
	} {
		if got := IsSkillContentHash(value); got != want {
			t.Errorf("IsSkillContentHash(%q) = %v, want %v", value, got, want)
		}
	}
}
