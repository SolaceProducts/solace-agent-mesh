// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	HILSubmitAction = "a2a_user_input_submit"
	HILCancelAction = "a2a_user_input_cancel"
)

func (a *TeamsAdapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: true}
}

var _ entrypoint.CapabilityProvider = (*TeamsAdapter)(nil)

type teamsHILField struct {
	Input string            `json:"in"`
	Path  string            `json:"p"`
	Kind  a2ui.FieldKind    `json:"k"`
	Opts  map[string]string `json:"o,omitempty"`
}

func (a *TeamsAdapter) handleUserInputRequest(ctx context.Context, meta *TeamsTaskMeta, taskID string, req *a2a.UserInputRequestData) {
	log := a.logger.With("taskID", taskID, "requestID", req.RequestID)
	surface, err := a2ui.Parse(req.Surface)
	if err != nil {
		log.Error("failed to parse A2UI surface", "error", err)
		return
	}
	promptItem := itemHILPromptedPrefix + req.RequestID
	if a.hasItem(ctx, taskID, promptItem) {
		log.Debug("HIL card already posted")
		return
	}
	agentName := a.core.Tasks().AgentName(taskID)

	meta.mu.Lock()
	replyToID := ""
	if meta.Activity != nil {
		replyToID = meta.Activity.ID
	}
	meta.mu.Unlock()

	card, ok := a.buildHILCard(surface, req.RequestID, taskID, agentName, false)
	if ok {
		_, err := sendTeamsMessage(a.ctx, a.botClient, meta, surfaceTitle(surface), &card, replyToID)
		if err == nil {
			a.putItem(ctx, taskID, promptItem, "")
			return
		}
		log.Warn("Teams refused the HIL card; pointing the user at the web UI", "error", err)
	} else {
		log.Warn("HIL surface not representable in Teams; pointing the user at the web UI")
	}
	if _, err := sendTeamsMessage(a.ctx, a.botClient, meta, hilWebUINotice(a.core.WebUIURL()), nil, replyToID); err != nil {
		log.Error("failed to post HIL degrade notice", "error", err)
		return
	}
	a.putItem(ctx, taskID, promptItem, "")
}

func hilWebUINotice(webUIURL string) string {
	const cannotShow = "This agent needs input that can't be shown in Teams."
	if webUIURL == "" {
		return cannotShow + " Please use the web UI to respond."
	}
	return cannotShow + " Please respond in the web UI: " + webUIURL
}

func surfaceTitle(s *a2ui.Surface) string {
	return cmp.Or(s.Title, "Input requested")
}

const (
	teamsMaxHILValueRunes  = 2000
	teamsMaxHILDetailRunes = 12000
)

func (a *TeamsAdapter) buildHILCard(s *a2ui.Surface, requestID, taskID, agentName string, notifyRouted bool) (Attachment, bool) {
	var body []any
	if s.Title != "" {
		body = append(body, map[string]any{
			"type": "TextBlock", "text": s.Title, "wrap": true, "weight": "bolder",
		})
	}
	details, ok := hilDetailElements(s.Details)
	if !ok {
		return Attachment{}, false
	}
	body = append(body, details...)

	fields := make([]teamsHILField, 0, len(s.Fields))
	for i, f := range s.Fields {
		inputID := "hil_" + strconv.Itoa(i)
		if f.Label != "" {
			body = append(body, map[string]any{"type": "TextBlock", "text": f.Label, "wrap": true})
		}
		if el, opts := hilInputElement(f, inputID); el != nil {
			body = append(body, el)
			fields = append(fields, teamsHILField{Input: inputID, Path: f.Path, Kind: f.Kind, Opts: opts})
		}
	}

	qmeta, _ := json.Marshal(fields)

	var actions []any
	for _, btn := range s.Buttons {
		data := map[string]any{
			"requestId":       requestID,
			"taskId":          taskID,
			"targetAgentName": agentName,
		}
		if notifyRouted {
			data["notify"] = true
		}
		if btn.IsCancel() {
			data["action"] = HILCancelAction
		} else {
			data["action"] = HILSubmitAction
			data["qmeta"] = string(qmeta)
			if ctx, err := json.Marshal(btn.Context); err == nil {
				data["ctx"] = string(ctx)
			}
		}
		title := btn.Label
		if title == "" {
			if btn.IsCancel() {
				title = "Cancel"
			} else {
				title = "Submit"
			}
		}
		action := map[string]any{"type": "Action.Submit", "title": title, "data": data}
		if btn.IsCancel() {
			action["style"] = "destructive"
		} else if btn.Variant == "primary" {
			action["style"] = "positive"
		}
		actions = append(actions, action)
	}

	card := map[string]any{
		"type":    "AdaptiveCard",
		"$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
		"version": "1.5",
		"body":    body,
		"actions": actions,
	}
	return Attachment{ContentType: "application/vnd.microsoft.card.adaptive", Content: card}, true
}

