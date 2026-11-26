// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	eventTypeMessage      = "MESSAGE"
	eventTypeAddedToSpace = "ADDED_TO_SPACE"
	eventTypeRemoved      = "REMOVED_FROM_SPACE"
	eventTypeCardClicked  = "CARD_CLICKED"
)

const (
	userTypeBot          = "BOT"
	annotationMention    = "USER_MENTION"
	spaceTypeDM          = "DIRECT_MESSAGE"
	legacySpaceTypeDM    = "DM"
	attachmentUploaded   = "UPLOADED_CONTENT"
	attachmentDriveFile  = "DRIVE_FILE"
	chatMessageNameInfix = "/messages/"
)

type chatEvent struct {
	Type      string       `json:"type"`
	EventTime string       `json:"eventTime"`
	Space     chatSpace    `json:"space"`
	Message   *chatMessage `json:"message"`
	User      chatUser     `json:"user"`
	Action    *chatAction  `json:"action"`
	Common    *chatCommon  `json:"common"`
	AddOn     bool         `json:"-"`
}

type chatSpace struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	SpaceType       string `json:"spaceType"`
	SingleUserBotDM bool   `json:"singleUserBotDm"`
}

type chatUser struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Type        string `json:"type"`
}

type chatThread struct {
	Name string `json:"name"`
}

type chatMessage struct {
	Name         string           `json:"name"`
	Sender       *chatUser        `json:"sender"`
	Text         string           `json:"text"`
	ArgumentText string           `json:"argumentText"`
	Thread       *chatThread      `json:"thread"`
	Annotations  []chatAnnotation `json:"annotations"`
	SlashCommand *json.RawMessage `json:"slashCommand"`
	Attachments  []chatAttachment `json:"attachment"`
	ThreadReply  bool             `json:"threadReply"`
}

type chatAnnotation struct {
	Type        string           `json:"type"`
	UserMention *chatUserMention `json:"userMention"`
}

type chatUserMention struct {
	User chatUser `json:"user"`
}

type chatAttachment struct {
	Name              string                 `json:"name"`
	ContentName       string                 `json:"contentName"`
	ContentType       string                 `json:"contentType"`
	Source            string                 `json:"source"`
	AttachmentDataRef *chatAttachmentDataRef `json:"attachmentDataRef"`
}

type chatAttachmentDataRef struct {
	ResourceName string `json:"resourceName"`
}

type chatAction struct {
	ActionMethodName string            `json:"actionMethodName"`
	Parameters       []chatActionParam `json:"parameters"`
}

type chatActionParam struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type chatCommon struct {
	InvokedFunction string                    `json:"invokedFunction"`
	Parameters      map[string]string         `json:"parameters"`
	FormInputs      map[string]chatFormInputs `json:"formInputs"`
}

type chatFormInputs struct {
	StringInputs struct {
		Value []string `json:"value"`
	} `json:"stringInputs"`
}

type inboundKind int

const (
	kindIgnored inboundKind = iota
	kindMessage
	kindCardClick
)

type inboundEvent struct {
	Kind         inboundKind
	EventKey     string
	IgnoreReason string

	SpaceName   string
	IsDM        bool
	ThreadName  string
	MessageName string
	ThreadReply bool

	SenderName        string
	SenderEmail       string
	SenderDisplayName string

	Text      string
	Addressed bool

	Attachments []chatAttachment

	Action     map[string]string
	FormInputs map[string][]string

	AddOn bool
}

func parseEvent(payload []byte) (inboundEvent, error) {
	ev, err := decodeChatEvent(payload)
	if err != nil {
		return inboundEvent{}, fmt.Errorf("decode chat event: %w", err)
	}
	in := inboundEvent{
		SpaceName:         ev.Space.Name,
		IsDM:              isDirectMessage(ev.Space),
		SenderName:        ev.User.Name,
		SenderEmail:       strings.TrimSpace(ev.User.Email),
		SenderDisplayName: ev.User.DisplayName,
		AddOn:             ev.AddOn,
	}
	if ev.User.Type == userTypeBot {
		in.IgnoreReason = "sender_is_bot"
		return in, nil
	}

	switch ev.Type {
	case eventTypeMessage:
		return messageEvent(in, ev)
	case eventTypeCardClicked:
		return cardClickEvent(in, ev)
	case eventTypeAddedToSpace, eventTypeRemoved:
		in.IgnoreReason = "membership_event"
	default:
		in.IgnoreReason = "unhandled_event_type"
	}
	return in, nil
}

func messageEvent(in inboundEvent, ev chatEvent) (inboundEvent, error) {
	msg := ev.Message
	if msg == nil || !strings.Contains(msg.Name, chatMessageNameInfix) {
		in.IgnoreReason = "message_without_name"
		return in, nil
	}
	if msg.Sender != nil && msg.Sender.Type == userTypeBot {
		in.IgnoreReason = "sender_is_bot"
		return in, nil
	}
	if in.SpaceName == "" {
		in.IgnoreReason = "message_without_space"
		return in, nil
	}
	in.Kind = kindMessage
	in.EventKey = msg.Name
	in.MessageName = msg.Name
	if msg.Thread != nil {
		in.ThreadName = msg.Thread.Name
	}
	in.ThreadReply = msg.ThreadReply
	mentioned := mentionsChatApp(msg.Annotations)
	slash := msg.SlashCommand != nil
	in.Addressed = in.IsDM || mentioned || slash
	in.Text = strings.TrimSpace(msg.Text)
	if (mentioned || slash) && msg.ArgumentText != "" {
		in.Text = strings.TrimSpace(msg.ArgumentText)
	}
	in.Attachments = msg.Attachments
	return in, nil
}

func cardClickEvent(in inboundEvent, ev chatEvent) (inboundEvent, error) {
	params := map[string]string{}
	if ev.Action != nil {
		for _, p := range ev.Action.Parameters {
			params[p.Key] = p.Value
		}
		params[actionMethodParam] = ev.Action.ActionMethodName
	}
	if ev.Common != nil {
		for k, v := range ev.Common.Parameters {
			if _, set := params[k]; !set {
				params[k] = v
			}
		}
		if params[actionMethodParam] == "" {
			params[actionMethodParam] = ev.Common.InvokedFunction
		}
		in.FormInputs = make(map[string][]string, len(ev.Common.FormInputs))
		for name, input := range ev.Common.FormInputs {
			in.FormInputs[name] = input.StringInputs.Value
		}
	}
	if named := params[buttonActionParam]; named != "" {
		params[actionMethodParam] = named
	}
	if ev.Message != nil {
		in.MessageName = ev.Message.Name
		if ev.Message.Thread != nil {
			in.ThreadName = ev.Message.Thread.Name
		}
	}
	in.Kind = kindCardClick
	in.Action = params
	in.Addressed = true
	in.EventKey = strings.Join([]string{"click", in.MessageName, params[actionMethodParam], params[actionRequestIDParam], in.SenderName}, "|")
	return in, nil
}

const (
	actionMethodParam    = "_action"
	buttonActionParam    = "samAction"
	actionRequestIDParam = "requestId"
)

func isDirectMessage(space chatSpace) bool {
	if space.Type == legacySpaceTypeDM {
		return true
	}
	return space.SpaceType == spaceTypeDM && space.SingleUserBotDM
}

func mentionsChatApp(annotations []chatAnnotation) bool {
	for _, a := range annotations {
		if a.Type == annotationMention && a.UserMention != nil && a.UserMention.User.Type == userTypeBot {
			return true
		}
	}
	return false
}
