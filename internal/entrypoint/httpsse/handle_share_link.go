// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type shareLinkHandler struct {
	core         *entrypoint.GatewayCore
	linkRepo     *store.SharedLinkRepo
	linkUserRepo *store.SharedLinkUserRepo
	artifactRepo *store.SharedArtifactRepo
	sessRepo     *store.SessionRepo
	chatRepo     *store.ChatTaskRepo
	taskRepo     *store.TaskRepo
	eventRepo    *store.TaskEventRepo
	artSvc       *artifact.ScopedService
	userNotifier entrypoint.UserNotifier
	logger       *slog.Logger
}

type SharedLinkResponse struct {
	ID                    string   `json:"id"`
	SessionID             string   `json:"sessionId"`
	Title                 string   `json:"title"`
	IsPublic              bool     `json:"isPublic"`
	RequireAuthentication bool     `json:"requireAuthentication"`
	AllowedDomains        []string `json:"allowedDomains"`
	AccessType            string   `json:"accessType"`
	CreatedTime           string   `json:"createdAt"`
	SnapshotTime          string   `json:"snapshotTime"`
	ShareURL              string   `json:"shareUrl"`
}

type ShareLinkItem struct {
	ID                    string   `json:"id"`
	SessionID             string   `json:"sessionId"`
	Title                 string   `json:"title"`
	IsPublic              bool     `json:"isPublic"`
	RequireAuthentication bool     `json:"requireAuthentication"`
	AllowedDomains        []string `json:"allowedDomains"`
	AccessType            string   `json:"accessType"`
	CreatedTime           string   `json:"createdAt"`
	MessageCount          int      `json:"messageCount"`
}

type CreateShareLinkRequest struct {
	RequireAuthentication bool     `json:"requireAuthentication"`
	AllowedDomains        []string `json:"allowedDomains,omitempty"`
}

type UpdateShareLinkRequest struct {
	RequireAuthentication *bool     `json:"requireAuthentication"`
	AllowedDomains        *[]string `json:"allowedDomains"`
}

type ForkSharedChatResponse struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	Message     string `json:"message"`
}

type AddShareUserRequest struct {
	UserEmail   string `json:"userEmail"`
	AccessLevel string `json:"accessLevel"`
}

type BatchAddShareUsersRequest struct {
	Shares []AddShareUserRequest `json:"shares"`
}

type SharedLinkUserInfo struct {
	UserEmail           string  `json:"userEmail"`
	AccessLevel         string  `json:"accessLevel"`
	AddedAt             string  `json:"addedAt"`
	OriginalAccessLevel *string `json:"originalAccessLevel"`
	OriginalAddedAt     *string `json:"originalAddedAt"`
}

type ShareUsersResponse struct {
	ID         string               `json:"id"`
	OwnerEmail string               `json:"ownerEmail"`
	Users      []SharedLinkUserInfo `json:"users"`
}

type BatchAddShareUsersResponse struct {
	AddedCount int                  `json:"addedCount"`
	Users      []SharedLinkUserInfo `json:"users"`
}

type BatchDeleteShareUsersRequest struct {
	UserEmails []string `json:"userEmails"`
}

type BatchDeleteShareUsersResponse struct {
	DeletedCount int `json:"deletedCount"`
}

type SharedWithMeItem struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	OwnerEmail  string  `json:"ownerEmail"`
	AccessLevel string  `json:"accessLevel"`
	SharedAt    string  `json:"sharedAt"`
	ShareURL    string  `json:"shareUrl"`
	SessionID   *string `json:"sessionId"`
	ViewedAt    *string `json:"viewedAt"`
}

type UpdateSnapshotRequest struct {
	UserEmail *string `json:"userEmail,omitempty"`
}

type UpdateSnapshotResponse struct {
	SnapshotTime string `json:"snapshotTime"`
}

type MarkShareViewedResponse struct {
	ViewedAt string `json:"viewedAt"`
}

type SuccessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type SharedArtifactInfo struct {
	Filename     string  `json:"filename"`
	MIMEType     string  `json:"mimeType"`
	Size         int64   `json:"size"`
	LastModified *string `json:"lastModified"`
	Version      *int    `json:"version"`
	VersionCount *int    `json:"versionCount"`
	Description  *string `json:"description"`
	Source       *string `json:"source"`
}

type SharedSessionView struct {
	ID           string               `json:"id"`
	Title        string               `json:"title"`
	CreatedTime  string               `json:"createdAt"`
	AccessType   string               `json:"accessType"`
	Tasks        []map[string]any     `json:"tasks"`
	Artifacts    []SharedArtifactInfo `json:"artifacts"`
	TaskEvents   map[string]any       `json:"taskEvents"`
	IsOwner      bool                 `json:"isOwner"`
	SessionID    *string              `json:"sessionId"`
	SnapshotTime *string              `json:"snapshotTime"`
}

const (
	errShareLinkNotFound = "Share link not found"
	errInternal          = "internal error"
	errAccessDenied      = "Access denied"

	headerWWWAuthenticate = "WWW-Authenticate"
	wwwAuthenticateBearer = "Bearer"

	errShareUserNotFound = "Share user not found"

	notifySourceChatShare = "chat_share"

	notifyShareActionLabel = "View shared chat"

	accessLevelViewer = "resource_viewer"
	accessLevelEditor = "resource_editor"

	sharedWithMeDefaultLimit = 100

	notifyPublishTimeout = 5 * time.Second
)

func (h *shareLinkHandler) resolveShareForRead(w http.ResponseWriter, r *http.Request, shareID, anonymousMessage string) (link *store.SharedLink, isOwner bool, ok bool) {
	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil {
		h.logger.Error("get share link", "error", err, "shareID", shareID)
		writeError(w, http.StatusInternalServerError, errInternal)
		return nil, false, false
	}
	if link == nil {
		writeError(w, http.StatusNotFound, errShareLinkNotFound)
		return nil, false, false
	}

	userID, userEmail := auth.UserIDFromContext(r.Context())
	isOwner = userID != "" && link.UserID == userID
	if isOwner {
		return link, true, true
	}
	if !h.enforceShareAccess(w, r, link, userID, userEmail, anonymousMessage) {
		return nil, false, false
	}
	return link, false, true
}

