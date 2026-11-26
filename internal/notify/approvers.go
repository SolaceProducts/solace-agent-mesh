// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

const (
	maxApproverUsers  = 20
	maxApproverPlaces = 5

	hilViaWeb     = "web"
	hilViaUnknown = "unknown"

	systemPrincipalPrefix = "system:"
)

const (
	refusalApproversDisabled         = a2a.NotifyRefusalApproverSetsDisabled
	refusalApproverGroups            = a2a.NotifyRefusalApproverGroups
	refusalApproverNoUser            = a2a.NotifyRefusalApproverNoUser
	refusalApproverTooMany           = a2a.NotifyRefusalApproverTooMany
	refusalApproverSystem            = a2a.NotifyRefusalApproverSystem
	refusalApproverInvalid           = a2a.NotifyRefusalApproverInvalid
	refusalApproverPlaceInvalid      = a2a.NotifyRefusalApproverPlaceInvalid
	refusalApproverNotInput          = a2a.NotifyRefusalApproverNotInput
	refusalDeliveryFailed            = a2a.NotifyRefusalDeliveryFailed
	refusalApproverStoreUnavailable  = a2a.NotifyRefusalApproverStoreDown
	refusalApproverLookupUnavailable = a2a.NotifyRefusalApproverLookupDown

	refusalUnverifiedAnswerer = "answerer_unverified"
	refusalSystemAnswerer     = "answerer_is_system_principal"
	refusalNotAnApprover      = "answerer_not_an_approver"
	refusalUnreadableSet      = "approver_set_unreadable"
)

type approverRecord struct {
	Users  []approverUser    `json:"users"`
	Places []a2a.NotifyPlace `json:"places,omitempty"`
}

type approverUser struct {
	UserID         string `json:"user_id"`
	PlatformUserID string `json:"platform_user_id,omitempty"`
	Name           string `json:"name,omitempty"`
}

type answerer struct {
	userID         string
	platformUserID string
	via            string
}

func (a answerer) name() string { return cmp.Or(a.userID, a.platformUserID) }

func answererOf(msg *broker.Message) answerer {
	up := msg.UserProperties
	if up == nil {
		return answerer{via: hilViaUnknown}
	}
	a := answerer{userID: up[a2a.PropUserID], via: up[a2a.PropChannel]}
	if u, err := uuid.Parse(up[a2a.PropUserUUID]); err == nil {
		a.platformUserID = u.String()
	}
	if !a2a.ValidNotifyChannelType(a.via) {
		a.via = hilViaUnknown
	}
	return a
}

func answererProperties(a answerer) map[string]string {
	props := map[string]string{a2a.PropChannel: a.via}
	if a.userID != "" {
		props[a2a.PropUserID] = a.userID
	}
	if a.platformUserID != "" {
		props[a2a.PropUserUUID] = a.platformUserID
	}
	return props
}

func isSystemPrincipal(id string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), systemPrincipalPrefix)
}

func validateApprovers(set *a2a.NotifyApprovers) string {
	switch {
	case len(set.Groups) > 0:
		return refusalApproverGroups
	case len(set.UserIDs) == 0:
		return refusalApproverNoUser
	case len(set.UserIDs) > maxApproverUsers || len(set.Places) > maxApproverPlaces:
		return refusalApproverTooMany
	}
	for _, id := range set.UserIDs {
		switch {
		case isSystemPrincipal(id):
			return refusalApproverSystem
		case strings.TrimSpace(id) == "" || id != strings.TrimSpace(id):
			return refusalApproverInvalid
		}
	}
	for _, p := range set.Places {
		if !gatewayDeliverable(p.ChannelType) || !isJSONObject(p.Address) {
			return refusalApproverPlaceInvalid
		}
	}
	return ""
}

func isJSONObject(raw json.RawMessage) bool {
	var m map[string]any
	return json.Unmarshal(raw, &m) == nil && len(m) > 0
}

func (n *NotifyCore) approverSetsEnabled() bool {
	reg := n.svc.FeatureRegistry()
	return reg != nil && reg.IsEnabled(features.KeyHILApproverSets)
}

