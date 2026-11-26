// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func refusesForeignUserScopedName(requester, scopeOwner, filename string) bool {
	if artifact.SameOwner(scopeOwner, requester) {
		return false
	}
	_, userScoped := artifact.ResolveFilename(filename)
	return userScoped
}

func refusesUnreferencedUserScopedName(requester, scopeOwner, filename string, vouched func(filename string) bool) bool {
	return refusesForeignUserScopedName(requester, scopeOwner, filename) && !vouched(filename)
}

func admittedInfos(infos []ArtifactInfoResponse, requester, scopeOwner string) []ArtifactInfoResponse {
	admitted := make([]ArtifactInfoResponse, 0, len(infos))
	for _, info := range infos {
		if !refusesForeignUserScopedName(requester, scopeOwner, info.Filename) {
			admitted = append(admitted, info)
		}
	}
	return admitted
}

type artifactReadScope struct {
	requester     string
	sessionID     string
	storageUserID string
}

func (h *artifactHandler) resolveReadScope(w http.ResponseWriter, r *http.Request) (artifactReadScope, bool) {
	scope, fail := h.readScopeOf(r.Context(), resolveUserID(r, h.core), r.PathValue("id"), r.URL.Query().Get("projectId"))
	if fail != nil {
		fail.write(w)
		return artifactReadScope{}, false
	}
	return scope, true
}

func (h *artifactHandler) readScopeOf(ctx context.Context, requester, sessionID, projectID string) (artifactReadScope, *sessionFailure) {
	projectSession, storageUserID, status, err := h.projectScopeOf(ctx, projectID, requester)
	if status != 0 {
		return artifactReadScope{}, failSession(status, err.Error())
	}
	scope := artifactReadScope{requester: requester, sessionID: projectSession, storageUserID: storageUserID}
	if scope.sessionID == "" {
		scope.sessionID = sessionID
	}
	if scope.storageUserID == "" {
		scope.storageUserID = requester
	}
	return scope, nil
}

func (h *artifactHandler) resolveReadLocator(w http.ResponseWriter, r *http.Request) (artifact.Locator, bool) {
	loc, fail := h.readLocatorOf(r.Context(), resolveUserID(r, h.core), r.PathValue("id"), r.PathValue("filename"), r.URL.Query().Get("projectId"))
	if fail != nil {
		fail.write(w)
		return artifact.Locator{}, false
	}
	return loc, true
}

func (h *artifactHandler) readLocatorOf(ctx context.Context, requester, sessionID, filename, projectID string) (artifact.Locator, *sessionFailure) {
	scope, fail := h.readScopeOf(ctx, requester, sessionID, projectID)
	if fail != nil {
		return artifact.Locator{}, fail
	}
	if refusesForeignUserScopedName(scope.requester, scope.storageUserID, filename) {
		projectID, _ := projectIDOfSession(scope.sessionID)
		h.logger.Warn("artifact read refused: user-scoped file name in another user's project scope", "userID", scope.requester, "projectID", projectID)
		return artifact.Locator{}, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}
	return h.artSvc.Resolve("", scope.storageUserID, scope.sessionID, filename), nil
}
