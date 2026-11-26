// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestDataTagsContainWorking(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want bool
	}{
		{"working tag present", map[string]any{"tags": []any{"__working"}}, true},
		{"working among others", map[string]any{"tags": []any{"__user_uploaded", "__working"}}, true},
		{"other tags only", map[string]any{"tags": []any{"__user_uploaded"}}, false},
		{"empty tags", map[string]any{"tags": []any{}}, false},
		{"no tags key", map[string]any{}, false},
		{"tags wrong type", map[string]any{"tags": "__working"}, false},
		{"non-string element", map[string]any{"tags": []any{123, "__working"}}, true},
		{"in-memory []string tags", map[string]any{"tags": []string{a2a.ArtifactTagWorking}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DataTagsContainWorking(tt.data); got != tt.want {
				t.Errorf("DataTagsContainWorking(%v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
