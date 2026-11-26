// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Session) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Session is one conversation between a user and agents. Its history is the chat tasks the client saves into it.`,
		Fields: map[string]apidoc.Field{
			"ID":                       {Doc: `ID is the session's UUID. Pass it as contextId to send another message into the session.`},
			"Type":                     {Doc: `Type is always "session".`},
			"Name":                     {Doc: `Name is the session's title. Null until the user or the gateway names it.`},
			"AgentID":                  {Doc: `AgentID is the name of the agent the session talks to. Null when none was chosen.`},
			"Project":                  {Doc: `Project is the project the session belongs to. Null when it belongs to none.`},
			"Source":                   {Doc: `Source says where the session came from: chat, scheduler or builder_test. Null for a session created before the source was recorded.`},
			"HasRunningBackgroundTask": {Doc: `HasRunningBackgroundTask is true while a background task of the session is still running.`},
			"ScheduledTaskID":          {Doc: `ScheduledTaskID is the UUID of the scheduled task that created the session. Null unless source is scheduler.`},
			"ScheduledTaskName":        {Doc: `ScheduledTaskName is the name of that scheduled task. Null unless source is scheduler.`},
			"IsEphemeralTest":          {Doc: `IsEphemeralTest is true when the session talks to a temporary agent the agent builder started for testing.`},
			"CreatedBy":                {Doc: `CreatedBy identifies the user who owns the session.`},
			"CreatedTime":              {Doc: `CreatedTime is when the session was created.`},
			"UpdatedTime":              {Doc: `UpdatedTime is when the session last had activity. Null when it has had none.`},
			"LastViewedTime":           {Doc: `LastViewedTime is when the owner last opened the session. Null when they never have, which a client reads as unseen activity.`},
		},
	}
}

func (ListSessionsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ListSessionsRequest selects a page of the caller's sessions, newest activity first.`,
		Fields: map[string]apidoc.Field{
			"Page":            {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters.`},
			"ProjectID":       {Doc: `ProjectID limits the list to the sessions of one project, by UUID. Send __no_project__ to list the sessions that belong to none. Leave it out to list every session.`},
			"Source":          {Doc: `Source limits the list to chat or scheduler sessions. Any other value fails with 422. Leave it out to list both.`},
			"AgentIDs":        {Doc: `AgentIDs limits the list to sessions of these agents, as comma-separated names.`},
			"ExcludeAgentIDs": {Doc: `ExcludeAgentIDs hides sessions of these agents, as comma-separated names. Sessions of the gateway's internal agents are always hidden.`},
		},
	}
}

func (SearchSessionsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SearchSessionsRequest selects a page of the caller's sessions whose title contains a text. Message text is not searched.`,
		Fields: map[string]apidoc.Field{
			"Page":            {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters.`},
			"Query":           {Doc: `Query is the text to look for. It is trimmed, and an empty one fails with 422.`},
			"ProjectID":       {Doc: `ProjectID limits the search to one project, by UUID, or to sessions without a project when it is __no_project__.`},
			"AgentIDs":        {Doc: `AgentIDs limits the search to sessions of these agents, as comma-separated names.`},
			"ExcludeAgentIDs": {Doc: `ExcludeAgentIDs hides sessions of these agents, as comma-separated names.`},
		},
	}
}

func (CreateSessionRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateSessionRequest starts a new, empty session.`,
		Fields: map[string]apidoc.Field{
			"AgentID": {Doc: `AgentID is the name of the agent the session talks to. Leave it out to choose when the first message is sent.`},
			"Source":  {Doc: `Source marks the session's origin. Only builder_test is accepted, and any other value fails with 422. Leave it out for an ordinary chat session.`},
		},
	}
}

func (UpdateSessionRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateSessionRequest changes only the fields it includes.`,
		Fields: map[string]apidoc.Field{
			"ID":      {Doc: `ID is the session's UUID.`},
			"Name":    {Doc: `Name is the session's new title. Leave it out to keep it. Null or an empty string fails with 422.`},
			"AgentID": {Doc: `AgentID is the name of the agent the session talks to from now on. Leave it out to keep it. Null or an empty string fails with 422.`},
		},
	}
}

func (SessionProjectRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SessionProjectRequest moves a session into a project or out of it.`,
		Fields: map[string]apidoc.Field{
			"ID":        {Doc: `ID is the session's UUID.`},
			"ProjectID": {Doc: `ProjectID is the UUID of the project to move the session into. It is required: send null to take the session out of its project, since leaving it out fails with 422. A project the caller cannot read fails with 404.`},
		},
	}
}

func (GenerateSessionTitleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `GenerateSessionTitleRequest asks the gateway to name a session.`,
		Fields: map[string]apidoc.Field{
			"ID":            {Doc: `ID is the session's UUID.`},
			"UserMessage":   {Doc: `UserMessage is the user's first message. Leave it out and the gateway uses the one it has stored.`},
			"AgentResponse": {Doc: `AgentResponse is the agent's first answer. Leave it out and the gateway uses the one it has stored.`},
			"Force":         {Doc: `Force names the session even if it already has a title.`},
			"ClientSurface": {Doc: `ClientSurface tells the gateway which client is calling, for usage reporting: ui, cli or api. Any other value counts as api.`},
		},
	}
}

func (SessionMessagesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SessionMessagesRequest selects a page of a session's messages.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: `ID is the session's UUID.`},
			"Page": {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters. They page the saved chat tasks, oldest first, and each holds one or more messages.`},
		},
	}
}

