// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func wireObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not a JSON object %s: %v", raw, err)
	}
	return m
}

func requireKeys(t *testing.T, what string, m map[string]any, keys ...string) {
	t.Helper()
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	slices.Sort(got)
	want := slices.Clone(keys)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s has keys %v, want %v", what, got, want)
	}
}

func requireValues(t *testing.T, what string, m map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if !reflect.DeepEqual(m[k], v) {
			t.Fatalf("%s: %q is %#v, want %#v", what, k, m[k], v)
		}
	}
}

func requireNonEmptyString(t *testing.T, what string, m map[string]any, key string) {
	t.Helper()
	if s, ok := m[key].(string); !ok || s == "" {
		t.Fatalf("%s: %q is %#v, want a non-empty string", what, key, m[key])
	}
}

func TestTheWelcomeFrameIsPinnedByItsLiteralJSON(t *testing.T) {
	r := newRig(t, nil, nil)
	c, _, err := r.dial(agentName, token)
	if err != nil {
		t.Fatal(err)
	}
	m := wireObject(t, c.next(typeWelcome).Raw)

	requireKeys(t, "welcome", m, "type", "protocol", "agent", "connection", "maxInflight", "limits", "enabled")
	requireValues(t, "welcome", m, map[string]any{"type": "welcome", "protocol": 2.0, "agent": agentName, "maxInflight": 2.0, "enabled": true})
	requireNonEmptyString(t, "welcome", m, "connection")

	limits, _ := m["limits"].(map[string]any)
	requireKeys(t, "welcome.limits", limits, "maxFrameBytes", "maxFrameValues", "maxStringBytes", "maxIdBytes", "maxNameBytes",
		"maxToolCallsPerModelCall", "maxModelsPerTask", "maxOpenPeerCalls", "maxOpenPrompts", "maxToolScopes", "maxFailureMessageBytes",
		"maxPromptQuestions", "maxPromptOptions", "maxPromptTextBytes", "maxQuestionHeaderChars", "maxApprovalArgs", "maxApprovalArgsBytes", "maxDescriptionBytes", "maxSkills", "maxSkillDescriptionBytes", "taskTtlSeconds", "maxCallDepth", "maxConnectionsPerAgent",
		"maxToolPayloadBytes")
	requireValues(t, "welcome.limits", limits, map[string]any{
		"maxFrameBytes": 8388608.0, "maxFrameValues": 16384.0, "maxStringBytes": 16384.0, "maxIdBytes": 128.0, "maxNameBytes": 256.0,
		"maxToolCallsPerModelCall": 128.0, "maxModelsPerTask": 32.0, "maxOpenPeerCalls": 1024.0, "maxOpenPrompts": 1024.0, "maxToolScopes": 64.0,
		"maxFailureMessageBytes": 2048.0, "maxPromptQuestions": 4.0, "maxPromptOptions": 4.0, "maxPromptTextBytes": 2048.0,
		"maxQuestionHeaderChars": 12.0, "maxApprovalArgs": 64.0, "maxApprovalArgsBytes": 65536.0, "maxDescriptionBytes": 4096.0, "maxSkills": 32.0, "maxSkillDescriptionBytes": 1024.0, "taskTtlSeconds": 7200.0, "maxCallDepth": 10.0, "maxConnectionsPerAgent": 8.0, "maxToolPayloadBytes": 262144.0,
	})
}

func TestTheWelcomeOfAStoppedAgentSaysItIsNotEnabled(t *testing.T) {
	r := newRig(t, nil, nil)
	r.stop()
	c, _, err := r.dial(agentName, token)
	if err != nil {
		t.Fatal(err)
	}
	requireValues(t, "welcome", wireObject(t, c.next(typeWelcome).Raw), map[string]any{"enabled": false})
	requireValues(t, "control", wireObject(t, c.next(typeControl).Raw), map[string]any{"enabled": false, "reason": "stopped_by_sam"})
}

func TestTheTaskFrameIsPinnedByItsLiteralJSON(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.submit("T1", "tok:alice:T1", nil)
	m := wireObject(t, c.next(typeTask).Raw)

	requireKeys(t, "task frame", m, "type", "delivery", "taskId", "task")
	requireValues(t, "task frame", m, map[string]any{"type": "task", "delivery": 1.0, "taskId": "T1"})

	task, _ := m["task"].(map[string]any)
	requireKeys(t, "task", task, "contextId", "text", "files", "sessionBehavior", "user", "callDepth", "trigger", "traceId", "calledBy",
		"canPromptUser", "grantedToolScopes", "requestContext", "metadata")
	requireValues(t, "task", task, map[string]any{"text": "hi", "files": []any{}, "sessionBehavior": "session", "callDepth": 0.0,
		"traceId": "trace-1", "calledBy": "", "canPromptUser": true, "user": map[string]any{"id": "alice@example.com"}})
	requestContext, _ := task["requestContext"].(map[string]any)
	requireKeys(t, "task.requestContext", requestContext, "systemPurpose", "responseFormat", "projectId", "conversationContext", "userProfile",
		"userTimezone")
}

