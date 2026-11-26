// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (h *messageHandler) authorizeArtifactParts(ctx context.Context, msg a2a.Message, userID, traceID string) *submitRefusal {
	for _, part := range msg.Parts {
		if part.File == nil {
			continue
		}
		loc, _, isLocation := artifact.ParseURI(part.File.URI)
		if !isLocation {
			continue
		}
		allowed, err := mayReferenceArtifact(ctx, h.projectRepo, h.core.ProjectSharing(), userID, loc)
		if err != nil {
			return h.artifactCheckFailed(ctx, userID, traceID, err)
		}
		if !allowed {
			_, projectScoped := projectIDOfSession(loc.SessionID)
			h.log().Warn("submit message: artifact reference belongs to another user", "userID", userID, "projectScoped", projectScoped, "traceID", traceID)
			return refusal(rejection{status: http.StatusNotFound, code: codeInvalidParams, msg: artifactNotFoundMsg, reason: taxonomy.RejectForbidden})
		}
	}
	return nil
}

func (h *messageHandler) artifactCheckFailed(ctx context.Context, userID, traceID string, err error) *submitRefusal {
	if ctx.Err() != nil {
		h.log().Debug("submit message: client gone during artifact reference check", "error", err, "userID", userID, "traceID", traceID)
		return abandoned()
	}
	h.log().Error("submit message: check access to artifact reference", "error", err, "userID", userID, "traceID", traceID)
	return refusal(rejection{status: http.StatusInternalServerError, code: codeInternalError, msg: submitFailedMsg, reason: taxonomy.RejectInternal})
}
