// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (a *TeamsAdapter) startNotifyDeliverLoop() {
	a.core.StartNotifyDeliverConsumer(a.ctx, notifyDeliverOptions, a.logger, a.handleNotifyDeliver)
}

var notifyDeliverOptions = entrypoint.NotifyDeliverOptions{GatewayType: "teams", Interactable: true}

func (a *TeamsAdapter) publishPresence(ctx context.Context, userID, email string, addr teamsProactiveAddress) {
	a.core.PublishNotifyPresence(ctx, notifyDeliverOptions.GatewayType, userID, email, addr)
}

func (a *TeamsAdapter) handleNotifyDeliver(d a2a.NotifyDeliver) {
	var addr teamsProactiveAddress
	if err := json.Unmarshal(d.Address, &addr); err != nil {
		a.logger.Error("notify deliver: unusable teams address; dropping", "requestID", d.RequestID, "error", err)
		return
	}
	if addr.FromID == "" || addr.ServiceURL == "" {
		a.logger.Error("notify deliver: unusable teams address (missing from id or service URL); dropping", "requestID", d.RequestID, "userID", d.UserID)
		return
	}
	tenantID := addr.TenantID
	if tenantID == "" {
		tenantID = a.cfg.TenantID
	}
	convID, err := a.botClient.CreatePersonalConversation(a.ctx, addr.ServiceURL, addr.FromID, tenantID)
	if err != nil || convID == "" {
		a.logger.Warn("notify deliver: cannot create Teams 1:1; skipping out-of-band (inbox unaffected)", "requestID", d.RequestID, "userID", d.UserID, "error", err)
		return
	}
	meta := &TeamsTaskMeta{ServiceURL: addr.ServiceURL, ConversationID: convID}
	if d.Mode == a2a.NotifyModeInput {
		a.deliverProactiveHIL(d, meta)
	} else {
		a.deliverProactiveNotice(d, meta)
	}
}

func (a *TeamsAdapter) deliverProactiveHIL(d a2a.NotifyDeliver, meta *TeamsTaskMeta) {
	surface, err := a2ui.Parse(d.Surface)
	if err != nil {
		a.logger.Error("notify deliver: parse surface failed", "requestID", d.RequestID, "error", err)
		return
	}
	card, ok := a.buildHILCard(surface, d.RequestID, "", "", true)
	if ok {
		_, err := sendTeamsMessage(a.ctx, a.botClient, meta, surfaceTitle(surface), &card, "")
		if err == nil {
			return
		}
		a.logger.Warn("notify deliver: Teams refused the HIL card; pointing the user at the web UI", "requestID", d.RequestID, "error", err)
	} else {
		a.logger.Warn("notify deliver: HIL surface not representable in Teams; pointing the user at the web UI", "requestID", d.RequestID)
	}
	if _, err := sendTeamsMessage(a.ctx, a.botClient, meta, hilWebUINotice(a.core.WebUIURL()), nil, ""); err != nil {
		a.logger.Error("notify deliver: post HIL degrade notice failed", "requestID", d.RequestID, "error", err)
	}
}

func (a *TeamsAdapter) deliverProactiveNotice(d a2a.NotifyDeliver, meta *TeamsTaskMeta) {
	text := d.Title
	if d.Message != "" {
		if text != "" {
			text += "\n\n"
		}
		text += d.Message
	}
	if text == "" {
		text = "You have a new notification."
	}
	if _, err := sendTeamsMessage(a.ctx, a.botClient, meta, text, nil, ""); err != nil {
		a.logger.Error("notify deliver: post notification failed; still attempting artifacts",
			"requestID", d.RequestID, "error", err)
	}
	a.deliverProactiveArtifacts(d, meta)
}

func (a *TeamsAdapter) deliverProactiveArtifacts(d a2a.NotifyDeliver, meta *TeamsTaskMeta) {
	if len(d.Artifacts) == 0 {
		return
	}
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Debug("notify deliver: no artifact service; skipping artifact offer", "requestID", d.RequestID)
		return
	}
	var failed []string
	for _, art := range d.Artifacts {
		if rbac.IsNonDelegatablePrincipal(art.UserID) {
			a.logger.Warn("notify deliver: refusing to proactively offer a shared-principal artifact",
				"requestID", d.RequestID, "artifactOwner", art.UserID, "filename", art.Filename)
			continue
		}
		if art.UserID != d.UserID {
			a.logger.Warn("notify deliver: artifact owner != recipient; refusing",
				"requestID", d.RequestID, "recipient", d.UserID, "artifactOwner", art.UserID, "filename", art.Filename)
			continue
		}
		loc := artSvc.Resolve(art.AgentName, art.UserID, art.SessionID, art.Filename)
		content, mimeType, err := artSvc.Inner.Load(a.ctx, loc, art.Version)
		if err != nil || len(content) == 0 {
			a.logger.Warn("notify deliver: artifact load failed; skipping",
				"requestID", d.RequestID, "filename", art.Filename, "error", err)
			failed = append(failed, art.Filename)
			continue
		}
		ref := newConsentArtifactRef(loc, art.Version)
		if err := a.sendFileConsentCard(meta.ServiceURL, meta.ConversationID, art.Filename, content, mimeType, "", ref); err != nil {
			a.logger.Error("notify deliver: file consent card failed",
				"requestID", d.RequestID, "filename", art.Filename, "error", err)
			failed = append(failed, art.Filename)
		}
	}
	if len(failed) > 0 {
		notice := fmt.Sprintf("⚠️ Couldn't attach %d file(s): %s. They're available in the Solace Agent Mesh web UI.",
			len(failed), strings.Join(failed, ", "))
		if _, err := sendTeamsMessage(a.ctx, a.botClient, meta, notice, nil, ""); err != nil {
			a.logger.Error("notify deliver: failed to post undelivered-artifact notice",
				"requestID", d.RequestID, "error", err)
		}
	}
}

func (a *TeamsAdapter) publishNotifyReply(resp a2a.UserInputResponseData) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		a.logger.Error("notify reply: marshal failed", "requestID", resp.RequestID, "error", err)
		return fmt.Errorf("marshal notify reply: %w", err)
	}
	topic := a2a.NotifyReplyTopic(a.core.Namespace(), resp.RequestID)
	if err := a.core.Services().Broker().PublishGuaranteed(a.ctx, topic, &broker.Message{Payload: payload}); err != nil {
		a.logger.Error("notify reply: publish failed", "requestID", resp.RequestID, "error", err)
		return fmt.Errorf("publish notify reply: %w", err)
	}
	return nil
}