func TestTheCancelFrameIsPinnedByItsLiteralJSON(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	r.cancel("T1")
	m := wireObject(t, c.next(typeCancel).Raw)

	requireKeys(t, "cancel", m, "type", "taskId")
	requireValues(t, "cancel", m, map[string]any{"type": "cancel", "taskId": "T1"})
}

func TestThePromptAnswerFramesAreEachPinnedByTheirLiteralJSON(t *testing.T) {
	t.Run("an approval", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		c.send(approvalPrompt("hil-approval-1", 600))
		waitFor(t, func() bool { return openPrompts(r) == 1 })
		publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: "answered",
			Data: map[string]any{"approved": true}})
		m := wireObject(t, c.next(typePromptAnswer).Raw)

		requireKeys(t, "approval prompt_answer", m, "type", "taskId", "requestId", "status", "approved")
		requireValues(t, "approval prompt_answer", m, map[string]any{"type": "prompt_answer", "taskId": "T1", "requestId": "hil-approval-1",
			"status": "answered", "approved": true})
	})
	t.Run("questions", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
		waitFor(t, func() bool { return openPrompts(r) == 1 })
		publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: "answered",
			Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})
		m := wireObject(t, c.next(typePromptAnswer).Raw)

		requireKeys(t, "questions prompt_answer", m, "type", "taskId", "requestId", "status", "answers")
		requireValues(t, "questions prompt_answer", m, map[string]any{"type": "prompt_answer", "taskId": "T1", "requestId": "q-1",
			"status": "answered", "answers": []any{"Home"}})
	})
}

func openPrompts(r *rig) int {
	n := 0
	r.locked(func(s *agentState) { n = len(s.asks) })
	return n
}

func TestThePeerResultFramesArePinnedByTheirLiteralJSON(t *testing.T) {
	t.Run("a completed call", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
		peerID := r.callPeer(c, requests, "S1", "Is policy P-9 active?")
		r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, a2a.NewDataPart(map[string]any{"policy": "P-9"}), a2a.NewTextPart("Active")))
		m := wireObject(t, c.next(typePeerResult).Raw)

		requireKeys(t, "peer_result", m, "type", "subtaskId", "state", "text", "data")
		requireValues(t, "peer_result", m, map[string]any{"type": "peer_result", "subtaskId": "S1", "state": "completed", "text": "Active",
			"data": map[string]any{"policy": "P-9"}})
	})
	t.Run("a failed call", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
		peerID := r.callPeer(c, requests, "S1", "Pay the claim")
		body, _ := json.Marshal(a2a.NewErrorResponse(peerID, codeInvalidRequest, msgAuthFailed))
		r.peerAnswers(peerID, "response", body)
		m := wireObject(t, c.next(typePeerResult).Raw)

		requireKeys(t, "peer_result", m, "type", "subtaskId", "state", "text", "error")
		requireValues(t, "peer_result", m, map[string]any{"type": "peer_result", "subtaskId": "S1", "state": "failed", "text": "Authentication failed"})
		e, _ := m["error"].(map[string]any)
		requireKeys(t, "peer_result.error", e, "code", "message")
		requireValues(t, "peer_result.error", e, map[string]any{"code": -32600.0, "message": "Authentication failed"})
	})
	t.Run("a cancelled call", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		c.send(peerCall("S1", peerName, "Is policy P-9 active?"))
		waitFor(t, func() bool { return hasSubtask(r, "S1") })
		r.cancel("T1")
		m := wireObject(t, c.nextOf(typePeerResult, typeCancel).Raw)
		if m["type"] == "cancel" {
			m = wireObject(t, c.next(typePeerResult).Raw)
		}

		requireValues(t, "peer_result", m, map[string]any{"type": "peer_result", "subtaskId": "S1", "state": "canceled"})
	})
}

func TestThePeersFrameIsPinnedByItsLiteralJSON(t *testing.T) {
	r := newRig(t, nil, nil)
	r.offTheMesh()
	c, _, err := r.dial(agentName, token)
	if err != nil {
		t.Fatal(err)
	}
	c.next(typeWelcome)
	empty := wireObject(t, c.next(typePeers).Raw)
	requireKeys(t, "peers", empty, "type", "peers")
	requireValues(t, "peers", empty, map[string]any{"type": "peers", "peers": []any{}})

	card := a2a.AgentCard{Name: peerName, Description: "Answers policy questions", URL: "solace:" + a2a.AgentRequestTopic(testNS, peerName),
		Skills: []a2a.AgentSkill{{ID: "lookup", Name: "Lookup", Description: "Finds a policy"}},
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			{URI: a2a.ExtensionURIDisplayName, Params: map[string]any{"display_name": "Policy Desk"}}, {URI: a2a.ExtensionURIOnDemand}}}}
	body, _ := json.Marshal(card)
	if err := r.brk.Publish(t.Context(), a2a.DiscoveryAgentCardsTopic(testNS), &broker.Message{Payload: body,
		UserProperties: map[string]string{"instance_id": peerName}}); err != nil {
		t.Fatal(err)
	}
	listed := wireObject(t, c.next(typePeers).Raw)

	requireKeys(t, "peers", listed, "type", "peers")
	entries, _ := listed["peers"].([]any)
	if len(entries) != 1 {
		t.Fatalf("peers %v", listed["peers"])
	}
	requireValues(t, "peers entry", entries[0].(map[string]any), map[string]any{"name": peerName, "displayName": "Policy Desk",
		"description": "Answers policy questions", "onDemand": true,
		"skills": []any{map[string]any{"id": "lookup", "name": "Lookup", "description": "Finds a policy"}}})
	requireKeys(t, "peers entry", entries[0].(map[string]any), "name", "displayName", "description", "skills", "onDemand")
}

