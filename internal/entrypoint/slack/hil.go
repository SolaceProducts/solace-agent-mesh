// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const HILInputActionPrefix = "hil_input::"

const slackMaxButtonValue = 2000

const slackMaxOptionValueRunes = 75

func (a *SlackGatewayAdapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: true}
}

var _ entrypoint.CapabilityProvider = (*SlackGatewayAdapter)(nil)

type slackHILValue struct {
	RequestID   string          `json:"r"`
	TaskID      string          `json:"t"`
	AgentName   string          `json:"a"`
	Cancel      bool            `json:"x,omitempty"`
	Notify      bool            `json:"n,omitempty"`
	ApproverSet bool            `json:"s,omitempty"`
	Context     map[string]any  `json:"c,omitempty"`
	Fields      []slackHILField `json:"f,omitempty"`
}

type slackHILField struct {
	Block  string         `json:"b"`
	Action string         `json:"i"`
	Path   string         `json:"p"`
	Kind   a2ui.FieldKind `json:"k"`
}

func (a *SlackGatewayAdapter) handleUserInputRequest(meta *SlackTaskMeta, taskID string, req *a2a.UserInputRequestData) {
	log := a.logger.With("taskID", taskID, "requestID", req.RequestID)
	surface, err := a2ui.Parse(req.Surface)
	if err != nil {
		log.Error("failed to parse A2UI surface", "error", err)
		return
	}
	promptItem := itemHILPromptedPrefix + req.RequestID
	if _, posted := a.items.get(a.ctx, taskID, promptItem); posted {
		log.Debug("HIL prompt already posted")
		return
	}
	agentName := a.core.Tasks().AgentName(taskID)

	blocks, ok := a.buildHILBlocks(surface, req.RequestID, taskID, agentName, false)
	if ok {
		_, _, err := a.api.PostMessage(a.ctx, meta.ChannelID,
			goslack.MsgOptionBlocks(blocks...),
			goslack.MsgOptionText(hilNotificationText(surface), true),
			goslack.MsgOptionTS(meta.ThreadTS),
		)
		if err == nil {
			a.items.put(a.ctx, taskID, promptItem, "")
			return
		}
		log.Warn("Slack refused the HIL prompt; pointing the user at the web UI", "error", err)
	} else {
		log.Warn("HIL surface not representable in Slack; pointing the user at the web UI")
	}
	if err := a.postHILWebUINotice(meta.ChannelID, meta.ThreadTS); err != nil {
		log.Error("failed to post HIL degrade notice", "error", err)
		return
	}
	a.items.put(a.ctx, taskID, promptItem, "")
}

func hilWebUINotice(webUIURL string) string {
	const cannotShow = "This agent needs input that can't be shown in Slack."
	if webUIURL == "" {
		return cannotShow + " Please use the web UI to respond."
	}
	return cannotShow + " Please respond in the web UI: " + webUIURL
}

func (a *SlackGatewayAdapter) postHILWebUINotice(channelID, threadTS string) error {
	opts := []goslack.MsgOption{goslack.MsgOptionText(hilWebUINotice(a.core.WebUIURL()), false), noticeMetadata(noticeError)}
	if threadTS != "" {
		opts = append(opts, goslack.MsgOptionTS(threadTS))
	}
	_, _, err := a.api.PostMessage(a.ctx, channelID, opts...)
	return err
}

func hilNotificationText(s *a2ui.Surface) string {
	return cmp.Or(s.Summary(), "Input requested")
}

func (a *SlackGatewayAdapter) buildHILBlocks(s *a2ui.Surface, requestID, taskID, agentName string, notifyRouted bool) ([]goslack.Block, bool) {
	return a.buildHILBlocksFor(s, slackHILValue{RequestID: requestID, TaskID: taskID, AgentName: agentName, Notify: notifyRouted})
}

