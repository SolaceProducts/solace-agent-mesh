// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func peerCall(subtaskID, peer, text string) map[string]any {
	return map[string]any{"type": typePeerCall, "parentTaskId": "T1", "subtaskId": subtaskID, "peer": peer, "text": text, "callId": "call-1"}
}

func peerStatus(peerTaskID string, parts ...a2a.Part) []byte {
	b, _ := json.Marshal(a2a.NewResponse(peerTaskID, a2a.NewStatusUpdateEvent(peerTaskID, "", parts, false, map[string]any{"agent_name": peerName})))
	return b
}

func progressPart(text string) a2a.Part {
	return a2a.NewDataPart(a2a.AgentProgressUpdateData{Type: string(a2a.SignalAgentProgressUpdate), StatusText: text})
}

func peerReply(peerTaskID string, state a2a.TaskState, parts ...a2a.Part) []byte {
	b, _ := json.Marshal(a2a.NewResponse(peerTaskID, a2a.Task{ID: peerTaskID, Kind: "task",
		Status: a2a.TaskStatus{State: state, Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: parts}}}))
	return b
}

func peerRequestID(t *testing.T, m *broker.Message) string {
	t.Helper()
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(m.Payload, &req); err != nil || req.ID == "" {
		t.Fatalf("a peer request carries an id: %v %s", err, m.Payload)
	}
	return req.ID
}

func (r *rig) callPeer(c *client, requests <-chan *broker.Message, subtaskID, text string) string {
	r.t.Helper()
	c.send(peerCall(subtaskID, peerName, text))
	return peerRequestID(r.t, recv(r.t, requests))
}

func (r *rig) peerAnswers(peerTaskID string, kind string, payload []byte) {
	r.t.Helper()
	topic := a2a.PeerResponseTopic(testNS, agentName, peerTaskID)
	if kind == "status" {
		topic = a2a.PeerStatusTopic(testNS, agentName, peerTaskID)
	}
	if err := r.brk.Publish(r.t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		r.t.Fatal(err)
	}
}

