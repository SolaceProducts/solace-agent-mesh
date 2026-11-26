// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	msgInboxNotEnabled      = "inbox is not enabled (no database configured)"
	msgNotificationNotFound = "notification not found"
)

type NotificationDTO struct {
	ID               string              `json:"id"`
	Title            string              `json:"title"`
	Body             string              `json:"body"`
	Status           string              `json:"status"`
	Source           string              `json:"source,omitempty"`
	Category         string              `json:"category,omitempty"`
	CreatedAt        string              `json:"createdAt"`
	ReadAt           *string             `json:"readAt,omitempty"`
	RequestID        string              `json:"requestId,omitempty"`
	Surface          json.RawMessage     `json:"surface,omitempty"`
	Action           *NotificationAction `json:"action,omitempty"`
	PromptState      string              `json:"promptState,omitempty"`
	PromptAnsweredBy string              `json:"promptAnsweredBy,omitempty"`
	PromptOutcome    string              `json:"promptOutcome,omitempty"`
}

type NotificationAction struct {
	Label     string `json:"label"`
	SessionID string `json:"sessionId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	ShareID   string `json:"shareId,omitempty"`
	Path      string `json:"path,omitempty"`
}

const (
	defaultSessionActionLabel = "Go to session"
	defaultShareActionLabel   = "View shared chat"
	defaultPathActionLabel    = "Open"
)

const (
	promptStatePending  = "pending"
	promptStateResolved = "resolved"
)

type PaginatedResponse[T any] struct {
	Data []T            `json:"data"`
	Meta paginationMeta `json:"meta"`
}

type paginationMeta struct {
	Pagination paginationInfo `json:"pagination"`
}

type paginationInfo struct {
	PageNumber int  `json:"pageNumber"`
	PageSize   int  `json:"pageSize"`
	TotalPages int  `json:"totalPages"`
	Count      int  `json:"count"`
	NextPage   *int `json:"nextPage"`
}

func newPaginationInfo(pageNumber, pageSize, total int) paginationInfo {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	var nextPage *int
	if pageNumber < totalPages {
		n := pageNumber + 1
		nextPage = &n
	}
	return paginationInfo{PageNumber: pageNumber, PageSize: pageSize, TotalPages: totalPages, Count: total, NextPage: nextPage}
}

func toDTO(n store.Notification) NotificationDTO {
	dto := NotificationDTO{
		ID:        n.ID,
		Title:     n.Title,
		Body:      n.Body,
		Status:    n.Status,
		Source:    n.Source,
		Category:  catalogCategory(n.Category),
		CreatedAt: isoFromMillis(n.CreatedAt),
	}
	if n.ReadAt != nil {
		s := isoFromMillis(*n.ReadAt)
		dto.ReadAt = &s
	}
	dto.RequestID, dto.Surface = parseHILMeta(n.Payload)
	dto.Action = parseActionMeta(n.Payload)
	return dto
}

func catalogCategory(key string) string {
	if _, err := LookupCategory(key); err != nil {
		return ""
	}
	return key
}

func parseActionMeta(payload string) *NotificationAction {
	if payload == "" {
		return nil
	}
	var md struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		ShareID   string `json:"share_id"`
		LinkPath  string `json:"link_path"`
		LinkLabel string `json:"link_label"`
	}
	if err := json.Unmarshal([]byte(payload), &md); err != nil {
		return nil
	}
	path := ""
	if inAppPath(md.LinkPath) {
		path = md.LinkPath
	}
	if md.SessionID == "" && md.ShareID == "" && path == "" {
		return nil
	}
	return &NotificationAction{
		Label:     cmp.Or(md.LinkLabel, defaultActionLabel(md.SessionID, md.ShareID)),
		SessionID: md.SessionID,
		AgentID:   md.AgentID,
		ShareID:   md.ShareID,
		Path:      path,
	}
}

func defaultActionLabel(sessionID, shareID string) string {
	switch {
	case sessionID != "":
		return defaultSessionActionLabel
	case shareID != "":
		return defaultShareActionLabel
	default:
		return defaultPathActionLabel
	}
}

func inAppPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsRune(p, '\\') {
		return false
	}
	if strings.ContainsFunc(p, unicode.IsControl) {
		return false
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == ""
}

func (n *NotifyCore) stampPromptStates(ctx context.Context, rowOwner string, data []NotificationDTO) {
	if n.pending == nil {
		return
	}
	for i := range data {
		if data[i].RequestID == "" {
			continue
		}
		pr, err := n.pending.Get(ctx, data[i].RequestID)
		if err != nil {
			n.logger.Warn("notify: HIL prompt liveness lookup failed for inbox listing",
				"requestID", data[i].RequestID, "error", err)
			continue
		}
		if pr != nil && pr.Status == "pending" {
			data[i].PromptState = promptStatePending
			continue
		}
		data[i].PromptState = promptStateResolved
		if pr == nil {
			continue
		}
		if pr.Approvers == nil || !n.approverSetListsOwner(ctx, pr, rowOwner) {
			continue
		}
		data[i].PromptOutcome = resolvedOutcome(pr).Outcome
		if pr.AnsweredBy != nil {
			data[i].PromptAnsweredBy = cmp.Or(deref(pr.AnsweredByName), *pr.AnsweredBy)
		}
	}
}

func isoFromMillis(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func (n *NotifyCore) handleInboxList(w http.ResponseWriter, r *http.Request) {
	if n.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, msgInboxNotEnabled, n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifyReadOthers)
	if !ok {
		return
	}
	pageNumber, pageSize := parsePagination(r)
	category, ok := n.categoryParam(w, r)
	if !ok {
		return
	}
	filter := store.InboxFilter{
		UnreadOnly: r.URL.Query().Get("status") == "unread",
		Category:   category,
	}

	total, err := n.inbox.CountByUser(r.Context(), userID, filter)
	if err != nil {
		writeServerError(w, "failed to count notifications", err, n.logger)
		return
	}
	rows, err := n.inbox.ListByUser(r.Context(), userID, filter, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		writeServerError(w, "failed to list notifications", err, n.logger)
		return
	}
	data := make([]NotificationDTO, 0, len(rows))
	for _, row := range rows {
		data = append(data, toDTO(row))
	}
	n.stampPromptStates(r.Context(), userID, data)
	writeJSON(w, http.StatusOK, PaginatedResponse[NotificationDTO]{
		Data: data,
		Meta: paginationMeta{Pagination: newPaginationInfo(pageNumber, pageSize, total)},
	})
}

func (n *NotifyCore) handleInboxMarkRead(w http.ResponseWriter, r *http.Request) {
	if n.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, msgInboxNotEnabled, n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifyReadOthers)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validatePathUUID(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), n.logger)
		return
	}
	switch err := n.inbox.MarkRead(r.Context(), userID, id); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, msgNotificationNotFound, n.logger)
	default:
		writeServerError(w, "failed to mark notification read", err, n.logger)
	}
}

func (n *NotifyCore) handleInboxMarkUnread(w http.ResponseWriter, r *http.Request) {
	if n.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, msgInboxNotEnabled, n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifySendOthers)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validatePathUUID(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), n.logger)
		return
	}
	switch err := n.inbox.MarkUnread(r.Context(), userID, id); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, msgNotificationNotFound, n.logger)
	default:
		writeServerError(w, "failed to mark notification unread", err, n.logger)
	}
}

func (n *NotifyCore) categoryParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	category := r.URL.Query().Get("category")
	if category == "" {
		return "", true
	}
	if _, err := n.lookupOfferedCategory(r, category); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), n.logger)
		return "", false
	}
	return category, true
}

func (n *NotifyCore) handleInboxMarkAllRead(w http.ResponseWriter, r *http.Request) {
	if n.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, msgInboxNotEnabled, n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifyReadOthers)
	if !ok {
		return
	}
	category, ok := n.categoryParam(w, r)
	if !ok {
		return
	}
	if _, err := n.inbox.MarkAllRead(r.Context(), userID, store.InboxFilter{Category: category}); err != nil {
		writeServerError(w, "failed to mark notifications read", err, n.logger)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (n *NotifyCore) handleInboxDelete(w http.ResponseWriter, r *http.Request) {
	if n.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, msgInboxNotEnabled, n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifySendOthers)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validatePathUUID(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), n.logger)
		return
	}
	switch err := n.inbox.Delete(r.Context(), userID, id); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, msgNotificationNotFound, n.logger)
	default:
		writeServerError(w, "failed to delete notification", err, n.logger)
	}
}

func (n *NotifyCore) handleInboxClearAll(w http.ResponseWriter, r *http.Request) {
	if n.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, msgInboxNotEnabled, n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifySendOthers)
	if !ok {
		return
	}
	if _, err := n.inbox.DeleteAllByUser(r.Context(), userID); err != nil {
		writeServerError(w, "failed to clear notifications", err, n.logger)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type PreferenceDTO struct {
	ChannelType    string `json:"channelType"`
	DestinationKey string `json:"destinationKey,omitempty"`
	Destination    string `json:"destination,omitempty"`
	Enabled        bool   `json:"enabled"`
}

type setPreferenceRequest struct {
	ChannelType    string `json:"channelType"`
	DestinationKey string `json:"destinationKey"`
	Enabled        *bool  `json:"enabled"`
}

func (n *NotifyCore) handlePreferencesList(w http.ResponseWriter, r *http.Request) {
	if n.prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences are not enabled (no database configured)", n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifyReadOthers)
	if !ok {
		return
	}
	disabled, err := n.prefs.DisabledChannels(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list preferences", err, n.logger)
		return
	}
	data := n.reachablePreferences(r.Context(), requestDestinationKeys(r, userID), disabled)
	pageNumber, pageSize := parsePagination(r)
	writeJSON(w, http.StatusOK, PaginatedResponse[PreferenceDTO]{
		Data: data,
		Meta: paginationMeta{Pagination: newPaginationInfo(pageNumber, pageSize, len(data))},
	})
}

func (n *NotifyCore) reachablePreferences(ctx context.Context, keys []string, disabled map[string]bool) []PreferenceDTO {
	reachable, _, err := n.preferenceChannels(ctx, keys, disabled)
	if err != nil {
		n.logger.Warn("notify: channels unavailable for preferences", "userID", keys[0], "error", err)
	}
	return reachable
}

func (n *NotifyCore) preferenceChannels(ctx context.Context, keys []string, disabled map[string]bool) (reachable, unconfirmed []PreferenceDTO, err error) {
	reachable = make([]PreferenceDTO, 0, 4)
	seen := make(map[string]bool, 4)
	add := func(to *[]PreferenceDTO, ch Channel) {
		key := destinationKey(ch)
		pk := store.PrefKey(ch.Type, key)
		if seen[pk] {
			return
		}
		seen[pk] = true
		*to = append(*to, PreferenceDTO{
			ChannelType:    ch.Type,
			DestinationKey: key,
			Destination:    destinationLabel(ch),
			Enabled:        !disabled[pk],
		})
	}
	for _, userID := range keys {
		if n.dir != nil {
			chs, derr := n.dir.ChannelsFor(ctx, userID)
			if derr != nil {
				err = errors.Join(err, fmt.Errorf("directory channels: %w", derr))
			}
			for _, ch := range chs {
				if ch.Type == "webhook" || ch.Type == "connector" {
					add(&reachable, ch)
				}
			}
		}
		if n.channels != nil {
			learned, lerr := n.channels.ListByUser(ctx, userID)
			if lerr != nil {
				err = errors.Join(err, fmt.Errorf("learned channels: %w", lerr))
			}
			for _, uc := range learned {
				if uc.Confirmed() {
					add(&reachable, Channel{Type: uc.ChannelType})
				} else {
					add(&unconfirmed, Channel{Type: uc.ChannelType})
				}
			}
		}
	}
	return reachable, unconfirmed, err
}

func requestDestinationKeys(r *http.Request, target string) []string {
	login := canonicalUserKey(r.URL.Query().Get("userId"))
	if id, ok := auth.SubjectFromContext(r.Context()); ok && id != nil && target == callerKey(id) {
		login = id.UserID()
	}
	if login == "" || login == target {
		return []string{target}
	}
	return []string{target, login}
}

func (n *NotifyCore) handlePreferencesSet(w http.ResponseWriter, r *http.Request) {
	if n.prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences are not enabled (no database configured)", n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifySendOthers)
	if !ok {
		return
	}
	var req setPreferenceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), n.logger)
		return
	}
	if req.ChannelType == "" {
		writeError(w, http.StatusBadRequest, "channelType is required", n.logger)
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required", n.logger)
		return
	}
	if err := n.prefs.Set(r.Context(), userID, req.ChannelType, req.DestinationKey, *req.Enabled); err != nil {
		writeServerError(w, "failed to set preference", err, n.logger)
		return
	}
	writeJSON(w, http.StatusOK, PreferenceDTO{ChannelType: req.ChannelType, DestinationKey: req.DestinationKey, Enabled: *req.Enabled})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

func (n *NotifyCore) approverSetListsOwner(ctx context.Context, pr *store.PendingRequest, rowOwner string) bool {
	var rec approverRecord
	if json.Unmarshal([]byte(*pr.Approvers), &rec) != nil {
		return false
	}
	owner := canonicalUserKey(rowOwner)
	return slices.ContainsFunc(rec.Users, func(u approverUser) bool {
		if owner == u.UserID || (u.PlatformUserID != "" && owner == u.PlatformUserID) {
			return true
		}
		return u.PlatformUserID == "" && n.aliasResolvesTo(ctx, u.UserID, owner)
	})
}

func (n *NotifyCore) aliasResolvesTo(ctx context.Context, login, owner string) bool {
	if n.db == nil {
		return false
	}
	platformID, err := n.db.PlatformUserIDFor(ctx, login)
	if err != nil {
		n.logger.Warn("notify: approver alias lookup failed while labelling an inbox prompt", "error", err)
		return false
	}
	return platformID != "" && canonicalUserKey(platformID) == owner
}
