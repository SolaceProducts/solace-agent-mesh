// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const GateSessionOwner apicontract.Gate = "sessionOwner"

const sessionNotFoundMessage = "session not found"

var sessionsTag = apicontract.Tag{Name: "Chat Sessions", Group: tagGroup, Text: "List, create, rename and delete conversations, and save and reopen their history."}

const sessionPath = "/sessions/{id}"

type SessionProject struct {
	ID   string  `json:"id" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
	Name *string `json:"name" example:"Support runbooks"`
}

type Session struct {
	ID                       string              `json:"id" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Type                     string              `json:"type" example:"session"`
	Name                     *string             `json:"name" example:"Quarterly report"`
	AgentID                  *string             `json:"agentId" example:"Support"`
	Project                  *SessionProject     `json:"project"`
	Source                   *string             `json:"source" example:"chat"`
	HasRunningBackgroundTask bool                `json:"hasRunningBackgroundTask" example:"false"`
	ScheduledTaskID          *string             `json:"scheduledTaskId" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
	ScheduledTaskName        *string             `json:"scheduledTaskName" example:"Daily digest"`
	IsEphemeralTest          bool                `json:"isEphemeralTest" example:"false"`
	CreatedBy                string              `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime              apicontract.Instant `json:"createdTime"`
	UpdatedTime              apicontract.Instant `json:"updatedTime"`
	LastViewedTime           apicontract.Instant `json:"lastViewedTime"`
}

type ListSessionsRequest struct {
	apicontract.StrictRequest
	Page            apicontract.Page `json:"-"`
	ProjectID       string           `query:"projectId" json:"-" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
	Source          string           `query:"source" json:"-" example:"chat"`
	AgentIDs        string           `query:"agentIds" json:"-" example:"Support,Billing"`
	ExcludeAgentIDs string           `query:"excludeAgentIds" json:"-" example:"Billing"`
}

type SearchSessionsRequest struct {
	apicontract.StrictRequest
	Page            apicontract.Page `json:"-"`
	Query           string           `query:"query" json:"-" example:"quarterly report"`
	ProjectID       string           `query:"projectId" json:"-" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
	AgentIDs        string           `query:"agentIds" json:"-" example:"Support,Billing"`
	ExcludeAgentIDs string           `query:"excludeAgentIds" json:"-" example:"Billing"`
}

type CreateSessionRequest struct {
	AgentID *string `json:"agentId" example:"Support"`
	Source  *string `json:"source" example:"builder_test"`
}

type UpdateSessionRequest struct {
	ID      string                    `path:"id" json:"-"`
	Name    apicontract.Patch[string] `json:"name" example:"Quarterly report"`
	AgentID apicontract.Patch[string] `json:"agentId" example:"Support"`
}

