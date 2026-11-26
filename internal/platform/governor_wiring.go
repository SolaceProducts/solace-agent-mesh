// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (p *PlatformCore) newGovernor() *governor.Governor {
	var missing []string
	if p.db == nil {
		missing = append(missing, "database")
	}
	if p.svc.Broker() == nil {
		missing = append(missing, "broker")
	}
	if len(missing) > 0 {
		p.logger.Warn("the spend governor cannot start; spend is not being filed",
			"missing", missing)
		return nil
	}
	return governor.New(governor.Config{
		Namespace: p.cfg.Namespace,
		Broker:    p.svc.Broker(),
		Ledger:    store.NewSpendLedgerRepo(p.db),
		Prices:    governor.NewModelConfigPrices(store.NewModelConfigRepo(p.db), p.logger),
		Logger:    p.logger,
		Metrics:   p.svc.Metrics(),
	})
}