func (a *SlackGatewayAdapter) buildHILBlocksFor(s *a2ui.Surface, route slackHILValue) ([]goslack.Block, bool) {
	var blocks []goslack.Block

	if s.Title != "" {
		blocks = append(blocks, goslack.NewSectionBlock(
			goslack.NewTextBlockObject(goslack.MarkdownType, s.Title, false, false), nil, nil))
	}
	details, ok := hilDetailBlocks(s.Details)
	if !ok {
		return nil, false
	}
	blocks = append(blocks, details...)

	fields := make([]slackHILField, 0, len(s.Fields))
	for _, f := range s.Fields {
		actionID := f.ID + "-input"
		block, ok := hilFieldBlock(f, actionID)
		if !ok {
			return nil, false
		}
		blocks = append(blocks, block)
		fields = append(fields, slackHILField{Block: f.ID, Action: actionID, Path: f.Path, Kind: f.Kind})
	}

	var elements []goslack.BlockElement
	for _, btn := range s.Buttons {
		val := slackHILValue{
			RequestID:   route.RequestID,
			TaskID:      route.TaskID,
			AgentName:   route.AgentName,
			Cancel:      btn.IsCancel(),
			Notify:      route.Notify,
			ApproverSet: route.ApproverSet,
			Fields:      fields,
		}
		if !btn.IsCancel() {
			val.Context = btn.Context
		}
		raw, err := json.Marshal(val)
		if err != nil || len(raw) > slackMaxButtonValue {
			return nil, false
		}
		label := btn.Label
		if label == "" {
			if btn.IsCancel() {
				label = "Cancel"
			} else {
				label = "Submit"
			}
		}
		el := goslack.NewButtonBlockElement(HILInputActionPrefix+btn.ID, string(raw),
			goslack.NewTextBlockObject(goslack.PlainTextType, label, false, false))
		if btn.Variant == "primary" {
			el.Style = goslack.StylePrimary
		}
		elements = append(elements, el)
	}
	if len(elements) == 0 {
		return nil, false
	}
	blocks = append(blocks, goslack.NewActionBlock("hil_input_actions", elements...))
	if len(blocks) > slackMaxBlocks {
		return nil, false
	}
	return blocks, true
}

const (
	slackMaxBlocks       = 50
	slackMaxSectionChars = 3000
)

const (
	slackMaxHILValueChars  = 2500
	slackMaxHILDetailChars = 12000
)

func hilDetailBlocks(details []a2ui.Detail) (blocks []goslack.Block, ok bool) {
	total := 0
	for _, d := range details {
		var text string
		switch d.Kind {
		case a2ui.DetailValue:
			text = "*" + escapeClipped(d.Label, 200) + "*\n" + codeBlock(d.Text, slackMaxHILValueChars)
		default:
			text = escapeClipped(d.Text, slackMaxSectionChars)
		}
		total += utf8.RuneCountInString(text)
		if total > slackMaxHILDetailChars {
			return nil, false
		}
		obj := goslack.NewTextBlockObject(goslack.MarkdownType, text, false, false)
		if d.Kind == a2ui.DetailNote {
			blocks = append(blocks, goslack.NewContextBlock("", obj))
		} else {
			blocks = append(blocks, goslack.NewSectionBlock(obj, nil, nil))
		}
	}
	return blocks, true
}

func codeBlock(value string, maxChars int) string {
	if value == "" {
		return "_(empty)_"
	}
	for strings.Contains(value, codeFence) {
		value = strings.ReplaceAll(value, codeFence, brokenCodeFence)
	}
	body := escapeClipped(value, maxChars-2*len(codeFence)-2)
	return codeFence + "\n" + body + "\n" + codeFence
}

const codeFence = "```"

const brokenCodeFence = "``" + string(rune(0x200B)) + "`"

func escapeClipped(s string, maxChars int) string {
	escaped := slackEscaper.Replace(s)
	if utf8.RuneCountInString(escaped) <= maxChars {
		return escaped
	}
	total := utf8.RuneCountInString(s)
	budget := maxChars - utf8.RuneCountInString(a2ui.ClipNotice(total))
	var b strings.Builder
	used, kept := 0, 0
	for _, r := range s {
		piece := slackEscaper.Replace(string(r))
		n := utf8.RuneCountInString(piece)
		if used+n > budget {
			break
		}
		b.WriteString(piece)
		used += n
		kept++
	}
	return b.String() + a2ui.ClipNotice(total-kept)
}

