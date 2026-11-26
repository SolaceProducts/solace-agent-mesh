// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package embedded

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestTheFreeEditionKeepsTheFreeModelGrant(t *testing.T) {
	core := platform.NewPlatformCore(nil, platform.ExtensionPoints{})
	retireFreeModel(core)
	if len(core.RetiredGrants()) != 0 {
		t.Errorf("retired grants = %v, want none", core.RetiredGrants())
	}
}
