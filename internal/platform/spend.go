// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func (p *PlatformCore) SpendPublisher() *spend.Publisher {
	return p.spend.Get()
}

func (p *PlatformCore) BudgetGate() *budget.Gate {
	if p.svc == nil {
		return nil
	}
	return p.svc.BudgetGate()
}

func (p *PlatformCore) closeSpendPublisher(ctx context.Context) {
	if err := p.spend.Close(ctx); err != nil {
		p.logger.Debug("platform spend publisher close failed", "error", err)
	}
}
