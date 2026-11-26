// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestWebRequestIsSideEffectingWhateverTheMethod(t *testing.T) {
	t.Parallel()
	wr := NewWebRequestTool(nil)
	want := tool.DeclaredEffect(tool.EffectSideEffecting)
	if got := tool.EffectClassOf(wr); got != want {
		t.Fatalf("static class = %+v, want declared side_effecting", got)
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"default method", map[string]any{"url": "https://example.com"}},
		{"GET", map[string]any{"method": "GET", "url": "https://example.com/?q=secret"}},
		{"lowercase head", map[string]any{"method": "head"}},
		{"OPTIONS", map[string]any{"method": "OPTIONS"}},
		{"POST", map[string]any{"method": "POST"}},
		{"GET saved under a chosen name", map[string]any{"method": "GET", "output_artifact_filename": "page.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tool.CallEffectClassOf(wr, tc.args); got != want {
				t.Fatalf("CallEffectClassOf(%v) = %+v, want %+v: a fetch of a chosen URL is never read_only", tc.args, got, want)
			}
		})
	}
}