func TestAPeerCallSendsTheRequestANativeAgentSends(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	c.send(peerCall("S1", peerName, "Is policy P-9 active?"))
	m := recv(t, requests)

	peerTaskID := peerRequestID(t, m)
	native, _ := json.Marshal(agent.PeerRequest(agentName, peerName, peerTaskID, "Is policy P-9 active?", &tool.Context{ParentTaskID: "T1", ToolCallID: "call-1"}, nil))
	sameMessage(t, &broker.Message{Payload: m.Payload}, &broker.Message{Payload: native})
	var sent struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Message struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(m.Payload, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.JSONRPC != "2.0" || sent.ID == "" || sent.ID == "S1" || sent.Method != "message/send" || sent.Params.Message.Role != "user" ||
		len(sent.Params.Message.Parts) == 0 || !strings.Contains(sent.Params.Message.Parts[0].Text, "Is policy P-9 active?") ||
		!strings.Contains(sent.Params.Message.Parts[0].Text, agent.PeerTaskSentinel) {
		t.Fatalf("peer request %s", m.Payload)
	}
	up := a2a.DecodeUserProperties(m.UserProperties)
	if up.ReplyTo != a2a.PeerResponseTopic(testNS, agentName, sent.ID) || up.A2AStatusTopic != a2a.PeerStatusTopic(testNS, agentName, sent.ID) ||
		strings.Contains(up.ReplyTo, "S1") || strings.Contains(up.A2AStatusTopic, "S1") ||
		m.ReplyTo != up.ReplyTo || up.DelegatingAgentName != agentName || up.CallDepth != 1 || up.AuthToken != "tok:alice:T1" ||
		up.UserID != "alice@example.com" || up.TraceID != "trace-1" || up.RootKind != a2a.RootKindAgent || up.RootID != a2a.CanonicalComponentID(agentName) {
		t.Fatalf("properties %v", m.UserProperties)
	}
}

func TestAPeersProgressIsForwardedIntoTheCallingTaskAndItsAnswerReturned(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "Is policy P-9 active?")

	r.peerAnswers(peerID, "status", peerStatus(peerID, progressPart("Checking P-9")))
	m := recv(t, status)
	ev := signalOf(t, m, nil)
	meta := ev.Status.Message.Metadata
	if ev.TaskID != "T1" || meta["forwarded_from_peer"] != peerName || meta["function_call_id"] != "call-1" || meta["agent_name"] != agentName ||
		meta["original_peer_event_taskId"] != peerID || m.UserProperties[a2a.PropDelegatingAgentName] != agentName || m.UserProperties["publisherId"] != agentName {
		t.Fatalf("forwarded %s %v", m.Payload, m.UserProperties)
	}
	r.peerAnswers(peerID, "status", peerStatus(peerID, a2a.NewTextPart("streamed words")))
	none(t, status)

	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, a2a.NewDataPart(map[string]any{"policy": "P-9"}), a2a.NewTextPart("Active")))
	f := c.next(typePeerResult)
	if f.SubtaskID != "S1" || f.State != stateCompleted || f.Text == nil || *f.Text != "Active" || string(f.Data) != `{"policy":"P-9"}` || f.Error != nil {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAPeersFailureOrRefusalComesBackAsItsResult(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	first := r.callPeer(c, requests, "S1", "Pay the claim")
	second := r.callPeer(c, requests, "S2", "Pay the claim")
	errBody, _ := json.Marshal(a2a.NewErrorResponse(first, codeInvalidRequest, msgAuthFailed))
	r.peerAnswers(first, "response", errBody)
	if f := c.next(typePeerResult); f.SubtaskID != "S1" || f.State != stateFailed || f.Error == nil || f.Error.Code != codeInvalidRequest || f.Error.Message != msgAuthFailed {
		t.Fatalf("%s", f.Raw)
	}
	r.peerAnswers(second, "response", peerReply(second, a2a.TaskStateRejected, a2a.NewTextPart("Not my area")))
	if f := c.next(typePeerResult); f.SubtaskID != "S2" || f.State != stateRejected || *f.Text != "Not my area" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAPeerCallIsRefusedForAnUnconfiguredPeerAForeignTaskOrAUserWhoMayNotInvokeIt(t *testing.T) {
	logs := captureAllLogs(t)
	r := newRig(t, map[string]any{"peers": []any{peerName, "payments"}}, denyPeers{denied: map[string]bool{"payments": true}})
	c := r.connect()
	r.consume(c)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	for _, tc := range []struct {
		call map[string]any
		code string
		ref  string
		says string
	}{
		{peerCall("S1", "hr-directory", "x"), errCodeForbidden, "S1", ""},
		{map[string]any{"type": typePeerCall, "parentTaskId": "T-unknown", "subtaskId": "S2", "peer": peerName, "text": "x"}, errCodeUnknownTask, "S2",
			"the parent task is not an open task of this agent"},
		{peerCall("S3", "payments", "x"), errCodeForbidden, "S3", ""},
		{peerCall("S4/>", peerName, "x"), errCodeBadMessage, "", ""},
		{peerCall(strings.Repeat("s", 129), peerName, "x"), errCodeBadMessage, "", ""},
		{peerCall("S5", peerName, ""), errCodeBadMessage, "S5", ""},
	} {
		c.send(tc.call)
		if refusal := c.refused(tc.code, tc.ref); tc.says != "" && refusal.Message != tc.says {
			t.Fatalf("the refusal says %q, want %q", refusal.Message, tc.says)
		}
	}
	none(t, requests)
	theOneLineLogged(t, logs, refusedATaskFrame, "level=DEBUG", "taskID=T-unknown", "frameType=peer_call", "reason=absent")

	c.send(peerCall("S6", peerName, "x"))
	recv(t, requests)
	c.send(peerCall("S6", peerName, "x"))
	c.refused(errCodeBadMessage, "S6")

	s := r.adapter.agent(agentName)
	s.mu.Lock()
	for i := len(s.subtasks); i < maxOpenSubtasks; i++ {
		s.subtasks[fmt.Sprintf("filler-%d", i)] = &subtaskBinding{parent: "T1"}
	}
	s.mu.Unlock()
	c.send(peerCall("S7", peerName, "x"))
	c.refused(errCodeLimit, "S7")

	r.stop()
	c.next(typeControl)
	c.send(peerCall("S8", peerName, "x"))
	c.refused(errCodeForbidden, "S8")
	none(t, requests)
}

func TestAPeerCallCanBeCancelledAndEndsWithItsTask(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	first := r.callPeer(c, requests, "S1", "x")
	c.send(map[string]any{"type": typePeerCancel, "subtaskId": "S1"})
	cancelSent(t, requests, first)
	if f := c.next(typePeerResult); f.SubtaskID != "S1" || f.State != stateCanceled {
		t.Fatalf("%s", f.Raw)
	}
	c.send(map[string]any{"type": typePeerCancel, "subtaskId": "S9"})
	c.refused(errCodeUnknownTask, "S9")

	second := r.callPeer(c, requests, "S2", "x")
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	cancelSent(t, requests, second)
}

func TestAnAgentIsToldItsPeersAsTheyComeAndGo(t *testing.T) {
	r := newRig(t, nil, nil)
	r.offTheMesh()
	c, _, err := r.dial(agentName, token)
	if err != nil {
		t.Fatal(err)
	}
	c.next(typeWelcome)
	if f := c.next(typePeers); len(*f.Peers) != 0 {
		t.Fatalf("%s", f.Raw)
	}
	for _, name := range []string{peerName, "hr-directory"} {
		card := a2a.AgentCard{Name: name, Description: "Answers policy questions", URL: "solace:" + a2a.AgentRequestTopic(testNS, name),
			Skills: []a2a.AgentSkill{{ID: "lookup", Name: "Lookup", Description: "Finds a policy"}},
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
				{URI: a2a.ExtensionURIDisplayName, Params: map[string]any{"display_name": "Policy Desk"}}, {URI: a2a.ExtensionURIOnDemand}}}}
		body, _ := json.Marshal(card)
		if err := r.brk.Publish(t.Context(), a2a.DiscoveryAgentCardsTopic(testNS), &broker.Message{Payload: body, UserProperties: map[string]string{"instance_id": name}}); err != nil {
			t.Fatal(err)
		}
	}
	f := c.next(typePeers)
	if peers := *f.Peers; len(peers) != 1 || peers[0].Name != peerName || peers[0].DisplayName != "Policy Desk" || !peers[0].OnDemand ||
		peers[0].Description != "Answers policy questions" || len(peers[0].Skills) != 1 || peers[0].Skills[0].ID != "lookup" {
		t.Fatalf("%s", f.Raw)
	}
	body, _ := json.Marshal(a2a.AgentCard{Name: peerName})
	if err := r.brk.Publish(t.Context(), a2a.DiscoveryAgentCardsTopic(testNS), &broker.Message{Payload: body,
		UserProperties: map[string]string{"instance_id": peerName, "removed": "true"}}); err != nil {
		t.Fatal(err)
	}
	if f := c.next(typePeers); len(*f.Peers) != 0 {
		t.Fatalf("%s", f.Raw)
	}
}

