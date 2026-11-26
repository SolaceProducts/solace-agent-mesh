// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	statusFrame = `{"jsonrpc":"2.0","result":{"kind":"status-update","status":{"state":"working"}}}`
	askFrame    = `{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"req-1","source":"ask_user_question"}}]}}}}`
)

func appendEvent(t *testing.T, a *HTTPSSEAdapter, taskID, eventType, payload string) {
	t.Helper()
	if _, err := a.eventLog.Append(context.Background(), taskID, sweepSession, sweepOwner, eventType, []byte(payload)); err != nil {
		t.Fatal(err)
	}
}

func streamEvents(a *HTTPSSEAdapter, taskID, caller, lastEventID string, scopes ...string) *httptest.ResponseRecorder {
	req := withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/tasks/"+taskID+"/events", nil), caller, scopes...)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

func seedEndedStream(t *testing.T, a *HTTPSSEAdapter) string {
	t.Helper()
	taskID := seedSweepTask(t, a)
	appendEvent(t, a, taskID, "status_update", statusFrame)
	appendEvent(t, a, taskID, "final_response", completedReply)
	waitForSeededBufferRow(t, a, taskID)
	return taskID
}

func TestChatV2EventsReplayAnEndedTaskToItsOwner(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedEndedStream(t, a)

	rec := streamEvents(a, taskID, sweepOwner, "")

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != chatv2.EventStreamType {
		t.Fatalf("got %d %q, want 200 %s", rec.Code, rec.Header().Get("Content-Type"), chatv2.EventStreamType)
	}
	body := rec.Body.String()
	for _, want := range []string{"event: status_update", "event: final_response", "forty-two"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream lacks %q: %s", want, body)
		}
	}
	idOfFrame(t, body, "status_update")
	idOfFrame(t, body, "final_response")
}

func TestChatV2EventsResumeAfterLastEventID(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedEndedStream(t, a)
	finalID := idOfFrame(t, streamEvents(a, taskID, sweepOwner, "").Body.String(), "final_response")
	last, err := strconv.ParseInt(finalID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}

	before := streamEvents(a, taskID, sweepOwner, strconv.FormatInt(last-1, 10)).Body.String()

	if strings.Contains(before, "event: status_update") || !strings.Contains(before, "event: final_response") {
		t.Errorf("resuming after id %d should send only the final response: %s", last-1, before)
	}
}

func TestChatV2EventsMatchTheV1Stream(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedEndedStream(t, a)

	v2 := streamEvents(a, taskID, sweepOwner, "").Body.String()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/sse/subscribe/"+taskID, nil), sweepOwner)
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)

	if v2 != rec.Body.String() {
		t.Errorf("v2 stream differs from v1:\nv2: %s\nv1: %s", v2, rec.Body)
	}
}

func TestChatV2EventsAreNotForOthers(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedEndedStream(t, a)

	if rec := streamEvents(a, taskID, sweepStranger, ""); rec.Code != http.StatusNotFound {
		t.Errorf("another user got %d, want 404", rec.Code)
	}
	if rec := streamEvents(a, taskID, sweepStranger, "", rbac.ScopeActivityRead); rec.Code != http.StatusOK {
		t.Errorf("an activity reader got %d, want 200", rec.Code)
	}
	if rec := streamEvents(a, taskID, sweepStranger, "", rbac.ScopeAgentReadTraceAny); rec.Code != http.StatusNotFound {
		t.Errorf("a trace reader got %d, want 404: the stream carries replies a trace grant does not open", rec.Code)
	}
}

