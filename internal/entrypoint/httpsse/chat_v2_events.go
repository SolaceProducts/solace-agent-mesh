// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

func (a *HTTPSSEAdapter) chatV2EventRoutes(sub *subscribeHandler, hil *hilHandler) []apicontract.Route {
	return chatv2.EventRoutes(chatv2.Deps{
		StreamTask:       a.streamChatTask(sub),
		ReadPendingInput: hil.readChatPendingInput,
		AnswerInput:      hil.answerChatInput,
	})
}

func (a *HTTPSSEAdapter) streamChatTask(sub *subscribeHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID, ok := apicontract.PathUUID(w, r, "id")
		if !ok {
			return
		}
		fromSeq, ok := chatResumeFrom(w, r)
		if !ok || !a.callerMayReadTask(w, r, taskID) {
			return
		}
		sub.stream(w, r, taskID, fromSeq)
	}
}

func chatResumeFrom(w http.ResponseWriter, r *http.Request) (int64, bool) {
	fromSeq, resumed := resumeFrom(r)
	if !resumed && r.Header.Get(lastEventIDHeader) != "" {
		apicontract.WriteErrorWithCause(w, http.StatusBadRequest, apicontract.MsgInvalidHeader,
			map[string][]string{lastEventIDHeader: {"must be the non-negative integer id of an event"}}, nil)
		return 0, false
	}
	return fromSeq, true
}

func (h *hilHandler) readChatPendingInput(ctx context.Context, taskID string) (*chatv2.PendingInput, error) {
	pending := h.pendingInput(ctx, taskID, resolveUserIDCtx(ctx, h.core))
	if pending == nil {
		return nil, nil
	}
	return &chatv2.PendingInput{ID: pending.taskID, Type: "pendingInput", AgentName: pending.agentName, Request: pending.request}, nil
}

func (h *hilHandler) answerChatInput(ctx context.Context, answer chatv2.UserInputAnswer) (chatv2.UserInputAnswered, error) {
	userID := auth.UserIDFromRequestContext(ctx, h.core.Config().DefaultUserIdentity)
	ref := h.respond(ctx, requestCallerCtx(ctx, userID), userID, hilRespondRequest{
		TaskID: answer.ID, AgentName: answer.AgentName, RequestID: answer.RequestID, Status: answer.Status, Data: answer.Data,
	}, true)
	if ref != nil {
		return chatv2.UserInputAnswered{}, &apicontract.CallerError{Status: ref.status, Message: ref.msg}
	}
	return chatv2.UserInputAnswered{ID: answer.ID, Type: "userInputAnswer", RequestID: answer.RequestID, Status: answer.Status}, nil
}