func TestTwoLinkAgentsCallEachOther(t *testing.T) {
	r := newRigAgents(t, []any{
		map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{peerName}},
		map[string]any{"name": peerName, "token_sha256": digest(peerToken), "peers": []any{agentName}},
	}, nil)
	caller := r.connect()
	r.consume(caller)
	callee := r.connectAs(peerName, peerToken)
	r.consumeAs(callee, peerName)

	r.taskOn(caller, "T1", nil)
	status := r.listen(statusTopic("T1"))
	caller.send(peerCall("S1", peerName, "Is policy P-9 active?"))
	asked := callee.next(typeTask)
	peerTaskID := asked.TaskID
	if peerTaskID == "" || peerTaskID == "S1" || asked.Task.CalledBy != agentName || asked.Task.User.ID != "alice@example.com" ||
		asked.Task.CallDepth != 1 || asked.Task.SessionBehavior != sessionRun || !strings.Contains(asked.Task.Text, agent.PeerTaskSentinel) {
		t.Fatalf("the called agent got %s", asked.Raw)
	}
	callee.send(map[string]any{"type": typeAck, "delivery": asked.Delivery})
	callee.send(event(peerTaskID, map[string]any{"kind": eventProgress, "text": "Checking"}))
	if ev := signalOf(t, recv(t, status), nil); ev.TaskID != "T1" || ev.Status.Message.Metadata["forwarded_from_peer"] != peerName {
		t.Fatalf("forwarded %+v", ev)
	}
	callee.send(map[string]any{"type": typeResult, "taskId": peerTaskID, "state": stateCompleted, "text": "Active"})
	if f := caller.next(typePeerResult); f.SubtaskID != "S1" || f.State != stateCompleted || *f.Text != "Active" {
		t.Fatalf("%s", f.Raw)
	}
}

