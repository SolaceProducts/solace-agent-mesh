// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import "encoding/json"

type addonEvent struct {
	Chat   *addonChat   `json:"chat"`
	Common *addonCommon `json:"commonEventObject"`
}

type addonChat struct {
	User                    chatUser             `json:"user"`
	EventTime               string               `json:"eventTime"`
	MessagePayload          *addonMessagePayload `json:"messagePayload"`
	ButtonClickedPayload    *addonMessagePayload `json:"buttonClickedPayload"`
	AddedToSpacePayload     *addonSpacePayload   `json:"addedToSpacePayload"`
	RemovedFromSpacePayload *addonSpacePayload   `json:"removedFromSpacePayload"`
}

type addonMessagePayload struct {
	Message *chatMessage `json:"message"`
	Space   chatSpace    `json:"space"`
}

type addonSpacePayload struct {
	Space chatSpace `json:"space"`
}

type addonCommon struct {
	InvokedFunction string                    `json:"invokedFunction"`
	Parameters      map[string]string         `json:"parameters"`
	FormInputs      map[string]chatFormInputs `json:"formInputs"`
}

func decodeChatEvent(payload []byte) (chatEvent, error) {
	var addon addonEvent
	if err := json.Unmarshal(payload, &addon); err != nil {
		return chatEvent{}, err
	}
	if addon.Chat == nil {
		var ev chatEvent
		err := json.Unmarshal(payload, &ev)
		return ev, err
	}
	return addon.classic(), nil
}

func (a addonEvent) classic() chatEvent {
	c := a.Chat
	ev := chatEvent{User: c.User, EventTime: c.EventTime, AddOn: true}
	switch {
	case c.MessagePayload != nil:
		ev.Type, ev.Message, ev.Space = eventTypeMessage, c.MessagePayload.Message, c.MessagePayload.Space
	case c.ButtonClickedPayload != nil:
		ev.Type, ev.Message, ev.Space = eventTypeCardClicked, c.ButtonClickedPayload.Message, c.ButtonClickedPayload.Space
	case c.AddedToSpacePayload != nil:
		ev.Type, ev.Space = eventTypeAddedToSpace, c.AddedToSpacePayload.Space
	case c.RemovedFromSpacePayload != nil:
		ev.Type, ev.Space = eventTypeRemoved, c.RemovedFromSpacePayload.Space
	}
	if a.Common != nil {
		ev.Common = &chatCommon{InvokedFunction: a.Common.InvokedFunction, Parameters: a.Common.Parameters, FormInputs: a.Common.FormInputs}
	}
	return ev
}