func (SessionMessage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SessionMessage is one message of a session's saved history.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: `ID is the message's id, or the id of its chat task when the saved bubble had none.`},
			"Type":        {Doc: `Type is always "sessionMessage".`},
			"SessionID":   {Doc: `SessionID is the UUID of the session.`},
			"Text":        {Doc: `Text is the message's text.`},
			"SenderType":  {Doc: `SenderType is user or agent.`},
			"SenderName":  {Doc: `SenderName is the user's id for a user message, and the agent's name for an agent message.`},
			"CreatedTime": {Doc: `CreatedTime is when the message's chat task was first saved.`},
		},
	}
}

func (UnconsumedEventsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UnconsumedEventsRequest addresses the events buffered for a session.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the session's UUID.`},
		},
	}
}

func (UnconsumedEvents) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UnconsumedEvents are the task events of a session that no saved chat task has absorbed yet, so a client that reopens the session can rebuild the answer of a task that finished while it was away.`,
		Fields: map[string]apidoc.Field{
			"HasEvents":    {Doc: `HasEvents is true when at least one task has buffered events.`},
			"ID":           {Doc: `ID is the UUID of the session the figures describe.`},
			"Type":         {Doc: `Type is always "unconsumedEvents".`},
			"TaskIDs":      {Doc: `TaskIDs lists the tasks that have buffered events. A task that is still running is left out: follow it with its event stream.`},
			"EventsByTask": {Doc: `EventsByTask holds each listed task's events, in the order they happened.`},
		},
	}
}

func (UnconsumedTaskEvents) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UnconsumedTaskEvents are the buffered events of one task.`,
		Fields: map[string]apidoc.Field{
			"Events": {Doc: `Events are the task's events, oldest first.`},
		},
	}
}

func (SessionProject) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SessionProject is the project a session belongs to.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: `ID is the project's UUID.`},
			"Name": {Doc: `Name is the project's name. Null when the project is no longer readable.`},
		},
	}
}

func (UnconsumedEvent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UnconsumedEvent is one buffered task event.`,
		Fields: map[string]apidoc.Field{
			"Sequence":  {Doc: `Sequence is the event's position within its task.`},
			"EventType": {Doc: `EventType is status_update, artifact_update or final_response.`},
			"Data":      {Doc: `Data is the A2A event the event stream carried, as an object.`},
		},
	}
}

func (ContextUsageRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ContextUsageRequest asks how full the model's context window is for a session.`,
		Fields: map[string]apidoc.Field{
			"ID":    {Doc: `ID is the session's UUID.`},
			"Model": {Doc: `Model measures against this model instead of the one the session used.`},
		},
	}
}

func (ContextUsage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ContextUsage is how much of the model's context window a session uses.`,
		Fields: map[string]apidoc.Field{
			"ID":                   {Doc: `ID is the UUID of the session the figures describe.`},
			"Type":                 {Doc: `Type is always "contextUsage".`},
			"CurrentContextTokens": {Doc: `CurrentContextTokens is the size of the context the next message will start from.`},
			"PromptTokens":         {Doc: `PromptTokens is the prompt tokens spent across the whole session.`},
			"CompletionTokens":     {Doc: `CompletionTokens is the completion tokens spent across the whole session.`},
			"CachedTokens":         {Doc: `CachedTokens is the prompt tokens served from the model's cache across the whole session.`},
			"MaxInputTokens":       {Doc: `MaxInputTokens is the model's input limit. Null when it is not known.`},
			"UsagePercentage":      {Doc: `UsagePercentage is currentContextTokens as a percentage of maxInputTokens. It is 0 when the limit is not known.`},
			"Model":                {Doc: `Model is the model the figures are measured against.`},
			"TotalEvents":          {Doc: `TotalEvents is how many events the session's context holds.`},
			"TotalMessages":        {Doc: `TotalMessages is how many messages the session holds.`},
			"TotalTasks":           {Doc: `TotalTasks is how many tasks the session ran.`},
			"HasCompaction":        {Doc: `HasCompaction is true once the session's context has been compacted.`},
		},
	}
}

func (CompactSessionRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CompactSessionRequest asks the session's agent to summarize older context.`,
		Fields: map[string]apidoc.Field{
			"ID":            {Doc: `ID is the session's UUID.`},
			"TargetPercent": {Doc: `TargetPercent is how full the context may be after compaction, from 0 to 100. Send 0 to let the agent use its default of 50.`},
			"ClientSurface": {Doc: `ClientSurface tells the gateway which client is calling, for usage reporting: ui, cli or api. Any other value counts as api.`},
		},
	}
}

func (Compaction) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Compaction is what compacting a session's context achieved.`,
		Fields: map[string]apidoc.Field{
			"ID":                     {Doc: `ID is the UUID of the session the figures describe.`},
			"Type":                   {Doc: `Type is always "compaction".`},
			"EventsCompacted":        {Doc: `EventsCompacted is how many events the summary replaced.`},
			"Summary":                {Doc: `Summary is the text that replaced them.`},
			"RemainingEvents":        {Doc: `RemainingEvents is how many events the context holds afterwards.`},
			"RemainingTokens":        {Doc: `RemainingTokens is the size of the context afterwards.`},
			"SummarizerCostUsd":      {Doc: `SummarizerCostUsd is what the summary cost, in US dollars.`},
			"SummarizerInputTokens":  {Doc: `SummarizerInputTokens is the tokens the summarizer read.`},
			"SummarizerOutputTokens": {Doc: `SummarizerOutputTokens is the tokens the summarizer wrote.`},
		},
	}
}