func (h *shareLinkHandler) enforceShareAccess(w http.ResponseWriter, r *http.Request, link *store.SharedLink, userID, userEmail, anonymousMessage string) bool {
	if userID == "" {
		w.Header().Set(headerWWWAuthenticate, wwwAuthenticateBearer)
		writeError(w, http.StatusUnauthorized, anonymousMessage)
		return false
	}
	ok, reason := h.checkAccess(r.Context(), link, userID, userEmail)
	if ok {
		return true
	}
	msg := errAccessDenied
	switch reason {
	case "domain_mismatch":
		msg = "Access restricted to authorized domains"
	case "invalid_email":
		msg = "Invalid email format"
	case "not_shared_with_user":
		msg = errAccessDenied
	}
	writeError(w, http.StatusForbidden, msg)
	return false
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func buildShareURL(shareID string, r *http.Request) string {
	return fmt.Sprintf("%s/#/share/%s", strings.TrimRight(baseURL(r), "/"), shareID)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func recipientKeyFromContext(ctx context.Context) string {
	userID, email := auth.UserIDFromContext(ctx)
	if key := normalizeEmail(email); key != "" {
		return key
	}
	return normalizeEmail(userID)
}

func titleOrUntitled(t *string) string {
	if t == nil || strings.TrimSpace(*t) == "" {
		return "Untitled"
	}
	return *t
}

func formatAllowedDomains(domains []string) *string {
	cleaned := make([]string, 0, len(domains))
	for _, d := range domains {
		if v := strings.TrimSpace(d); v != "" {
			cleaned = append(cleaned, v)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	joined := strings.Join(cleaned, ",")
	return &joined
}

func validateDomainsList(domains []string) error {
	for _, d := range domains {
		v := strings.TrimSpace(d)
		if v == "" {
			continue
		}
		if strings.Contains(v, " ") || !strings.Contains(v, ".") {
			return fmt.Errorf("invalid domain: %q", d)
		}
	}
	return nil
}

func linkSnapshotTime(s *store.SharedLink) int64 {
	if s.SnapshotTime == 0 {
		return s.CreatedTime
	}
	return s.SnapshotTime
}

func sharedLinkToResponse(s *store.SharedLink, hasSharedUsers bool, r *http.Request) SharedLinkResponse {
	return SharedLinkResponse{
		ID:                    s.ShareID,
		SessionID:             s.SessionID,
		Title:                 titleOrUntitled(s.Title),
		IsPublic:              s.IsPublic,
		RequireAuthentication: s.RequireAuthentication,
		AllowedDomains:        s.GetAllowedDomainsList(),
		AccessType:            s.GetAccessType(hasSharedUsers),
		CreatedTime:           epochMsToISO8601(s.CreatedTime),
		SnapshotTime:          epochMsToISO8601(linkSnapshotTime(s)),
		ShareURL:              buildShareURL(s.ShareID, r),
	}
}

func sharedLinkUserToInfo(u *store.SharedLinkUser) SharedLinkUserInfo {
	return SharedLinkUserInfo{
		UserEmail:           u.UserEmail,
		AccessLevel:         u.AccessLevel,
		AddedAt:             epochMsToISO8601(u.AddedAt),
		OriginalAccessLevel: u.OriginalAccessLevel,
		OriginalAddedAt:     optionalISO8601(u.OriginalAddedAt),
	}
}

func emailToDomain(email string) string {
	if at := strings.LastIndex(email, "@"); at >= 0 {
		return strings.ToLower(email[at+1:])
	}
	return ""
}

const shareAPIPrefix = "/api/v1/share/"

func sharePathSegments(path string) []string {
	rest := strings.TrimPrefix(path, shareAPIPrefix)
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

func (h *shareLinkHandler) handleShareGetDispatch(w http.ResponseWriter, r *http.Request) {
	segs := sharePathSegments(r.URL.Path)

	switch {
	case len(segs) == 0:
		h.handleList(w, r)

	case len(segs) == 1 && segs[0] == "sharedWithMe":
		h.handleListSharedWithMe(w, r)

	case len(segs) == 2 && segs[0] == "link":
		r.SetPathValue("id", segs[1])
		h.handleGetBySession(w, r)

	case len(segs) == 2 && segs[1] == "users":
		r.SetPathValue("id", segs[0])
		h.handleListUsers(w, r)

	case len(segs) >= 3 && segs[1] == "artifacts":
		r.SetPathValue("id", segs[0])
		r.SetPathValue("filename", strings.Join(segs[2:], "/"))
		h.handleSharedArtifactContent(w, r)

	case len(segs) == 1:
		r.SetPathValue("id", segs[0])
		h.handleView(w, r)

	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *shareLinkHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if err := validatePathUUID(sessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	userID, _ := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sess, err := h.sessRepo.GetByID(r.Context(), sessionID)
	if err != nil {
		h.logger.Error("load session for share", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if sess == nil || sess.UserID != userID {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Session %s not found or access denied", sessionID))
		return
	}

	existing, err := h.linkRepo.GetBySessionAndUser(r.Context(), sessionID, userID)
	if err != nil {
		h.logger.Error("check existing share", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if existing != nil {
		hasUsers, _ := h.linkUserRepo.HasAny(r.Context(), existing.ShareID)
		writeJSON(w, http.StatusOK, sharedLinkToResponse(existing, hasUsers, r))
		return
	}

	var req CreateShareLinkRequest
	if r.Body != nil {
		if err := decodeJSON(r, &req); err != nil && err.Error() != "EOF" {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	req.RequireAuthentication = true

	if len(req.AllowedDomains) > 0 {
		if err := validateDomainsList(req.AllowedDomains); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid domains: %s", err.Error()))
			return
		}
	}

	title := "Untitled Session"
	if sess.Name != nil && strings.TrimSpace(*sess.Name) != "" {
		title = *sess.Name
	}

	link := &store.SharedLink{
		ShareID:               generateShareID(),
		SessionID:             sessionID,
		UserID:                userID,
		Title:                 &title,
		IsPublic:              true,
		RequireAuthentication: true,
		AllowedDomains:        formatAllowedDomains(req.AllowedDomains),
	}

	if err := h.linkRepo.Create(r.Context(), link); err != nil {
		h.logger.Error("create share link", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	writeJSON(w, http.StatusCreated, sharedLinkToResponse(link, false, r))
}

func (h *shareLinkHandler) handleGetBySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if err := validatePathUUID(sessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID, _ := auth.UserIDFromContext(r.Context())

	link, err := h.linkRepo.GetBySessionAndUser(r.Context(), sessionID, userID)
	if err != nil {
		h.logger.Error("get share by session", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if link == nil {
		writeError(w, http.StatusNotFound, "No share link found for this session")
		return
	}

	hasUsers, _ := h.linkUserRepo.HasAny(r.Context(), link.ShareID)
	writeJSON(w, http.StatusOK, sharedLinkToResponse(link, hasUsers, r))
}

func (h *shareLinkHandler) handleList(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserIDFromContext(r.Context())

	pageNumber := queryInt(r, "pageNumber", 1)
	pageSize := queryInt(r, "pageSize", 20)
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (pageNumber - 1) * pageSize

	links, err := h.linkRepo.ListByUser(r.Context(), userID, pageSize, offset)
	if err != nil {
		h.logger.Error("list shares", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	total := offset + len(links)

	items := make([]ShareLinkItem, 0, len(links))
	for _, l := range links {
		hasUsers, _ := h.linkUserRepo.HasAny(r.Context(), l.ShareID)
		msgCount := 0
		if tasks, tErr := h.chatRepo.ListBySession(r.Context(), l.SessionID); tErr == nil {
			for _, t := range tasks {
				msgCount += countMessageBubbles(t.MessageBubbles)
			}
		}
		items = append(items, ShareLinkItem{
			ID:                    l.ShareID,
			SessionID:             l.SessionID,
			Title:                 titleOrUntitled(l.Title),
			IsPublic:              l.IsPublic,
			RequireAuthentication: l.RequireAuthentication,
			AllowedDomains:        l.GetAllowedDomainsList(),
			AccessType:            l.GetAccessType(hasUsers),
			CreatedTime:           epochMsToISO8601(l.CreatedTime),
			MessageCount:          msgCount,
		})
	}

	writeJSON(w, http.StatusOK, PaginatedResponse[ShareLinkItem]{
		Data: items,
		Meta: PaginationMeta{Pagination: newPaginationInfo(pageNumber, pageSize, total)},
	})
}

func countMessageBubbles(bubbles string) int {
	if bubbles == "" {
		return 0
	}
	var arr []any
	if err := json.Unmarshal([]byte(bubbles), &arr); err != nil {
		return 0
	}
	return len(arr)
}

func (h *shareLinkHandler) handleListSharedWithMe(w http.ResponseWriter, r *http.Request) {
	email := recipientKeyFromContext(r.Context())
	if email == "" {
		writeJSON(w, http.StatusOK, unpagedResponse([]SharedWithMeItem{}))
		return
	}

	if hasPaginationParams(r) {
		page, pageSize, pageErr := parsePagination(r)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, pageErr.Error())
			return
		}
		items, err := h.listSharedWithMeItems(r.Context(), email, pageSize, (page-1)*pageSize, r)
		if err != nil {
			h.logger.Error("list shared-with-me page", "error", err)
			writeError(w, http.StatusInternalServerError, errInternal)
			return
		}
		total, err := h.linkRepo.CountSharedWithUser(r.Context(), email)
		if err != nil {
			h.logger.Error("count shared-with-me", "error", err)
			writeError(w, http.StatusInternalServerError, errInternal)
			return
		}
		writeJSON(w, http.StatusOK, PaginatedResponse[SharedWithMeItem]{
			Data: nonNilList(items),
			Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
		})
		return
	}

	items, err := h.listSharedWithMeItems(r.Context(), email, sharedWithMeDefaultLimit, 0, r)
	if err != nil {
		h.logger.Error("list shared-with-me", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	total, err := h.linkRepo.CountSharedWithUser(r.Context(), email)
	if err != nil {
		h.logger.Error("count shared-with-me", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	writeJSON(w, http.StatusOK, PaginatedResponse[SharedWithMeItem]{
		Data: nonNilList(items),
		Meta: PaginationMeta{Pagination: newPaginationInfo(1, sharedWithMeDefaultLimit, total)},
	})
}

func (h *shareLinkHandler) listSharedWithMeItems(ctx context.Context, userEmail string, limit, offset int, r *http.Request) ([]SharedWithMeItem, error) {
	rows, err := h.linkRepo.ListSharedWithUser(ctx, userEmail, limit, offset)
	if err != nil {
		return nil, err
	}
	items := make([]SharedWithMeItem, 0, len(rows))
	for _, row := range rows {
		title := titleOrUntitled(row.Title)
		var sessionID *string
		if row.AccessLevel == accessLevelEditor {
			sid := row.SessionID
			sessionID = &sid
			if sess, sErr := h.sessRepo.GetByID(ctx, row.SessionID); sErr == nil && sess != nil && sess.Name != nil && *sess.Name != "" {
				title = *sess.Name
			}
		}

		items = append(items, SharedWithMeItem{
			ID:          row.ShareID,
			Title:       title,
			OwnerEmail:  row.OwnerUserID,
			AccessLevel: row.AccessLevel,
			SharedAt:    epochMsToISO8601(row.AddedAt),
			ShareURL:    buildShareURL(row.ShareID, r),
			SessionID:   sessionID,
			ViewedAt:    optionalISO8601(row.ViewedAt),
		})
	}
	return items, nil
}

func (h *shareLinkHandler) handleView(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	link, isOwner, ok := h.resolveShareForRead(w, r, shareID, "Authentication required to view this shared session")
	if !ok {
		return
	}
	userID, userEmail := auth.UserIDFromContext(r.Context())

	var snapshotTime *int64
	var snapshotTimeISO *string
	isEditor := false
	if !isOwner && userEmail != "" {
		user, _ := h.linkUserRepo.GetByShareAndEmail(r.Context(), shareID, userEmail)
		if user != nil {
			switch user.AccessLevel {
			case accessLevelViewer:
				t := user.AddedAt
				snapshotTime = &t
				iso := epochMsToISO8601(t)
				snapshotTimeISO = &iso
			case accessLevelEditor:
				isEditor = true
			}
		}
	}
	if !isOwner && !isEditor && snapshotTime == nil {
		t := linkSnapshotTime(link)
		snapshotTime = &t
		iso := epochMsToISO8601(t)
		snapshotTimeISO = &iso
	}

	tasks, err := h.chatRepo.ListBySession(r.Context(), link.SessionID)
	if err != nil {
		h.logger.Error("list chat tasks for share", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	filtered := make([]*store.ChatTask, 0, len(tasks))
	for _, t := range tasks {
		if !isOwner && !isEditor && t.UserID != link.UserID {
			continue
		}
		if snapshotTime != nil && t.CreatedTime > *snapshotTime {
			continue
		}
		filtered = append(filtered, t)
	}

	anonymized := make([]map[string]any, 0, len(filtered))
	for _, t := range filtered {
		anonymized = append(anonymized, anonymizeChatTask(t))
	}

	artifacts := h.listSharedArtifacts(r.Context(), link, h.scopeFromLoadedTasks(userID, link, tasks))

	hasUsers, _ := h.linkUserRepo.HasAny(r.Context(), shareID)

	resp := SharedSessionView{
		ID:          link.ShareID,
		Title:       titleOrUntitled(link.Title),
		CreatedTime: epochMsToISO8601(link.CreatedTime),
		AccessType:  link.GetAccessType(hasUsers),
		Tasks:       anonymized,
		Artifacts:   artifacts,
		TaskEvents:  h.buildSharedTaskEvents(r.Context(), filtered),
		IsOwner:     isOwner,
	}
	if isOwner {
		sid := link.SessionID
		resp.SessionID = &sid
	}
	resp.SnapshotTime = snapshotTimeISO

	writeJSON(w, http.StatusOK, resp)
}

var pathTraversalRE = regexp.MustCompile(`(?:^|[/\\])\.\.(?:[/\\]|$)`)

func (h *shareLinkHandler) handleSharedArtifactContent(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	filename := r.PathValue("filename")

	if filename == "" ||
		strings.HasPrefix(filename, "/") ||
		strings.HasPrefix(filename, "\\") ||
		strings.ContainsRune(filename, '\x00') ||
		pathTraversalRE.MatchString(filename) ||
		path.Clean(filename) != filename {
		writeError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	link, scope, ok := h.resolveSharedArtifactAccess(w, r, shareID, filename)
	if !ok {
		return
	}

	artifacts := h.listSharedArtifactsExcept(r.Context(), link, func(name string) bool { return name != filename })
	var artInfo *SharedArtifactInfo
	for i := range artifacts {
		if artifacts[i].Filename == filename {
			artInfo = &artifacts[i]
			break
		}
	}
	if artInfo == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Artifact %q not found in shared session", filename))
		return
	}
	if h.artSvc == nil {
		writeError(w, http.StatusNotFound, "Artifact content not found")
		return
	}

	loc := h.artSvc.Resolve("", link.UserID, link.SessionID, filename)
	content, mimeType, loadErr := h.artSvc.Inner.Load(r.Context(), loc, scope.pinnedVersion(filename))
	if loadErr != nil {
		h.logger.Error("load shared artifact", "error", loadErr, "filename", filename)
		writeError(w, http.StatusInternalServerError, artifactLoadFailedMsg)
		return
	}
	if content == nil {
		writeError(w, http.StatusNotFound, "Artifact content not found")
		return
	}

	if mimeType == "" {
		if artInfo.MIMEType != "" {
			mimeType = artInfo.MIMEType
		} else {
			mimeType = "application/octet-stream"
		}
	}

	safeName := stripControlChars(path.Base(filename))
	contentType, inline := artifact.InlineContentType(mimeType)
	dispositionType := "inline"
	if !inline {
		contentType = mimeType
		dispositionType = "attachment"
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	disposition := fmt.Sprintf("%s; filename=%q", dispositionType, safeName)
	if !isASCII(safeName) {
		disposition += fmt.Sprintf(`; filename*=UTF-8''%s`, url.PathEscape(safeName))
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (h *shareLinkHandler) resolveSharedArtifactAccess(w http.ResponseWriter, r *http.Request, shareID, filename string) (*store.SharedLink, *shareArtifactScope, bool) {
	link, _, ok := h.resolveShareForRead(w, r, shareID, "Authentication required to access this artifact")
	if !ok {
		return nil, nil, false
	}
	scope, ok := h.admitSharedArtifact(w, r, link, filename)
	return link, scope, ok
}

func (h *shareLinkHandler) admitSharedArtifact(w http.ResponseWriter, r *http.Request, link *store.SharedLink, filename string) (*shareArtifactScope, bool) {
	userID, _ := auth.UserIDFromContext(r.Context())
	scope := h.scopeFromStoredTasks(r.Context(), userID, link)
	refused := scope.refuses(r.Context(), filename)
	if failure := scope.unavailable(); failure != nil {
		h.logger.Error("shared artifact: could not decide whether the shared session references the file",
			"shareID", link.ShareID, "userID", userID, "error", failure)
		writeError(w, http.StatusInternalServerError, errInternal)
		return nil, false
	}
	if refused {
		h.logger.Warn("shared artifact refused: user-scoped file not referenced by the shared session", "shareID", link.ShareID, "userID", userID)
		writeError(w, http.StatusNotFound, artifactNotFoundMsg)
		return nil, false
	}
	return scope, true
}

func stripControlChars(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func chatTaskA2AID(t *store.ChatTask) string {
	if t.TaskMetadata != nil && *t.TaskMetadata != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(*t.TaskMetadata), &meta); err == nil {
			if tid, ok := meta["taskId"].(string); ok && tid != "" {
				return tid
			}
			if tid, ok := meta["task_id"].(string); ok && tid != "" {
				return tid
			}
		}
	}
	return t.ID
}

func anonymizeChatTask(t *store.ChatTask) map[string]any {
	var bubbles any = []any{}
	if t.MessageBubbles != "" {
		var arr []map[string]any
		if err := json.Unmarshal([]byte(t.MessageBubbles), &arr); err == nil {
			for _, b := range arr {
				if m, ok := b["metadata"].(map[string]any); ok {
					delete(m, "userId")
					delete(m, "sessionId")
				}
			}
			bubbles = arr
		}
	}

	workflowTaskID := chatTaskA2AID(t)
	var taskMetadata any
	if t.TaskMetadata != nil && *t.TaskMetadata != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(*t.TaskMetadata), &meta); err == nil {
			taskMetadata = meta
		} else {
			taskMetadata = *t.TaskMetadata
		}
	}

	return map[string]any{
		"id":             t.ID,
		"sessionId":      anonymizeSessionID(t.SessionID),
		"userId":         "anonymous",
		"userMessage":    t.UserMessage,
		"messageBubbles": bubbles,
		"taskMetadata":   taskMetadata,
		"createdAt":      epochMsToISO8601(t.CreatedTime),
		"updatedAt":      optionalISO8601(t.UpdatedTime),
		"workflowTaskId": workflowTaskID,
	}
}

func anonymizeSessionID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("session_%x", sum[:8])
}

func (h *shareLinkHandler) listSharedArtifacts(ctx context.Context, link *store.SharedLink, scope *shareArtifactScope) []SharedArtifactInfo {
	out := h.listSharedArtifactsExcept(ctx, link, func(filename string) bool { return scope.refuses(ctx, filename) })
	if failure := scope.unavailable(); failure != nil {
		h.logger.Warn("list shared artifacts: user-scoped files left out, could not decide whether the shared session references them",
			"shareID", link.ShareID, "userID", scope.requester, "error", failure)
	}
	return out
}

func (h *shareLinkHandler) listSharedArtifactsExcept(ctx context.Context, link *store.SharedLink, leaveOut func(filename string) bool) []SharedArtifactInfo {
	userID, sessionID := link.UserID, link.SessionID
	if h.artSvc == nil {
		return []SharedArtifactInfo{}
	}
	infos, err := listScopeArtifactInfos(ctx, h.artSvc, userID, sessionID)
	if err != nil {
		h.logger.Warn("list shared artifacts", "error", err, "userID", userID, "sessionID", sessionID)
		return []SharedArtifactInfo{}
	}
	out := make([]SharedArtifactInfo, 0, len(infos))
	for _, info := range infos {
		if leaveOut(info.Filename) {
			continue
		}
		shared := SharedArtifactInfo{
			Filename:     info.Filename,
			Size:         info.Size,
			LastModified: info.LastModified,
			Version:      info.Version,
			VersionCount: info.VersionCount,
			Description:  info.Description,
			Source:       info.Source,
		}
		if info.MIMEType != nil {
			shared.MIMEType = *info.MIMEType
		}
		out = append(out, shared)
	}
	return out
}

func (h *shareLinkHandler) buildSharedTaskEvents(ctx context.Context, tasks []*store.ChatTask) map[string]any {
	if h.taskRepo == nil || h.eventRepo == nil || len(tasks) == 0 {
		return nil
	}
	seeds := make([]string, 0, len(tasks))
	for _, t := range tasks {
		seeds = append(seeds, chatTaskA2AID(t))
	}
	entries, _, err := collectTaskEvents(ctx, h.taskRepo, h.eventRepo, seeds, false, h.logger)
	if err != nil {
		h.logger.Error("build shared task events", "error", err)
		return nil
	}
	out := make(map[string]any, len(entries))
	for id, entry := range entries {
		if evts, ok := entry["events"].([]map[string]any); ok && len(evts) == 0 {
			continue
		}
		out[id] = entry
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (h *shareLinkHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	userID, _ := auth.UserIDFromContext(r.Context())

	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil {
		h.logger.Error("get share for update", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if link == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Share link %s not found", shareID))
		return
	}
	if link.UserID != userID {
		writeError(w, http.StatusForbidden, "Not authorized to modify this share link")
		return
	}

	var req UpdateShareLinkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.AllowedDomains != nil {
		wantAuth := link.RequireAuthentication
		if req.RequireAuthentication != nil {
			wantAuth = *req.RequireAuthentication
		}
		if len(*req.AllowedDomains) > 0 && !wantAuth {
			writeError(w, http.StatusBadRequest, "Domain restrictions require authentication to be enabled")
			return
		}
		if len(*req.AllowedDomains) > 0 {
			if err := validateDomainsList(*req.AllowedDomains); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid domains: %s", err.Error()))
				return
			}
		}
		link.AllowedDomains = formatAllowedDomains(*req.AllowedDomains)
	}
	if req.RequireAuthentication != nil {
		link.RequireAuthentication = *req.RequireAuthentication
	}

	if err := h.linkRepo.Update(r.Context(), link); err != nil {
		h.logger.Error("update share", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	hasUsers, _ := h.linkUserRepo.HasAny(r.Context(), link.ShareID)
	writeJSON(w, http.StatusOK, sharedLinkToResponse(link, hasUsers, r))
}

func (h *shareLinkHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	userID, _ := auth.UserIDFromContext(r.Context())

	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil {
		h.logger.Error("get share for delete", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if link == nil {
		writeError(w, http.StatusNotFound, errShareLinkNotFound)
		return
	}
	if link.UserID != userID {
		writeError(w, http.StatusForbidden, "Not authorized to delete this share link")
		return
	}

	_ = h.artifactRepo.DeleteByShareID(r.Context(), shareID)

	if err := h.linkRepo.SoftDelete(r.Context(), shareID); err != nil {
		h.logger.Error("soft delete share", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	writeJSON(w, http.StatusOK, SuccessResponse{Success: true, Message: "Share link deleted successfully"})
}

func (h *shareLinkHandler) handleFork(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	userID, userEmail := auth.UserIDFromContext(r.Context())
	if userID == "" {
		w.Header().Set(headerWWWAuthenticate, wwwAuthenticateBearer)
		writeError(w, http.StatusUnauthorized, "Authentication required to fork this chat")
		return
	}

	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil || link == nil {
		writeError(w, http.StatusNotFound, errShareLinkNotFound)
		return
	}

	if link.UserID != userID {
		ok, _ := h.checkAccess(r.Context(), link, userID, userEmail)
		if !ok {
			writeError(w, http.StatusForbidden, errAccessDenied)
			return
		}
	}

	origSession, err := h.sessRepo.GetByID(r.Context(), link.SessionID)
	if err != nil || origSession == nil {
		writeError(w, http.StatusNotFound, "original session not found")
		return
	}

	origTasks, err := h.chatRepo.ListBySession(r.Context(), link.SessionID)
	if err != nil {
		h.logger.Error("list tasks for fork", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if len(origTasks) == 0 {
		writeError(w, http.StatusNotFound, "No messages found in the shared session")
		return
	}

	artifactKeys, pinned, err := h.planForkArtifacts(r.Context(), link, userID, origTasks)
	if err != nil {
		h.logger.Error("fork: could not decide which files of the shared session to copy", "shareID", shareID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	title := titleOrUntitled(link.Title) + " (forked)"
	newSessionID := idgen.New()
	newSession := &store.Session{
		ID:     newSessionID,
		Name:   &title,
		UserID: userID,
	}
	if createErr := h.sessRepo.Create(r.Context(), newSession); createErr != nil {
		h.logger.Error("create forked session", "error", createErr)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	now := time.Now().UnixMilli()
	for _, t := range origTasks {
		forked := &store.ChatTask{
			ID:             idgen.New(),
			SessionID:      newSessionID,
			UserID:         userID,
			UserMessage:    t.UserMessage,
			MessageBubbles: t.MessageBubbles,
			TaskMetadata:   forkedTaskMetadata(t, link.SessionID, link.UserID),
			CreatedTime:    now,
		}
		if err := h.chatRepo.Create(r.Context(), forked); err != nil {
			h.logger.Warn("copy chat task during fork", "error", err)
		}
	}

	copySessionArtifacts(r.Context(), forkCopy{
		artSvc:    h.artSvc,
		srcUserID: link.UserID, srcSessionID: link.SessionID,
		dstUserID: userID, dstSessionID: newSessionID,
		logger: h.logger,
	}, artifactKeys, pinned)

	writeJSON(w, http.StatusOK, ForkSharedChatResponse{
		SessionID:   newSessionID,
		SessionName: title,
		Message:     fmt.Sprintf("Chat forked successfully with %d messages", len(origTasks)),
	})
}

func (h *shareLinkHandler) planForkArtifacts(ctx context.Context, link *store.SharedLink, forker string, tasks []*store.ChatTask) ([]string, map[string]int, error) {
	scope := h.scopeFromLoadedTasks(forker, link, tasks)
	keys, leftOut, err := forkableArtifacts(ctx, h.artSvc, link.UserID, link.SessionID, func(filename string) bool {
		return scope.refuses(ctx, filename)
	})
	if err == nil {
		err = scope.unavailable()
	}
	if err != nil {
		return nil, nil, err
	}
	if leftOut > 0 {
		h.logger.Debug("fork left out user-scoped files the shared session does not reference", "shareID", link.ShareID, "userID", forker, "count", leftOut)
	}
	pinned := map[string]int{}
	for _, key := range keys {
		if version := scope.pinnedVersion(key); version != nil {
			pinned[key] = *version
		}
	}
	return keys, pinned, nil
}

func forkedTaskMetadata(src *store.ChatTask, origSessionID, origOwnerID string) *string {
	newTaskID := idgen.New()
	meta := map[string]any{
		"taskId":                      newTaskID,
		"forkedFrom":                  src.ID,
		"forkedFromSessionId":         origSessionID,
		entrypoint.ForkedFromOwnerKey: origOwnerID,
	}
	if src.TaskMetadata != nil && *src.TaskMetadata != "" {
		var existing map[string]any
		if err := json.Unmarshal([]byte(*src.TaskMetadata), &existing); err == nil {
			delete(existing, "task_id")
			maps.DeleteFunc(existing, func(key string, _ any) bool { return entrypoint.IsForkMarkerKey(key) })
			for k, v := range existing {
				if k == "status" {
					continue
				}
				if _, reserved := meta[k]; !reserved {
					meta[k] = v
				}
			}
		}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

func (h *shareLinkHandler) handleUpdateSnapshot(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	userID, userEmail := auth.UserIDFromContext(r.Context())

	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil {
		h.logger.Error("get share for snapshot", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if link == nil {
		writeError(w, http.StatusNotFound, errShareLinkNotFound)
		return
	}

	var req UpdateSnapshotRequest
	if r.ContentLength != 0 && r.Body != nil {
		if decodeErr := decodeJSON(r, &req); decodeErr != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	if req.UserEmail == nil && userID != "" && link.UserID == userID {
		newTime := time.Now().UnixMilli()
		ok, updErr := h.linkRepo.UpdateLinkSnapshotTime(r.Context(), shareID, newTime)
		if updErr != nil {
			h.logger.Error("update link snapshot", "error", updErr, "shareID", shareID)
			writeError(w, http.StatusInternalServerError, errInternal)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, errShareLinkNotFound)
			return
		}
		writeJSON(w, http.StatusOK, UpdateSnapshotResponse{SnapshotTime: epochMsToISO8601(newTime)})
		return
	}

	targetEmail := ""
	if req.UserEmail != nil {
		if link.UserID != userID {
			writeError(w, http.StatusForbidden, "Only the owner can update another user's snapshot")
			return
		}
		targetEmail = strings.ToLower(strings.TrimSpace(*req.UserEmail))
	} else {
		targetEmail = strings.ToLower(strings.TrimSpace(userEmail))
		if targetEmail == "" {
			writeEmailRequired(w, userID)
			return
		}
		if link.UserID != userID {
			has, _ := h.linkUserRepo.HasAccess(r.Context(), shareID, targetEmail)
			if !has {
				writeError(w, http.StatusForbidden, errAccessDenied)
				return
			}
		}
	}
	if targetEmail == "" {
		writeError(w, http.StatusBadRequest, "userEmail is required")
		return
	}

	newTime := time.Now().UnixMilli()
	advance := h.linkUserRepo.UpdateSnapshotTime
	if targetEmail == normalizeEmail(userEmail) {
		advance = h.linkUserRepo.RefreshOwnSnapshot
	}
	ok, err := advance(r.Context(), shareID, targetEmail, newTime)
	if err != nil {
		h.logger.Error("update snapshot", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errShareUserNotFound)
		return
	}

	writeJSON(w, http.StatusOK, UpdateSnapshotResponse{SnapshotTime: epochMsToISO8601(newTime)})
}

func (h *shareLinkHandler) handleMarkShareViewed(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	lookup := recipientKeyFromContext(r.Context())
	if lookup == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	viewedAt := time.Now().UnixMilli()
	ok, err := h.linkUserRepo.MarkViewed(r.Context(), shareID, lookup, viewedAt)
	if err != nil {
		h.logger.Error("mark share viewed", "shareID", shareID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errShareUserNotFound)
		return
	}

	writeJSON(w, http.StatusOK, MarkShareViewedResponse{ViewedAt: epochMsToISO8601(viewedAt)})
}

func (h *shareLinkHandler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	userID, _ := auth.UserIDFromContext(r.Context())

	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil || link == nil {
		writeError(w, http.StatusNotFound, errShareLinkNotFound)
		return
	}
	if link.UserID != userID {
		writeError(w, http.StatusForbidden, "Not authorized to view share users")
		return
	}

	users, err := h.linkUserRepo.ListByShareID(r.Context(), shareID)
	if err != nil {
		h.logger.Error("list share users", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	ownerEmail := link.UserID
	if sess, sErr := h.sessRepo.GetByID(r.Context(), link.SessionID); sErr == nil && sess != nil {
		ownerEmail = sess.UserID
	}

	infos := make([]SharedLinkUserInfo, 0, len(users))
	for i := range users {
		infos = append(infos, sharedLinkUserToInfo(&users[i]))
	}

	writeJSON(w, http.StatusOK, ShareUsersResponse{
		ID:         shareID,
		OwnerEmail: ownerEmail,
		Users:      infos,
	})
}

func (h *shareLinkHandler) handleAddUsers(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	userID, _ := auth.UserIDFromContext(r.Context())

	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil || link == nil {
		writeError(w, http.StatusNotFound, errShareLinkNotFound)
		return
	}
	if link.UserID != userID {
		writeError(w, http.StatusForbidden, "Not authorized to add share users")
		return
	}

	var req BatchAddShareUsersRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	added := make([]SharedLinkUserInfo, 0, len(req.Shares))
	for _, s := range req.Shares {
		email := strings.ToLower(strings.TrimSpace(s.UserEmail))
		if email == "" {
			continue
		}
		level := s.AccessLevel
		if level == "" {
			level = accessLevelViewer
		}

		existing, getErr := h.linkUserRepo.GetByShareAndEmail(r.Context(), shareID, email)
		if getErr != nil {
			h.logger.Error("lookup existing share user", "error", getErr)
			continue
		}
		if existing != nil {
			if existing.AccessLevel == level {
				continue
			}
			updated, uErr := h.linkUserRepo.UpdateAccessLevel(r.Context(), shareID, email, level)
			if uErr != nil {
				h.logger.Error("update share user access", "error", uErr)
				continue
			}
			if updated != nil {
				added = append(added, sharedLinkUserToInfo(updated))
				h.pushShareEvent(link, email, level, shareReasonAccessChanged, updated.AddedAt)
			}
			continue
		}

		u := &store.SharedLinkUser{
			ShareID:       shareID,
			UserEmail:     email,
			AccessLevel:   level,
			AddedByUserID: userID,
		}
		if _, addErr := h.linkUserRepo.Add(r.Context(), u); addErr != nil {
			h.logger.Error("add share user", "error", addErr)
			continue
		}
		added = append(added, sharedLinkUserToInfo(u))

		h.notifyShared(r.Context(), link, email, level, userID, r)
		h.pushShareEvent(link, email, level, shareReasonShared, u.AddedAt)
	}

	writeJSON(w, http.StatusOK, BatchAddShareUsersResponse{
		AddedCount: len(added),
		Users:      added,
	})
}

const shareEventChatShared = "chat_shared"

const (
	shareReasonShared        = "shared"
	shareReasonAccessChanged = "access_changed"
)

type chatSharedEvent struct {
	ShareID     string  `json:"shareId"`
	Title       string  `json:"title"`
	OwnerEmail  string  `json:"ownerEmail"`
	AccessLevel string  `json:"accessLevel"`
	SessionID   *string `json:"sessionId"`
	SharedAt    string  `json:"sharedAt"`
	Reason      string  `json:"reason"`
}

func (h *shareLinkHandler) notifyShared(ctx context.Context, link *store.SharedLink, recipientEmail, accessLevel, ownerUserID string, r *http.Request) {
	if recipientEmail == strings.ToLower(strings.TrimSpace(ownerUserID)) {
		return
	}
	if reg := h.core.Services().FeatureRegistry(); reg == nil || !reg.IsEnabled(features.KeyNotifyService) {
		return
	}

	shareURL := buildShareURL(link.ShareID, r)
	title := titleOrUntitled(link.Title)
	metadata := map[string]any{
		"share_id":     link.ShareID,
		"share_url":    shareURL,
		"owner_email":  ownerUserID,
		"access_level": accessLevel,
		"link_label":   notifyShareActionLabel,
	}
	if accessLevel == accessLevelEditor {
		metadata["session_id"] = link.SessionID
	}
	req := a2a.NotifyRequest{
		RequestID:       idgen.New(),
		RecipientUserID: recipientEmail,
		Mode:            a2a.NotifyModeNotify,
		SenderUserID:    ownerUserID,
		SenderAgent:     notifySourceChatShare,
		Category:        a2a.CategoryChatShare,
		Title:           ownerUserID + " shared a chat with you",
		Message:         fmt.Sprintf("%q\n%s", title, shareURL),
		Metadata:        metadata,
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyPublishTimeout)
	defer cancel()

	if err := h.core.PublishNotification(pubCtx, recipientEmail, req); err != nil {
		h.logger.Warn("share notification publish failed", "shareID", link.ShareID, "error", err)
		return
	}
	h.logger.Debug("share notification published", "shareID", link.ShareID)
}

func (h *shareLinkHandler) pushShareEvent(link *store.SharedLink, recipientEmail, accessLevel, reason string, addedAt int64) {
	if h.userNotifier == nil {
		return
	}
	if recipientEmail == normalizeEmail(link.UserID) {
		return
	}
	var sessionID *string
	if accessLevel == accessLevelEditor {
		sid := link.SessionID
		sessionID = &sid
	}
	evt := chatSharedEvent{
		ShareID:     link.ShareID,
		Title:       titleOrUntitled(link.Title),
		OwnerEmail:  link.UserID,
		AccessLevel: accessLevel,
		SessionID:   sessionID,
		SharedAt:    epochMsToISO8601(addedAt),
		Reason:      reason,
	}
	if err := h.userNotifier.SendUserNotification(recipientEmail, shareEventChatShared, evt); err != nil {
		h.logger.Warn("push chat_shared event failed", "shareID", link.ShareID, "error", err)
	}
}

func (h *shareLinkHandler) handleRemoveUsers(w http.ResponseWriter, r *http.Request) {
	shareID := r.PathValue("id")
	userID, _ := auth.UserIDFromContext(r.Context())

	link, err := h.linkRepo.GetByID(r.Context(), shareID)
	if err != nil || link == nil {
		writeError(w, http.StatusNotFound, errShareLinkNotFound)
		return
	}
	if link.UserID != userID {
		writeError(w, http.StatusForbidden, "Not authorized to remove share users")
		return
	}

	var req BatchDeleteShareUsersRequest
	if decErr := decodeJSON(r, &req); decErr != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	n, err := h.linkUserRepo.BatchRemove(r.Context(), shareID, req.UserEmails)
	if err != nil {
		h.logger.Error("remove share users", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	writeJSON(w, http.StatusOK, BatchDeleteShareUsersResponse{DeletedCount: n})
}

func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{}
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok || id == nil {
		userID, userEmail := auth.UserIDFromContext(r.Context())
		resp["id"] = userID
		resp["email"] = userEmail
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["id"] = id.UserID()
	resp["email"] = id.Email()
	if id.Name() != "" {
		resp["name"] = id.Name()
	}
	if id.EnrichmentStatus() != "" {
		resp["enrichmentStatus"] = string(id.EnrichmentStatus())
	}
	if id.EnrichmentReason() != "" {
		resp["enrichmentReason"] = id.EnrichmentReason()
	}
	if id.Profile() != nil {
		resp["profile"] = id.Profile()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *shareLinkHandler) checkAccess(ctx context.Context, link *store.SharedLink, userID, userEmail string) (bool, string) {
	if userID == "" {
		return false, "authentication_required"
	}

	sharedUsers, err := h.linkUserRepo.ListByShareID(ctx, link.ShareID)
	if err != nil {
		return false, "error"
	}
	if len(sharedUsers) > 0 {
		lower := strings.ToLower(userEmail)
		for _, u := range sharedUsers {
			if strings.EqualFold(u.UserEmail, lower) {
				return true, "user_specific"
			}
		}
		return false, "not_shared_with_user"
	}

	if link.AllowedDomains == nil || *link.AllowedDomains == "" {
		return true, "authenticated"
	}
	if userEmail == "" || !strings.Contains(userEmail, "@") {
		return false, "invalid_email"
	}
	userDomain := emailToDomain(userEmail)
	for _, d := range link.GetAllowedDomainsList() {
		if strings.EqualFold(strings.TrimSpace(d), userDomain) {
			return true, "domain_match"
		}
	}
	return false, "domain_mismatch"
}

func generateShareID() string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_-"
	const size = 21
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[b[i]%byte(len(alphabet))]
	}
	return string(b)
}
