// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

func (h *scheduledTaskHandler) chatV2Routes() []apicontract.Route {
	return chatv2.ScheduledArtifactRoutes(chatv2.Deps{DownloadScheduledArtifact: h.downloadChatScheduledArtifact})
}

func (h *scheduledTaskHandler) downloadChatScheduledArtifact(ctx context.Context, req chatv2.ScheduledArtifactRequest) (apicontract.FileResponse, error) {
	if req.Download != "" && req.Download != "true" && req.Download != "false" {
		return apicontract.FileResponse{}, malformedQuery("download", "must be true or false")
	}
	file, fail := h.openScheduledArtifact(ctx, req.SessionID, req.Filename, req.Download == "true")
	if fail != nil {
		return apicontract.FileResponse{}, scheduledArtifactFailure(fail)
	}
	return apicontract.FileResponse{
		ContentType: file.contentType,
		Filename:    file.filename,
		Size:        int64(len(file.content)),
		Inline:      file.inline,
		Body:        io.NopCloser(bytes.NewReader(file.content)),
	}, nil
}

func scheduledArtifactFailure(fail *sessionFailure) error {
	switch fail.status {
	case http.StatusNotFound:
		return chatv2.ArtifactNotFound()
	case http.StatusBadRequest:
		return invalidField("filename", fail.message)
	default:
		return fail.asError()
	}
}
