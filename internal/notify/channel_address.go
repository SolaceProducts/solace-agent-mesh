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
	"log/slog"
	"net/http"
	"net/mail"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

var resolveTimeout = 5 * time.Second

const (
	msgResolveNoUser       = "SAM could not find a %s account for your login email. Check that the email on your %s profile matches the one you sign in to SAM with"
	msgResolveMissingScope = "SAM is not allowed to look up %s users by email. Ask an administrator to check the %s entry point's token and permissions"
	msgResolveNoDM         = "SAM found your %s account but cannot send you a direct message there. Ask an administrator to check that the %s entry point may send direct messages"
	msgResolveUnavailable  = "SAM could not reach %s to find your account; try again"
	msgResolveNoGateway    = "the %s entry point is not responding; try again in a few minutes, or ask an administrator"
	msgResolveFailed       = "SAM could not look up your account right now; try again in a minute, or ask an administrator"
	msgNoLoginEmail        = "your SAM login has no single email address to look you up with; ask an administrator"
)

var channelDisplayNames = map[string]string{"slack": "Slack", "teams": "Microsoft Teams", "google_chat": "Google Chat"}

func writeNotSeenOnChannel(w http.ResponseWriter, channelType string, logger *slog.Logger) {
	name := channelDisplayName(channelType)
	writeError(w, http.StatusBadRequest, fmt.Sprintf(msgNotSeenOnChannel, name, name), logger)
}

func channelDisplayName(channelType string) string {
	if name, ok := channelDisplayNames[channelType]; ok {
		return name
	}
	return channelType
}

func callerEmail(r *http.Request) (string, bool) {
	id, found := auth.SubjectFromContext(r.Context())
	if !found || id == nil || id.Email() == "" {
		return "", false
	}
	parsed, err := mail.ParseAddress(id.Email())
	if err != nil || parsed.Name != "" || parsed.Address != id.Email() {
		return "", false
	}
	return parsed.Address, true
}

func (n *NotifyCore) candidateAddress(w http.ResponseWriter, r *http.Request, userID, channelType string) (string, bool) {
	if n.resolvesAddress(channelType) {
		return n.candidateFromGateway(w, r, userID, channelType)
	}
	return n.candidateFromPresence(w, r, userID, channelType)
}

func (n *NotifyCore) candidateFromGateway(w http.ResponseWriter, r *http.Request, userID, channelType string) (string, bool) {
	email, ok := callerEmail(r)
	if !ok {
		writeError(w, http.StatusBadRequest, msgNoLoginEmail, n.logger)
		return "", false
	}
	name := channelDisplayName(channelType)
	req := a2a.NotifyResolveRequest{RequestID: idgen.New(), UserID: userID, Email: email}
	resp, err := n.requestResolve(r, channelType, req)
	if err != nil {
		n.writeResolveRequestFailure(w, r, err, name, req.RequestID)
		return "", false
	}
	switch {
	case resp.Error == a2a.NotifyResolveNoUser:
		writeError(w, http.StatusBadRequest, fmt.Sprintf(msgResolveNoUser, name, name), n.logger)
	case resp.Error == a2a.NotifyResolveMissingScope:
		writeError(w, http.StatusBadRequest, fmt.Sprintf(msgResolveMissingScope, name, name), n.logger)
	case resp.Error == a2a.NotifyResolveNoDirectMessage:
		writeError(w, http.StatusBadRequest, fmt.Sprintf(msgResolveNoDM, name, name), n.logger)
	case resp.Error != "":
		cause := fmt.Errorf("gateway answered with a reason of %d bytes, known: %t", len(resp.Error), resp.Error == a2a.NotifyResolveUnavailable)
		n.writeResolveFailure(r.Context(), w, http.StatusBadGateway, slog.LevelWarn, fmt.Sprintf(msgResolveUnavailable, name), req.RequestID, cause)
	case !usableAddress(resp.Address):
		cause := fmt.Errorf("gateway broke the resolve contract: an address of %d bytes that is not a JSON object", len(resp.Address))
		n.writeResolveFailure(r.Context(), w, http.StatusBadGateway, slog.LevelError, msgResolveFailed, req.RequestID, cause)
	default:
		return n.recordCandidate(w, r, userID, channelType, email, string(resp.Address))
	}
	return "", false
}

func usableAddress(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && len(fields) > 0
}

func (n *NotifyCore) writeResolveRequestFailure(w http.ResponseWriter, r *http.Request, err error, name, requestID string) {
	switch {
	case r.Context().Err() != nil:
		n.writeResolveFailure(r.Context(), w, http.StatusServiceUnavailable, slog.LevelDebug, fmt.Sprintf(msgResolveNoGateway, name), requestID, err)
	case errors.Is(err, context.DeadlineExceeded):
		n.writeResolveFailure(r.Context(), w, http.StatusServiceUnavailable, slog.LevelWarn, fmt.Sprintf(msgResolveNoGateway, name), requestID, err)
	default:
		n.writeResolveFailure(r.Context(), w, http.StatusBadGateway, slog.LevelError, msgResolveFailed, requestID, err)
	}
}

