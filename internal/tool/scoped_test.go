// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"slices"
	"testing"
)

type scopedTestTool struct {
	*mockTool
	scopes []string
}

func (s *scopedTestTool) RequiredScopes() []string { return s.scopes }

func TestRequiredScopesOf(t *testing.T) {
	t.Run("unscoped tool returns nil", func(t *testing.T) {
		if got := RequiredScopesOf(newMockTool("plain")); got != nil {
			t.Errorf("RequiredScopesOf(unscoped) = %v, want nil", got)
		}
	})

	t.Run("scoped tool returns its scopes", func(t *testing.T) {
		want := []string{"tool:scoped:invoke"}
		st := &scopedTestTool{mockTool: newMockTool("scoped"), scopes: want}
		if got := RequiredScopesOf(st); !slices.Equal(got, want) {
			t.Errorf("RequiredScopesOf(scoped) = %v, want %v", got, want)
		}
	})
}
