// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func (a *SlackGatewayAdapter) ack(client *socketmode.Client, evt socketmode.Event) {
	if err := client.Ack(*evt.Request); err != nil {
		a.logger.Warn("slack socket mode ack failed", "envelopeID", evt.Request.EnvelopeID, "error", err)
	}
}

func (a *SlackGatewayAdapter) registerSocketHandlers(client *socketmode.Client) {
	a.wg.Go(func() {
		for {
			select {
			case <-a.ctx.Done():
				return
			case evt, ok := <-client.Events:
				if !ok {
					return
				}
				switch evt.Type {
				case socketmode.EventTypeEventsAPI:
					a.handleEventsAPI(client, evt)
				case socketmode.EventTypeInteractive:
					a.handleInteractive(client, evt)
				case socketmode.EventTypeSlashCommand:
					a.handleSlashCommand(client, evt)
				case socketmode.EventTypeHello, socketmode.EventTypeDisconnect:
				default:
					if evt.Request != nil && evt.Request.EnvelopeID != "" {
						a.ack(client, evt)
					}
				}
			}
		}
	})
}

type slackFileInfo struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Title              string `json:"title"`
	MimeType           string `json:"mimetype"`
	URLPrivate         string `json:"url_private"`
	URLPrivateDownload string `json:"url_private_download"`
}

func (a *SlackGatewayAdapter) handleEventsAPI(client *socketmode.Client, evt socketmode.Event) {
	evtAPI, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		a.ack(client, evt)
		return
	}
	a.receiveEvent(evtAPI, evt.Request.Payload, func() { a.ack(client, evt) })
}

func (a *SlackGatewayAdapter) extractFiles(req *socketmode.Request) []slackFileInfo {
	if req == nil {
		return nil
	}
	return a.filesFromPayload(req.Payload)
}

func (a *SlackGatewayAdapter) filesFromPayload(payload json.RawMessage) []slackFileInfo {
	if len(payload) == 0 {
		return nil
	}
	var pl struct {
		Event struct {
			Files []slackFileInfo `json:"files"`
		} `json:"event"`
	}
	if err := json.Unmarshal(payload, &pl); err != nil {
		a.logger.Debug("slack envelope unmarshal failed in extractFiles", "error", err)
		return nil
	}
	return pl.Event.Files
}

func (a *SlackGatewayAdapter) HandleMessage(ev *slackevents.MessageEvent, teamID string) {
	a.handleMessage(ev, teamID, nil, nil)
}

func (a *SlackGatewayAdapter) HandleAppMention(ev *slackevents.AppMentionEvent, teamID string) {
	a.handleAppMention(ev, teamID, nil, nil)
}

type TestFileInfo struct {
	ID                 string
	Name               string
	MimeType           string
	URLPrivateDownload string
}

func (a *SlackGatewayAdapter) HandleMessageWithFiles(ev *slackevents.MessageEvent, teamID string, files []TestFileInfo) {
	a.handleMessage(ev, teamID, toSlackFileInfo(files), nil)
}

func toSlackFileInfo(in []TestFileInfo) []slackFileInfo {
	if len(in) == 0 {
		return nil
	}
	out := make([]slackFileInfo, len(in))
	for i, f := range in {
		out[i] = slackFileInfo{
			ID:                 f.ID,
			Name:               f.Name,
			MimeType:           f.MimeType,
			URLPrivateDownload: f.URLPrivateDownload,
		}
	}
	return out
}

func (a *SlackGatewayAdapter) handleMessage(ev *slackevents.MessageEvent, teamID string, files []slackFileInfo, hold *inboundHold) bool {
	if isBotMessage(ev.BotID, ev.User, ev.SubType, a.botUserID, a.botID, a.identityResolved) {
		return true
	}
	if !isMessageSubtypeAllowed(ev.SubType) {
		return true
	}

	if ev.ChannelType != "im" {
		return true
	}

	channelID := ev.Channel
	inThread := ev.ThreadTimeStamp != ""
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}
	text := ev.Text
	userID := ev.User
	isDM := ev.ChannelType == "im"

	if a.handleCommand(channelID, threadTS, userID, teamID, text) {
		return true
	}

	return a.submitSlackTask(slackInbound{
		channelID: channelID, threadTS: threadTS, userID: userID, teamID: teamID,
		text: text, files: files, inThread: inThread, isDM: isDM,
		messageTS: ev.TimeStamp,
		hold:      hold,
	})
}

