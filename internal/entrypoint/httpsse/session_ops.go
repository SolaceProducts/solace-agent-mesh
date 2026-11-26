// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	sessionListFailedMsg   = "failed to list sessions"
	sessionSearchFailedMsg = "failed to search sessions"
	sessionCreateFailedMsg = "failed to create session"
	sessionLoadFailedMsg   = "failed to get session"
)

var errSessionSourceInvalid = fmt.Sprintf("invalid source: must be %q or %q", store.SourceFilterChat, store.SourceFilterScheduler)

type sessionListQuery struct {
	userID          string
	page            int
	pageSize        int
	projectID       string
	source          string
	includeAgentIDs []string
	excludeAgentIDs []string
}

type sessionPage struct {
	rows     []*store.Session
	sessions []SessionResponse
	total    int
	hasMore  bool
}

const (
	sessionSourceProblem           = "must be chat or scheduler"
	errSessionProjectFilterInvalid = "must be a UUID or __no_project__"
	errSessionProjectIDInvalid     = "must be a canonical lowercase UUID (8-4-4-4-12 hex digits)"
)

func validSourceFilter(source string) bool {
	return source == "" || source == store.SourceFilterChat || source == store.SourceFilterScheduler
}

func validProjectFilter(projectID string) bool {
	if projectID == "" || projectID == store.UnassignedProjectFilter {
		return true
	}
	return isCanonicalUUID(projectID)
}

func (h *sessionHandler) listSessions(ctx context.Context, q sessionListQuery) (sessionPage, *sessionFailure) {
	filter := store.SessionListFilter{
		NonEmptyOnly: true, ExcludeExternalGateways: true, ProjectID: q.projectID,
		Source:          q.source,
		ExcludeAgentIDs: q.excludeAgentIDs, IncludeAgentIDs: q.includeAgentIDs,
	}
	sessions, hasMore, err := h.sessRepo.ListByUserPage(ctx, q.userID, filter, q.pageSize, (q.page-1)*q.pageSize)
	if err != nil {
		return sessionPage{}, h.loggedFailure(http.StatusInternalServerError, sessionListFailedMsg, "list sessions", err, "userID", q.userID)
	}
	total := h.cachedSessionCount(ctx,
		sessionCountCacheKey(q.userID, q.projectID, q.source, q.excludeAgentIDs, q.includeAgentIDs),
		func() (int, error) { return h.sessRepo.CountByUser(ctx, q.userID, filter) })
	return sessionPage{rows: sessions, sessions: h.sessionResponses(ctx, sessions), total: total, hasMore: hasMore}, nil
}

func (h *sessionHandler) searchSessions(ctx context.Context, q sessionListQuery, text string) (sessionPage, *sessionFailure) {
	sessions, total, err := h.sessRepo.Search(ctx, q.userID, text, store.SessionSearchFilter{
		NonEmptyOnly: true, ProjectID: q.projectID,
		ExcludeAgentIDs: q.excludeAgentIDs, IncludeAgentIDs: q.includeAgentIDs,
	}, q.pageSize, (q.page-1)*q.pageSize)
	if err != nil {
		return sessionPage{}, h.loggedFailure(http.StatusInternalServerError, sessionSearchFailedMsg, "search sessions", err, "userID", q.userID, "projectID", q.projectID)
	}
	return sessionPage{rows: sessions, sessions: h.sessionResponses(ctx, sessions), total: total}, nil
}

func (h *sessionHandler) sessionResponses(ctx context.Context, sessions []*store.Session) []SessionResponse {
	data := make([]SessionResponse, 0, len(sessions))
	for _, s := range sessions {
		data = append(data, sessionToResponse(s))
	}
	h.enrichSessions(ctx, data)
	return data
}

func (h *sessionHandler) sessionResponse(ctx context.Context, s *store.Session) SessionResponse {
	return h.sessionResponses(ctx, []*store.Session{s})[0]
}

type createdSession struct {
	row     *store.Session
	session SessionResponse
	existed bool
}

func (h *sessionHandler) createSession(ctx context.Context, userID, id string, agentID, source *string) (createdSession, *sessionFailure) {
	existing, err := h.sessRepo.GetByID(ctx, id)
	if err != nil {
		return createdSession{}, h.createFailure("create session: lookup existing", id, err)
	}
	if existing != nil {
		return h.existingSession(ctx, existing, userID)
	}
	sess := &store.Session{ID: id, UserID: userID, AgentID: agentID}
	if source != nil && *source == ephemeralTestSessionSource {
		sess.Source = source
	}
	if err := h.sessRepo.Create(ctx, sess); err != nil {
		if dbutil.IsUniqueViolation(err) {
			return h.resolveCreateConflict(ctx, id, userID)
		}
		return createdSession{}, h.createFailure("create session", id, err)
	}
	return createdSession{row: sess, session: h.sessionResponse(ctx, sess)}, nil
}

