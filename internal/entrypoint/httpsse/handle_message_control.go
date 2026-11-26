// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const controlResponseRefusedMsg = "control responses are not accepted on this route"

func (h *messageHandler) refuseControlResponseParts(parts []PartDTO, userID, traceID string) *submitRefusal {
	for _, part := range parts {
		signal, isControl := a2a.ControlResponseType(part.Kind, part.Data)
		if !isControl {
			continue
		}
		h.log().Warn("submit message: control response part refused", "responseType", string(signal), "userID", userID, "traceID", traceID)
		return refusal(rejection{status: http.StatusBadRequest, code: codeInvalidParams, msg: controlResponseRefusedMsg, reason: taxonomy.RejectMalformed})
	}
	return nil
}
