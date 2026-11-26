// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func buildExternalKey(ev *inboundEvent) string {
	thread := cmp.Or(ev.ThreadName, ev.MessageName)
	kind := "space:"
	if ev.IsDM {
		kind = "dm:"
	}
	key := kind + ev.SpaceName + ":" + thread
	if len(key) <= maxExternalKeyLen {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "gc:" + hex.EncodeToString(sum[:])
}

func (a *Adapter) resolveSessionID(ctx context.Context, externalKey, agentName, userID string) (string, error) {
	return a.sessKeys.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType:       store.GatewayTypeGoogleChat,
		ExternalKey:       externalKey,
		UserID:            userID,
		AgentID:           &agentName,
		RollOnOwnerChange: true,
	})
}