var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func hilFieldBlock(f a2ui.Field, actionID string) (goslack.Block, bool) {
	label := f.Label
	if label == "" {
		label = "Response"
	}
	labelObj := goslack.NewTextBlockObject(goslack.PlainTextType, truncate(label, 150), false, false)

	switch f.Kind {
	case a2ui.FieldSingleChoice:
		opts, ok := optionObjects(f.Options)
		if !ok || len(opts) == 0 {
			return nil, false
		}
		var el goslack.BlockElement
		if len(opts) > 10 {
			el = goslack.NewOptionsSelectBlockElement(goslack.OptTypeStatic,
				goslack.NewTextBlockObject(goslack.PlainTextType, "Select one", false, false), actionID, opts...)
		} else {
			el = goslack.NewRadioButtonsBlockElement(actionID, opts...)
		}
		ib := goslack.NewInputBlock(f.ID, labelObj, nil, el)
		ib.Optional = true
		return ib, true
	case a2ui.FieldMultiChoice:
		opts, ok := optionObjects(f.Options)
		if !ok || len(opts) == 0 {
			return nil, false
		}
		var el goslack.BlockElement
		if len(opts) > 10 {
			el = goslack.NewOptionsMultiSelectBlockElement(goslack.MultiOptTypeStatic,
				goslack.NewTextBlockObject(goslack.PlainTextType, "Select", false, false), actionID, opts...)
		} else {
			el = goslack.NewCheckboxGroupsBlockElement(actionID, opts...)
		}
		ib := goslack.NewInputBlock(f.ID, labelObj, nil, el)
		ib.Optional = true
		return ib, true
	case a2ui.FieldText:
		ph := f.Placeholder
		if ph == "" {
			ph = "Type your answer..."
		}
		el := goslack.NewPlainTextInputBlockElement(
			goslack.NewTextBlockObject(goslack.PlainTextType, truncate(ph, 150), false, false), actionID)
		el.Multiline = f.Multiline
		ib := goslack.NewInputBlock(f.ID, labelObj, nil, el)
		ib.Optional = true
		return ib, true
	default:
		return nil, false
	}
}

func optionObjects(opts []a2ui.Option) ([]*goslack.OptionBlockObject, bool) {
	out := make([]*goslack.OptionBlockObject, 0, len(opts))
	for _, o := range opts {
		if utf8.RuneCountInString(o.Value) > slackMaxOptionValueRunes {
			return nil, false
		}
		label := o.Label
		if label == "" {
			label = o.Value
		}
		var desc *goslack.TextBlockObject
		if o.Description != "" {
			desc = goslack.NewTextBlockObject(goslack.PlainTextType, truncate(o.Description, 75), false, false)
		}
		out = append(out, goslack.NewOptionBlockObject(o.Value,
			goslack.NewTextBlockObject(goslack.PlainTextType, truncate(label, 75), false, false), desc))
	}
	return out, true
}

const msgHILNotYours = "Only the person who asked can answer this."

const (
	msgHILUnverifiedClicker = "Agent Mesh can't match your Slack account to an Agent Mesh user, so it can't accept your answer. " +
		"If you are an approver, answer from the Agent Mesh web UI."
	msgHILAnswerSent = "Your answer was sent. The card shows the result once Agent Mesh records it; " +
		"if your answer is not counted, you are told why here."
	msgHILAnswerNotSent = "⚠️ Your response couldn't be submitted — please try again."
)

func (a *SlackGatewayAdapter) hilResponse(callback goslack.InteractionCallback, val slackHILValue) a2a.UserInputResponseData {
	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: val.RequestID,
	}
	if val.Cancel {
		resp.Status = "cancelled"
	} else {
		resp.Status = "answered"
		resp.Data = a2ui.ResolveContext(val.Context, a.collectHILState(callback, val.Fields))
	}
	return resp
}

