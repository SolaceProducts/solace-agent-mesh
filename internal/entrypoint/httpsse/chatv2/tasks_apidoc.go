// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Task) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Task is one message sent to an agent and the answer it produces. Send a message to start one, then read the task until status.state is a final state to get the whole answer in one response. The states are the A2A task states, the same ones the event stream reports. A task ends in completed, failed, canceled or rejected. It pauses in input-required or auth-required until the user answers.`,
		Fields: map[string]apidoc.Field{
			"ID":            {Doc: `ID is the task's UUID, the id the send response returns.`},
			"Type":          {Doc: `Type is always "task".`},
			"ContextID":     {Doc: `ContextID is the UUID of the chat session the task runs in. Null when the task belongs to no session.`},
			"Status":        {Doc: `Status is where the task stands and, once it ends, the agent's closing message.`},
			"Artifacts":     {Doc: `Artifacts are the files and data the agent produced. Empty while the task runs and for a task that produced none. The gateway keeps a task's artifacts for a limited time after it ends, so an empty list on an old task does not prove the agent produced none.`},
			"ErrorMessage":  {Doc: `ErrorMessage says why the task failed. Null for a task that did not fail.`},
			"CreatedTime":   {Doc: `CreatedTime is when the task started.`},
			"CompletedTime": {Doc: `CompletedTime is when the task reached a final state. Null while it has not, including while it waits for the user.`},
		},
	}
}

func (TaskStatus) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `TaskStatus is the state of a task and the message that goes with it.`,
		Fields: map[string]apidoc.Field{
			"State":   {Doc: `State is submitted, working, input-required, auth-required, completed, failed, canceled, rejected or unknown. A task whose state this gateway cannot read is unknown.`},
			"Message": {Doc: `Message is the agent's closing message, as an A2A message. Null while the task runs and when the agent sent none. The gateway keeps the closing message for a limited time after the task ends; an old task that shows no message may have lost it.`},
		},
	}
}

func (TaskMessage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `TaskMessage is an A2A message from the agent.`,
		Fields: map[string]apidoc.Field{
			"Parts": {Doc: `Parts are the message's A2A content parts, each an object whose kind is text, data or file. The gateway passes them on as the agent sent them.`},
		},
	}
}

func (TaskArtifact) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `TaskArtifact is a file or data the agent produced during a task.`,
		Fields: map[string]apidoc.Field{
			"ArtifactID": {Doc: `ArtifactID is the artifact's id within the task.`},
			"Name":       {Doc: `Name is the artifact's display name.`},
			"Parts":      {Doc: `Parts are the artifact's A2A content parts, each an object whose kind is text, data or file, as the agent sent them.`},
		},
	}
}
