// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type authEvents struct {
	auth.NoopAuditLogger
	mu     sync.Mutex
	events []auth.AuthEvent
}

func (a *authEvents) LogAuth(_ context.Context, e auth.AuthEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

func (a *authEvents) failures(reason string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.events {
		if !e.Success && e.Reason == reason {
			n++
		}
	}
	return n
}

func TestConfigRefusesTheDigestOfAnEmptyToken(t *testing.T) {
	empty := sha256.Sum256(nil)
	_, err := ParseConfig(configWith(map[string]any{"token_sha256": hex.EncodeToString(empty[:])}))
	if err == nil || !strings.Contains(err.Error(), "digest of an empty token") {
		t.Fatalf("err %v", err)
	}
}

func TestAConnectWithNoTokenIsRefusedEvenWhenTheAgentsDigestIsThatOfAnEmptyToken(t *testing.T) {
	for name, header := range map[string]http.Header{
		"no Authorization header": {headerAgent: {agentName}},
		"an empty bearer":         credentials(agentName, ""),
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureAllLogs(t)
			audit := &authEvents{}
			r := newRigWith(t, nil, runtime.WithAuditLogger(audit))
			empty := sha256.Sum256(nil)
			r.adapter.agent(agentName).cfg.TokenSHA256 = empty[:]

			if c, resp := r.guardDial(header); c != nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("client %v response %v", c != nil, resp)
			}
			requireOneRefusal(t, logs, "invalid credentials", "")
			if audit.failures("invalid agent credentials") != 1 {
				t.Fatalf("audit events %+v", audit.events)
			}
		})
	}
}

func TestATaskWithNoUserTokenIsRefusedByAnAuthenticatorThatRequiresOne(t *testing.T) {
	audit := &authEvents{}
	r := newRigWith(t, nil, runtime.WithAuditLogger(audit))
	c := r.connect()
	r.consume(c)
	answers := r.listen(responseTopic("T1"))

	r.submit("T1", "", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAuthFailed) {
		t.Fatalf("the caller got %s", m.Payload)
	}
	c.quiet()
	if audit.failures(errMissingToken.Error()) != 1 {
		t.Fatalf("audit events %+v", audit.events)
	}
}

func TestAnotherAgentsConnectionCannotTouchAnAgentsTaskPromptOrPeerCall(t *testing.T) {
	const otherToken = "s3cret-other-token"
	r := newRigAgents(t, []any{
		map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{peerName, otherAgentName}},
		map[string]any{"name": otherAgentName, "token_sha256": digest(otherToken), "peers": []any{peerName, agentName}},
	}, nil)
	first := r.connect()
	r.consume(first)
	second := r.connectAs(otherAgentName, otherToken)
	r.consumeAs(second, otherAgentName)
	r.taskOn(first, "T1", nil)
	first.send(peerCall("S1", peerName, "x"))
	first.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "R1", "questions": []any{map[string]any{"question": "Which?"}}})
	first.quiet()

	second.send(event("T1", map[string]any{"kind": eventProgress, "text": "x"}))
	second.refused(errCodeUnknownTask, "T1")
	second.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "stolen"})
	second.refused(errCodeUnknownTask, "T1")
	second.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "R2", "questions": []any{map[string]any{"question": "q?"}}})
	second.refused(errCodeUnknownTask, "T1")
	second.send(map[string]any{"type": typePeerCall, "parentTaskId": "T1", "subtaskId": "S9", "peer": peerName, "text": "x"})
	second.refused(errCodeUnknownTask, "S9")
	second.send(map[string]any{"type": typePeerCancel, "subtaskId": "S1"})
	second.refused(errCodeUnknownTask, "S1")

	r.submitTo(otherAgentName, "T2", "tok:alice:T2", nil)
	if f := second.next(typeTask); f.TaskID != "T2" {
		t.Fatalf("task %s", f.TaskID)
	}
	second.send(map[string]any{"type": typeAck, "delivery": 1})
	second.send(map[string]any{"type": typePrompt, "taskId": "T2", "requestId": "R1", "questions": []any{map[string]any{"question": "Which?"}}})
	second.quiet()

	r.locked(func(s *agentState) {
		if p := s.asks["R1"]; p == nil || p.taskID != "T1" {
			t.Fatalf("agent A's prompt R1 is %+v", p)
		}
	})
	first.quiet()
}

