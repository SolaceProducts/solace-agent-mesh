// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	actionCancelTask      = "sam_cancel_task"
	actionHILSubmit       = "sam_hil_submit"
	actionHILCancel       = "sam_hil_cancel"
	actionFeedback        = "sam_feedback"
	actionFeedbackDetails = "sam_feedback_details"
	actionFeedbackCancel  = "sam_feedback_cancel"
)

const (
	paramTaskID    = "taskId"
	paramAgentName = "agentName"
	paramRequestID = actionRequestIDParam
	paramFields    = "fields"
	paramContext   = "context"
	paramSessionID = "sessionId"
	paramRating    = "rating"
	paramRater     = "rater"
	paramNotify    = "notify"
	paramRecipient = "recipient"
)

const notifyRoutedValue = "true"

const feedbackCommentInput = "feedback_comment"

const signInCardID = "sam-authorize"

const (
	ratingUp   = "up"
	ratingDown = "down"
)

const (
	cancelButtonLabel    = "Cancel"
	authorizeButtonLabel = "Authorize"
	hilSubmitLabel       = "Submit"
	hilDefaultTitle      = "Input requested"
	feedbackPrompt       = "Was this answer helpful?"
	feedbackCommentLabel = "Comment"
	feedbackCommentHint  = "Optional"
	feedbackSubmitLabel  = "Submit"
	feedbackCardID       = "sam-feedback"
)

const (
	selectionRadio    = "RADIO_BUTTON"
	selectionCheckbox = "CHECK_BOX"
	textSingleLine    = "SINGLE_LINE"
	textMultiLine     = "MULTIPLE_LINE"
)

func card(id string, sections ...*chat.GoogleAppsCardV1Section) []*chat.CardWithId {
	return []*chat.CardWithId{{CardId: id, Card: &chat.GoogleAppsCardV1Card{Sections: sections}}}
}

func section(widgets ...*chat.GoogleAppsCardV1Widget) *chat.GoogleAppsCardV1Section {
	return &chat.GoogleAppsCardV1Section{Widgets: widgets}
}

func buttons(bs ...*chat.GoogleAppsCardV1Button) *chat.GoogleAppsCardV1Widget {
	return &chat.GoogleAppsCardV1Widget{ButtonList: &chat.GoogleAppsCardV1ButtonList{Buttons: bs}}
}

func paragraph(text string) *chat.GoogleAppsCardV1Widget {
	return &chat.GoogleAppsCardV1Widget{TextParagraph: &chat.GoogleAppsCardV1TextParagraph{Text: text}}
}

func actionButton(label, function string, params map[string]string) *chat.GoogleAppsCardV1Button {
	action := &chat.GoogleAppsCardV1Action{Function: function}
	params = maps.Clone(params)
	params[buttonActionParam] = function
	for _, key := range slices.Sorted(maps.Keys(params)) {
		action.Parameters = append(action.Parameters, &chat.GoogleAppsCardV1ActionParameter{Key: key, Value: params[key]})
	}
	return &chat.GoogleAppsCardV1Button{Text: label, OnClick: &chat.GoogleAppsCardV1OnClick{Action: action}}
}

func (a *Adapter) buttonsReachAdapter(meta taskMeta) bool {
	return !meta.AddOn || a.addOnButtonURL() != ""
}

func (a *Adapter) addOnButtonURL() string {
	if a.cfg.Delivery != DeliveryHTTPS {
		return ""
	}
	return a.cfg.EndpointURL
}

func (a *Adapter) routeButtons(cards []*chat.CardWithId, meta taskMeta) []*chat.CardWithId {
	if !meta.AddOn {
		return cards
	}
	url := a.addOnButtonURL()
	for _, c := range cards {
		for _, sec := range c.Card.Sections {
			for _, w := range sec.Widgets {
				if w.ButtonList == nil {
					continue
				}
				for _, b := range w.ButtonList.Buttons {
					if b.OnClick != nil && b.OnClick.Action != nil {
						b.OnClick.Action.Function = url
					}
				}
			}
		}
	}
	return cards
}

func (a *Adapter) taskControls(taskID string, meta taskMeta) []*chat.CardWithId {
	if !a.buttonsReachAdapter(meta) {
		return nil
	}
	return a.routeButtons(cancelControls(taskID, meta.AgentName), meta)
}

func cancelControls(taskID, agentName string) []*chat.CardWithId {
	return card("sam-task-controls", section(buttons(
		actionButton(cancelButtonLabel, actionCancelTask, map[string]string{paramTaskID: taskID, paramAgentName: agentName}),
	)))
}

