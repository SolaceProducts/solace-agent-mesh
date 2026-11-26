// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

const latestVersion = "latest"

func (h *artifactHandler) chatV2Routes() []apicontract.Route {
	return chatv2.ArtifactRoutes(chatv2.Deps{
		DownloadArtifact:        h.downloadChatArtifact,
		DownloadArtifactVersion: h.downloadChatArtifactVersion,
		DownloadArtifactByURI:   h.downloadChatArtifactByURI,
		ListAllArtifacts:        h.listAllChatArtifacts,
		ListSessionArtifacts:    h.listChatSessionArtifacts,
		ListArtifactVersions:    h.listChatArtifactVersions,
		UploadArtifact:          h.uploadChatArtifact,
		DeleteArtifact:          h.deleteChatArtifact,
		CopyArtifacts:           h.copyChatArtifacts,
	})
}

func (h *artifactHandler) callerID(ctx context.Context) string {
	return resolveUserIDCtx(ctx, h.core)
}

func artifactFailure(fail *sessionFailure) error {
	if fail.status == http.StatusNotFound || fail.status == http.StatusForbidden {
		return chatv2.ArtifactNotFound()
	}
	return fail.asError()
}

func previewLimit(text string) (int, error) {
	if text == "" {
		return 0, nil
	}
	maxBytes, err := strconv.Atoi(text)
	if err != nil {
		return 0, malformedQuery("maxBytes", "must be an integer")
	}
	if maxBytes < 1 || maxBytes > maxBytesLimit {
		return 0, invalidField("maxBytes", "must be between 1 and "+strconv.Itoa(maxBytesLimit))
	}
	return maxBytes, nil
}

func (h *artifactHandler) fileOf(ctx context.Context, loc artifact.Locator, version *int, maxBytes int) (apicontract.FileResponse, error) {
	body, fail := h.openArtifactBody(ctx, h.artSvc.Inner, loc, version, maxBytes)
	if fail != nil {
		return apicontract.FileResponse{}, artifactFailure(fail)
	}
	if body == nil {
		return apicontract.FileResponse{}, chatv2.ArtifactNotFound()
	}
	return apicontract.FileResponse{
		ContentType: body.mimeType, Filename: loc.Filename, Size: body.size,
		Headers: body.headers, Body: body.reader,
	}, nil
}

func (h *artifactHandler) downloadChatArtifact(ctx context.Context, req chatv2.DownloadArtifactRequest) (apicontract.FileResponse, error) {
	return h.downloadChatArtifactAt(ctx, req, nil)
}

func (h *artifactHandler) downloadChatArtifactVersion(ctx context.Context, req chatv2.DownloadArtifactVersionRequest) (apicontract.FileResponse, error) {
	if req.Version == latestVersion {
		return h.downloadChatArtifactAt(ctx, req.DownloadArtifactRequest, nil)
	}
	version, err := strconv.Atoi(req.Version)
	if err != nil {
		return apicontract.FileResponse{}, malformedPath("version", "must be a version number or latest")
	}
	if version < 0 {
		return apicontract.FileResponse{}, invalidField("version", "must not be negative")
	}
	return h.downloadChatArtifactAt(ctx, req.DownloadArtifactRequest, &version)
}

func (h *artifactHandler) downloadChatArtifactAt(ctx context.Context, req chatv2.DownloadArtifactRequest, version *int) (apicontract.FileResponse, error) {
	maxBytes, err := previewLimit(req.MaxBytes)
	if err != nil {
		return apicontract.FileResponse{}, err
	}
	loc, fail := h.readLocatorOf(ctx, h.callerID(ctx), req.SessionID, req.Filename, req.ProjectID)
	if fail != nil {
		return apicontract.FileResponse{}, artifactFailure(fail)
	}
	return h.fileOf(ctx, loc, version, maxBytes)
}

func (h *artifactHandler) downloadChatArtifactByURI(ctx context.Context, req chatv2.DownloadArtifactByURIRequest) (apicontract.FileResponse, error) {
	maxBytes, err := previewLimit(req.MaxBytes)
	if err != nil {
		return apicontract.FileResponse{}, err
	}
	loc, version, fail := h.byURILocator(ctx, h.callerID(ctx), req.URI)
	if fail != nil {
		return apicontract.FileResponse{}, artifactFailure(fail)
	}
	return h.fileOf(ctx, loc, &version, maxBytes)
}
