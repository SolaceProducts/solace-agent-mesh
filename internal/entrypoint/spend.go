// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func (g *GatewayCore) SpendPublisher() *spend.Publisher {
	if g.svc == nil || g.teardown.Load() {
		return nil
	}
	l := g.spend.Load()
	if l == nil {
		g.spend.CompareAndSwap(nil, spend.NewLazyPublisher(g.svc, g.Namespace()))
		l = g.spend.Load()
	}
	return l.Get()
}

func (g *GatewayCore) MeteredClient(client llm.Client, boundModel string) llm.Client {
	return spend.NewMeteredClient(client, boundModel, g.SpendPublisher(), g.BudgetGate())
}

func (g *GatewayCore) closeSpendPublisher(ctx context.Context) {
	if err := g.spend.Swap(nil).Close(ctx); err != nil {
		g.logger.Debug("spend publisher close", "error", err)
	}
}

func (g *GatewayCore) RequestSpend(caller *auth.Subject, userID, clientSurface string, surface spend.Surface, modelKey, modelConfigID, sessionID string) *llm.SpendAttribution {
	origin := g.RequestOrigin(caller, userID, clientSurface)
	userRowID, userSub := spend.StampedRowOr(origin.UserUUID, userID)
	entrypointID, entrypointName := spend.StampedRowOr(origin.EntrypointUUID, g.GatewayID())
	return &llm.SpendAttribution{
		UserID:         userRowID,
		UserSub:        userSub,
		EntrypointID:   entrypointID,
		EntrypointName: entrypointName,
		Channel:        origin.Channel,
		TriggerKind:    string(spend.TriggerRequest),
		Surface:        string(surface),
		ModelKey:       modelKey,
		ModelConfigID:  modelConfigID,
		SessionID:      sessionID,
	}
}
