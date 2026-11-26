// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"context"
	"net/http"
	"reflect"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const EventStreamType = "text/event-stream"

const GateTaskStream apicontract.Gate = "taskStream"

const GateTaskInputReader apicontract.Gate = "taskInputReader"

type EventsRequest struct {
	ID          string `path:"id" json:"-"`
	LastEventID string `header:"Last-Event-ID" json:"-" example:"42"`
}

const userInputFlowText = "When an agent needs the user's input, a status_update event carries a data part of type user_input_request with a request_id, an expires_at time, a source (ask_user_question or tool_approval) and the surface to render. " +
	"The task then waits. Answer it with PUT /tasks/{id}/userInput, using the same request_id, or read it again with GET /tasks/{id}/pendingInput if the client reloaded. " +
	"Once the answer is accepted, the stream carries a working status_update whose data part has type user_input_response, the same request_id and the status that was sent, and the task carries on. " +
	"A question that expires sends no event: the task carries on as if it had gone unanswered. "

const toolAuthFlowText = "When a tool needs the user to sign in to another service, a status_update event carries a data part of type authentication_required with an auth_uri. " +
	"Open auth_uri with window.open, because a page the client did not open itself cannot close itself afterwards. The identity provider then calls the gateway's callback at /api/v1/auth/tool/callback. " +
	"That path stays on v1 on purpose: customers register it as a redirect URI at their identity provider, and a client never calls it. " +
	"The task carries on by itself after the callback, so do not send the message again. Wait for a working status_update whose data part has type authentication_result and a success value of true or false. "

const eventsText = "Streams the events of a task as server-sent events (text/event-stream), from the first event or, when the Last-Event-ID header is sent, from the one after it. " +
	"Each event has an event type (status_update, artifact_update, activity_event or final_response), a data field holding the A2A event as JSON, and, except for activity_event, an id that rises with each event of the task. " +
	"The stream ends after final_response, which also carries the task's failure as a JSON-RPC error. Read the task to get the same result as one response. " +
	"To resume after a dropped connection, send the last id you received as Last-Event-ID: the replay has no gaps and no duplicates, and works from any gateway instance. " +
	"The ids are not contiguous: a burst of artifact_update events before the final_response is replayed as the final_response instead. " +
	"activity_event events have no id and are live only. A comment line is sent every 15 seconds to keep the connection open. " +
	"A task id that is not a UUID, or a Last-Event-ID that is not a non-negative integer, answers 400. A task that is unknown or that the caller may not read answers 404, which a client must not retry; 503 means the event log is unavailable and the client should retry. " +
	"Events are kept for 7 days after the task ends. " + userInputFlowText + toolAuthFlowText

const eventsUnavailableMessage = "Chat events are not available on this gateway."

type eventsHandler struct {
	deps Deps
}

func (h *eventsHandler) serve(w http.ResponseWriter, r *http.Request) {
	if h.deps.StreamTask == nil {
		apicontract.WriteErrorWithCause(w, http.StatusServiceUnavailable, eventsUnavailableMessage, nil, nil)
		return
	}
	h.deps.StreamTask(w, r)
}

type PendingInput struct {
	ID        string         `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	Type      string         `json:"type" example:"pendingInput"`
	AgentName string         `json:"agentName" example:"Support"`
	Request   map[string]any `json:"request" example:"{\"type\":\"user_input_request\",\"request_id\":\"req-1\",\"source\":\"ask_user_question\"}"`
}

type UserInputAnswer struct {
	ID        string         `path:"id" json:"-"`
	AgentName string         `json:"agentName" example:"Support" validate:"required"`
	RequestID string         `json:"requestId" example:"req-1" validate:"required"`
	Status    string         `json:"status" example:"answered" validate:"required,oneof=answered cancelled"`
	Data      map[string]any `json:"data" example:"{\"answers\":{\"q0\":\"Tea\"},\"other_text\":{\"q0\":\"\"}}"`
}

type UserInputAnswered struct {
	ID        string `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	Type      string `json:"type" example:"userInputAnswer"`
	RequestID string `json:"requestId" example:"req-1"`
	Status    string `json:"status" example:"answered"`
}

func (h *eventsHandler) pending(ctx context.Context, req apicontract.IDRequest) (apicontract.Optional[PendingInput], error) {
	if h.deps.ReadPendingInput == nil {
		return apicontract.Optional[PendingInput]{}, unavailable(eventsUnavailableMessage)
	}
	q, err := h.deps.ReadPendingInput(ctx, req.ID)
	return apicontract.Optional[PendingInput]{Value: q}, err
}

func (h *eventsHandler) answer(ctx context.Context, req UserInputAnswer) (UserInputAnswered, error) {
	if h.deps.AnswerInput == nil {
		return UserInputAnswered{}, unavailable(eventsUnavailableMessage)
	}
	return h.deps.AnswerInput(ctx, req)
}

func EventRoutes(deps Deps) []apicontract.Route {
	h := &eventsHandler{deps: deps}
	return []apicontract.Route{
		{Method: http.MethodGet, Path: "/tasks/{id}/events", Gate: GateTaskStream,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "streamChatTaskEvents", Summary: "Get the events of a task",
				Text: eventsText, FileTypes: []string{EventStreamType}, Tag: tasksTag},
			Handler: apicontract.HandleStream(reflect.TypeFor[EventsRequest](), h.serve)},
		{Method: http.MethodGet, Path: "/tasks/{id}/pendingInput", Gate: GateTaskInputReader,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatTaskPendingInput", Summary: "Get the question a task is waiting on",
				Text: "Returns the question a running task has asked the user and is waiting for, so a client that was away, such as after a page reload, can show it again. " +
					"Only questions are returned here: a task paused for sign-in is finished through its own sign-in flow, not this route. " +
					"Answers 204 with no body when the task is waiting on no question, has ended, or is one the caller may not read.",
				Tag: tasksTag},
			Handler: apicontract.Handle(h.pending, "Unable to get pending input")},
		{Method: http.MethodPut, Path: "/tasks/{id}/userInput", Gate: GateTaskOwner,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "answerChatTaskInput", Summary: "Update a task to answer its question",
				Text: "Sends the user's answer to the question a task is waiting on, or cancels the question, and returns once the agent has been sent the answer. " +
					"Only the user who started the task can answer. A task id that is not a UUID, or a body that is not valid JSON, answers 400; a status other than answered or cancelled answers 422. A task that belongs to another user answers 404, the same as one that does not exist; the owner gets 404 for an agent that is not registered, 403 when the caller may not delegate to the agent, 409 when the task is not waiting on that requestId (it was answered already, or the question has gone), and 500 when sending the answer fails. " +
					"The answer is not an A2A message: a message that carries an answer is refused by PUT /message/send. " +
					"The data field is not checked against the question, so build it from the question's surface as its description says: an answer of the wrong shape is accepted, and the agent ignores it for a question and denies the tool call for an approval. " + userInputFlowText,
				Tag: tasksTag},
			Handler: apicontract.Handle(h.answer, "Unable to answer the task's question")},
	}
}
