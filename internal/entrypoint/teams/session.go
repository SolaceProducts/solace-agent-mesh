// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const maxExternalKeyLen = 128

func buildExternalKey(activity *Activity) string {
	dateBucket := time.Now().UTC().Format("20060102")
	convID := activity.Conversation.ID
	convType := activity.Conversation.ConversationType

	var key string
	switch convType {
	case ConversationTypePersonal, "":
		key = "personal:" + convID + ":" + dateBucket
	case ConversationTypeGroupChat, ConversationTypeChannel:
		key = convType + ":" + convID + ":" + dateBucket
	default:
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(convID)))[:16]
		key = "h:" + hash + ":" + dateBucket
	}

	if len(key) > maxExternalKeyLen {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(convID)))[:16]
		key = "h:" + hash + ":" + dateBucket
	}
	return key
}

func (a *TeamsAdapter) resolveSessionID(ctx context.Context, externalKey, agentName, userID string) (string, error) {
	if a.sessKeys == nil {
		return idgen.New(), nil
	}
	return a.sessKeys.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeTeams,
		ExternalKey: externalKey,
		UserID:      userID,
		AgentID:     &agentName,
	})
}
