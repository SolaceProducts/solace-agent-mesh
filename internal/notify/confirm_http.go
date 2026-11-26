// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const confirmCommitTimeout = 5 * time.Second

const (
	msgNotSeenOnChannel          = "Unable to enable notifications because Agent Mesh has not received a message from this user in %s. Send a message to the Agent Mesh application in %s first."
	msgChannelsDisabled          = "channels are not enabled (no database configured)"
	msgChannelNotConfirmable     = "the gateway for this channel does not accept notifications"
	msgChannelTypeNotConfirmable = "channel type does not support confirmation"
	msgStartFailed               = "failed to turn the channel on"
	msgDisableFailed             = "failed to turn the channel off"
	msgChannelTypeNotDisableable = "channel type cannot be turned off"
	msgNoticeFailed              = "the confirmation message could not be sent; try again"
	msgInvalidBodyPrefix         = "invalid request body: "
)

type UserChannelDTO struct {
	ChannelType string `json:"channelType"`
	Confirmed   bool   `json:"confirmed"`
	Pending     bool   `json:"pending"`
	Locked      bool   `json:"locked"`
	ConfirmedAt string `json:"confirmedAt,omitempty"`
}

type VerifyChannelCodeRequest struct {
	Code string `json:"code"`
}

func (n *NotifyCore) isConfirmableChannel(channelType string) bool {
	return !n.capabilities.Capability(channelType).DeclinesDelivery()
}

func userChannelDTO(uc store.UserChannel, nowMillis int64) UserChannelDTO {
	dto := UserChannelDTO{
		ChannelType: uc.ChannelType,
		Confirmed:   uc.Confirmed(),
		Pending:     uc.CodeUsableAt(nowMillis, maxVerificationAttempts),
		Locked:      uc.CodeLockedAt(nowMillis, maxVerificationAttempts),
	}
	if uc.ConfirmedAt > 0 {
		dto.ConfirmedAt = isoFromMillis(uc.ConfirmedAt)
	}
	return dto
}

func (n *NotifyCore) selfUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok || id == nil || id.UserID() == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", n.logger)
		return "", false
	}
	if q := r.URL.Query().Get("userId"); q != "" && !isCaller(id, q) {
		writeError(w, http.StatusForbidden, "channel confirmation cannot be performed for another user", n.logger)
		return "", false
	}
	return callerKey(id), true
}

func rejectRequestBody(r *http.Request) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var empty struct{}
	return dec.Decode(&empty)
}

func (n *NotifyCore) handleChannelsList(w http.ResponseWriter, r *http.Request) {
	if n.channels == nil {
		writeError(w, http.StatusServiceUnavailable, msgChannelsDisabled, n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifyReadOthers)
	if !ok {
		return
	}
	rows, err := n.channelsUnderKeys(r.Context(), requestDestinationKeys(r, userID))
	if err != nil {
		writeServerError(w, "failed to list channels", err, n.logger)
		return
	}
	data := n.listedChannels(r, userID, rows, time.Now().UnixMilli())
	pageNumber, pageSize := parsePagination(r)
	writeJSON(w, http.StatusOK, PaginatedResponse[UserChannelDTO]{
		Data: data,
		Meta: paginationMeta{Pagination: newPaginationInfo(pageNumber, pageSize, len(data))},
	})
}

func (n *NotifyCore) handleChannelConfirmStart(w http.ResponseWriter, r *http.Request) {
	if n.channels == nil {
		writeError(w, http.StatusServiceUnavailable, msgChannelsDisabled, n.logger)
		return
	}
	userID, ok := n.selfUserID(w, r)
	if !ok {
		return
	}
	channelType := r.PathValue("channelType")
	if !gatewayDeliverable(channelType) {
		writeError(w, http.StatusBadRequest, msgChannelTypeNotConfirmable, n.logger)
		return
	}
	if !n.isConfirmableChannel(channelType) {
		writeError(w, http.StatusBadRequest, msgChannelNotConfirmable, n.logger)
		return
	}
	if err := rejectRequestBody(r); err != nil {
		writeError(w, http.StatusBadRequest, msgInvalidBodyPrefix+err.Error(), n.logger)
		return
	}

	address, ok := n.candidateAddress(w, r, userID, channelType)
	if !ok {
		return
	}
	current, err := n.channels.GetByType(r.Context(), userID, channelType)
	if err != nil {
		writeServerError(w, msgStartFailed, err, n.logger)
		return
	}
	if current != nil && current.ConfirmedAddress == address && !current.CodeOutstandingAt(time.Now().UnixMilli()) {
		n.writeConfirmedChannel(w, r, userID, channelType)
		return
	}

	if sendErr := n.sendTurnedOnNotice(r.Context(), userID, channelType, address); sendErr != nil {
		writeServerErrorStatus(w, http.StatusBadGateway, msgNoticeFailed, sendErr, n.logger)
		return
	}
	commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(r.Context()), confirmCommitTimeout)
	defer cancelCommit()
	err = n.channels.ConfirmAddress(commitCtx, userID, channelType, address)
	if errors.Is(err, store.ErrNoCandidateAddress) {
		writeNotSeenOnChannel(w, channelType, n.logger)
		return
	}
	if err != nil {
		writeServerError(w, msgStartFailed, err, n.logger)
		return
	}
	n.logger.Info("notify: channel turned on", "userID", userID, "channelType", channelType)
	n.writeConfirmedChannel(w, r, userID, channelType)
}

