// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"cmp"
	"context"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

const sessionIDMustBeUUID = "must be a UUID"

func (h *artifactHandler) uploadChatArtifact(ctx context.Context, req chatv2.UploadArtifactRequest) (chatv2.ChatArtifact, error) {
	filename := cmp.Or(req.Filename, req.File.Name)
	if problem := uploadNameProblem(filename); problem != "" {
		return chatv2.ChatArtifact{}, invalidField("filename", problem)
	}
	if req.SessionID != "" && !apicontract.IsCanonicalUUID(req.SessionID) {
		return chatv2.ChatArtifact{}, invalidField("sessionId", sessionIDMustBeUUID)
	}

	stored, fail := h.storeUpload(ctx, h.callerID(ctx), artifactUpload{
		SessionID:    req.SessionID,
		Filename:     filename,
		ContentType:  req.File.ContentType,
		Content:      req.File.Data,
		MetadataJSON: req.Metadata,
	})
	if fail != nil {
		return chatv2.ChatArtifact{}, fail.asError()
	}

	return h.uploadedArtifactOf(ctx, stored), nil
}

func (h *artifactHandler) uploadedArtifactOf(ctx context.Context, stored storedArtifact) chatv2.ChatArtifact {
	info := h.getArtifactInfo(ctx, stored.Locator)
	out := chatArtifactOf(info)
	sessionID := stored.Locator.SessionID
	out.SessionID = &sessionID
	out.Size = int64(stored.Size)
	out.Version = &stored.Version
	out.UpdatedTime = apicontract.Instant(time.Now().UnixMilli())
	if out.MimeType == nil {
		out.MimeType = &stored.MimeType
	}
	if len(out.Tags) == 0 {
		out.Tags = []string{a2a.ArtifactTagUserUploaded}
	}
	return out
}

func (h *artifactHandler) deleteChatArtifact(ctx context.Context, req chatv2.ArtifactRequest) (struct{}, error) {
	if fail := h.removeArtifact(ctx, h.callerID(ctx), req.SessionID, req.Filename); fail != nil {
		if fail.status == http.StatusForbidden {
			return struct{}{}, chatv2.ArtifactNotFound()
		}
		return struct{}{}, fail.asError()
	}
	return struct{}{}, nil
}

func (h *artifactHandler) copyChatArtifacts(ctx context.Context, req chatv2.CopyArtifactsRequest) (chatv2.ArtifactCopyResult, error) {
	if !apicontract.IsCanonicalUUID(req.SourceSessionID) {
		return chatv2.ArtifactCopyResult{}, invalidField("sourceSessionId", sessionIDMustBeUUID)
	}
	copied, failed, fail := h.copySessionArtifactsBetween(ctx, h.callerID(ctx), req.SourceSessionID, req.ID, req.Filenames)
	if fail != nil {
		if fail.status == http.StatusNotFound || fail.status == http.StatusForbidden {
			return chatv2.ArtifactCopyResult{}, chatv2.SessionNotFound()
		}
		return chatv2.ArtifactCopyResult{}, fail.asError()
	}
	return chatv2.ArtifactCopyResult{ID: req.ID, Type: "artifactCopy", Copied: nonNilList(copied), Failed: nonNilList(failed)}, nil
}
