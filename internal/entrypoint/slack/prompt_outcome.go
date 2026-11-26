// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"strings"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
)

type slackCard struct {
	ChannelID string          `json:"channel_id"`
	TS        string          `json:"ts"`
	UserID    string          `json:"user_id,omitempty"`
	Blocks    json.RawMessage `json:"blocks,omitempty"`
}

const slackMaxSettledBlocksBytes = 32 * 1024

const expiryNotePrefix = "Expires in "

func settledBlocks(s *a2ui.Surface) json.RawMessage {
	details := s.Details
	if n := len(details); n > 0 && details[n-1].Kind == a2ui.DetailNote && strings.HasPrefix(details[n-1].Text, expiryNotePrefix) {
		details = details[:n-1]
	}
	var blocks []goslack.Block
	if s.Title != "" {
		blocks = append(blocks, goslack.NewSectionBlock(goslack.NewTextBlockObject(goslack.MarkdownType, s.Title, false, false), nil, nil))
	}
	detailBlocks, _ := hilDetailBlocks(details)
	blocks = append(blocks, detailBlocks...)
	raw, err := json.Marshal(blocks)
	if err != nil || len(raw) > slackMaxSettledBlocksBytes {
		return nil
	}
	return raw
}

func (a *SlackGatewayAdapter) reportPostedCard(requestID string, card slackCard) {
	payload, err := json.Marshal(a2a.UserInputResponseData{
		Type: string(a2a.SignalUserInputResponse), RequestID: requestID, Status: a2a.HILStatusDelivered,
		Data: map[string]any{"card": card},
	})
	if err != nil {
		return
	}
	msg := &broker.Message{Payload: payload, UserProperties: map[string]string{a2a.PropChannel: notifyDeliverOptions.GatewayType}}
	if err := a.core.Services().Broker().PublishGuaranteed(a.ctx, a2a.NotifyReplyTopic(a.core.Namespace(), requestID), msg); err != nil {
		a.logger.Warn("notify deliver: posted prompt not reported; it keeps its buttons when answered elsewhere", "requestID", requestID, "error", err)
	}
}

func (a *SlackGatewayAdapter) showPromptOutcome(d a2a.NotifyDeliver) {
	var card slackCard
	if err := json.Unmarshal(d.Address, &card); err != nil || card.ChannelID == "" {
		a.logger.Warn("notify deliver: prompt outcome for a card this gateway cannot read; dropping", "requestID", d.RequestID, "error", err)
		return
	}
	outcome := d.PromptOutcome
	if outcome.Refusal != "" && card.UserID != "" {
		if outcome.Retryable {
			a.hilDedup.Delete(approverClickKey(d.RequestID, card.UserID))
		}
		a.postHILEphemeral(card.ChannelID, card.UserID, d.RequestID, outcome.Refusal)
	}
	if outcome.Text == "" || card.TS == "" || len(card.Blocks) == 0 {
		return
	}
	var kept goslack.Blocks
	if err := json.Unmarshal(card.Blocks, &kept); err != nil {
		a.logger.Warn("notify deliver: settled card blocks unreadable; dropping", "requestID", d.RequestID, "error", err)
		return
	}
	line := slackEscaper.Replace(outcome.Text)
	if _, _, _, err := a.api.UpdateMessage(a.ctx, card.ChannelID, card.TS,
		goslack.MsgOptionBlocks(buildApprovalResultBlocks(kept.BlockSet, line)...),
		goslack.MsgOptionText(line, false),
	); err != nil {
		a.logger.Warn("notify deliver: card not shown settled", "requestID", d.RequestID, "outcome", outcome.Outcome, "error", err)
		return
	}
	a.logger.Debug("notify deliver: card shown settled", "requestID", d.RequestID, "outcome", outcome.Outcome)
}

const slackMaxClickedBlocksBytes = 16 * 1024

func clickedCard(callback goslack.InteractionCallback) string {
	card := slackCard{
		ChannelID: callback.Container.ChannelID, TS: callback.Container.MessageTs, UserID: callback.User.ID,
		Blocks: clickedBlocks(callback.Message.Blocks.BlockSet),
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return ""
	}
	return string(raw)
}

func clickedBlocks(blocks []goslack.Block) json.RawMessage {
	var kept []goslack.Block
	for _, b := range blocks {
		if b.BlockType() == goslack.MBTAction || isExpiryNote(b) {
			continue
		}
		kept = append(kept, b)
	}
	if len(kept) == 0 {
		return nil
	}
	raw, err := json.Marshal(kept)
	if err != nil || len(raw) > slackMaxClickedBlocksBytes {
		return nil
	}
	return raw
}

func isExpiryNote(b goslack.Block) bool {
	note, ok := b.(*goslack.ContextBlock)
	if !ok {
		return false
	}
	for _, e := range note.ContextElements.Elements {
		if text, ok := e.(*goslack.TextBlockObject); ok && strings.HasPrefix(text.Text, expiryNotePrefix) {
			return true
		}
	}
	return false
}

func approverClickKey(requestID, slackUserID string) string { return requestID + "/" + slackUserID }