func hilDetailElements(details []a2ui.Detail) (elements []any, ok bool) {
	total := 0
	for _, d := range details {
		text := clipRunes(d.Text, teamsMaxHILValueRunes)
		total += utf8.RuneCountInString(d.Label) + utf8.RuneCountInString(text)
		if total > teamsMaxHILDetailRunes {
			return nil, false
		}
		run := map[string]any{"type": "TextRun", "text": text}
		switch d.Kind {
		case a2ui.DetailValue:
			label := map[string]any{"type": "TextRun", "text": clipRunes(d.Label, 200), "weight": "bolder", "isSubtle": true}
			elements = append(elements, richTextBlock(label))
			run["fontType"] = "monospace"
			if text == "" {
				run["text"] = "(empty)"
			}
		case a2ui.DetailNote:
			run["size"] = "small"
			run["isSubtle"] = true
		}
		elements = append(elements, richTextBlock(run))
	}
	return elements, true
}

func richTextBlock(run map[string]any) map[string]any {
	return map[string]any{"type": "RichTextBlock", "inlines": []any{run}}
}

func clipRunes(s string, maxRunes int) string {
	total := utf8.RuneCountInString(s)
	if total <= maxRunes {
		return s
	}
	keep := max(maxRunes-utf8.RuneCountInString(a2ui.ClipNotice(total)), 0)
	return string([]rune(s)[:keep]) + a2ui.ClipNotice(total-keep)
}

func hilInputElement(f a2ui.Field, inputID string) (map[string]any, map[string]string) {
	switch f.Kind {
	case a2ui.FieldSingleChoice, a2ui.FieldMultiChoice:
		if len(f.Options) == 0 {
			return nil, nil
		}
		choices := make([]any, 0, len(f.Options))
		opts := make(map[string]string, len(f.Options))
		for i, o := range f.Options {
			title := o.Label
			if title == "" {
				title = o.Value
			}
			syn := "o" + strconv.Itoa(i)
			opts[syn] = o.Value
			choices = append(choices, map[string]any{"title": title, "value": syn})
		}
		return map[string]any{
			"type":          "Input.ChoiceSet",
			"id":            inputID,
			"style":         "expanded",
			"isMultiSelect": f.Kind == a2ui.FieldMultiChoice,
			"choices":       choices,
		}, opts
	case a2ui.FieldText:
		el := map[string]any{"type": "Input.Text", "id": inputID, "isMultiline": f.Multiline}
		if f.Placeholder != "" {
			el["placeholder"] = f.Placeholder
		}
		return el, nil
	default:
		return nil, nil
	}
}

type hilClick struct {
	raw       map[string]any
	requestID string
	taskID    string
	agentName string
	notify    bool
	answerer  *hilAnswerer
	claim     string
}

func hilAnswerItem(requestID string) string { return itemHILAnsweredPrefix + requestID }

