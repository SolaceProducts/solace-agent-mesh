// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

func (h *messageHandler) chatV2Routes() []apicontract.Route {
	return chatv2.MessageRoutes(chatv2.Deps{Send: h.sendChatMessage})
}

func (h *messageHandler) sendChatMessage(w http.ResponseWriter, r *http.Request) {
	h.handleMessage(w, r)
}
