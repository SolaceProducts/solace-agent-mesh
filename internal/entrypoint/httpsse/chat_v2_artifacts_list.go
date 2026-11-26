// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func (h *artifactHandler) listChatSessionArtifacts(ctx context.Context, req chatv2.ListSessionArtifactsRequest) (apicontract.List[chatv2.ChatArtifact], error) {
	includeWorking, ok := parseArtifactVisibility(req.Visibility)
	if !ok {
		return apicontract.List[chatv2.ChatArtifact]{}, invalidField("visibility", "must be one of: visible, all")
	}
	scope, fail := h.readScopeOf(ctx, h.callerID(ctx), req.ID, req.ProjectID)
	if fail != nil {
		return apicontract.List[chatv2.ChatArtifact]{}, artifactFailure(fail)
	}
	infos, gone := h.listScopeFiles(ctx, scope, includeWorking)
	if gone {
		return apicontract.List[chatv2.ChatArtifact]{}, nil
	}
	items := make([]chatv2.ChatArtifact, len(infos))
	for i, info := range infos {
		items[i] = chatArtifactOf(info)
		if req.ProjectID != "" {
			items[i].ProjectID = &req.ProjectID
		}
	}
	return apicontract.PageOf(items, req.Page), nil
}

func (h *artifactHandler) listAllChatArtifacts(ctx context.Context, req chatv2.ListAllArtifactsRequest) (apicontract.List[chatv2.ChatArtifact], error) {
	if !validArtifactProjectFilter(req.ProjectID) {
		return apicontract.List[chatv2.ChatArtifact]{}, malformedQuery("projectId", "must be a UUID or __no_project__")
	}
	items, total, fail := h.listAllFiles(ctx, h.callerID(ctx), req.Page, req.Search, req.ProjectID)
	if fail != nil {
		return apicontract.List[chatv2.ChatArtifact]{}, artifactFailure(fail)
	}
	out := make([]chatv2.ChatArtifact, len(items))
	for i, item := range items {
		out[i] = chatArtifactOfAll(item)
	}
	return apicontract.List[chatv2.ChatArtifact]{Items: out, Total: total}, nil
}

func (h *artifactHandler) listChatArtifactVersions(ctx context.Context, req chatv2.ListArtifactVersionsRequest) (apicontract.List[chatv2.ArtifactVersion], error) {
	loc, fail := h.readLocatorOf(ctx, h.callerID(ctx), req.SessionID, req.Filename, req.ProjectID)
	if fail != nil {
		return apicontract.List[chatv2.ArtifactVersion]{}, artifactFailure(fail)
	}
	versions, fail := h.versionsOf(ctx, loc)
	if fail != nil {
		return apicontract.List[chatv2.ArtifactVersion]{}, fail.asError()
	}
	items := make([]chatv2.ArtifactVersion, len(versions))
	for i, v := range versions {
		items[i] = chatv2.ArtifactVersion{ID: strconv.Itoa(v), Type: "artifactVersion", Version: v}
	}
	return apicontract.PageOf(items, req.Page), nil
}

func validArtifactProjectFilter(projectID string) bool {
	return projectID == "" || projectID == store.UnassignedProjectFilter || isCanonicalUUID(projectID)
}

func chatArtifactOf(info ArtifactInfoResponse) chatv2.ChatArtifact {
	return chatv2.ChatArtifact{
		ID:           info.Filename,
		Type:         "artifact",
		MimeType:     info.MIMEType,
		Size:         info.Size,
		UpdatedTime:  lastModifiedInstant(info.LastModified),
		Description:  info.Description,
		Schema:       info.Schema,
		URI:          info.URI,
		Version:      info.Version,
		VersionCount: info.VersionCount,
		Source:       info.Source,
		Tags:         nonNilTags(info.Tags),
	}
}

func chatArtifactOfAll(item ArtifactAllItemResponse) chatv2.ChatArtifact {
	out := chatArtifactOf(item.ArtifactInfoResponse)
	if !strings.HasPrefix(item.SessionID, projectSessionPrefix) {
		out.SessionID = apicontract.NullIfEmpty(&item.SessionID)
	}
	out.ProjectID = apicontract.NullIfEmpty(&item.ProjectID)
	out.ProjectName = apicontract.NullIfEmpty(&item.ProjectName)
	out.AgentID = apicontract.NullIfEmpty(&item.AgentID)
	return out
}

func lastModifiedInstant(lastModified *string) apicontract.Instant {
	if lastModified == nil {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, *lastModified)
	if err != nil {
		return 0
	}
	return apicontract.Instant(t.UnixMilli())
}

func nonNilTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