func hasSubtask(r *rig, subtaskID string) bool {
	s := r.adapter.agent(agentName)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subtasks[subtaskID] != nil
}

func TestAPeersPromptIsRelayedToTheUserAndTheAnswerBackToThePeer(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "Pay the claim")

	prompt := a2a.UserInputRequestData{Type: string(a2a.SignalUserInputRequest), RequestID: "peer-q-1", Source: sourceAskQuestion,
		Surface: json.RawMessage(`{"surfaceId":"peer-q-1","components":[]}`)}
	ask := a2a.NewStatusUpdateEvent(peerID, "", []a2a.Part{a2a.NewDataPart(prompt)}, false, map[string]any{"agent_name": peerName})
	ask.Status.State = a2a.TaskStateInputNeeded
	body, _ := json.Marshal(a2a.NewResponse(peerID, ask))
	r.peerAnswers(peerID, "status", body)
	var shown a2a.UserInputRequestData
	if ev := signalOf(t, recv(t, status), &shown); ev.TaskID != "T1" || shown.RequestID != "peer-q-1" || ev.Status.Message.Metadata["agent_name"] != agentName {
		t.Fatalf("relayed prompt %+v", ev)
	}

	publishAnswer(t, r, "T1", "bob@example.com", a2a.UserInputResponseData{RequestID: "peer-q-1", Status: answerAnswered, Data: map[string]any{"answers": map[string]any{"q0": "no"}}})
	none(t, requests)
	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "peer-q-1", Status: answerAnswered, Data: map[string]any{"answers": map[string]any{"q0": "yes"}}})
	m := recv(t, requests)
	var req struct {
		ID     string `json:"id"`
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(m.Payload, &req); err != nil {
		t.Fatal(err)
	}
	data, _ := req.Params.Message.Parts[0].Data.(map[string]any)
	if req.ID != peerID || data["type"] != string(a2a.SignalUserInputResponse) || data["request_id"] != "peer-q-1" ||
		m.UserProperties[a2a.PropDelegatingAgentName] != agentName || m.UserProperties[a2a.PropAuthToken] != "tok:alice:T1" {
		t.Fatalf("relayed answer %s %v", m.Payload, m.UserProperties)
	}
	c.quiet()
}

type emptyTokenAuth struct{ fakeAuth }

func (emptyTokenAuth) AllowEmptyToken() {}

type identityRequired struct{ auth.NoopAgentAuthorizer }

func (identityRequired) AuthorizeAgent(_ context.Context, identity *auth.Identity, _ string) error {
	if identity == nil {
		return errors.New("no identity provided")
	}
	return nil
}

func TestAPeerCallOfATaskWithoutAnIdentityIsLeftToTheAuthorizer(t *testing.T) {
	r := newRigAuth(t, []any{map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{peerName}}},
		emptyTokenAuth{}, identityRequired{})
	c := r.connect()
	r.consume(c)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	r.taskOn(c, "T1", map[string]string{a2a.PropAuthToken: ""})
	c.send(peerCall("S1", peerName, "Is policy P-9 active?"))
	c.refused(errCodeForbidden, "S1")
	none(t, requests)
}