func TestTheControlFramesArePinnedByTheirLiteralJSON(t *testing.T) {
	r, c := connectedRig(t, nil)

	r.stop()
	stopped := wireObject(t, c.next(typeControl).Raw)
	requireKeys(t, "control", stopped, "type", "enabled", "reason")
	requireValues(t, "control", stopped, map[string]any{"type": "control", "enabled": false, "reason": "stopped_by_sam"})

	r.release()
	released := wireObject(t, c.next(typeControl).Raw)
	requireKeys(t, "control", released, "type", "enabled", "reason")
	requireValues(t, "control", released, map[string]any{"type": "control", "enabled": true, "reason": "released_by_sam"})
}

func TestTheErrorFramesArePinnedByTheirLiteralJSON(t *testing.T) {
	r, c := connectedRig(t, nil)

	c.send(map[string]any{"type": "no-such-type"})
	unknown := wireObject(t, c.next(typeError).Raw)
	requireKeys(t, "error", unknown, "type", "code", "message")
	requireValues(t, "error", unknown, map[string]any{"type": "error", "code": "unknown_type"})
	requireNonEmptyString(t, "error", unknown, "message")

	c.send(event("T9", map[string]any{"kind": eventProgress, "text": "x"}))
	task := wireObject(t, c.next(typeError).Raw)
	requireKeys(t, "error", task, "type", "code", "message", "ref")
	requireValues(t, "error", task, map[string]any{"type": "error", "code": "unknown_task", "ref": "T9"})

	c.send(map[string]any{"type": typePrompt, "taskId": "T9", "requestId": "q-7", "questions": []any{map[string]any{"question": "Which?"}}})
	prompt := wireObject(t, c.next(typeError).Raw)
	requireKeys(t, "error", prompt, "type", "code", "message", "ref", "requestId")
	requireValues(t, "error", prompt, map[string]any{"type": "error", "code": "unknown_task", "ref": "T9", "requestId": "q-7"})

	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	describe := wireObject(t, c.next(typeError).Raw)
	requireKeys(t, "error", describe, "type", "code", "message", "frame")
	requireValues(t, "error", describe, map[string]any{"type": "error", "code": "bad_message", "frame": "describe"})

	c.send(map[string]any{"type": typeAck, "delivery": 42})
	delivery := wireObject(t, c.next(typeError).Raw)
	requireKeys(t, "error", delivery, "type", "code", "message", "ref")
	requireValues(t, "error", delivery, map[string]any{"type": "error", "code": "unknown_delivery", "ref": "42"})
	_ = r
}

func observedOutboxConn(t *testing.T, r *rig) (*conn, <-chan error) {
	t.Helper()
	ended := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				ended <- err
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := r.adapter.agent(agentName)
	c := newConn(r.adapter, s, ws)
	for range cap(c.out) {
		c.out <- outbound{data: []byte("{}")}
	}
	c.consuming = true
	s.mu.Lock()
	s.conns[c.id] = c
	s.mu.Unlock()
	return c, ended
}

func endedWith(t *testing.T, ended <-chan error) *websocket.CloseError {
	t.Helper()
	select {
	case err := <-ended:
		closeErr, ok := err.(*websocket.CloseError)
		if !ok {
			t.Fatalf("the connection ended with %v", err)
		}
		return closeErr
	case <-time.After(arrivalWait):
		t.Fatal("the connection was not closed")
		return nil
	}
}

func TestAConnectionThatCannotKeepUpIsClosedWithCode1013(t *testing.T) {
	r := newRig(t, nil, nil)
	c, ended := observedOutboxConn(t, r)

	if c.send(map[string]any{"type": "cancel", "taskId": "T1"}) {
		t.Fatal("a frame was queued on a full outbox")
	}
	if closeErr := endedWith(t, ended); closeErr.Code != 1013 || closeErr.Text != "too slow" {
		t.Fatalf("closed with %d %q, want 1013 \"too slow\"", closeErr.Code, closeErr.Text)
	}
}

func TestAConnectionThatSendsNothingIsClosedWithCode4408(t *testing.T) {
	r := newRig(t, nil, nil)
	c := r.connect()
	var server *conn
	r.locked(func(s *agentState) {
		for _, held := range s.conns {
			server = held
		}
	})

	if err := server.ws.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if closeErr := closedWithin(t, c); closeErr.Code != 4408 || closeErr.Text != "idle" {
		t.Fatalf("closed with %d %q, want 4408 \"idle\"", closeErr.Code, closeErr.Text)
	}
}