func TestChatV2EventsRejectAMalformedID(t *testing.T) {
	a := newCoverageAdapter(t)

	rec := streamEvents(a, "not-a-uuid", sweepOwner, "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct {
		ValidationDetails map[string][]string `json:"validationDetails"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.ValidationDetails["id"]) == 0 {
		t.Errorf("validationDetails = %v, want an entry for id", body.ValidationDetails)
	}
}

func TestChatV2EventsRejectAMalformedLastEventID(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedEndedStream(t, a)

	for _, lastEventID := range []string{"abc", "-1", "99999999999999999999"} {
		t.Run(lastEventID, func(t *testing.T) {
			rec := streamEvents(a, taskID, sweepOwner, lastEventID)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "event: ") {
				t.Errorf("a rejected resume streamed events: %s", rec.Body)
			}
		})
	}
}

func getPending(a *HTTPSSEAdapter, taskID, caller string, scopes ...string) *httptest.ResponseRecorder {
	req := withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/tasks/"+taskID+"/pendingInput", nil), caller, scopes...)
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

func seedWaitingTask(t *testing.T, a *HTTPSSEAdapter) string {
	t.Helper()
	taskID := idgen.New()
	_ = a.core.Tasks().Register(taskID, sweepAgent, sweepOwner, entrypoint.TaskIdentity{SessionID: sweepSession})
	a.core.Tasks().SetWaiting(taskID, "req-1")
	appendEvent(t, a, taskID, "status_update", askFrame)
	return taskID
}

func TestChatV2PendingInputReturnsTheQuestionToItsOwner(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedWaitingTask(t, a)

	rec := getPending(a, taskID, sweepOwner)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		Data chatv2.PendingInput `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ID != taskID || body.Data.Type != "pendingInput" || body.Data.AgentName != sweepAgent || body.Data.Request["request_id"] != "req-1" {
		t.Errorf("pending input = %+v, want the question of %s", body.Data, taskID)
	}
}

func TestChatV2PendingInputIsNothingForEveryoneElse(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedWaitingTask(t, a)

	if rec := getPending(a, taskID, sweepStranger); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("another user got %d %q, want 204 with no body", rec.Code, rec.Body)
	}
	if rec := getPending(a, taskID, sweepStranger, rbac.ScopeActivityRead); rec.Code != http.StatusOK {
		t.Errorf("an activity reader got %d, want 200", rec.Code)
	}
	if rec := getPending(a, idgen.New(), sweepOwner); rec.Code != http.StatusNoContent {
		t.Errorf("an unknown task got %d, want 204", rec.Code)
	}
}

func answerInput(a *HTTPSSEAdapter, taskID, caller, body string, scopes ...string) *httptest.ResponseRecorder {
	req := withIdentity(httptest.NewRequest(http.MethodPut, chatv2.Prefix+"/tasks/"+taskID+"/userInput", strings.NewReader(body)), caller, scopes...)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

const answerBody = `{"agentName":"` + sweepAgent + `","requestId":"req-1","status":"answered","data":{"choice":"approve"}}`

func subscribeAgentRequests(t *testing.T, a *HTTPSSEAdapter) (recv func() *broker.Message) {
	t.Helper()
	brk := a.core.Services().Broker()
	q, err := brk.CreateQueue("test-agent-requests", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", sweepAgent)); err != nil {
		t.Fatal(err)
	}
	return func() *broker.Message {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			t.Fatalf("nothing reached the agent: %v", err)
		}
		return msg
	}
}

func sentAnswer(t *testing.T, msg *broker.Message) map[string]any {
	t.Helper()
	var req struct {
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatal(err)
	}
	for _, part := range req.Params.Message.Parts {
		if data, ok := part.Data.(map[string]any); ok && data["type"] == "user_input_response" {
			return data
		}
	}
	t.Fatalf("no user_input_response part in %s", msg.Payload)
	return nil
}

func TestChatV2AnswerSendsTheOwnersAnswerAndEndsTheWait(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})
	taskID := seedWaitingTask(t, a)
	received := subscribeAgentRequests(t, a)

	rec := answerInput(a, taskID, sweepOwner, answerBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		Data chatv2.UserInputAnswered `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ID != taskID || body.Data.RequestID != "req-1" || body.Data.Status != "answered" {
		t.Errorf("answer = %+v", body.Data)
	}
	sent := sentAnswer(t, received())
	data, _ := sent["data"].(map[string]any)
	if sent["request_id"] != "req-1" || sent["status"] != "answered" || data["choice"] != "approve" {
		t.Errorf("the agent was sent %v, want req-1 answered with choice approve", sent)
	}
	if got := getPending(a, taskID, sweepOwner); got.Code != http.StatusNoContent {
		t.Errorf("the answered question is still pending: %d %s", got.Code, got.Body)
	}
}

func TestChatV2AnswerCancelsTheQuestionWithoutData(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})
	taskID := seedWaitingTask(t, a)
	received := subscribeAgentRequests(t, a)

	rec := answerInput(a, taskID, sweepOwner, `{"agentName":"`+sweepAgent+`","requestId":"req-1","status":"cancelled","data":null}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	sent := sentAnswer(t, received())
	if sent["request_id"] != "req-1" || sent["status"] != "cancelled" {
		t.Errorf("the agent was sent %v, want req-1 cancelled", sent)
	}
	if got := getPending(a, taskID, sweepOwner); got.Code != http.StatusNoContent {
		t.Errorf("the cancelled question is still pending: %d %s", got.Code, got.Body)
	}
}