func (a *SlackGatewayAdapter) handleAppMention(ev *slackevents.AppMentionEvent, teamID string, files []slackFileInfo, hold *inboundHold) bool {
	if isBotMessage(ev.BotID, ev.User, "", a.botUserID, a.botID, a.identityResolved) {
		return true
	}

	if claimed, pending := a.claimMention(mentionKey(ev.Channel, ev.TimeStamp)); !claimed {
		if hold != nil && hold.token != 0 && pending {
			a.logger.Info("replayed app_mention waits; original still before its submit", "channelID", ev.Channel, "messageTS", ev.TimeStamp)
			return false
		}
		a.logger.Debug("dropping app_mention; original still in flight", "channelID", ev.Channel, "messageTS", ev.TimeStamp)
		return true
	}

	channelID := ev.Channel
	inThread := ev.ThreadTimeStamp != ""
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}
	text := ev.Text
	userID := ev.User

	text = stripBotMention(text)

	if a.handleCommand(channelID, threadTS, userID, teamID, text) {
		a.releaseMentionKey(mentionKey(channelID, ev.TimeStamp))
		return true
	}

	return a.submitSlackTask(slackInbound{
		channelID: channelID, threadTS: threadTS, userID: userID, teamID: teamID,
		text: text, files: files, inThread: inThread, isDM: false,
		messageTS:    ev.TimeStamp,
		mentionMsgTS: ev.TimeStamp,
		hold:         hold,
	})
}

func isBotMessage(botID, user, subType, ownBotUserID, ownBotID string, identityResolved bool) bool {
	if !identityResolved {
		return botID != "" || subType == "bot_message"
	}
	if isSelfAuthored(user, botID, ownBotUserID, ownBotID) {
		return true
	}
	return (botID != "" && user == "") || subType == "bot_message"
}

func isSelfAuthored(user, botID, ownUserID, ownBotID string) bool {
	return (ownUserID != "" && user == ownUserID) || (ownBotID != "" && botID == ownBotID)
}

func isMessageSubtypeAllowed(subType string) bool {
	switch subType {
	case "", "file_share", "thread_broadcast":
		return true
	default:
		return false
	}
}

func (a *SlackGatewayAdapter) handleCommand(channelID, threadTS, userID, teamID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "!") {
		return false
	}

	parts := strings.Fields(trimmed)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "!help":
		a.cmdHelp(channelID, threadTS, userID, teamID)
	case "!artifacts":
		a.cmdArtifacts(channelID, threadTS, userID)
	default:
		return false
	}
	return true
}

func (a *SlackGatewayAdapter) cmdHelp(channelID, threadTS, userID, teamID string) {
	caller := a.emailCache.ResolveIdentity(userID, teamID)
	var agents []string
	for _, card := range a.core.AgentRegistry().List() {
		if a2a.IsEphemeralCard(card) {
			continue
		}
		if !a.core.CanAccessAgent(a.ctx, caller, card) {
			continue
		}
		agents = append(agents, entrypoint.AgentLabel(card))
	}

	var sb strings.Builder
	sb.WriteString("*Available Commands:*\n")
	sb.WriteString("• `!help` — show this help\n")
	sb.WriteString("• `!artifacts` — list session artifacts\n")
	sb.WriteString("\n*Discovered Agents:*\n")
	if len(agents) == 0 {
		sb.WriteString("_No agents discovered yet._\n")
	} else {
		for _, name := range agents {
			fmt.Fprintf(&sb, "• `%s`\n", name)
		}
	}

	_, _, _ = a.api.PostMessage(a.ctx, channelID,
		goslack.MsgOptionText(sb.String(), false),
		goslack.MsgOptionTS(threadTS),
		noticeMetadata(noticeInfo),
	)
}

func (a *SlackGatewayAdapter) cmdArtifacts(channelID, threadTS, _ string) {
	_, _, _ = a.api.PostMessage(a.ctx, channelID,
		goslack.MsgOptionText("_Artifact listing is not yet implemented in the Go adapter._", false),
		goslack.MsgOptionTS(threadTS),
		noticeMetadata(noticeInfo),
	)
}

