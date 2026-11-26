// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

var notifyDeliverOptions = entrypoint.NotifyDeliverOptions{GatewayType: "slack", Interactable: true, ApproverSets: true}

func (a *SlackGatewayAdapter) startNotifyDeliverLoop(ctx context.Context) {
	a.core.StartNotifyDeliverConsumer(ctx, notifyDeliverOptions, a.logger, a.handleNotifyDeliver)
	a.core.StartNotifyResolveConsumer(ctx, notifyDeliverOptions.GatewayType, a.logger, a.resolveNotifyAddress)
}

func (a *SlackGatewayAdapter) resolveNotifyAddress(ctx context.Context, req a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
	user, err := a.api.GetUserByEmail(ctx, req.Email)
	if err != nil {
		reason, level := slackResolveFailure(err)
		a.logger.Log(ctx, level, "notify resolve: Slack lookup by email failed", "requestID", req.RequestID, "userID", req.UserID, "reason", reason, "error", err)
		return a2a.NotifyResolveResponse{Error: reason}
	}
	if user == nil || user.ID == "" || user.Deleted || user.IsBot {
		a.logger.Debug("notify resolve: no usable Slack account for the login email", "requestID", req.RequestID, "userID", req.UserID)
		return a2a.NotifyResolveResponse{Error: a2a.NotifyResolveNoUser}
	}
	if _, err = a.api.OpenDMChannel(ctx, user.ID); err != nil {
		reason, level := slackDMFailure(err)
		a.logger.Log(ctx, level, "notify resolve: cannot open a Slack direct message", "requestID", req.RequestID, "userID", req.UserID, "reason", reason, "error", err)
		return a2a.NotifyResolveResponse{Error: reason}
	}
	address, err := json.Marshal(slackProactiveAddress{TeamID: user.TeamID, UserID: user.ID})
	if err != nil {
		return a2a.NotifyResolveResponse{Error: a2a.NotifyResolveUnavailable}
	}
	return a2a.NotifyResolveResponse{Address: address}
}

func slackDMFailure(err error) (string, slog.Level) {
	switch slackErrorCode(err) {
	case "missing_scope", "not_allowed_token_type", "no_permission", "access_denied", "team_access_not_granted",
		"user_not_visible", "restricted_action", "invalid_users":
		return a2a.NotifyResolveNoDirectMessage, slog.LevelWarn
	case "user_disabled", "user_not_found", "cannot_dm_bot":
		return a2a.NotifyResolveNoUser, slog.LevelDebug
	default:
		return slackResolveFailure(err)
	}
}

var slackAuthFailures = []string{
	"invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive",
	"no_permission", "not_allowed_token_type", "access_denied", "team_access_not_granted", "org_login_required",
}

func slackErrorCode(err error) string {
	var apiErr goslack.SlackErrorResponse
	if errors.As(err, &apiErr) {
		return apiErr.Err
	}
	return err.Error()
}

func slackResolveFailure(err error) (string, slog.Level) {
	code := slackErrorCode(err)
	switch {
	case code == "users_not_found":
		return a2a.NotifyResolveNoUser, slog.LevelDebug
	case code == "missing_scope":
		return a2a.NotifyResolveMissingScope, slog.LevelWarn
	case slices.Contains(slackAuthFailures, code):
		return a2a.NotifyResolveMissingScope, slog.LevelError
	default:
		return a2a.NotifyResolveUnavailable, slog.LevelWarn
	}
}

func (a *SlackGatewayAdapter) publishPresence(ctx context.Context, userID, email string, addr slackProactiveAddress) {
	if a.core.NotifyResolverBound() {
		return
	}
	a.core.PublishNotifyPresence(ctx, notifyDeliverOptions.GatewayType, userID, email, addr)
}

func (a *SlackGatewayAdapter) handleNotifyDeliver(d a2a.NotifyDeliver) {
	if d.PromptOutcome != nil {
		a.showPromptOutcome(d)
		return
	}
	if d.ApproverSet && d.UserID == "" {
		a.deliverToPlace(d)
		return
	}
	var addr slackProactiveAddress
	if err := json.Unmarshal(d.Address, &addr); err != nil || addr.UserID == "" {
		a.logger.Error("notify deliver: unusable slack address; dropping", "requestID", d.RequestID, "userID", d.UserID, "error", err)
		return
	}
	dm, err := a.api.OpenDMChannel(a.ctx, addr.UserID)
	if err != nil || dm == "" {
		a.logger.Warn("notify deliver: cannot open Slack DM; skipping out-of-band (inbox unaffected)", "requestID", d.RequestID, "userID", d.UserID, "slackUserID", addr.UserID, "error", err)
		return
	}
	addr.ChannelID = dm
	if d.Mode == a2a.NotifyModeInput {
		a.deliverProactiveHIL(d, addr)
	} else {
		a.deliverProactiveNotice(d, addr)
	}
}

func (a *SlackGatewayAdapter) deliverToPlace(d a2a.NotifyDeliver) {
	var addr slackProactiveAddress
	if err := json.Unmarshal(d.Address, &addr); err != nil || addr.ChannelID == "" {
		a.logger.Error("notify deliver: unusable slack place address; dropping", "requestID", d.RequestID, "error", err)
		return
	}
	addr.UserID = ""
	if d.Mode == a2a.NotifyModeInput {
		a.deliverProactiveHIL(d, addr)
		return
	}
	d.Artifacts = nil
	a.deliverProactiveNotice(d, addr)
}

