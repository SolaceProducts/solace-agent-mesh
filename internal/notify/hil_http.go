// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	auditevent "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type respondRequest struct {
	Status string         `json:"status"`
	Data   map[string]any `json:"data,omitempty"`
}

func (n *NotifyCore) handleInboxRespond(w http.ResponseWriter, r *http.Request) {
	if n.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "inbox is not enabled (no database configured)", n.logger)
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
	var body respondRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), n.logger)
		return
	}
	switch body.Status {
	case "answered", "cancelled":
	default:
		writeError(w, http.StatusBadRequest, `status must be "answered" or "cancelled"`, n.logger)
		return
	}

	row, err := n.inbox.Get(r.Context(), userID, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "notification not found", n.logger)
		return
	case err != nil:
		writeServerError(w, "failed to load notification", err, n.logger)
		return
	}

	requestID, _ := parseHILMeta(row.Payload)
	if requestID == "" {
		writeError(w, http.StatusBadRequest, "this notification is not an input request (nothing to respond to)", n.logger)
		return
	}

	var props map[string]string
	if n.pending != nil {
		pr, perr := n.pending.Get(r.Context(), requestID)
		switch {
		case perr != nil:
			writeServerError(w, "failed to check request state", perr, n.logger)
			return
		case pr == nil || pr.Status != "pending":
			writeError(w, http.StatusConflict, "this request is no longer awaiting input (already answered, cancelled, or expired)", n.logger)
			return
		}
		if pr.Approvers != nil {
			var ok bool
			if props, ok = n.authorizeInboxAnswer(w, r, userID, pr); !ok {
				return
			}
		}
	}

	data := body.Data
	if body.Status == "cancelled" {
		data = nil
	}
	resp := a2a.UserInputResponseData{
		Type:      string(a2a.SignalUserInputResponse),
		RequestID: requestID,
		Status:    body.Status,
		Data:      data,
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		writeServerError(w, "failed to encode response", err, n.logger)
		return
	}
	topic := a2a.NotifyReplyTopic(n.cfg.Namespace, requestID)
	if perr := n.svc.Broker().PublishGuaranteed(r.Context(), topic, &broker.Message{Payload: payload, UserProperties: props}); perr != nil {
		writeServerError(w, "failed to submit response", perr, n.logger)
		return
	}

	if merr := n.inbox.MarkRead(r.Context(), userID, id); merr != nil && !errors.Is(merr, sql.ErrNoRows) {
		n.logger.Warn("notify: failed to mark HIL notification read after respond",
			"id", id, "userID", userID, "requestID", requestID, "error", merr)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (n *NotifyCore) authorizeInboxAnswer(w http.ResponseWriter, r *http.Request, rowOwner string, pr *store.PendingRequest) (map[string]string, bool) {
	id, _ := auth.SubjectFromContext(r.Context())
	if id == nil || rowOwner != callerKey(id) {
		writeError(w, http.StatusForbidden, "only an approver can answer this request, from their own inbox", n.logger)
		return nil, false
	}
	var rec approverRecord
	if err := json.Unmarshal([]byte(*pr.Approvers), &rec); err != nil {
		writeServerError(w, "failed to read the request's approvers", err, n.logger)
		return nil, false
	}
	a := answerer{userID: id.UserID(), platformUserID: canonicalUserKey(id.PlatformUserID()), via: hilViaWeb}
	_, reason, err := n.authorizeWithRetry(r.Context(), pr.RequestID, rec, a)
	if err != nil {
		writeServerError(w, "failed to verify who is answering", err, n.logger)
		return nil, false
	}
	if reason != "" {
		n.auditApproval(r.Context(), auditevent.HILApprovalEvent{
			RequestID: pr.RequestID, Outcome: auditevent.HILApprovalRefused, Answerer: a.name(), Via: a.via, Reason: reason,
		}, rec)
		writeError(w, http.StatusForbidden, "you are not one of the approvers of this request", n.logger)
		return nil, false
	}
	return answererProperties(a), true
}

func parseHILMeta(payload string) (requestID string, surface json.RawMessage) {
	if payload == "" {
		return "", nil
	}
	var md map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &md); err != nil {
		return "", nil
	}
	var mode string
	if json.Unmarshal(md["mode"], &mode) != nil || mode != a2a.NotifyModeInput {
		return "", nil
	}
	if json.Unmarshal(md["request_id"], &requestID) != nil {
		return "", nil
	}
	return requestID, md["surface"]
}
