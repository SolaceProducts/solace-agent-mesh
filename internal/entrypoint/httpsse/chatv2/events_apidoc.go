// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (EventsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `EventsRequest addresses the event stream of one task.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: `ID is the task's UUID.`},
			"LastEventID": {Doc: `LastEventID is the id of the last event the client received. The stream resumes with the event after it. Browsers send it by themselves when they reconnect.`},
		},
	}
}

func (PendingInput) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `PendingInput is the question a task is waiting for the user to answer.`,
		Fields: map[string]apidoc.Field{
			"ID":        {Doc: `ID is the UUID of the task that asks.`},
			"Type":      {Doc: `Type is always "pendingInput".`},
			"AgentName": {Doc: `AgentName is the agent that asks.`},
			"Request":   {Doc: `Request is the user_input_request the agent sent, as the event stream carries it: request_id, expires_at, source (ask_user_question or tool_approval) and the surface to render.`},
		},
	}
}

func (UserInputAnswer) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UserInputAnswer is a user's answer to a question a task asked.`,
		Fields: map[string]apidoc.Field{
			"ID":        {Doc: `ID is the task's UUID.`},
			"AgentName": {Doc: `AgentName is the agent that asked.`},
			"RequestID": {Doc: `RequestID is the request_id of the question being answered.`},
			"Status":    {Doc: `Status is answered or cancelled. It is the agent's own answer status, spelled cancelled, where the task state is spelled canceled.`},
			"Data": {Doc: `Data is the user's answer. Build it from the surface in the question's request: take the context of the
button the user pressed (the one whose event name is submit, or the approve or deny button) and replace each {"path": "/x/y"} in it with the value at that path in
the surface's dataModel after the user has filled it in. A question from ask_user_question has a picker
bound to /answers/q0 and a text field bound to /other_text/q0, so choosing Tea sends
{"answers":{"q0":"Tea"},"other_text":{"q0":""}}. For a multiple-selection picker the value is a list, and for
the Other choice it is "__other__" with the typed text in other_text. A tool approval sends
{"approved":true} or {"approved":false}, the context of its approve and deny buttons; any other shape, such as
{"approve":true} or {"approved":"true"}, denies the tool call. Null when cancelling.
For a question from ask_user_question, an answer of the wrong shape is accepted but the agent ignores it, so the task goes on as if the user had not chosen.`},
		},
	}
}

func (UserInputAnswered) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UserInputAnswered says the agent was sent the user's answer.`,
		Fields: map[string]apidoc.Field{
			"ID":        {Doc: `ID is the task's UUID.`},
			"Type":      {Doc: `Type is always "userInputAnswer".`},
			"RequestID": {Doc: `RequestID is the question that was answered.`},
			"Status":    {Doc: `Status is the status that was sent.`},
		},
	}
}