func (n *NotifyCore) resolveApprovers(ctx context.Context, requestID string, set *a2a.NotifyApprovers) (approverRecord, error) {
	rec := approverRecord{Places: set.Places}
	for _, id := range set.UserIDs {
		u := approverUser{UserID: canonicalUserKey(id), Name: strings.TrimSpace(set.Names[id])}
		if _, err := uuid.Parse(id); err == nil {
			u.PlatformUserID = u.UserID
		} else if n.db != nil {
			err := n.retryTransient(ctx, "look up approver alias", requestID, func() error {
				platformID, lerr := n.db.PlatformUserIDFor(ctx, id)
				u.PlatformUserID = platformID
				return lerr
			})
			if err != nil {
				return approverRecord{}, fmt.Errorf("look up an approver's platform id: %w", err)
			}
		}
		rec.Users = append(rec.Users, u)
	}
	return rec, nil
}

func (n *NotifyCore) authorize(ctx context.Context, rec approverRecord, a answerer) (approverUser, string, error) {
	switch {
	case a.userID == "" && a.platformUserID == "":
		return approverUser{}, refusalUnverifiedAnswerer, nil
	case isSystemPrincipal(a.userID):
		return approverUser{}, refusalSystemAnswerer, nil
	}
	platformID := a.platformUserID
	if platformID == "" && a.userID != "" && n.db != nil {
		id, err := n.db.PlatformUserIDFor(ctx, a.userID)
		if err != nil {
			return approverUser{}, "", fmt.Errorf("look up the answerer's platform id: %w", err)
		}
		platformID = id
	}
	for _, u := range rec.Users {
		if a.userID != "" && a.userID == u.UserID {
			return u, "", nil
		}
		if platformID != "" && platformID == u.PlatformUserID {
			return u, "", nil
		}
	}
	return approverUser{}, refusalNotAnApprover, nil
}

func (n *NotifyCore) authorizeWithRetry(ctx context.Context, requestID string, rec approverRecord, a answerer) (approver approverUser, reason string, err error) {
	err = n.retryTransient(ctx, "verify answerer", requestID, func() error {
		var aerr error
		approver, reason, aerr = n.authorize(ctx, rec, a)
		return aerr
	})
	return approver, reason, err
}

func (n *NotifyCore) handleApproverSetRequest(ctx context.Context, req a2a.NotifyRequest, refuseNow bool) (retry bool) {
	if req.RequesterAddr == "" {
		n.logger.Error("notify: approver-set request without requester_addr; cannot route a reply", "requestID", req.RequestID)
		return false
	}
	reason := refusalApproversDisabled
	if n.approverSetsEnabled() {
		reason = validateApprovers(req.Approvers)
	}
	if reason != "" {
		return !n.refuseApproverSetRequest(ctx, req, reason)
	}
	rec, err := n.resolveApprovers(ctx, req.RequestID, req.Approvers)
	if err != nil {
		return n.retryOrRefuse(ctx, req, refuseNow, refusalApproverLookupUnavailable, err)
	}
	stored, err := json.Marshal(rec)
	if err != nil {
		n.logger.Error("notify: encode approver set failed; dropping", "requestID", req.RequestID, "error", err)
		return false
	}
	storedSet := string(stored)
	pr := store.PendingRequest{
		RequestID:     req.RequestID,
		RequesterAddr: req.RequesterAddr,
		ExpiresAt:     parseExpiresAtMillis(req.ExpiresAt),
		Approvers:     &storedSet,
	}
	if req.Dedup != "" {
		pr.Dedup = &req.Dedup
	}
	switch err := n.insertPendingWithRetry(ctx, pr); {
	case errors.Is(err, store.ErrDuplicate):
		n.logger.Info("notify: duplicate HIL request dropped (dedup)", "requestID", req.RequestID, "dedup", req.Dedup)
		return false
	case err != nil:
		return n.retryOrRefuse(ctx, req, refuseNow, refusalApproverStoreUnavailable, err)
	}
	n.auditApproval(ctx, auth.HILApprovalEvent{RequestID: req.RequestID, Outcome: auth.HILApprovalAsked}, rec)

	reachedUsers := n.deliverToApprovers(ctx, req, rec)
	n.logger.Info("notify: approver-set prompt delivered",
		"requestID", req.RequestID, "approverCount", len(rec.Users), "placeCount", len(rec.Places), "sentCount", reachedUsers)
	if reachedUsers > 0 {
		return false
	}
	n.auditApproval(ctx, auth.HILApprovalEvent{RequestID: req.RequestID, Outcome: auth.HILApprovalUndeliverable, Reason: refusalDeliveryFailed}, rec)
	n.failUndeliverable(ctx, req.RequestID)
	n.announceResolution(ctx, req.RequestID, rec, nil, a2a.NotifyOutcomeFailed, "")
	return false
}

