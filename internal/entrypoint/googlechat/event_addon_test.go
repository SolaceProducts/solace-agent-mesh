// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import "testing"

func TestParseEvent_AddonMessageInDirectMessage(t *testing.T) {
	ev, err := parseEvent([]byte(`{
		"commonEventObject": {"hostApp": "CHAT"},
		"chat": {
			"user": {"name": "users/1", "email": "ada@example.com", "displayName": "Ada", "type": "HUMAN"},
			"eventTime": "2026-09-30T12:00:00Z",
			"messagePayload": {
				"space": {"name": "spaces/D", "spaceType": "DIRECT_MESSAGE", "singleUserBotDm": true},
				"message": {"name": "spaces/D/messages/M", "text": " hello ", "thread": {"name": "spaces/D/threads/T"}}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != kindMessage || !ev.IsDM || !ev.Addressed || ev.Text != "hello" || ev.EventKey != "spaces/D/messages/M" ||
		ev.ThreadName != "spaces/D/threads/T" || ev.SenderEmail != "ada@example.com" || !ev.AddOn {
		t.Errorf("event = %+v", ev)
	}

	classic, err := parseEvent([]byte(`{"type": "MESSAGE", "user": {"name": "users/1", "type": "HUMAN"},
		"space": {"name": "spaces/D", "spaceType": "DIRECT_MESSAGE", "singleUserBotDm": true},
		"message": {"name": "spaces/D/messages/M", "text": "hello"}}`))
	if err != nil || classic.AddOn {
		t.Errorf("classic event = %+v (err %v), want AddOn false", classic, err)
	}
}

func TestParseEvent_AddonSpaceMentionAndMembership(t *testing.T) {
	mention, _ := parseEvent([]byte(`{"chat": {
		"user": {"name": "users/1", "email": "ada@example.com", "type": "HUMAN"},
		"messagePayload": {
			"space": {"name": "spaces/S", "spaceType": "SPACE"},
			"message": {"name": "spaces/S/messages/M", "text": "@SAM hi", "argumentText": " hi",
				"annotations": [{"type": "USER_MENTION", "userMention": {"user": {"name": "users/app", "type": "BOT"}}}]}
		}}}`))
	if mention.IsDM || !mention.Addressed || mention.Text != "hi" {
		t.Errorf("mention = %+v", mention)
	}

	added, _ := parseEvent([]byte(`{"chat": {"user": {"type": "HUMAN"}, "addedToSpacePayload": {"space": {"name": "spaces/S"}}}}`))
	if added.Kind != kindIgnored || added.IgnoreReason != "membership_event" {
		t.Errorf("added = %+v", added)
	}
}

func TestParseEvent_AddonButtonClick(t *testing.T) {
	ev, err := parseEvent([]byte(`{
		"commonEventObject": {"invokedFunction": "sam_cancel_task", "parameters": {"taskId": "t1", "agentName": "a"}},
		"chat": {
			"user": {"name": "users/1", "email": "ada@example.com", "type": "HUMAN"},
			"buttonClickedPayload": {
				"space": {"name": "spaces/D", "type": "DM"},
				"message": {"name": "spaces/D/messages/M", "thread": {"name": "spaces/D/threads/T"}}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != kindCardClick || ev.Action[actionMethodParam] != "sam_cancel_task" || ev.Action["taskId"] != "t1" || ev.MessageName != "spaces/D/messages/M" {
		t.Errorf("click = %+v", ev)
	}
}

func TestParseEvent_AddonClickRoutesOnButtonParameterAndReadsForm(t *testing.T) {
	ev, err := parseEvent([]byte(`{
		"commonEventObject": {
			"invokedFunction": "projects/p/topics/t",
			"parameters": {"samAction": "sam_hil_submit", "requestId": "r1", "taskId": "t1"},
			"formInputs": {"hil_0": {"stringInputs": {"value": ["o1"]}}}
		},
		"chat": {"user": {"name": "users/1", "email": "ada@example.com", "type": "HUMAN"},
			"buttonClickedPayload": {"space": {"name": "spaces/D", "type": "DM"}, "message": {"name": "spaces/D/messages/M"}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Action[actionMethodParam] != actionHILSubmit || ev.FormInputs["hil_0"][0] != "o1" {
		t.Errorf("click = %+v", ev)
	}
}