func (a *TeamsAdapter) parseHILRouting(activity *Activity, kind string) (*hilClick, bool) {
	var raw map[string]any
	if err := json.Unmarshal(activity.Value, &raw); err != nil {
		if kind == "submit" {
			a.logger.Warn("invalid HIL submit value", "error", err)
		} else {
			a.logger.Warn("invalid HIL cancel value", "error", err)
		}
		return nil, false
	}
	requestID, _ := raw["requestId"].(string)
	taskID, _ := raw["taskId"].(string)
	agentName, _ := raw["targetAgentName"].(string)
	notify, _ := raw["notify"].(bool)
	if requestID == "" {
		return nil, false
	}
	if notify == (taskID != "") {
		a.logger.Warn("HIL click refused: the card's routing is not one this gateway renders", "requestID", requestID, "notify", notify, "hasTaskID", taskID != "")
		return nil, false
	}
	click := &hilClick{raw: raw, requestID: requestID, taskID: taskID, agentName: agentName, notify: notify}
	if !notify {
		click.answerer = a.promptAnswerer(activity, taskID)
		if !a.core.MayAnswerPrompt(taskID, click.answerer.userID) {
			a.logger.Warn("HIL click refused: clicker does not own the task", "requestID", requestID, "taskID", taskID)
			a.replyToHILClick(activity, requestID, msgHILNotYours)
			return nil, false
		}
	}
	if !a.hilDedup.SetIfAbsent(requestID, struct{}{}, runtime.WithTTL(5*time.Minute)) {
		if kind == "submit" {
			a.logger.Debug("duplicate HIL submit ignored", "requestID", requestID)
		} else {
			a.logger.Debug("duplicate HIL cancel ignored", "requestID", requestID)
		}
		if !notify && a.answerWasForwarded(a.ctx, taskID, hilAnswerItem(requestID)) {
			a.replyToHILClick(activity, requestID, msgHILAlreadyAnswered)
		}
		return nil, false
	}
	if notify {
		return click, true
	}
	state, claim := a.claimAnswer(a.ctx, taskID, hilAnswerItem(requestID))
	if state == answerClaimWon {
		click.claim = claim
		return click, true
	}
	a.hilDedup.Delete(requestID)
	a.logger.Debug("HIL click on a prompt another click answers", "requestID", requestID, "taskID", taskID)
	if state == answerClaimForwarded {
		a.replyToHILClick(activity, requestID, msgHILAlreadyAnswered)
	}
	return nil, false
}

func (a *TeamsAdapter) handleHILSubmit(activity *Activity) {
	click, ok := a.parseHILRouting(activity, "submit")
	if !ok {
		return
	}
	raw, requestID := click.raw, click.requestID

	var fields []teamsHILField
	if qmeta, _ := raw["qmeta"].(string); qmeta != "" {
		if err := json.Unmarshal([]byte(qmeta), &fields); err != nil {
			a.logger.Warn("invalid HIL qmeta; response may be incomplete", "error", err, "requestID", requestID)
		}
	}
	var ctx map[string]any
	if ctxStr, _ := raw["ctx"].(string); ctxStr != "" {
		if err := json.Unmarshal([]byte(ctxStr), &ctx); err != nil {
			a.logger.Warn("invalid HIL ctx; response bindings dropped", "error", err, "requestID", requestID)
		}
	}

	dataModel := map[string]any{}
	for _, f := range fields {
		val := raw[f.Input]
		switch f.Kind {
		case a2ui.FieldMultiChoice:
			tokens := commaTokens(val)
			out := make([]any, 0, len(tokens))
			for _, tok := range tokens {
				out = append(out, mapOpt(f.Opts, tok))
			}
			a2ui.SetPath(dataModel, f.Path, out)
		case a2ui.FieldSingleChoice:
			s, _ := val.(string)
			a2ui.SetPath(dataModel, f.Path, mapOpt(f.Opts, s))
		default:
			s, _ := val.(string)
			a2ui.SetPath(dataModel, f.Path, s)
		}
	}

	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: requestID,
		Status:    "answered",
		Data:      a2ui.ResolveContext(ctx, dataModel),
	}
	a.logger.Info("HIL submit received", "requestID", requestID, "taskID", click.taskID, "agentName", click.agentName)
	a.submitHILResponse(activity, click, resp)
}

func (a *TeamsAdapter) handleHILCancel(activity *Activity) {
	click, ok := a.parseHILRouting(activity, "cancel")
	if !ok {
		return
	}
	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: click.requestID,
		Status:    "cancelled",
	}
	a.logger.Info("HIL cancel received", "requestID", click.requestID, "taskID", click.taskID)
	a.submitHILResponse(activity, click, resp)
}

const (
	msgHILNotYours        = "Only the person who asked can answer this."
	msgHILAlreadyAnswered = "This request has already been answered."
	msgHILCardSubmitted   = "✅ Response submitted by %s"
	msgHILCardCancelled   = "🛑 Request cancelled by %s"
)

