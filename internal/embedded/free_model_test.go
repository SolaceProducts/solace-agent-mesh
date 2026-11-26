// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !solace_id

package embedded

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestABuildWithoutTheFreeModelRetiresItsGrant(t *testing.T) {
	core := platform.NewPlatformCore(nil, platform.ExtensionPoints{})
	retireFreeModel(core)
	if !slices.Equal(core.RetiredGrants(), []string{llm.FreeModelGrantID}) {
		t.Errorf("retired grants = %v, want the free model's grant", core.RetiredGrants())
	}
}