func (n *NotifyCore) retryOrRefuse(ctx context.Context, req a2a.NotifyRequest, refuseNow bool, reason string, cause error) (retry bool) {
	switch {
	case ctx.Err() != nil:
		n.logger.Warn("notify: approver-set request interrupted by shutdown; leaving it to its redelivery or the requester's timeout",
			"requestID", req.RequestID, "reason", reason, "error", cause)
		return true
	case !refuseNow:
		n.logger.Warn("notify: approver-set request could not be handled; returning it to the queue for one more attempt",
			"requestID", req.RequestID, "reason", reason, "error", cause)
		return true
	}
	n.logger.Error("notify: approver-set request could not be handled and cannot be retried; failing it",
		"requestID", req.RequestID, "reason", reason, "error", cause)
	return !n.refuseApproverSetRequest(ctx, req, reason)
}

func (n *NotifyCore) refuseApproverSetRequest(ctx context.Context, req a2a.NotifyRequest, reason string) (published bool) {
	n.logger.Warn("notify: approver-set request refused",
		"requestID", req.RequestID, "reason", reason,
		"approverCount", len(req.Approvers.UserIDs), "groupCount", len(req.Approvers.Groups), "placeCount", len(req.Approvers.Places))
	if n.firstTime(ctx, "request-refused:"+req.RequestID) {
		n.auditApproval(ctx, auth.HILApprovalEvent{RequestID: req.RequestID, Outcome: auth.HILApprovalRequestDenied, Reason: reason},
			approverRecord{Users: listedUsers(req.Approvers.UserIDs), Places: req.Approvers.Places})
	}
	if err := n.routeResponse(ctx, req.RequesterAddr, a2a.UserInputResponseData{
		RequestID: req.RequestID,
		Status:    "cancelled",
		Data:      map[string]any{"reason": reason},
	}); err != nil {
		n.logger.Error("notify: failed to route approver-set refusal; requester will rely on its own timeout",
			"requestID", req.RequestID, "error", err)
		return false
	}
	return true
}

func listedUsers(ids []string) []approverUser {
	users := make([]approverUser, 0, len(ids))
	for _, id := range ids {
		users = append(users, approverUser{UserID: id})
	}
	return users
}

func (n *NotifyCore) deliverToApprovers(ctx context.Context, req a2a.NotifyRequest, rec approverRecord) int {
	userDeliveries := 0
	for _, u := range rec.Users {
		for _, r := range n.dispatcher.Deliver(ctx, approverRequest(req, u)) {
			if r.Status == "sent" {
				userDeliveries++
			}
		}
	}
	for _, p := range rec.Places {
		n.postToPlace(ctx, req.RequestID, p, a2a.NotifyDeliver{
			Mode:      a2a.NotifyModeInput,
			Title:     req.Title,
			Message:   req.Message,
			Surface:   req.Surface,
			ExpiresAt: req.ExpiresAt,
		})
	}
	return userDeliveries
}

func approverRequest(req a2a.NotifyRequest, u approverUser) a2a.NotifyRequest {
	r := req
	r.Dedup = ""
	r.RecipientUserID, r.RecipientPlatformUserID = u.UserID, u.PlatformUserID
	if r.RecipientUserID == r.RecipientPlatformUserID {
		r.RecipientUserID = ""
	}
	return r
}