func (a *SlackGatewayAdapter) handleApproverSetClick(callback goslack.InteractionCallback, val slackHILValue) {
	channelID, slackUserID := callback.Container.ChannelID, callback.User.ID
	email := a.emailCache.lookupEmail(slackUserID)
	if email == "" {
		a.logger.Warn("HIL click refused: the clicker has no email to name them by", "requestID", val.RequestID, "slackUserID", slackUserID)
		a.postHILEphemeral(channelID, slackUserID, val.RequestID, msgHILUnverifiedClicker)
		return
	}
	dedupKey := approverClickKey(val.RequestID, slackUserID)
	if !a.hilDedup.SetIfAbsent(dedupKey, struct{}{}, runtime.WithTTL(a.approverClickWindow())) {
		a.logger.Debug("duplicate HIL input click ignored", "requestID", val.RequestID)
		return
	}
	resp := a.hilResponse(callback, val)
	answerer := map[string]string{a2a.PropUserID: email, a2a.PropChannel: notifyDeliverOptions.GatewayType, a2a.PropNotifyCard: clickedCard(callback)}
	if err := a.publishNotifyReplyAs(resp, answerer); err != nil {
		a.hilDedup.Delete(dedupKey)
		a.postHILEphemeral(channelID, slackUserID, val.RequestID, msgHILAnswerNotSent)
		return
	}
	a.logger.Info("HIL input received", "taskStatus", resp.Status, "requestID", val.RequestID, "slackUserID", slackUserID)
	a.postHILEphemeral(channelID, slackUserID, val.RequestID, msgHILAnswerSent)
}

const defaultApproverClickWindow = 30 * time.Second

func (a *SlackGatewayAdapter) approverClickWindow() time.Duration {
	if a.approverClickTTL > 0 {
		return a.approverClickTTL
	}
	return defaultApproverClickWindow
}

func (a *SlackGatewayAdapter) postHILEphemeral(channelID, slackUserID, requestID, text string) {
	if _, err := a.api.PostEphemeral(a.ctx, channelID, slackUserID, goslack.MsgOptionText(text, false)); err != nil {
		a.logger.Debug("failed to post a HIL notice", "requestID", requestID, "error", err)
	}
}

func (a *SlackGatewayAdapter) askedPlace(taskID string) entrypoint.ChatPlace {
	if taskID == "" {
		return entrypoint.ChatPlace{}
	}
	meta := a.getTaskMeta(a.ctx, taskID)
	if meta == nil {
		a.logger.Warn("HIL click: no record of where the task was asked; answering as the clicker", "taskID", taskID)
		return entrypoint.ChatPlace{}
	}
	return entrypoint.ChatPlace{Conversation: meta.ChannelID, User: meta.UserID}
}

