// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func TestRendersStatusTextOnlyForSurfacesThatShowTheStream(t *testing.T) {
	tests := map[string]bool{
		httputil.SurfaceWebUI: true,
		httputil.SurfaceCLI:   true,
		httputil.SurfaceAPI:   false,
		"":                    false,
	}
	adapter := &HTTPSSEAdapter{}
	for surface, want := range tests {
		t.Run("surface="+surface, func(t *testing.T) {
			if got := adapter.RendersStatusText(surface); got != want {
				t.Errorf("RendersStatusText(%q) = %v, want %v", surface, got, want)
			}
		})
	}
}
