// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
)

func TestStatelessDatetimeEmbedRendersUTC(t *testing.T) {
	ref := &agent.EmbedRef{Type: "datetime", Params: map[string]string{"value": "%Z"}}
	got, ok := resolveStatelessEarlyEmbed(ref)
	if !ok || got != "UTC" {
		t.Errorf("got (%q, %v), want (%q, true)", got, ok, "UTC")
	}
}