func (a *SlackGatewayAdapter) handleHILInput(callback goslack.InteractionCallback, action *goslack.BlockAction) {
	var val slackHILValue
	if err := json.Unmarshal([]byte(action.Value), &val); err != nil {
		a.logger.Error("invalid HIL input payload", "error", err)
		return
	}
	if val.Notify && val.ApproverSet {
		a.handleApproverSetClick(callback, val)
		return
	}

	userID := callback.User.ID
	var identity *auth.Subject
	answerUserID := ""
	if !val.Notify {
		teamID := cmp.Or(callback.Team.ID, callback.User.TeamID)
		realIdentity := a.emailCache.ResolveIdentity(userID, teamID)
		isDM := strings.HasPrefix(callback.Container.ChannelID, "D")
		identity = auth.ForChatResponder(realIdentity, auth.ChannelPolicy{
			IsMultiParty:              !isDM,
			UseUserIdentityInChannels: a.cfg.UseUserIdentityInChannels,
			RunAs:                     a.cfg.RunAs,
		})
		answerUserID = a.core.PromptAnswerUserID(val.TaskID, identity.UserID(), a.askedPlace(val.TaskID),
			entrypoint.ChatPlace{Conversation: callback.Container.ChannelID, User: userID})
		if val.TaskID != "" && !a.core.MayAnswerPrompt(val.TaskID, answerUserID) {
			a.logger.Warn("HIL click refused: clicker does not own the task", "requestID", val.RequestID, "taskID", val.TaskID, "userID", answerUserID)
			if _, err := a.api.PostEphemeral(a.ctx, callback.Container.ChannelID, userID, goslack.MsgOptionText(msgHILNotYours, false)); err != nil {
				a.logger.Debug("failed to post the HIL refusal notice", "requestID", val.RequestID, "error", err)
			}
			return
		}
	}

	if !a.hilDedup.SetIfAbsent(val.RequestID, struct{}{}, runtime.WithTTL(5*time.Minute)) {
		a.logger.Debug("duplicate HIL input click ignored", "requestID", val.RequestID)
		return
	}

	answerItem, claim := "", ""
	if !val.Notify && val.TaskID != "" {
		answerItem = itemHILAnsweredPrefix + val.RequestID
		var won bool
		if won, claim = a.items.claimAnswer(a.ctx, val.TaskID, answerItem); !won {
			a.hilDedup.Delete(val.RequestID)
			a.logger.Debug("HIL input already answered on another instance", "requestID", val.RequestID)
			return
		}
	}

	userName := callback.User.Name
	if userName == "" {
		userName = userID
	}

	resp := a.hilResponse(callback, val)

	a.logger.Info("HIL input received",
		"taskStatus", resp.Status, "requestID", val.RequestID, "taskID", val.TaskID,
		"agentName", val.AgentName, "userID", userName)

	verb := "Response submitted"
	if val.Cancel {
		verb = "Cancelled"
	}
	resultText := fmt.Sprintf("*%s* by <@%s>", verb, userID)

	if val.Notify {
		if err := a.publishNotifyReply(resp); err != nil {
			a.hilDedup.Delete(val.RequestID)
			a.postHILCorrection(callback.Container.ChannelID, callback.Container.MessageTs, val.RequestID,
				msgHILAnswerNotSent)
			return
		}
		a.markHILAnswered(callback, val.RequestID, resultText)
		return
	}
	if err := a.core.SubmitUserInputResponseAttributedTo(a.ctx, val.AgentName, val.TaskID, resp, identity, answerUserID); err != nil {
		a.logger.Error("failed to submit HIL response", "error", err,
			"taskID", val.TaskID, "agentName", val.AgentName)
		a.hilDedup.Delete(val.RequestID)
		a.items.releaseAnswer(a.ctx, val.TaskID, answerItem, claim)
		a.postHILCorrection(callback.Container.ChannelID, callback.Container.MessageTs, val.RequestID,
			msgHILAnswerNotSent)
		return
	}
	a.items.answerForwarded(a.ctx, val.TaskID, answerItem, claim)
	a.markHILAnswered(callback, val.RequestID, resultText)
}

func (a *SlackGatewayAdapter) markHILAnswered(callback goslack.InteractionCallback, requestID, resultText string) {
	resultBlocks := buildApprovalResultBlocks(callback.Message.Blocks.BlockSet, resultText)
	if _, _, _, err := a.api.UpdateMessage(a.ctx,
		callback.Container.ChannelID, callback.Container.MessageTs,
		goslack.MsgOptionBlocks(resultBlocks...),
		goslack.MsgOptionText(resultText, false),
	); err != nil {
		a.logger.Error("failed to update HIL message", "error", err, "requestID", requestID)
	}
}

func (a *SlackGatewayAdapter) postHILCorrection(channelID, messageTs, requestID, text string) {
	if _, _, perr := a.api.PostMessage(a.ctx, channelID,
		goslack.MsgOptionText(text, false),
		goslack.MsgOptionTS(messageTs),
		noticeMetadata(noticeError),
	); perr != nil {
		a.logger.Error("failed to post HIL correction notice", "error", perr, "requestID", requestID)
	}
}

func (a *SlackGatewayAdapter) collectHILState(callback goslack.InteractionCallback, fields []slackHILField) map[string]any {
	dataModel := map[string]any{}
	var state map[string]map[string]goslack.BlockAction
	if callback.BlockActionState != nil {
		state = callback.BlockActionState.Values
	}
	for _, f := range fields {
		ba := state[f.Block][f.Action]
		switch f.Kind {
		case a2ui.FieldSingleChoice:
			a2ui.SetPath(dataModel, f.Path, ba.SelectedOption.Value)
		case a2ui.FieldMultiChoice:
			vals := make([]any, 0, len(ba.SelectedOptions))
			for _, o := range ba.SelectedOptions {
				vals = append(vals, o.Value)
			}
			a2ui.SetPath(dataModel, f.Path, vals)
		case a2ui.FieldText:
			a2ui.SetPath(dataModel, f.Path, ba.Value)
		}
	}
	return dataModel
}
