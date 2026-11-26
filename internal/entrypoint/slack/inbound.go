// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/slack-go/slack/slackevents"
)

const msgInboundAbandoned = "Sorry, I couldn't process your message. Please send it again."

type inboundHold struct {
	key   string
	token int64
}

func (h *inboundHold) renew(core *entrypoint.GatewayCore) bool {
	if h == nil {
		return true
	}
	token, held := core.InboundRenew(h.key, h.token)
	h.token = token
	return held
}

func (a *SlackGatewayAdapter) receiveEvent(evtAPI slackevents.EventsAPIEvent, payload json.RawMessage, ack func()) {
	key, ok := a.inboundKey(evtAPI)
	if !ok {
		ack()
		return
	}
	a.wg.Go(func() {
		claim, err := a.core.AcceptInbound(a.ctx, key, string(payload))
		if err != nil {
			a.logger.Warn("slack message not recorded; leaving it unacknowledged so Slack sends it again", "messageKey", key, "error", err)
			return
		}
		ack()
		if claim.Outcome == entrypoint.InboundSkip {
			a.logger.Debug("dropping a copy of a slack message another instance took", "messageKey", key)
			return
		}
		hold := &inboundHold{key: key, token: claim.Token}
		a.settleInbound(hold, a.dispatchInbound(evtAPI, a.filesFromPayload(payload), hold))
	})
}

func (a *SlackGatewayAdapter) settleInbound(hold *inboundHold, settled bool) {
	if !settled {
		a.logger.Warn("slack message not settled; another instance will process it after its lease", "messageKey", hold.key)
		return
	}
	a.core.InboundDone(hold.key, hold.token)
}

func (a *SlackGatewayAdapter) inboundKey(evtAPI slackevents.EventsAPIEvent) (string, bool) {
	switch ev := evtAPI.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		if ev.ChannelType != "im" ||
			isBotMessage(ev.BotID, ev.User, ev.SubType, a.botUserID, a.botID, a.identityResolved) ||
			!isMessageSubtypeAllowed(ev.SubType) {
			return "", false
		}
		return inboundMessageKey(ev.Channel, ev.TimeStamp), ev.TimeStamp != ""
	case *slackevents.AppMentionEvent:
		if isBotMessage(ev.BotID, ev.User, "", a.botUserID, a.botID, a.identityResolved) {
			return "", false
		}
		return inboundMessageKey(ev.Channel, ev.TimeStamp), ev.TimeStamp != ""
	}
	return "", false
}

func inboundMessageKey(channelID, ts string) string {
	return channelID + ":" + ts
}

func (a *SlackGatewayAdapter) dispatchInbound(evtAPI slackevents.EventsAPIEvent, files []slackFileInfo, hold *inboundHold) bool {
	switch ev := evtAPI.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		return a.handleMessage(ev, evtAPI.TeamID, files, hold)
	case *slackevents.AppMentionEvent:
		return a.handleAppMention(ev, evtAPI.TeamID, files, hold)
	}
	return true
}

func parseInbound(payload string) (slackevents.EventsAPIEvent, error) {
	evtAPI, err := slackevents.ParseEvent(json.RawMessage(payload), slackevents.OptionNoVerifyToken())
	if err != nil {
		return slackevents.EventsAPIEvent{}, fmt.Errorf("parse recorded slack event: %w", err)
	}
	return evtAPI, nil
}

func (a *SlackGatewayAdapter) ReplayInbound(_ context.Context, msg entrypoint.InboundMessage) {
	evtAPI, err := parseInbound(msg.Payload)
	if err != nil {
		a.logger.Error("recorded slack message cannot be replayed", "messageKey", msg.ID, "error", err)
		a.core.InboundDone(msg.ID, msg.Token)
		return
	}
	hold := &inboundHold{key: msg.ID, token: msg.Token}
	a.settleInbound(hold, a.dispatchInbound(evtAPI, a.filesFromPayload(json.RawMessage(msg.Payload)), hold))
}

func (a *SlackGatewayAdapter) AbandonInbound(_ context.Context, msg entrypoint.InboundMessage) bool {
	evtAPI, err := parseInbound(msg.Payload)
	if err != nil {
		a.logger.Error("recorded slack message cannot be read to tell the user", "messageKey", msg.ID, "error", err)
		return true
	}
	var channelID, threadTS string
	switch ev := evtAPI.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		channelID, threadTS = ev.Channel, ev.ThreadTimeStamp
		if threadTS == "" {
			threadTS = ev.TimeStamp
		}
	case *slackevents.AppMentionEvent:
		channelID, threadTS = ev.Channel, ev.ThreadTimeStamp
		if threadTS == "" {
			threadTS = ev.TimeStamp
		}
	default:
		return true
	}
	return a.postUserError(channelID, threadTS, noticeError, msgInboundAbandoned)
}

var _ entrypoint.InboundReplayer = (*SlackGatewayAdapter)(nil)