func (a *TeamsAdapter) replyToHILClick(activity *Activity, requestID, text string) {
	meta := &TeamsTaskMeta{ConversationID: activity.Conversation.ID, ServiceURL: activity.ServiceURL}
	if _, err := sendTeamsMessage(a.ctx, a.botClient, meta, text, nil, activity.ID); err != nil {
		a.logger.Debug("failed to post the HIL click notice", "requestID", requestID, "error", err)
	}
}

type hilAnswerer struct {
	identity *auth.Subject
	userID   string
}

func (a *TeamsAdapter) promptAnswerer(activity *Activity, taskID string) *hilAnswerer {
	realIdentity := a.emailCache.ResolveIdentity(a.ctx, activity.From.ID, activity.From.Name,
		activity.From.AadObjectID, activity.ServiceURL, activity.Conversation.ID)
	convType := activity.Conversation.ConversationType
	isMultiParty := convType != ConversationTypePersonal || activity.Conversation.IsGroup
	identity := auth.ForChatResponder(realIdentity, auth.ChannelPolicy{
		IsMultiParty:              isMultiParty,
		UseUserIdentityInChannels: a.cfg.UseUserIdentityInChannels,
		RunAs:                     a.cfg.RunAs,
	})
	clicked := entrypoint.ChatPlace{Conversation: activity.Conversation.ID, User: activity.From.ID}
	return &hilAnswerer{identity: identity, userID: a.core.PromptAnswerUserID(taskID, identity.UserID(), a.askedPlace(taskID), clicked)}
}

func (a *TeamsAdapter) askedPlace(taskID string) entrypoint.ChatPlace {
	if dest, ok := a.recordedDestination(a.ctx, taskID); ok {
		return entrypoint.ChatPlace{Conversation: dest.ConversationID, User: dest.AskedBy}
	}
	meta, _ := a.core.Tasks().Metadata(taskID).(*TeamsTaskMeta)
	if meta == nil || meta.Activity == nil {
		a.logger.Warn("HIL click: no record of where the task was asked; answering as the clicker", "taskID", taskID)
		return entrypoint.ChatPlace{}
	}
	return entrypoint.ChatPlace{Conversation: meta.ConversationID, User: meta.Activity.From.ID}
}

func (a *TeamsAdapter) retryHILSubmit(activity *Activity, requestID string) {
	a.hilDedup.Delete(requestID)
	meta := &TeamsTaskMeta{ConversationID: activity.Conversation.ID, ServiceURL: activity.ServiceURL}
	if _, serr := sendTeamsMessage(a.ctx, a.botClient, meta, "⚠️ Your response couldn't be submitted — please try again.", nil, activity.ID); serr != nil {
		a.logger.Error("failed to post HIL delivery-error notice", "error", serr, "requestID", requestID)
	}
}

func (a *TeamsAdapter) submitHILResponse(activity *Activity, click *hilClick, resp a2a.UserInputResponseData) {
	if click.notify {
		if err := a.publishNotifyReply(resp); err != nil {
			a.retryHILSubmit(activity, resp.RequestID)
			return
		}
	} else {
		answerItem := hilAnswerItem(click.requestID)
		if err := a.core.SubmitUserInputResponseAttributedTo(a.ctx, click.agentName, click.taskID, resp, click.answerer.identity, click.answerer.userID); err != nil {
			a.logger.Error("failed to submit HIL response", "error", err, "taskID", click.taskID, "agentName", click.agentName)
			a.releaseAnswer(a.ctx, click.taskID, answerItem, click.claim)
			a.retryHILSubmit(activity, resp.RequestID)
			return
		}
		a.markAnswerForwarded(a.ctx, click.taskID, answerItem, click.claim)
	}
	ack, answered := "✅ Response submitted.", msgHILCardSubmitted
	if resp.Status == "cancelled" {
		ack, answered = "🛑 Request cancelled.", msgHILCardCancelled
	}
	a.retireClickedCard(a.ctx, activity, fmt.Sprintf(answered, activity.From.Name))
	meta := &TeamsTaskMeta{ConversationID: activity.Conversation.ID, ServiceURL: activity.ServiceURL}
	if _, err := sendTeamsMessage(a.ctx, a.botClient, meta, ack, nil, activity.ID); err != nil {
		a.logger.Debug("failed to post HIL ack", "error", err)
	}
}

func commaTokens(v any) []string {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mapOpt(opts map[string]string, token string) string {
	if real, ok := opts[token]; ok {
		return real
	}
	return token
}
