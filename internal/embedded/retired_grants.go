// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import "github.com/SolaceProducts/solace-agent-mesh/internal/platform"

func retireFreeModel(core *platform.PlatformCore) {
	for _, id := range grantsLeftByOtherEditions {
		core.RetireGrant(id)
	}
}