func (a *SlackGatewayAdapter) deliverProactiveHIL(d a2a.NotifyDeliver, addr slackProactiveAddress) {
	surface, err := a2ui.Parse(d.Surface)
	if err != nil {
		a.logger.Error("notify deliver: parse surface failed", "requestID", d.RequestID, "error", err)
		return
	}
	blocks, ok := a.buildHILBlocksFor(surface, slackHILValue{RequestID: d.RequestID, Notify: true, ApproverSet: d.ApproverSet})
	if ok {
		channelID, ts, err := a.api.PostMessage(a.ctx, addr.ChannelID,
			goslack.MsgOptionBlocks(blocks...),
			goslack.MsgOptionText(hilNotificationText(surface), true),
		)
		if err == nil {
			if d.ApproverSet {
				kept := settledBlocks(surface)
				if kept == nil {
					a.logger.Warn("notify deliver: posted card too large to settle in place; it keeps its buttons when answered elsewhere",
						"requestID", d.RequestID)
				}
				a.reportPostedCard(d.RequestID, slackCard{ChannelID: cmp.Or(channelID, addr.ChannelID), TS: ts, Blocks: kept})
			}
			return
		}
		a.logger.Warn("notify deliver: Slack refused the HIL prompt; pointing the user at the web UI", "requestID", d.RequestID, "error", err)
	} else {
		a.logger.Warn("notify deliver: HIL surface not representable in Slack; pointing the user at the web UI", "requestID", d.RequestID)
	}
	if err := a.postHILWebUINotice(addr.ChannelID, ""); err != nil {
		a.logger.Error("notify deliver: post HIL degrade notice failed", "requestID", d.RequestID, "error", err)
	}
}

func (a *SlackGatewayAdapter) deliverProactiveNotice(d a2a.NotifyDeliver, addr slackProactiveAddress) {
	message := d.Message
	if a.cfg.CorrectMarkdown {
		message = ConvertMarkdownKeepingLinks(message)
	}
	text := d.Title
	if message != "" {
		if text != "" {
			text += "\n"
		}
		text += message
	}
	if text == "" {
		text = "You have a new notification."
	}
	for remaining := text; remaining != ""; {
		head, tail := splitForSlack(remaining, slackMaxMessageRunes)
		if _, _, err := a.api.PostMessage(a.ctx, addr.ChannelID, goslack.MsgOptionText(head, false)); err != nil {
			a.logger.Error("notify deliver: post notification failed; stopping text, still attempting artifacts",
				"requestID", d.RequestID, "error", err)
			break
		}
		remaining = tail
	}
	a.deliverProactiveArtifacts(d, addr)
}

func (a *SlackGatewayAdapter) deliverProactiveArtifacts(d a2a.NotifyDeliver, addr slackProactiveAddress) {
	if len(d.Artifacts) == 0 {
		return
	}
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Debug("notify deliver: no artifact service; skipping artifact upload", "requestID", d.RequestID)
		return
	}
	var failed []string
	for _, art := range d.Artifacts {
		if art.UserID != d.UserID {
			a.logger.Warn("notify deliver: artifact owner != recipient; refusing",
				"requestID", d.RequestID, "recipient", d.UserID, "artifactOwner", art.UserID, "filename", art.Filename)
			continue
		}
		loc := artSvc.Resolve(art.AgentName, art.UserID, art.SessionID, art.Filename)
		content, _, err := artSvc.Inner.Load(a.ctx, loc, art.Version)
		if err != nil || len(content) == 0 {
			a.logger.Warn("notify deliver: artifact load failed; skipping",
				"requestID", d.RequestID, "filename", art.Filename, "error", err)
			failed = append(failed, art.Filename)
			continue
		}
		if err := slackapi.UploadFile(a.ctx, a.api, a.logger, addr.ChannelID, "", art.Filename, content, ""); err != nil {
			a.logger.Error("notify deliver: artifact upload failed",
				"requestID", d.RequestID, "filename", art.Filename, "error", err)
			failed = append(failed, art.Filename)
		}
	}
	if len(failed) > 0 {
		notice := fmt.Sprintf("⚠️ Couldn't attach %d file(s): %s. They're available in the Solace Agent Mesh web UI.",
			len(failed), strings.Join(failed, ", "))
		if _, _, err := a.api.PostMessage(a.ctx, addr.ChannelID, goslack.MsgOptionText(notice, false), noticeMetadata(noticeError)); err != nil {
			a.logger.Error("notify deliver: failed to post undelivered-artifact notice",
				"requestID", d.RequestID, "error", err)
		}
	}
}

func (a *SlackGatewayAdapter) publishNotifyReply(resp a2a.UserInputResponseData) error {
	return a.publishNotifyReplyAs(resp, nil)
}

func (a *SlackGatewayAdapter) publishNotifyReplyAs(resp a2a.UserInputResponseData, answerer map[string]string) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		a.logger.Error("notify reply: marshal failed", "requestID", resp.RequestID, "error", err)
		return fmt.Errorf("marshal notify reply: %w", err)
	}
	topic := a2a.NotifyReplyTopic(a.core.Namespace(), resp.RequestID)
	if err := a.core.Services().Broker().PublishGuaranteed(a.ctx, topic, &broker.Message{Payload: payload, UserProperties: answerer}); err != nil {
		a.logger.Error("notify reply: publish failed", "requestID", resp.RequestID, "error", err)
		return fmt.Errorf("publish notify reply: %w", err)
	}
	return nil
}