func authorizeCard(text, uri string) []*chat.CardWithId {
	link := &chat.GoogleAppsCardV1Button{Text: authorizeButtonLabel, OnClick: &chat.GoogleAppsCardV1OnClick{OpenLink: &chat.GoogleAppsCardV1OpenLink{Url: uri}}}
	return card(signInCardID, section(paragraph(text), buttons(link)))
}

func signInOutcomeCard(outcome string) []*chat.CardWithId {
	return card(signInCardID, section(paragraph(outcome)))
}

func feedbackCard(taskID, sessionID string) []*chat.CardWithId {
	params := func(rating string) map[string]string {
		return map[string]string{paramTaskID: taskID, paramSessionID: sessionID, paramRating: rating, paramRequestID: idgen.New()}
	}
	return card(feedbackCardID, section(
		paragraph(feedbackPrompt),
		buttons(
			actionButton("👍", actionFeedback, params(ratingUp)),
			actionButton("👎", actionFeedback, params(ratingDown)),
		),
	))
}

type hilField struct {
	Name string            `json:"n"`
	Path string            `json:"p"`
	Kind a2ui.FieldKind    `json:"k"`
	Opts map[string]string `json:"o,omitempty"`
}

func hilCard(s *a2ui.Surface, requestID, taskID, agentName string) (cards []*chat.CardWithId, ok bool) {
	return hilCardRouted(s, requestID, map[string]string{paramRequestID: requestID, paramTaskID: taskID, paramAgentName: agentName})
}

func notifyHILCard(s *a2ui.Surface, requestID, recipientEmail string) (cards []*chat.CardWithId, ok bool) {
	return hilCardRouted(s, requestID, map[string]string{paramRequestID: requestID, paramNotify: notifyRoutedValue, paramRecipient: recipientEmail})
}

func hilCardRouted(s *a2ui.Surface, requestID string, routing map[string]string) (cards []*chat.CardWithId, ok bool) {
	widgets, ok := hilDetailWidgets(s.Details)
	if !ok {
		return nil, false
	}
	if s.Question != "" {
		widgets = append(widgets, paragraph(cardText(s.Question)))
	}
	fields := make([]hilField, 0, len(s.Fields))
	for i, f := range s.Fields {
		name := "hil_" + strconv.Itoa(i)
		widget, opts := hilWidget(f, name)
		if widget == nil {
			continue
		}
		widgets = append(widgets, widget)
		fields = append(fields, hilField{Name: name, Path: f.Path, Kind: f.Kind, Opts: opts})
	}
	encodedFields, _ := json.Marshal(fields)

	var bs []*chat.GoogleAppsCardV1Button
	for _, btn := range s.Buttons {
		params := maps.Clone(routing)
		function := actionHILCancel
		label := btn.Label
		if !btn.IsCancel() {
			function = actionHILSubmit
			params[paramFields] = string(encodedFields)
			if encodedCtx, err := json.Marshal(btn.Context); err == nil {
				params[paramContext] = string(encodedCtx)
			}
			if label == "" {
				label = hilSubmitLabel
			}
		} else if label == "" {
			label = cancelButtonLabel
		}
		bs = append(bs, actionButton(label, function, params))
	}
	widgets = append(widgets, buttons(bs...))

	cards = card("sam-hil-"+requestID, section(widgets...))
	cards[0].Card.Header = &chat.GoogleAppsCardV1CardHeader{Title: cmpLabel(s.Title, hilDefaultTitle)}
	return cards, true
}

func hilWebUINoticeCard(s *a2ui.Surface, requestID, webUIURL string) []*chat.CardWithId {
	cards := card("sam-hil-"+requestID, section(paragraph(cardText(hilWebUINotice(webUIURL)))))
	cards[0].Card.Header = &chat.GoogleAppsCardV1CardHeader{Title: cmpLabel(s.Title, hilDefaultTitle)}
	return cards
}

func hilWebUINotice(webUIURL string) string {
	const cannotShow = "This agent needs input that can't be shown in Google Chat."
	if webUIURL == "" {
		return cannotShow + " Please use the web UI to respond."
	}
	return cannotShow + " Please respond in the web UI: " + webUIURL
}

const (
	chatMaxHILValueRunes  = 2000
	chatMaxHILLabelRunes  = 200
	chatMaxHILDetailBytes = 16000
)

func hilDetailWidgets(details []a2ui.Detail) (widgets []*chat.GoogleAppsCardV1Widget, ok bool) {
	total := 0
	for _, d := range details {
		var text string
		switch d.Kind {
		case a2ui.DetailValue:
			text = "<b>" + cardText(a2ui.Clip(d.Label, chatMaxHILLabelRunes)) + "</b><br>" + cardValue(d.Text)
		case a2ui.DetailNote:
			text = "<i>" + cardText(a2ui.Clip(d.Text, chatMaxHILValueRunes)) + "</i>"
		default:
			text = cardText(a2ui.Clip(d.Text, chatMaxHILValueRunes))
		}
		total += len(text)
		if total > chatMaxHILDetailBytes {
			return nil, false
		}
		widgets = append(widgets, paragraph(text))
	}
	return widgets, true
}