func TestAPeerResultLargerThanTheFrameLimitIsCutToFitWithItsDataDropped(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cfg.MaxFrameBytes = 8 << 10
	c := r.connect()
	r.consume(c)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "Send me the policy file")
	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted,
		a2a.NewTextPart(strings.Repeat("policy text ", 1_000)), a2a.NewDataPart(map[string]any{"rows": strings.Repeat("r", 10_000)})))
	f := c.next(typePeerResult)
	if len(f.Raw) > 8<<10 || !f.Truncated || f.Data != nil || f.Text == nil || !strings.Contains(*f.Text, "truncated") ||
		!strings.HasPrefix(*f.Text, "policy text") {
		t.Fatalf("a %d-byte peer result: truncated %v, data %d bytes", len(f.Raw), f.Truncated, len(f.Data))
	}
}

func TestAPeersToolFailureAndArtifactNoticesDoNotReachTheAgent(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "Pay the claim")

	failure := a2a.NewDataPart(map[string]any{"a2a_signal_type": "tool_execution_error", "code": "E", "message": "boom",
		"stderr_tail": "AWS_SECRET_ACCESS_KEY=abc at /srv/internal/x.py", "stack_trace": "Traceback /srv/internal"})
	notice := a2a.NewDataPartWithMetadata(map[string]any{"filename": "report.csv", "version": 1, "status": "saved"},
		map[string]any{"tool_name": "_notify_artifact_save"})
	answer := a2a.NewDataPart(map[string]any{"claim": "C-1", "api_key": "sk-live-123"})
	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateFailed, failure, notice, answer, a2a.NewTextPart("it failed")))

	f := c.next(typePeerResult)
	if f.State != stateFailed || strings.Contains(string(f.Raw), "stderr_tail") || strings.Contains(string(f.Raw), "AWS_SECRET") ||
		strings.Contains(string(f.Raw), "report.csv") || strings.Contains(string(f.Raw), "sk-live-123") ||
		!strings.Contains(string(f.Data), `"claim":"C-1"`) {
		t.Fatalf("peer_result %s", f.Raw)
	}
}

func TestAPeersOutputWithFileLikeKeysReachesTheAgent(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "Export the claims")
	output := a2a.NewDataPart(map[string]any{"filename": "claims.csv", "version": 3, "rows": 12})
	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, output, a2a.NewTextPart("exported")))
	if f := c.next(typePeerResult); string(f.Data) != `{"filename":"claims.csv","rows":12,"version":3}` || f.Truncated {
		t.Fatalf("peer_result %s", f.Raw)
	}
}

func TestAPeersLongReplyIsCutByTheRedactorAndMarkedTruncated(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	long := strings.Repeat("x", maxStringBytes+100)

	first := r.callPeer(c, requests, "S1", "a")
	r.peerAnswers(first, "response", peerReply(first, a2a.TaskStateCompleted, a2a.NewTextPart(long)))
	if f := c.next(typePeerResult); !f.Truncated || f.Text == nil || len(*f.Text) >= len(long) {
		t.Fatalf("long text: %d bytes, truncated %v", len(*f.Text), f.Truncated)
	}

	second := r.callPeer(c, requests, "S2", "b")
	errBody, _ := json.Marshal(a2a.NewErrorResponse(second, codeInvalidRequest, long))
	r.peerAnswers(second, "response", errBody)
	if f := c.next(typePeerResult); !f.Truncated || f.Error == nil || len(f.Error.Message) >= len(long) || len(*f.Text) >= len(long) {
		t.Fatalf("long error: truncated %v", f.Truncated)
	}

	third := r.callPeer(c, requests, "S3", "c")
	r.peerAnswers(third, "response", peerReply(third, a2a.TaskStateCompleted, a2a.NewDataPart(map[string]any{"rows": []any{long}}), a2a.NewTextPart("ok")))
	if f := c.next(typePeerResult); !f.Truncated || len(f.Data) >= len(long) || len(f.Data) == 0 {
		t.Fatalf("long data: %d bytes, truncated %v", len(f.Data), f.Truncated)
	}
}