func (n *NotifyCore) postToPlace(ctx context.Context, requestID string, p a2a.NotifyPlace, d a2a.NotifyDeliver) bool {
	if n.capabilities == nil || !n.capabilities.Capability(p.ChannelType).ApproverSets {
		n.logger.Warn("notify: place skipped: no live gateway of its type handles approver sets",
			"requestID", requestID, "channelType", p.ChannelType)
		return false
	}
	d.RequestID = requestID
	d.ChannelType = p.ChannelType
	d.Address = p.Address
	d.ApproverSet = true
	if err := n.PublishDeliver(ctx, p.ChannelType, d); err != nil {
		n.logger.Error("notify: place deliver publish failed", "requestID", requestID, "channelType", p.ChannelType, "error", err)
		return false
	}
	return true
}

func (n *NotifyCore) failUndeliverable(ctx context.Context, requestID string) {
	addr, claimed, err := n.pending.Claim(ctx, requestID, "failed")
	switch {
	case err != nil:
		n.logger.Error("notify: failed to mark undeliverable HIL request failed; requester will rely on its own timeout",
			"requestID", requestID, "error", err)
	case claimed:
		if rerr := n.routeResponse(ctx, addr, a2a.UserInputResponseData{
			RequestID: requestID,
			Status:    "cancelled",
			Data:      map[string]any{"reason": refusalDeliveryFailed},
		}); rerr != nil {
			n.logger.Error("notify: failed to route delivery_failed to requester; requester will rely on its own timeout",
				"requestID", requestID, "error", rerr)
		}
	}
}

func (n *NotifyCore) handleApproverReply(ctx context.Context, msg *broker.Message, resp a2a.UserInputResponseData, pr *store.PendingRequest) {
	a := answererOf(msg)
	event := auth.HILApprovalEvent{RequestID: resp.RequestID, Answerer: a.name(), Via: a.via, Decision: decisionOf(resp)}
	var rec approverRecord
	if err := json.Unmarshal([]byte(*pr.Approvers), &rec); err != nil {
		n.logger.Error("notify: stored approver set is unreadable; refusing the answer", "requestID", resp.RequestID, "error", err)
		event.Outcome, event.Reason = auth.HILApprovalRefused, refusalUnreadableSet
		n.auditApproval(ctx, event, rec)
		n.tellOnFirstDelivery(ctx, msg, resp.RequestID, msgApproversUnreadable)
		msg.Ack()
		return
	}
	approver, reason, err := n.authorizeWithRetry(ctx, resp.RequestID, rec, a)
	if err != nil {
		n.logger.Error("notify: could not verify the answerer after retries; nacking for redelivery", "requestID", resp.RequestID, "error", err)
		n.tellOnFirstDelivery(ctx, msg, resp.RequestID, msgNotRecorded)
		msg.Nack()
		return
	}
	if reason != "" {
		n.logger.Warn("notify: HIL answer refused: answerer is not in the approver set",
			"requestID", resp.RequestID, "reason", reason, "via", a.via)
		event.Outcome, event.Reason = auth.HILApprovalRefused, reason
		n.auditApproval(ctx, event, rec)
		n.tellClicker(ctx, msg, resp.RequestID, a2a.NotifyPromptOutcome{Refusal: refusalNotCounted(reason)})
		msg.Ack()
		return
	}
	if pr.Status != "pending" {
		n.answerOnSettledRequest(ctx, msg, resp, event, rec, approver)
		return
	}
	res := store.Resolution{
		Status: resp.Status, Decision: answerOutcome(resp), AnsweredBy: a.name(),
		AnsweredByName: approverName(approver, a), AnsweredVia: a.via,
	}
	addr, claimed, err := n.claimResolutionWithRetry(ctx, resp.RequestID, store.ApproverSet(*pr.Approvers), res)
	switch {
	case err != nil:
		n.logger.Error("notify: claim on HIL reply failed after retries; nacking for redelivery", "requestID", resp.RequestID, "error", err)
		n.tellOnFirstDelivery(ctx, msg, resp.RequestID, msgNotRecorded)
		msg.Nack()
		return
	case !claimed:
		n.answerOnSettledRequest(ctx, msg, resp, event, rec, approver)
		return
	}
	if rerr := n.routeResponse(ctx, addr, resp); rerr != nil {
		if oerr := n.pending.Reopen(ctx, resp.RequestID, resp.Status); oerr != nil {
			n.logger.Error("notify: failed to re-open HIL request after route failure", "requestID", resp.RequestID, "error", oerr)
		}
		n.tellOnFirstDelivery(ctx, msg, resp.RequestID, msgNotRecorded)
		msg.Nack()
		return
	}
	n.auditAcceptedOnce(ctx, event, rec)
	n.announceResolution(ctx, resp.RequestID, rec, &approver, outcomeOf(res.Status, res.Decision), res.AnsweredByName)
	msg.Ack()
}