type SessionProjectRequest struct {
	ID        string                    `path:"id" json:"-"`
	ProjectID apicontract.Patch[string] `json:"projectId" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
}

type GenerateSessionTitleRequest struct {
	ID            string `path:"id" json:"-"`
	UserMessage   string `json:"userMessage" example:"Summarize the quarterly report."`
	AgentResponse string `json:"agentResponse" example:"Revenue grew 12% over the quarter."`
	Force         bool   `json:"force" example:"false"`
	ClientSurface string `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

type SessionMessagesRequest struct {
	apicontract.StrictRequest
	ID   string           `path:"id" json:"-"`
	Page apicontract.Page `json:"-"`
}

type SessionMessage struct {
	ID          string              `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	Type        string              `json:"type" example:"sessionMessage"`
	SessionID   string              `json:"sessionId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Text        string              `json:"text" example:"Summarize the quarterly report."`
	SenderType  string              `json:"senderType" example:"user"`
	SenderName  string              `json:"senderName" example:"jane.doe@example.com"`
	CreatedTime apicontract.Instant `json:"createdTime"`
}

type UnconsumedEventsRequest struct {
	ID string `path:"id" json:"-"`
}

type UnconsumedEvents struct {
	HasEvents    bool                            `json:"hasEvents" example:"true"`
	ID           string                          `json:"id" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Type         string                          `json:"type" example:"unconsumedEvents"`
	TaskIDs      []string                        `json:"taskIds" example:"[\"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c\"]"`
	EventsByTask map[string]UnconsumedTaskEvents `json:"eventsByTask" example:"{\"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c\":{\"events\":[{\"sequence\":3,\"eventType\":\"final_response\",\"data\":{}}]}}"`
}

type UnconsumedTaskEvents struct {
	Events []UnconsumedEvent `json:"events"`
}

type UnconsumedEvent struct {
	Sequence  int            `json:"sequence" example:"3"`
	EventType string         `json:"eventType" example:"final_response"`
	Data      map[string]any `json:"data" example:"{\"jsonrpc\":\"2.0\",\"result\":{\"kind\":\"task\"}}"`
}

type ContextUsageRequest struct {
	ID    string `path:"id" json:"-"`
	Model string `query:"model" json:"-" example:"gpt-4o"`
}

type ContextUsage struct {
	ID                   string  `json:"id" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Type                 string  `json:"type" example:"contextUsage"`
	CurrentContextTokens int     `json:"currentContextTokens" example:"18250"`
	PromptTokens         int     `json:"promptTokens" example:"41200"`
	CompletionTokens     int     `json:"completionTokens" example:"6300"`
	CachedTokens         int     `json:"cachedTokens" example:"12000"`
	MaxInputTokens       *int    `json:"maxInputTokens" example:"128000"`
	UsagePercentage      float64 `json:"usagePercentage" example:"14.3"`
	Model                string  `json:"model" example:"gpt-4o"`
	TotalEvents          int     `json:"totalEvents" example:"24"`
	TotalMessages        int     `json:"totalMessages" example:"12"`
	TotalTasks           int     `json:"totalTasks" example:"6"`
	HasCompaction        bool    `json:"hasCompaction" example:"false"`
}

type CompactSessionRequest struct {
	ID            string `path:"id" json:"-"`
	TargetPercent int    `json:"targetPercent" validate:"min=0,max=100" example:"50"`
	ClientSurface string `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

type Compaction struct {
	ID                     string  `json:"id" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Type                   string  `json:"type" example:"compaction"`
	EventsCompacted        int     `json:"eventsCompacted" example:"18"`
	Summary                string  `json:"summary" example:"The user asked for the quarterly report and the agent summarized revenue."`
	RemainingEvents        int     `json:"remainingEvents" example:"6"`
	RemainingTokens        int     `json:"remainingTokens" example:"3100"`
	SummarizerCostUsd      float64 `json:"summarizerCostUsd" example:"0.0042"`
	SummarizerInputTokens  int     `json:"summarizerInputTokens" example:"9800"`
	SummarizerOutputTokens int     `json:"summarizerOutputTokens" example:"420"`
}

func SessionNotFound() error {
	return &apicontract.CallerError{Status: http.StatusNotFound, Message: sessionNotFoundMessage}
}

func sessionsUnavailable() error {
	return &apicontract.CallerError{Status: http.StatusServiceUnavailable, Message: "Chat sessions are not available on this gateway."}
}

func callIf[Req, Resp any](fn func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	if fn == nil {
		return func(context.Context, Req) (resp Resp, err error) { return resp, sessionsUnavailable() }
	}
	return fn
}

func SessionRoutes(deps Deps) []apicontract.Route {
	d := deps
	return []apicontract.Route{
		{Method: http.MethodGet, Path: "/sessions", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "listChatSessions", Summary: "Get a list of sessions",
				Text: "Returns one page of the caller's sessions, most recently active first. Sessions that hold no messages yet are left out.",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.ListSessions), "Unable to list sessions")},
		{Method: http.MethodGet, Path: "/sessions/search", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "searchChatSessions", Summary: "Get a list of sessions that match a text",
				Text: "Returns one page of the caller's sessions whose title contains the text. Message text is not searched.",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.SearchSessions), "Unable to search sessions")},
		{Method: http.MethodPost, Path: "/sessions", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "createChatSession", Summary: "Create a session",
				Text: "Creates an empty session for the caller and returns it with a server-chosen id. " +
					"Send that id as contextId on the first message, then save the conversation's history into the session as chat tasks.",
				Tag: sessionsTag},
			Handler: apicontract.Handle(callIf(d.CreateSession), "Unable to create session")},
		{Method: http.MethodGet, Path: sessionPath, Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc:     apicontract.Doc{ID: "getChatSession", Summary: "Get a session", Text: "Returns one of the caller's sessions. A session that belongs to another user answers 404, the same as one that does not exist.", Tag: sessionsTag},
			Handler: apicontract.Handle(callIf(d.GetSession), "Unable to get session")},
		{Method: http.MethodPatch, Path: sessionPath, Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "updateChatSession", Summary: "Update a session",
				Text: "Renames a session or changes the agent it talks to. Only the fields sent change.", Tag: sessionsTag},
			Handler: apicontract.Handle(callIf(d.UpdateSession), "Unable to update session")},
		{Method: http.MethodDelete, Path: sessionPath, Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "deleteChatSession", Summary: "Delete a session",
				Text: "Deletes the session and its history, and asks the agents to stop any task still running in it.", Tag: sessionsTag},
			Handler: apicontract.Handle(callIf(d.DeleteSession), "Unable to delete session")},
		{Method: http.MethodPut, Path: "/sessions/{id}/project", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "setChatSessionProject", Summary: "Update the project of a session",
				Text: "Moves the session into a project, which copies the project's files into the session, or out of its project when projectId is null. A project the caller cannot read answers 404.",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.SetSessionProject), "Unable to update the session's project")},
		{Method: http.MethodPut, Path: "/sessions/{id}/viewed", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "markChatSessionViewed", Summary: "Update a session to mark it viewed",
				Text: "Records that the owner opened the session now, which clears its unseen-activity indicator, and returns the session. It does not count as activity.",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.MarkSessionViewed), "Unable to mark the session viewed")},
		{Method: http.MethodPut, Path: "/sessions/{id}/title", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "generateChatSessionTitle", Summary: "Update a session to generate its title",
				Text: "Has the gateway name the session from its first messages. The name is written after the response, so read the session to see it. " +
					"A session that already has a title is left as it is unless force is true. A request while a naming is already running joins it.",
				Tag: sessionsTag},
			Handler: apicontract.Handle(callIf(d.GenerateSessionTitle), "Unable to generate the session's title")},
		{Method: http.MethodGet, Path: "/sessions/{id}/messages", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "listChatSessionMessages", Summary: "Get a list of the messages of a session",
				Text: "Returns one page of the session's saved history flattened to messages, oldest first. The page counts chat tasks, so it may hold more messages than pageSize.",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.ListSessionMessages), "Unable to list the session's messages")},
		{Method: http.MethodGet, Path: "/sessions/{id}/events/unconsumed", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatSessionUnconsumedEvents", Summary: "Get the events of a session that were not saved",
				Text: "Returns the buffered events of tasks that finished while no client was following them, and of tasks whose result was never saved because the client's save failed or never ran. A client that reopens a session replays them, then saves the result as a chat task, which consumes them. This is the only way to recover such a turn, and it works only until the completed-task retention of the event buffer removes the events (7 days by default).",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.ReadUnconsumedEvents), "Unable to read the session's events")},
		{Method: http.MethodGet, Path: "/sessions/{id}/contextUsage", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatSessionContextUsage", Summary: "Get the context usage of a session",
				Text: "Returns how many tokens the session's context holds and how that compares with the model's input limit. It is computed from the gateway's records and asks no agent.",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.ReadContextUsage), "Unable to get the session's context usage")},
		{Method: http.MethodPut, Path: "/sessions/{id}/compaction", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "compactChatSession", Summary: "Update a session to compact its context",
				Text: "Has the session's agent replace older context with a summary, and returns what it achieved. Send {} as the body to use the agent's default target. " +
					"Answers 403 when the caller may not use the session's agent, 409 while a compaction of the session is already running, 502 when the session has no agent or its agent cannot compact, and 504 when the agent does not answer in time.",
				Tag: sessionsTag},
			Handler: apicontract.Handle(callIf(d.CompactSession), "Unable to compact the session")},
		{Method: http.MethodPut, Path: "/sessions/{sessionId}/chatTasks/{id}", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "saveChatSessionTask", Summary: "Replace a saved chat task of a session",
				Text: saveChatTaskText, Tag: sessionsTag},
			Handler: apicontract.Handle(callIf(d.SaveChatTask), "Unable to save the chat task")},
		{Method: http.MethodGet, Path: "/sessions/{id}/chatTasks", Gate: GateSessionOwner, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "listChatSessionTasks", Summary: "Get a list of the saved chat tasks of a session",
				Text: "Returns one page of the session's saved history, oldest first, with each chat task's bubbles and metadata as the client saved them.",
				Tag:  sessionsTag},
			Handler: apicontract.Handle(callIf(d.ListChatTasks), "Unable to list the session's chat tasks")},
	}
}