func TestAnAnsweredTaskKeepsNeitherTheUsersTokenNorTheRequest(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", map[string]string{a2a.PropA2AUserConfig: `{"k":"v"}`})
	var contextID string
	r.locked(func(s *agentState) {
		b := s.tasks["T1"]
		if b.props[a2a.PropAuthToken] == "" || b.identity == nil || len(b.request.message.Parts) == 0 {
			t.Fatalf("the open task lacks its request: %+v", b)
		}
		contextID = b.request.message.ContextID
	})

	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	recv(t, answers)
	waitFor(t, func() bool {
		done := false
		r.locked(func(s *agentState) { done = s.tasks["T1"].identity == nil })
		return done
	})
	r.locked(func(s *agentState) {
		b := s.tasks["T1"]
		if !b.final || b.props[a2a.PropAuthToken] != "" || b.props[a2a.PropA2AUserConfig] != "" ||
			len(b.request.message.Parts) != 0 || b.request.message.ContextID != contextID || b.replyTo != responseTopic("T1") {
			t.Fatalf("the answered task kept %+v", b)
		}
	})
}

func TestTaskTokenAuthenticationIsAuditedOnSuccessAndFailure(t *testing.T) {
	audit := &authEvents{}
	r := newRigWith(t, nil, runtime.WithAuditLogger(audit))
	c := r.connect()
	r.consume(c)
	answers := r.listen(responseTopic("T2"))

	r.taskOn(c, "T1", nil)
	r.submit("T2", "tok:alice:forged", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAuthFailed) {
		t.Fatalf("the caller got %s", m.Payload)
	}

	audit.mu.Lock()
	defer audit.mu.Unlock()
	var succeeded, failed []auth.AuthEvent
	for _, e := range audit.events {
		if e.TaskID == "" {
			continue
		}
		if e.Success {
			succeeded = append(succeeded, e)
		} else {
			failed = append(failed, e)
		}
	}
	if len(succeeded) != 1 || succeeded[0].UserID != "alice" || succeeded[0].CredentialID != "cred-alice" || succeeded[0].TaskID != "T1" {
		t.Fatalf("successful authentications %+v, want one for alice's credential on T1", succeeded)
	}
	if len(failed) != 1 || failed[0].TaskID != "T2" || failed[0].Reason != "invalid auth token" || failed[0].UserID != "alice@example.com" {
		t.Fatalf("failed authentications %+v, want one for the claimed user on T2 with its reason", failed)
	}
}

func TestARequestWithAnUnknownMethodOrACancelNamingNoTaskNeverReachesTheAgent(t *testing.T) {
	r := newRig(t, nil, nil)
	c := r.connect()
	r.consume(c)
	unknownAnswers := r.listen(responseTopic("T9"))

	var unknown map[string]any
	if err := json.Unmarshal(request("T9"), &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["method"] = "tasks/get"
	payload, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	props := map[string]string{a2a.PropReplyTo: responseTopic("T9"), a2a.PropA2AStatusTopic: statusTopic("T9"),
		a2a.PropUserID: "alice@example.com", a2a.PropAuthToken: "tok:alice:T9"}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: payload, UserProperties: props}); err != nil {
		t.Fatal(err)
	}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName),
		&broker.Message{Payload: []byte(`{"jsonrpc":"2.0","id":"C1","method":"` + methodCancel + `","params":{}}`),
			UserProperties: map[string]string{a2a.PropUserID: "alice@example.com"}}); err != nil {
		t.Fatal(err)
	}

	if f := r.taskOn(c, "T2", nil); f.TaskID != "T2" {
		t.Fatalf("the next task is %s, want T2", f.TaskID)
	}
	select {
	case m := <-unknownAnswers:
		t.Fatalf("the link answered a request with an unknown method: %s", m.Payload)
	case <-time.After(300 * time.Millisecond):
	}
	c.quiet()
	if n := r.brk.nacks.Load(); n != 0 {
		t.Fatalf("%d requests were given back to the queue, want the unknown method and the bare cancel acknowledged", n)
	}
}
