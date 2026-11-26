// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Bubble) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Bubble is one message of a saved turn. The gateway reads type, text, id and parts, and keeps every other key as the client sent it, so a client can store whatever it needs to draw the message again.`,
		Fields: map[string]apidoc.Field{
			"Type":    {Doc: `Type is user or agent. A system bubble is written by the gateway, for example to note a compaction, and is returned but cannot be saved.`},
			"Text":    {Doc: `Text is the message's text. It may be empty only for a message that is all files.`},
			"ID":      {Doc: `ID identifies the message. Leave it out and the chat task's id stands in for it.`},
			"IsError": {Doc: `IsError is true when the message reports a failure instead of an answer.`},
			"Parts":   {Doc: `Parts are the message's content parts. For every file the message produced or used, send a part {"kind": "artifact", "file": {"uri": "artifact://0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c/user:report.pdf?version=2"}}, with a version in the uri. The gateway counts these files as the conversation's own.`},
		},
	}
}

func (ChatTaskMetadata) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatTaskMetadata describes a saved turn. The gateway reads agentName and keeps every other key as the client sent it.`,
		Fields: map[string]apidoc.Field{
			"AgentName": {Doc: `AgentName is the name of the agent that answered. It labels the agent's messages in the session's history.`},
		},
	}
}

func (SessionChatTask) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SessionChatTask is one saved turn of a session's history: the user's message and the agent's answer, as the client drew them.`,
		Fields: map[string]apidoc.Field{
			"ID":             {Doc: `ID is the UUID of the task the turn belongs to.`},
			"Type":           {Doc: `Type is always "sessionChatTask".`},
			"SessionID":      {Doc: `SessionID is the UUID of the session.`},
			"CreatedBy":      {Doc: `CreatedBy identifies the user who saved the turn.`},
			"UserMessage":    {Doc: `UserMessage is the user's message as plain text. Null when none was saved.`},
			"MessageBubbles": {Doc: `MessageBubbles are the turn's messages, in order.`},
			"TaskMetadata":   {Doc: `TaskMetadata describes the turn. Null when the client saved none.`},
			"CreatedTime":    {Doc: `CreatedTime is when the turn was first saved.`},
			"UpdatedTime":    {Doc: `UpdatedTime is when the turn was last saved. Null if it was saved once.`},
		},
	}
}

func (SaveChatTaskRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SaveChatTaskRequest stores one turn of a session's history.`,
		Fields: map[string]apidoc.Field{
			"SessionID":         {Doc: `SessionID is the session's UUID.`},
			"ID":                {Doc: `ID is the UUID of the task the turn belongs to, the id the send response returned. A task that belongs to another user fails with 404.`},
			"UserMessage":       {Doc: `UserMessage is the user's message as plain text, for the session list and search.`},
			"MessageBubbles":    {Doc: `MessageBubbles are the turn's messages, in order. At least one is required.`},
			"TaskMetadata":      {Doc: `TaskMetadata describes the turn. Leave it out to save none.`},
			"AutoGenerateTitle": {Doc: `AutoGenerateTitle asks the gateway to name the session from this turn if it has no title yet.`},
			"ClientSurface":     {Doc: `ClientSurface tells the gateway which client is calling, for usage reporting: ui, cli or api. Any other value counts as api.`},
		},
	}
}

func (ListChatTasksRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ListChatTasksRequest selects a page of a session's saved turns.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: `ID is the session's UUID.`},
			"Page": {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters.`},
		},
	}
}
