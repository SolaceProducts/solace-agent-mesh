// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (SendRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SendRequest is a JSON-RPC 2.0 request that sends one message to an agent.`,
		Fields: map[string]apidoc.Field{
			"JSONRPC": {Doc: `JSONRPC is always "2.0".`},
			"ID":      {Doc: `ID is the caller's id for the request. The response repeats it.`},
			"Params":  {Doc: `Params holds the message.`},
		},
	}
}

func (SendParams) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SendParams are the parameters of a send request.`,
		Fields: map[string]apidoc.Field{
			"Message": {Doc: `Message is what the user says to the agent.`},
		},
	}
}

func (SendMessage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SendMessage is an A2A message from the user.`,
		Fields: map[string]apidoc.Field{
			"Kind":      {Doc: `Kind is always "message".`},
			"MessageID": {Doc: `MessageID identifies the message. The gateway creates one when it is left out.`},
			"Role":      {Doc: `Role is always "user".`},
			"Parts":     {Doc: `Parts are the message's A2A content parts, each an object whose kind is text, data or file.`},
			"ContextID": {Doc: `ContextID is the UUID of the chat session to continue. Leave it out to start a new session.`},
			"Metadata":  {Doc: `Metadata carries agent_name, the name of the agent to send the message to, which is required, and optionally project_id.`},
		},
	}
}

func (SendResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SendResponse is the JSON-RPC 2.0 response to a send request. It carries the task the message started, not the agent's answer: read the task with Get a task, or follow its events.`,
		Fields: map[string]apidoc.Field{
			"JSONRPC": {Doc: `JSONRPC is always "2.0".`},
			"ID":      {Doc: `ID repeats the request's id.`},
			"Result":  {Doc: `Result is the task that was started.`},
		},
	}
}

func (SendResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SendResult is the task a send started.`,
		Fields: map[string]apidoc.Field{
			"ID":        {Doc: `ID is the task's UUID. Use it to read, follow or cancel the task.`},
			"ContextID": {Doc: `ContextID is the UUID of the chat session the task runs in.`},
			"Kind":      {Doc: `Kind is always "task".`},
			"TraceID":   {Doc: `TraceID ties this task to the gateway's logs.`},
		},
	}
}