func TestAToolNamedLikeADelegationIsPublishedUnderAPrefixUnlessItCallsAConfiguredPeer(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	published := func(ev map[string]any) string {
		t.Helper()
		c.send(event("T1", ev))
		return string(recv(t, status).Payload)
	}
	renamed := []string{"peer_hr-directory", "peer_" + peerName + "-evil", "peer_", "peer_policy", "workflow_payroll", a2a.WorkflowToolName(peerName) + "2", "_notify_artifact_save", "sub_task", "load_skill"}
	kept := []string{"peer_" + peerName, a2a.WorkflowToolName(peerName), "lookup", "review_peer_notes", "not_workflow_x", "my_peer_x"}
	for i, name := range append(append([]string{}, renamed...), kept...) {
		want := name
		if i < len(renamed) {
			want = "tool_" + name
		}
		call := fmt.Sprintf("c%d", i)
		for _, ev := range []map[string]any{
			{"kind": eventToolStarted, "callId": call, "name": name, "args": map[string]any{}},
			{"kind": eventToolFinished, "callId": call, "name": name, "result": "ok"},
			{"kind": eventModelFinished, "model": "gpt-4o", "toolCalls": []any{map[string]any{"id": "m" + call, "name": name, "args": map[string]any{}}}},
		} {
			if got := published(ev); !strings.Contains(got, `"`+want+`"`) || (want != name && strings.Contains(got, `"`+name+`"`)) {
				t.Fatalf("%s %s published %s, want the tool named %s", ev["kind"], name, got, want)
			}
		}
	}
	c.quiet()
}

func TestAPeersReplyIsMarkedTruncatedOnlyWhenTheRedactorCutIt(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	credentialDense := strings.Repeat("http://a:b@c ", maxStringBytes/13)
	for i, tc := range []struct {
		data          map[string]any
		wantTruncated bool
	}{
		{map[string]any{"rows": strings.Repeat("x", maxStringBytes)}, false},
		{map[string]any{"rows": strings.Repeat("x", maxStringBytes+1)}, true},
		{map[string]any{"rows": credentialDense}, true},
		{map[string]any{"password": strings.Repeat("x", 20_000)}, false},
		{map[string]any{"rows": "…[truncated 9 bytes]" + strings.Repeat("x", maxStringBytes)}, true},
		{map[string]any{"rows": strings.Repeat("x", maxStringBytes-10) + "…[truncated 9 bytes]" + strings.Repeat("y", 100)}, true},
		{map[string]any{"rows": "…[truncated 9 bytes]"}, false},
		{map[string]any{"rows": strings.Repeat("x", maxStringBytes+1), "notes": "short"}, true},
		{map[string]any{"a": "short", "b": strings.Repeat("x", maxStringBytes+1)}, true},
		{map[string]any{"rows": strings.Repeat("x", 16352-9) + "password=" + strings.Repeat("A", 2000)}, true},
		{map[string]any{"rows": strings.Repeat("x", 16322) + "http://h/?token=" + strings.Repeat("A", 2000)}, true},
	} {
		peerID := r.callPeer(c, requests, fmt.Sprintf("S%d", i), "q")
		r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, a2a.NewDataPart(tc.data), a2a.NewTextPart("ok")))
		if f := c.next(typePeerResult); f.Truncated != tc.wantTruncated {
			t.Fatalf("row %d: truncated %v, want %v (%d bytes of data)", i, f.Truncated, tc.wantTruncated, len(f.Data))
		}
	}
}

func TestAPeersDataPartWithOtherMetadataReachesTheAgent(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "q")
	part := a2a.NewDataPartWithMetadata(map[string]any{"claim": "C-1"}, map[string]any{"tool_name": "other"})
	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, part, a2a.NewTextPart("ok")))
	if f := c.next(typePeerResult); string(f.Data) != `{"claim":"C-1"}` {
		t.Fatalf("peer_result %s", f.Raw)
	}
}

