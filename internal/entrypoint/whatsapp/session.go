// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"strconv"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const sessionIdleReset = 24 * time.Hour

func buildExternalKey(waID string, sessionEpoch int64) string {
	return "whatsapp:" + waID + ":" + strconv.FormatInt(sessionEpoch, 10)
}

func (a *Adapter) resolveSessionID(ctx context.Context, externalKey, agentName, userID string) (string, error) {
	if a.sessKeys == nil {
		return idgen.New(), nil
	}
	return a.sessKeys.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType:       store.GatewayTypeWhatsApp,
		ExternalKey:       externalKey,
		UserID:            userID,
		AgentID:           &agentName,
		RollOnOwnerChange: true,
	})
}
