// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAResultWhoseDataHasASignalsTypeIsRefusedWhateverItsState(t *testing.T) {
	for _, state := range []string{stateCompleted, stateFailed} {
		for _, kind := range []string{"authentication_required", "user_input_request", "gateway_timeout_warning"} {
			t.Run(state+" "+kind, func(t *testing.T) {
				r, c := connectedRig(t, nil)
				answers := r.listen(responseTopic("T1"))
				r.taskOn(c, "T1", nil)

				c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": state, "text": "sign in",
					"data": map[string]any{"type": kind, "auth_uri": "https://attacker.example/login"}})
				c.refused(errCodeBadMessage, "T1")
				none(t, answers)

				c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
				if m := recv(t, answers); strings.Contains(string(m.Payload), "attacker.example") {
					t.Fatalf("the caller got the agent's signal: %s", m.Payload)
				}
			})
		}
	}
}

func TestAFailedResultWithoutAnErrorIsTheInternalErrorAndCarriesNoAgentParts(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)

	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateFailed, "text": "it broke",
		"data": map[string]any{"type": "report", "auth_uri": "https://attacker.example/login"}})
	m := recv(t, answers)
	var resp struct {
		Result any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(m.Payload, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != codeInternalError || resp.Error.Message != msgInternalError || resp.Result != nil {
		t.Fatalf("the caller got %s", m.Payload)
	}
	if strings.Contains(string(m.Payload), "attacker.example") || strings.Contains(string(m.Payload), "it broke") {
		t.Fatalf("the agent's parts reached the caller: %s", m.Payload)
	}
}

func TestAResultWithASignalNestedInItsDataIsRefused(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done",
		"data": map[string]any{"type": "report", "steps": []any{map[string]any{"type": "user_input_request"}}}})
	c.refused(errCodeBadMessage, "T1")
	none(t, answers)
}

func TestAToolResultCarryingAShapeSAMActsOnIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		result any
		failed bool
	}{
		"a redirect button":     {map[string]any{"type": "redirect", "route": "/builder", "label": "Continue"}, false},
		"a nested sign-in card": {map[string]any{"rows": []any{map[string]any{"type": "authentication_required"}}}, false},
		"a failed call's data":  {map[string]any{"detail": map[string]any{"type": "request_build_activate"}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			r, c := connectedRig(t, nil)
			status := r.listen(statusTopic("T1"))
			r.taskOn(c, "T1", nil)
			c.send(event("T1", map[string]any{"kind": eventToolFinished, "callId": "c1", "name": "lookup", "result": tc.result, "failed": tc.failed}))
			c.refused(errCodeBadMessage, "T1")
			none(t, status)
		})
	}
}

func TestAnAnswerNamingATaskRecordKeyIsRefusedButAToolResultMayCarryOne(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	status := r.listen(statusTopic("T1"))
	r.taskOn(c, "T1", nil)

	c.send(event("T1", map[string]any{"kind": eventToolFinished, "callId": "c1", "name": "fetch",
		"result": map[string]any{"http": map[string]any{"metadata": map[string]any{"retry_attempt": 2, "trace_id": "abc"}}}}))
	recv(t, status)

	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done",
		"data": map[string]any{"report": 1, "metadata": map[string]any{"trace_id": "forged-trace"}}})
	if f := c.refused(errCodeBadMessage, "T1"); !strings.Contains(f.Message, "trace_id") {
		t.Fatalf("the refusal does not name the key: %q", f.Message)
	}
	none(t, answers)
}