func (n *NotifyCore) writeResolveFailure(ctx context.Context, w http.ResponseWriter, status int, level slog.Level, message, requestID string, cause error) {
	errID := idgen.New()
	n.logger.Log(ctx, level, "notify resolve: address lookup failed", "errorId", errID, "httpStatus", status, "requestID", requestID, "error", cause)
	writeJSON(w, status, map[string]any{"message": message, "errorId": errID})
}

func (n *NotifyCore) candidateFromPresence(w http.ResponseWriter, r *http.Request, userID, channelType string) (string, bool) {
	candidate, err := n.latestPresence(r.Context(), requestDestinationKeys(r, userID), channelType)
	if err != nil {
		writeServerError(w, "failed to look up channel", err, n.logger)
		return "", false
	}
	if candidate == nil {
		writeNotSeenOnChannel(w, channelType, n.logger)
		return "", false
	}
	if candidate.UserID != userID {
		recorded := *candidate
		recorded.UserID = userID
		if err := n.channels.Upsert(r.Context(), recorded); err != nil {
			writeServerError(w, msgStartFailed, err, n.logger)
			return "", false
		}
	}
	return candidate.Address, true
}

func (n *NotifyCore) latestPresence(ctx context.Context, keys []string, channelType string) (*store.UserChannel, error) {
	var latest *store.UserChannel
	for _, k := range keys {
		uc, err := n.channels.GetByType(ctx, k, channelType)
		if err != nil {
			return nil, err
		}
		if uc != nil && uc.Address != "" && (latest == nil || uc.UpdatedAt > latest.UpdatedAt) {
			latest = uc
		}
	}
	return latest, nil
}

func (n *NotifyCore) channelsUnderKeys(ctx context.Context, keys []string) ([]store.UserChannel, error) {
	var out []store.UserChannel
	for _, k := range keys {
		rows, err := n.channels.ListByUser(ctx, k)
		if err != nil {
			return nil, err
		}
		for _, uc := range rows {
			i := slices.IndexFunc(out, func(o store.UserChannel) bool { return o.ChannelType == uc.ChannelType })
			switch {
			case i < 0:
				out = append(out, uc)
			case !out[i].Confirmed() && uc.Confirmed():
				out[i] = uc
			}
		}
	}
	slices.SortStableFunc(out, func(a, b store.UserChannel) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	return out, nil
}

func (n *NotifyCore) recordCandidate(w http.ResponseWriter, r *http.Request, userID, channelType, email, address string) (string, bool) {
	if err := n.channels.Upsert(r.Context(), store.UserChannel{UserID: userID, ChannelType: channelType, Address: address, Email: email}); err != nil {
		writeServerError(w, msgStartFailed, err, n.logger)
		return "", false
	}
	return address, true
}

func (n *NotifyCore) requestResolve(r *http.Request, channelType string, req a2a.NotifyResolveRequest) (a2a.NotifyResolveResponse, error) {
	var resp a2a.NotifyResolveResponse
	req.ExpiresAt = isoFromMillis(time.Now().Add(resolveTimeout).UnixMilli())
	payload, err := json.Marshal(req)
	if err != nil {
		return resp, fmt.Errorf("marshal resolve request: %w", err)
	}
	reply, err := n.svc.Broker().Request(r.Context(), a2a.NotifyResolveTopic(n.cfg.Namespace, channelType), &broker.Message{
		Payload: payload,
		ReplyTo: a2a.NotifyResolveReplyTopic(n.cfg.Namespace, req.RequestID),
	}, resolveTimeout)
	if err != nil {
		return resp, fmt.Errorf("resolve address on %s: %w", channelType, err)
	}
	if reply == nil {
		return resp, errors.New("resolve address: empty reply")
	}
	if err := json.Unmarshal(reply.Payload, &resp); err != nil {
		return resp, fmt.Errorf("decode resolve reply: %w", err)
	}
	return resp, nil
}

func (n *NotifyCore) listedChannels(r *http.Request, userID string, rows []store.UserChannel, nowMillis int64) []UserChannelDTO {
	data := make([]UserChannelDTO, 0, len(rows)+1)
	started := make(map[string]bool, len(rows))
	for _, uc := range rows {
		started[uc.ChannelType] = true
		if uc.Confirmed() || uc.CodeOutstandingAt(nowMillis) || n.offersChannel(uc.ChannelType) {
			data = append(data, userChannelDTO(uc, nowMillis))
		}
	}
	if !n.listingOwnChannels(r, userID) {
		return data
	}
	for _, channelType := range n.capabilities.NotifiableTypes() {
		if !started[channelType] && gatewayDeliverable(channelType) {
			data = append(data, UserChannelDTO{ChannelType: channelType})
		}
	}
	return data
}

func (n *NotifyCore) offersChannel(channelType string) bool {
	c := n.capabilities.Capability(channelType)
	return c.Advertised && c.Notifiable
}

func (n *NotifyCore) listingOwnChannels(r *http.Request, userID string) bool {
	id, ok := auth.SubjectFromContext(r.Context())
	return ok && id != nil && isCaller(id, userID)
}

func (n *NotifyCore) resolvesAddress(channelType string) bool {
	return n.capabilities.Capability(channelType).ResolvesAddress
}