func (h *sessionHandler) existingSession(ctx context.Context, existing *store.Session, userID string) (createdSession, *sessionFailure) {
	if existing.UserID != userID {
		return createdSession{}, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	return createdSession{row: existing, session: h.sessionResponse(ctx, existing), existed: true}, nil
}

func (h *sessionHandler) resolveCreateConflict(ctx context.Context, sessionID, userID string) (createdSession, *sessionFailure) {
	existing, err := h.sessRepo.GetByID(ctx, sessionID)
	if err != nil {
		return createdSession{}, h.createFailure("create session: lookup after conflict", sessionID, err)
	}
	if existing == nil {
		h.logger.Warn("create session: id held by a row the lookup cannot see", "sessionID", sessionID)
		return createdSession{}, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	return h.existingSession(ctx, existing, userID)
}

func (h *sessionHandler) createFailure(logMsg, sessionID string, err error) *sessionFailure {
	return h.loggedFailure(http.StatusInternalServerError, sessionCreateFailedMsg, logMsg, err, "sessionID", sessionID)
}

func (h *sessionHandler) getSession(ctx context.Context, userID, sessionID string) (SessionResponse, *sessionFailure) {
	session := ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID)
	if session == nil {
		return SessionResponse{}, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	return h.sessionResponse(ctx, session), nil
}

func (h *sessionHandler) updateSession(ctx context.Context, userID, sessionID, name, agentID string) (*store.Session, *sessionFailure) {
	if ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID) == nil {
		return nil, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	if name != "" {
		if err := h.sessRepo.Rename(ctx, sessionID, name); err != nil {
			return nil, failSession(http.StatusNotFound, sessionNotFoundMsg)
		}
	}
	if agentID != "" {
		if err := h.sessRepo.SetAgentID(ctx, sessionID, agentID); err != nil {
			return nil, failSession(http.StatusNotFound, sessionNotFoundMsg)
		}
	}
	session, err := h.sessRepo.GetByID(ctx, sessionID)
	if err != nil || session == nil {
		return nil, failSession(http.StatusInternalServerError, sessionLoadFailedMsg)
	}
	return session, nil
}

func (h *sessionHandler) deleteSession(ctx context.Context, userID, sessionID string) *sessionFailure {
	if ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID) == nil {
		return failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	cancelsOwed, err := h.sessRepo.SoftDelete(ctx, sessionID, userID)
	if err != nil {
		return failSession(http.StatusNotFound, "session not found or already deleted")
	}
	if cancelsOwed > 0 {
		h.core.RunTaskSweepNow()
	}
	return nil
}

func (h *sessionHandler) markSessionViewed(ctx context.Context, userID, sessionID string) (viewedAt int64, fail *sessionFailure) {
	if isUnnamedSession(sessionID) {
		return 0, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	viewedAt = time.Now().UnixMilli()
	ok, err := h.sessRepo.MarkViewed(ctx, sessionID, userID, viewedAt)
	if err != nil {
		return 0, h.loggedFailure(http.StatusInternalServerError, "failed to mark session viewed", "mark session viewed", err, "sessionID", sessionID)
	}
	if !ok {
		return 0, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	return viewedAt, nil
}

func (h *sessionHandler) assignSessionProject(ctx context.Context, userID, sessionID, projectID string) (*store.Session, *sessionFailure) {
	if ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID) == nil {
		return nil, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	if projectID != "" && !isCanonicalUUID(projectID) {
		return nil, failSession(http.StatusBadRequest, "invalid projectId: must be a canonical lowercase UUID (8-4-4-4-12 hex digits)")
	}
	var ownerID string
	if projectID != "" {
		owner, allowed, err := resolveProjectOwner(ctx, h.projectRepo, h.core.ProjectSharing(), projectID, userID)
		if err != nil {
			return nil, h.loggedFailure(http.StatusInternalServerError, errInternal, "resolve project owner", err, "projectID", projectID)
		}
		if !allowed {
			return nil, failSession(http.StatusNotFound, projectNotFoundMsg)
		}
		ownerID = owner
	}
	if err := h.sessRepo.AssignProject(ctx, sessionID, projectID); err != nil {
		return nil, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	if projectID != "" {
		if artSvc := h.core.Services().Artifacts(); artSvc != nil {
			if _, _, err := copyProjectArtifactsToSession(ctx, artSvc,
				projectID, ownerID, userID, sessionID,
				true, true,
				h.core.Logger()); err != nil {
				return nil, h.loggedFailure(http.StatusInternalServerError, errInternal, "copy project artifacts on assign", err, "projectID", projectID, "sessionID", sessionID)
			}
		}
	}
	session, err := h.sessRepo.GetByID(ctx, sessionID)
	if err != nil {
		return nil, failSession(http.StatusInternalServerError, sessionLoadFailedMsg)
	}
	return session, nil
}

func isCanonicalUUID(id string) bool { return apicontract.IsCanonicalUUID(id) }

func (p sessionPage) consistentTotal(page, pageSize int) int {
	seen := (page-1)*pageSize + len(p.rows)
	switch {
	case p.hasMore && p.total <= seen:
		return seen + 1
	case !p.hasMore && len(p.rows) > 0:
		return seen
	default:
		return p.total
	}
}
