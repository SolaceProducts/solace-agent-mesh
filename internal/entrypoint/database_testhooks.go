// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"

func (g *GatewayCore) UseDatabaseForTest(db *store.DB) {
	g.useDatabase(db)
}

func (g *GatewayCore) UseTaskRegistryForTest(reg TaskRegistry) {
	g.tasks = reg
}
