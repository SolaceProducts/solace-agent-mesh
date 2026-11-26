// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"net/http"
	"reflect"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const GateAgentInvoke apicontract.Gate = "agentInvoke"

var messagesTag = apicontract.Tag{Name: "Chat Messages", Group: tagGroup, Text: "Send a message to an agent."}

type SendRequest struct {
	JSONRPC string     `json:"jsonrpc" example:"2.0"`
	ID      string     `json:"id" example:"req-1"`
	Params  SendParams `json:"params"`
}

type SendParams struct {
	Message SendMessage `json:"message"`
}

type SendMessage struct {
	Kind      string           `json:"kind" example:"message"`
	MessageID string           `json:"messageId" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	Role      string           `json:"role" example:"user"`
	Parts     []map[string]any `json:"parts" example:"[{\"kind\":\"text\",\"text\":\"Summarize the report.\"}]"`
	ContextID string           `json:"contextId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Metadata  map[string]any   `json:"metadata" example:"{\"agent_name\":\"Support\"}"`
}

type SendResponse struct {
	JSONRPC string     `json:"jsonrpc" example:"2.0"`
	ID      string     `json:"id" example:"req-1"`
	Result  SendResult `json:"result"`
}

type SendResult struct {
	ID        string `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	ContextID string `json:"contextId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Kind      string `json:"kind" example:"task"`
	TraceID   string `json:"traceId" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7d"`
}

const sendText = "Starts a task by sending a message to an agent and answers at once with the task, before the agent has replied. " +
	"The request and the response are JSON-RPC 2.0, as in A2A. A refusal is a JSON-RPC error with the HTTP status noted below and, in error.data.traceId, the trace of the attempt: " +
	"400 for a malformed request, 403 when the caller may not use the agent, 404 for an unknown agent or a session that is not the caller's, and 429 with Retry-After when a budget is spent. " +
	"To get the answer as one result, read the task until its state is final. "

func (h *messageHandler) handler() apicontract.Handler {
	return apicontract.HandleEnvelope(reflect.TypeFor[SendRequest](), reflect.TypeFor[SendResponse](), reflect.TypeFor[SendResponse](), h.serve)
}

type messageHandler struct {
	deps Deps
}

func (h *messageHandler) serve(w http.ResponseWriter, r *http.Request) {
	if h.deps.Send == nil {
		apicontract.WriteErrorWithCause(w, http.StatusServiceUnavailable, "Chat messages are not available on this gateway.", nil, nil)
		return
	}
	h.deps.Send(w, r)
}

func MessageRoutes(deps Deps) []apicontract.Route {
	h := &messageHandler{deps: deps}
	return []apicontract.Route{
		{Method: http.MethodPut, Path: "/message/send", Gate: GateAgentInvoke,
			Stability: apicontract.StabilityDraft, MaxBodyBytes: apicontract.MaxJSONBodyBytes,
			Doc: apicontract.Doc{ID: "sendChatMessage", Summary: "Initiate a task with a message",
				Text: sendText + "To follow the task's events as they happen, use the event stream.",
				Tag:  messagesTag},
			Handler: h.handler()},
		{Method: http.MethodPut, Path: "/message/stream", Gate: GateAgentInvoke,
			Stability: apicontract.StabilityDraft, MaxBodyBytes: apicontract.MaxJSONBodyBytes,
			Doc: apicontract.Doc{ID: "streamChatMessage", Summary: "Initiate a task with a message for use with an event stream",
				Text: sendText + "It answers exactly as the send operation does, with the task and not its events, and is kept for clients that pair a send with an event stream.",
				Tag:  messagesTag},
			Handler: h.handler()},
	}
}