func (n *NotifyCore) handleChannelConfirmVerify(w http.ResponseWriter, r *http.Request) {
	if n.channels == nil {
		writeError(w, http.StatusServiceUnavailable, msgChannelsDisabled, n.logger)
		return
	}
	userID, ok := n.selfUserID(w, r)
	if !ok {
		return
	}
	channelType := r.PathValue("channelType")
	var req VerifyChannelCodeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, msgInvalidBodyPrefix+err.Error(), n.logger)
		return
	}
	if normalizeVerificationCode(req.Code) == "" {
		writeError(w, http.StatusBadRequest, "code is required", n.logger)
		return
	}

	outcome, err := n.channels.ConfirmByCode(r.Context(), userID, channelType,
		hashVerificationCode(userID, channelType, req.Code), maxVerificationAttempts)
	if err != nil {
		writeServerError(w, "failed to confirm channel", err, n.logger)
		return
	}
	switch outcome {
	case store.ConfirmOK:
		n.logger.Info("notify: channel confirmed", "userID", userID, "channelType", channelType)
		n.writeConfirmedChannel(w, r, userID, channelType)
	case store.ConfirmAlreadyConfirmed:
		n.writeConfirmedChannel(w, r, userID, channelType)
	case store.ConfirmInvalid:
		writeError(w, http.StatusBadRequest, "that code is not correct; turn the channel on instead", n.logger)
	case store.ConfirmExpired:
		writeError(w, http.StatusBadRequest, "that code has expired; turn the channel on instead", n.logger)
	case store.ConfirmLocked:
		n.logger.Warn("notify: channel confirmation refused: code locked after too many incorrect attempts",
			"userID", userID, "channelType", channelType, "maxAttempts", maxVerificationAttempts)
		writeError(w, http.StatusBadRequest, "too many incorrect attempts; turn the channel on instead", n.logger)
	default:
		writeServerError(w, "failed to confirm channel", fmt.Errorf("unhandled confirm outcome %d", outcome), n.logger)
	}
}

func (n *NotifyCore) writeConfirmedChannel(w http.ResponseWriter, r *http.Request, userID, channelType string) {
	uc, err := n.channels.GetByType(r.Context(), userID, channelType)
	switch {
	case err != nil:
		n.logger.Warn("notify: confirmed channel could not be read back",
			"userID", userID, "channelType", channelType, "error", err)
	case uc == nil:
		n.logger.Warn("notify: confirmed channel missing on read back",
			"userID", userID, "channelType", channelType)
	default:
		writeJSON(w, http.StatusOK, userChannelDTO(*uc, time.Now().UnixMilli()))
		return
	}
	writeJSON(w, http.StatusOK, UserChannelDTO{ChannelType: channelType, Confirmed: true})
}

func (n *NotifyCore) handleChannelDisable(w http.ResponseWriter, r *http.Request) {
	if n.channels == nil {
		writeError(w, http.StatusServiceUnavailable, msgChannelsDisabled, n.logger)
		return
	}
	userID, ok := n.selfUserID(w, r)
	if !ok {
		return
	}
	channelType := r.PathValue("channelType")
	if !gatewayDeliverable(channelType) {
		writeError(w, http.StatusBadRequest, msgChannelTypeNotDisableable, n.logger)
		return
	}
	if err := rejectRequestBody(r); err != nil {
		writeError(w, http.StatusBadRequest, msgInvalidBodyPrefix+err.Error(), n.logger)
		return
	}
	if err := n.channels.ClearConfirmation(r.Context(), userID, channelType); err != nil {
		writeServerError(w, msgDisableFailed, err, n.logger)
		return
	}
	n.logger.Info("notify: channel turned off", "userID", userID, "channelType", channelType)

	uc, err := n.channels.GetByType(r.Context(), userID, channelType)
	if err != nil || uc == nil {
		writeJSON(w, http.StatusOK, UserChannelDTO{ChannelType: channelType})
		return
	}
	writeJSON(w, http.StatusOK, userChannelDTO(*uc, time.Now().UnixMilli()))
}

func (n *NotifyCore) sendTurnedOnNotice(ctx context.Context, userID, channelType, address string) error {
	return n.PublishDeliver(ctx, channelType, a2a.NotifyDeliver{
		RequestID:   idgen.New(),
		Mode:        a2a.NotifyModeNotify,
		UserID:      userID,
		ChannelType: channelType,
		Address:     json.RawMessage(address),
		Title:       "SAM notifications are on",
		Message:     "SAM can now send your notifications here. To choose which ones you get, open SAM and go to Settings → Notifications.",
	})
}
