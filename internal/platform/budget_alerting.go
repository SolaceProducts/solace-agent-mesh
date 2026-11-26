// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/budgetstate"
)

var errBudgetAdminsNotReady = errors.New("budget administrators are not available until the admin routes are registered")

func (p *PlatformCore) SetBudgetAdmins(d budgetstate.AdminDirectory) {
	p.budgetAdminsMu.Lock()
	defer p.budgetAdminsMu.Unlock()
	p.budgetAdmins = d
}

func (p *PlatformCore) BudgetAdmins(ctx context.Context) ([]budgetstate.Recipient, error) {
	p.budgetAdminsMu.Lock()
	d := p.budgetAdmins
	p.budgetAdminsMu.Unlock()
	if d == nil {
		return nil, errBudgetAdminsNotReady
	}
	return d.BudgetAdmins(ctx)
}

func (p *PlatformCore) budgetAlerting() budgetstate.Alerting {
	return budgetstate.Alerting{
		Admins:        p,
		NotifyEnabled: p.notifyServiceOn,
		Metrics:       p.svc.Metrics(),
	}
}

func (p *PlatformCore) notifyServiceOn() bool {
	reg := p.svc.FeatureRegistry()
	return reg != nil && reg.IsEnabled(features.KeyNotifyService)
}