func (a *SlackGatewayAdapter) handleInteractive(client *socketmode.Client, evt socketmode.Event) {
	ack := func() { a.ack(client, evt) }
	callback, ok := evt.Data.(goslack.InteractionCallback)
	if !ok {
		ack()
		return
	}

	if callback.Type == goslack.InteractionTypeViewSubmission {
		if callback.View.CallbackID == NativeFeedbackModalID {
			a.handleNativeFeedbackSubmit(callback, ack)
			return
		}
		ack()
		return
	}

	if actions := callback.ActionCallback.BlockActions; len(actions) == 1 && actions[0].ActionID == NativeFeedbackActionID {
		a.handleNativeFeedbackClick(callback, actions[0], ack)
		return
	}
	ack()

	for _, action := range callback.ActionCallback.BlockActions {
		if strings.HasPrefix(action.ActionID, HILInputActionPrefix) {
			a.handleHILInput(callback, action)
			continue
		}
		switch action.ActionID {
		case CancelButtonActionID:
			a.handleCancelAction(callback, action)
		case DownloadArtifactActionID:
			a.handleDownloadAction(callback, action)
		case NativeFeedbackActionID:
			a.handleNativeFeedbackClick(callback, action, func() {
			})
		case toolAuthActionID:
		default:
			a.logger.Warn("unhandled slack block action",
				"actionID", action.ActionID, "blockID", action.BlockID)
		}
	}
}

func (a *SlackGatewayAdapter) handleSlashCommand(client *socketmode.Client, evt socketmode.Event) {
	a.ack(client, evt)

	cmd, ok := evt.Data.(goslack.SlashCommand)
	if !ok {
		return
	}

	switch cmd.Command {
	case "/artifacts":
		a.cmdArtifacts(cmd.ChannelID, "", cmd.UserID)
	case "/help":
		a.cmdHelp(cmd.ChannelID, "", cmd.UserID, cmd.TeamID)
	}
}

func (a *SlackGatewayAdapter) handleCancelAction(callback goslack.InteractionCallback, action *goslack.BlockAction) {
	taskID := action.Value
	a.logger.Info("cancel button clicked", "taskID", taskID)
	if err := a.core.CancelTask(a.ctx, taskID, a.core.Tasks().AgentName(taskID)); err != nil {
		a.logger.Error("failed to cancel task", "taskID", taskID, "error", err)
	}
}

func (a *SlackGatewayAdapter) handleDownloadAction(callback goslack.InteractionCallback, action *goslack.BlockAction) {
	var payload struct {
		Filename string `json:"filename"`
		Version  int    `json:"version"`
	}
	if err := json.Unmarshal([]byte(action.Value), &payload); err != nil {
		a.logger.Error("invalid download payload", "error", err)
		return
	}

	channelID := callback.Container.ChannelID
	threadTS := callback.Container.ThreadTs

	a.logger.Info("artifact download requested", "filename", payload.Filename, "version", payload.Version)

	arts := a.core.Services().Artifacts()
	if arts == nil {
		a.api.PostMessage(a.ctx, channelID, //nolint:errcheck
			goslack.MsgOptionText("Artifact service is not configured.", false),
			goslack.MsgOptionTS(threadTS),
			noticeMetadata(noticeError),
		)
		return
	}

	a.api.PostMessage(a.ctx, channelID, //nolint:errcheck
		goslack.MsgOptionText("_Artifact download is not yet fully implemented in the Go adapter._", false),
		goslack.MsgOptionTS(threadTS),
		noticeMetadata(noticeInfo),
	)
}

func buildApprovalResultBlocks(original []goslack.Block, resultText string) []goslack.Block {
	var out []goslack.Block
	for _, block := range original {
		if block.BlockType() == goslack.MBTAction {
			continue
		}
		out = append(out, block)
	}
	return append(out, goslack.NewContextBlock("",
		goslack.NewTextBlockObject(goslack.MarkdownType, resultText, false, false),
	))
}

func stripBotMention(text string) string {
	if idx := strings.Index(text, ">"); idx >= 0 && strings.HasPrefix(text, "<@") {
		text = strings.TrimSpace(text[idx+1:])
	}
	return text
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	const ellipsis = "..."
	if n <= len(ellipsis) {
		return string(r[:n])
	}
	return string(r[:n-len(ellipsis)]) + ellipsis
}