func (n *NotifyCore) answerOnSettledRequest(ctx context.Context, msg *broker.Message, resp a2a.UserInputResponseData, event auth.HILApprovalEvent, rec approverRecord, approver approverUser) {
	var current *store.PendingRequest
	err := n.retryTransient(ctx, "read settled request", event.RequestID, func() (gerr error) {
		current, gerr = n.pending.Get(ctx, event.RequestID)
		return gerr
	})
	if err != nil {
		n.logger.Error("notify: lookup on a settled HIL request failed after retries; nacking for redelivery", "requestID", event.RequestID, "error", err)
		n.tellOnFirstDelivery(ctx, msg, event.RequestID, msgNotRecorded)
		msg.Nack()
		return
	}
	if isCountedAnswer(current, event.Answerer, resp) {
		n.finishCountedAnswer(ctx, msg, resp, event, rec, approver, current)
		return
	}
	n.refuseLateAnswer(ctx, msg, event, rec, current)
}

func isCountedAnswer(pr *store.PendingRequest, answerer string, resp a2a.UserInputResponseData) bool {
	return pr != nil && answerer != "" && pr.Status == resp.Status &&
		deref(pr.AnsweredBy) == answerer && deref(pr.Decision) == answerOutcome(resp)
}

func (n *NotifyCore) finishCountedAnswer(ctx context.Context, msg *broker.Message, resp a2a.UserInputResponseData, event auth.HILApprovalEvent, rec approverRecord, approver approverUser, counted *store.PendingRequest) {
	if rerr := n.routeResponse(ctx, counted.RequesterAddr, resp); rerr != nil {
		n.tellOnFirstDelivery(ctx, msg, event.RequestID, msgCountedPassOnRetrying)
		msg.Nack()
		return
	}
	n.auditAcceptedOnce(ctx, event, rec)
	n.announceResolution(ctx, event.RequestID, rec, &approver, outcomeOf(counted.Status, deref(counted.Decision)), deref(counted.AnsweredByName))
	msg.Ack()
}

func (n *NotifyCore) tellOnFirstDelivery(ctx context.Context, msg *broker.Message, requestID, refusal string) {
	if msg.Redelivered {
		return
	}
	n.tellClicker(ctx, msg, requestID, a2a.NotifyPromptOutcome{Refusal: refusal, Retryable: refusal == msgNotRecorded || refusal == msgCountedPassOnRetrying})
}

func (n *NotifyCore) refuseLateAnswer(ctx context.Context, msg *broker.Message, event auth.HILApprovalEvent, rec approverRecord, current *store.PendingRequest) {
	n.logger.Info("notify: HIL reply for an already-resolved request; dropping", "requestID", event.RequestID, "via", event.Via)
	event.Outcome = auth.HILApprovalLate
	n.auditApproval(ctx, event, rec)
	settled := a2a.NotifyPromptOutcome{Refusal: msgNoLongerOpen}
	if current != nil {
		settled = resolvedOutcome(current)
		if current.Status == "pending" {
			settled = a2a.NotifyPromptOutcome{Outcome: a2a.NotifyOutcomeExpired, Text: outcomeText(a2a.NotifyOutcomeExpired, "")}
		}
		settled.Refusal = msgAnswerTooLate + settled.Text + "."
	}
	n.tellClicker(ctx, msg, event.RequestID, settled)
	msg.Ack()
}