func TestChatV2AnswerIsRefused(t *testing.T) {
	const staleRequestBody = `{"agentName":"` + sweepAgent + `","requestId":"req-0","status":"answered","data":{}}`
	cases := []struct {
		name      string
		caller    string
		scopes    []string
		body      string
		malformed bool
		unknown   bool
		want      int
		message   string
		field     string
	}{
		{name: "another user", caller: sweepStranger, body: answerBody, want: http.StatusNotFound, message: "task not found"},
		{name: "an activity reader", caller: sweepStranger, scopes: []string{rbac.ScopeActivityRead}, body: answerBody, want: http.StatusNotFound, message: "task not found"},
		{name: "an unknown task", caller: sweepOwner, body: answerBody, unknown: true, want: http.StatusNotFound, message: "task not found"},
		{name: "an unknown agent", caller: sweepOwner, body: strings.Replace(answerBody, sweepAgent, "no-such-agent", 1), want: http.StatusNotFound, message: "agent not found: no-such-agent"},
		{name: "a question that is not the open one", caller: sweepOwner, body: staleRequestBody, want: http.StatusConflict, message: noOpenQuestionMsg},
		{name: "a malformed task id", caller: sweepOwner, body: answerBody, malformed: true, want: http.StatusBadRequest, field: "id"},
		{name: "a status that is neither answered nor cancelled", caller: sweepOwner, body: strings.Replace(answerBody, "answered", "maybe", 1), want: http.StatusUnprocessableEntity, field: "status"},
		{name: "a missing request id", caller: sweepOwner, body: strings.Replace(answerBody, `"requestId":"req-1",`, "", 1), want: http.StatusUnprocessableEntity, field: "requestId"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newCoverageAdapter(t)
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})
			taskID := seedWaitingTask(t, a)
			target := taskID
			if c.malformed {
				target = "not-a-uuid"
			}
			if c.unknown {
				target = idgen.New()
			}
			if got := getPending(a, taskID, sweepOwner); got.Code != http.StatusOK {
				t.Fatalf("the question is not pending before the answer: %d", got.Code)
			}

			rec := answerInput(a, target, c.caller, c.body, c.scopes...)

			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.want, rec.Body)
			}
			var refusal struct {
				Message           string              `json:"message"`
				ValidationDetails map[string][]string `json:"validationDetails"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
				t.Fatal(err)
			}
			if c.message != "" && refusal.Message != c.message {
				t.Errorf("message = %q, want %q", refusal.Message, c.message)
			}
			if c.field != "" && len(refusal.ValidationDetails[c.field]) == 0 {
				t.Errorf("validationDetails = %v, want an entry for %q", refusal.ValidationDetails, c.field)
			}
			if got := getPending(a, taskID, sweepOwner); got.Code != http.StatusOK {
				t.Errorf("a refused answer ended the wait: %d", got.Code)
			}
		})
	}
}

func TestChatV2AnAnswerIsSentOnce(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})
	taskID := seedWaitingTask(t, a)

	if rec := answerInput(a, taskID, sweepOwner, answerBody); rec.Code != http.StatusOK {
		t.Fatalf("first answer = %d: %s", rec.Code, rec.Body)
	}
	if rec := answerInput(a, taskID, sweepOwner, answerBody); rec.Code != http.StatusConflict {
		t.Errorf("repeated answer = %d, want 409: %s", rec.Code, rec.Body)
	}
}

func idOfFrame(t *testing.T, stream, eventType string) string {
	t.Helper()
	for frame := range strings.SplitSeq(stream, "\n\n") {
		if !strings.Contains(frame, "event: "+eventType+"\n") {
			continue
		}
		for line := range strings.SplitSeq(frame, "\n") {
			if id, ok := strings.CutPrefix(line, "id: "); ok {
				return id
			}
		}
	}
	t.Fatalf("no %s frame with an id in %s", eventType, stream)
	return ""
}