func cardText(s string) string {
	return strings.ReplaceAll(cardEscaper.Replace(s), "\n", "<br>")
}

var cardEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func cardValue(value string) string {
	if value == "" {
		return "<i>(empty)</i>"
	}
	lines := strings.Split(a2ui.Clip(value, chatMaxHILValueRunes), "\n")
	for i, line := range lines {
		rest := strings.TrimLeft(line, " ")
		lines[i] = strings.Repeat(noBreakSpace, len(line)-len(rest)) + rest
	}
	return cardText(strings.Join(lines, "\n"))
}

const noBreakSpace = string(rune(0x00A0))

func hilWidget(f a2ui.Field, name string) (*chat.GoogleAppsCardV1Widget, map[string]string) {
	switch f.Kind {
	case a2ui.FieldSingleChoice, a2ui.FieldMultiChoice:
		if len(f.Options) == 0 {
			return nil, nil
		}
		kind := selectionRadio
		if f.Kind == a2ui.FieldMultiChoice {
			kind = selectionCheckbox
		}
		input := &chat.GoogleAppsCardV1SelectionInput{Name: name, Label: f.Label, Type: kind}
		opts := make(map[string]string, len(f.Options))
		for i, o := range f.Options {
			value := "o" + strconv.Itoa(i)
			opts[value] = o.Value
			input.Items = append(input.Items, &chat.GoogleAppsCardV1SelectionItem{Text: cmpLabel(o.Label, o.Value), Value: value})
		}
		return &chat.GoogleAppsCardV1Widget{SelectionInput: input}, opts
	case a2ui.FieldText:
		kind := textSingleLine
		if f.Multiline {
			kind = textMultiLine
		}
		return &chat.GoogleAppsCardV1Widget{TextInput: &chat.GoogleAppsCardV1TextInput{Name: name, Label: f.Label, Type: kind, HintText: f.Placeholder}}, nil
	}
	return nil, nil
}

func hilResponseData(action map[string]string, formInputs map[string][]string) (map[string]any, error) {
	var fields []hilField
	if raw := action[paramFields]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return nil, err
		}
	}
	var bindings map[string]any
	if raw := action[paramContext]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &bindings); err != nil {
			return nil, err
		}
	}
	model := map[string]any{}
	for _, f := range fields {
		values := formInputs[f.Name]
		switch f.Kind {
		case a2ui.FieldMultiChoice:
			selected := make([]any, 0, len(values))
			for _, v := range values {
				selected = append(selected, optionValue(f.Opts, v))
			}
			a2ui.SetPath(model, f.Path, selected)
		case a2ui.FieldSingleChoice:
			a2ui.SetPath(model, f.Path, optionValue(f.Opts, first(values)))
		default:
			a2ui.SetPath(model, f.Path, first(values))
		}
	}
	return a2ui.ResolveContext(bindings, model), nil
}

func optionValue(opts map[string]string, synthetic string) string {
	if real, ok := opts[synthetic]; ok {
		return real
	}
	return synthetic
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func cmpLabel(label, value string) string {
	if label != "" {
		return label
	}
	return value
}

func feedbackAckCard(rating string) []*chat.CardWithId {
	return card(feedbackCardID, section(paragraph(feedbackAck(rating))))
}

func feedbackAck(rating string) string {
	return entrypoint.FeedbackAckNotice + " " + thumbFor(rating)
}

func feedbackChosen(rating string) string {
	return "Rating: " + thumbFor(rating)
}

func thumbFor(rating string) string {
	if rating == ratingDown {
		return "👎"
	}
	return "👍"
}

func feedbackDetailsCard(taskID, sessionID, rating, rater string) []*chat.CardWithId {
	widgets := []*chat.GoogleAppsCardV1Widget{
		paragraph(feedbackChosen(rating)),
		{TextInput: &chat.GoogleAppsCardV1TextInput{
			Name: feedbackCommentInput, Label: feedbackCommentLabel, HintText: feedbackCommentHint, Type: textMultiLine,
		}},
		paragraph(entrypoint.FeedbackDataUseNotice),
	}
	params := map[string]string{
		paramTaskID: taskID, paramSessionID: sessionID, paramRating: rating, paramRater: rater, paramRequestID: idgen.New(),
	}
	submit := actionButton(feedbackSubmitLabel, actionFeedbackDetails, params)
	cancel := actionButton(cancelButtonLabel, actionFeedbackCancel, params)
	return card(feedbackCardID, section(append(widgets, buttons(submit, cancel))...))
}