func (n *NotifyCore) claimResolutionWithRetry(ctx context.Context, requestID string, guard store.ClaimGuard, res store.Resolution) (string, bool, error) {
	return retryClaim(ctx, func() (string, bool, error) {
		return n.pending.ClaimResolution(ctx, requestID, guard, res)
	})
}

func decisionOf(resp a2a.UserInputResponseData) string {
	if resp.Status == "answered" {
		if approved, ok := resp.Data["approved"].(bool); ok {
			if approved {
				return "approved"
			}
			return "denied"
		}
	}
	return resp.Status
}

func (n *NotifyCore) announceResolution(ctx context.Context, requestID string, rec approverRecord, by *approverUser, outcome, name string) {
	settled := a2a.NotifyPromptOutcome{Outcome: outcome, Text: outcomeText(outcome, name)}
	posted := n.resolveCards(ctx, requestID, settled)
	if by != nil {
		for _, u := range rec.Users {
			if u == *by {
				continue
			}
			notice := approverRequest(a2a.NotifyRequest{
				RequestID: idgen.New(),
				Mode:      a2a.NotifyModeNotify,
				Category:  a2a.CategoryHIL,
				Title:     msgResolvedTitle,
				Message:   settled.Text + ".",
				Channels:  []string{hilViaWeb},
				Metadata:  map[string]any{"resolves_request_id": requestID},
			}, u)
			notice.Dedup = resolvedNoticeDedup(requestID)
			n.dispatcher.Deliver(ctx, notice)
		}
	}
	if posted || by == nil {
		return
	}
	if !n.firstTime(ctx, "place-resolved:"+requestID) {
		return
	}
	for _, p := range rec.Places {
		n.postToPlace(ctx, requestID, p, a2a.NotifyDeliver{Mode: a2a.NotifyModeNotify, Title: msgResolvedTitle, Message: settled.Text + "."})
	}
}

const onceClaimOwner = "hil-approval"

func (n *NotifyCore) firstTime(ctx context.Context, key string) bool {
	if n.inbox == nil {
		return true
	}
	claimed, err := n.inbox.ClaimDedup(ctx, onceClaimOwner, key)
	if err != nil {
		n.logger.Warn("notify: once-per-request claim failed; proceeding", "key", strings.SplitN(key, ":", 2)[0], "error", err)
		return true
	}
	return claimed
}

func (n *NotifyCore) auditAcceptedOnce(ctx context.Context, event auth.HILApprovalEvent, rec approverRecord) {
	if !n.firstTime(ctx, "accepted:"+event.RequestID) {
		return
	}
	event.Outcome = auth.HILApprovalAccepted
	n.auditApproval(ctx, event, rec)
}

func resolvedNoticeDedup(requestID string) string { return "approval-resolved:" + requestID }

func (n *NotifyCore) warnIfApprovalsNotAudited() {
	if !n.approverSetsEnabled() {
		return
	}
	logger := n.svc.AuditLogger()
	if _, isNoop := logger.(auth.NoopAuditLogger); isNoop {
		return
	}
	if _, ok := logger.(auth.HILApprovalAuditor); !ok {
		n.logger.Warn("notify: the audit logger does not record approver-set events; refusals reach the operational log only")
	}
}

func (n *NotifyCore) auditApproval(ctx context.Context, event auth.HILApprovalEvent, rec approverRecord) {
	auditor, ok := n.svc.AuditLogger().(auth.HILApprovalAuditor)
	if !ok {
		return
	}
	for _, u := range rec.Users {
		event.Approvers = append(event.Approvers, userKey(u.PlatformUserID, u.UserID))
	}
	for _, p := range rec.Places {
		event.PlaceTypes = append(event.PlaceTypes, p.ChannelType)
	}
	slices.Sort(event.PlaceTypes)
	auditor.LogHILApproval(ctx, event)
}