func TestAPeerCallToAConfiguredPeerThatIsNotOnTheMeshIsRefusedAtOnceAndWarnedOncePerPeer(t *testing.T) {
	logs := captureAllLogs(t)
	r, c := connectedRig(t, map[string]any{"peers": []any{peerName, "claims-archive"}})
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, "claims-archive"))
	for _, sub := range []string{"S1", "S2"} {
		c.send(peerCall(sub, "claims-archive", "Find claim C-1"))
		if f := c.refused("peer_unavailable", sub); f.Message != "the peer is not on the mesh" {
			t.Fatalf("refusal %s", f.Raw)
		}
	}
	c.send(map[string]any{"type": typePeerCall, "parentTaskId": strings.Repeat("t", 4096), "subtaskId": "S3", "peer": "claims-archive", "text": "x"})
	c.refused(errCodeUnknownTask, "S3")
	none(t, requests)
	if n := logs.count("agent link refused a peer call to a peer not on the mesh"); n != 1 {
		t.Fatalf("warned %d times, want once:\n%s", n, logs.all())
	}
	if strings.Contains(logs.all(), strings.Repeat("t", 4096)) {
		t.Fatal("an unadmitted parentTaskId was logged")
	}
}

func TestAPeerCallToAWakeAgentOfTheSameLinkThatIsNotListedReachesItsQueue(t *testing.T) {
	const sleeper = "claims-sleeper"
	r := newRigAgents(t, []any{
		map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{peerName, sleeper}},
		map[string]any{"name": sleeper, "token_sha256": digest("s3cret-sleeper-token"), "wake": map[string]any{"url": "http://127.0.0.1:1/wake", "on_start": false}},
	}, nil)
	c := r.connect()
	r.consume(c)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, sleeper))
	c.send(peerCall("S1", sleeper, "Find claim C-1"))
	recv(t, requests)
}

func TestAPeersLongTextOrErrorIsTruncatedEvenWhenItsCutHidesTheMark(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	tailMark := strings.Repeat("x", maxStringBytes-10) + "…[truncated 9 bytes]" + strings.Repeat("y", 100)
	first := r.callPeer(c, requests, "S1", "a")
	r.peerAnswers(first, "response", peerReply(first, a2a.TaskStateCompleted, a2a.NewTextPart(tailMark)))
	if f := c.next(typePeerResult); !f.Truncated {
		t.Fatal("text: truncated false")
	}
	second := r.callPeer(c, requests, "S2", "b")
	errBody, _ := json.Marshal(a2a.NewErrorResponse(second, codeInvalidRequest, tailMark))
	r.peerAnswers(second, "response", errBody)
	if f := c.next(typePeerResult); !f.Truncated || f.Error == nil || f.Error.Message != *f.Text {
		t.Fatalf("error: truncated %v", f.Truncated)
	}
}

func TestAPeerCallToAnUnlistedAgentOfTheSameLinkThatCannotBeWokenIsRefused(t *testing.T) {
	for name, sibling := range map[string]map[string]any{
		"no wake":  {},
		"disabled": {"enabled": false, "wake": map[string]any{"url": "http://127.0.0.1:1/wake", "on_start": false}},
	} {
		t.Run(name, func(t *testing.T) {
			const sibling2 = "claims-sibling"
			entry := map[string]any{"name": sibling2, "token_sha256": digest("s3cret-sibling-token")}
			for k, v := range sibling {
				entry[k] = v
			}
			r := newRigAgents(t, []any{
				map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{peerName, sibling2}},
				entry,
			}, nil)
			c := r.connect()
			r.consume(c)
			r.taskOn(c, "T1", nil)
			requests := r.listen(a2a.AgentRequestTopic(testNS, sibling2))
			c.send(peerCall("S1", sibling2, "Find claim C-1"))
			c.refused("peer_unavailable", "S1")
			none(t, requests)
		})
	}
}

func TestTheGatewayReplyCallbacksOfAnAdapterThatNeverInitializedDoNotPanic(t *testing.T) {
	adapter, err := New(map[string]any{"agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token)}}})
	if err != nil {
		t.Fatal(err)
	}
	adapter.OnStatusUpdate(t.Context(), "T1", nil, nil)
	adapter.OnArtifactUpdate(t.Context(), "T1", nil, nil)
	adapter.OnFinalResponse(t.Context(), "T1", nil, nil)
	adapter.OnError(t.Context(), "T1", nil, nil)
}
