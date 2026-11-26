// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestAWrongOrMissingTokenIsRefusedBeforeTheUpgrade(t *testing.T) {
	r := newRig(t, nil, nil)
	for _, tok := range []string{"", "not-the-token"} {
		_, resp, err := r.dial(agentName, tok)
		if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: want 401, got %v", tok, err)
		}
	}
	r.connect()
}

func TestAnAgentCannotAnswerATaskItWasNotGiven(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T9"))
	c.send(map[string]any{"type": typeResult, "taskId": "T9", "state": stateCompleted, "text": "done"})
	c.refused(errCodeUnknownTask, "T9")
	none(t, answers)
}

func TestAForgedUserTokenIsRefusedWithoutReachingTheAgent(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:OTHER-TASK", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAuthFailed) {
		t.Fatalf("refusal %s", m.Payload)
	}
	c.quiet()
}

func TestAStoppedAgentsTasksAreRefusedByTheLinkItself(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.stop()
	if f := c.next(typeControl); f.Enabled == nil || *f.Enabled || f.Reason != "stopped_by_sam" {
		t.Fatalf("%s", f.Raw)
	}
	r.submit("T1", "tok:alice:T1", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAgentStopped) {
		t.Fatalf("refusal %s", m.Payload)
	}
	c.quiet()
}

func TestAStoppedAgentIsNotWokenAndItsRequestIsRefusedWithoutIt(t *testing.T) {
	r, wakes := wakeRig(t)
	answers := r.listen(responseTopic("T1"))
	r.stop()
	r.submit("T1", "tok:alice:T1", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAgentStopped) {
		t.Fatalf("refusal %s", m.Payload)
	}
	time.Sleep(200 * time.Millisecond)
	if wakes.Load() != 0 {
		t.Fatalf("a stopped agent was woken %d times", wakes.Load())
	}
}

func TestAnUnsignedStopIsRefused(t *testing.T) {
	r := newRig(t, nil, nil)
	reply, err := r.brk.Request(t.Context(), a2a.ControlAppCustomTopic(testNS, agentName, "patch", "instances/"+agentName),
		&broker.Message{ReplyTo: testNS + "/sam/v1/control/reply/r1", Payload: []byte(`{"enabled":false}`), UserProperties: map[string]string{a2a.PropAuthToken: "forged"}}, 5*time.Second)
	if err != nil || !strings.Contains(string(reply.Payload), "error") {
		t.Fatalf("control reply %v %s", err, reply.Payload)
	}
	if r.adapter.agent(agentName).isStopped() {
		t.Fatal("an unsigned stop stopped the agent")
	}
}

func TestTheRegisteredOwnerIsStampedOnTheCardAndAnAgentCannotForgeIt(t *testing.T) {
	r := newRig(t, map[string]any{"owner": "claims-ops@example.com", "team": "Claims Operations"}, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"declared": map[string]any{"owner": "mallory@example.com", "team": "Payments"}}})
	var card a2a.AgentCard
	if err := json.Unmarshal(recv(t, cards).Payload, &card); err != nil {
		t.Fatal(err)
	}
	var registrations []map[string]any
	for _, e := range card.Capabilities.Extensions {
		if e.URI == a2a.ExtensionURIRegistration {
			registrations = append(registrations, e.Params)
		}
	}
	if len(registrations) != 1 || registrations[0]["owner"] != "claims-ops@example.com" || registrations[0]["team"] != "Claims Operations" {
		t.Fatalf("registration extensions %v", registrations)
	}
}

func TestUnacknowledgedTasksGoBackWhenTheConnectionDrops(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.submit("T1", "tok:alice:T1", nil)
	c.next(typeTask)
	_ = c.ws.Close()
	waitFor(t, func() bool { return r.brk.nacks.Load() == 1 })
	if r.brk.acks.Load() != 0 {
		t.Fatalf("acks %d nacks %d", r.brk.acks.Load(), r.brk.nacks.Load())
	}
}

func TestDeliveryStopsAtTheInflightLimit(t *testing.T) {
	r, c := connectedRig(t, nil)
	for _, id := range []string{"T1", "T2", "T3"} {
		r.submit(id, "tok:alice:"+id, nil)
	}
	first := c.next(typeTask)
	c.next(typeTask)
	c.quiet()
	c.send(map[string]any{"type": typeAck, "delivery": first.Delivery})
	if f := c.next(typeTask); f.TaskID != "T3" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAnAckForADeliveryThisConnectionDoesNotHoldIsRefused(t *testing.T) {
	_, c := connectedRig(t, nil)
	c.send(map[string]any{"type": typeAck, "delivery": 42})
	c.refused(errCodeUnknownDeliver, "42")
}

func TestAnAgentWithNoConnectionIsWokenOnceWithAnOAuthToken(t *testing.T) {
	var wakes atomic.Int32
	var gotAuth atomic.Value
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_secret") != "wake-secret" || r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"wake-token","expires_in":3600}`))
	}))
	defer idp.Close()
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		wakes.Add(1)
	}))
	defer runtimeSrv.Close()
	r := newRig(t, map[string]any{"wake": map[string]any{
		"url": runtimeSrv.URL, "token_url": idp.URL, "client_id": "app", "client_secret": "wake-secret", "scope": "api/.default",
	}}, nil)
	r.awaitWakeWatch()
	r.submit("T1", "tok:alice:T1", nil)
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return wakes.Load() > 0 })
	time.Sleep(300 * time.Millisecond)
	if wakes.Load() != 1 || gotAuth.Load() != "Bearer wake-token" {
		t.Fatalf("wakes %d auth %v", wakes.Load(), gotAuth.Load())
	}

	c := r.connect()
	r.consume(c)
	got := map[string]bool{}
	for range 2 {
		f := c.next(typeTask)
		got[f.TaskID] = true
		c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
	}
	if !got["T1"] || !got["T2"] {
		t.Fatalf("the woken agent got %v", got)
	}
}

func TestConfigRefusesWhatItCannotEnforce(t *testing.T) {
	for name, raw := range map[string]map[string]any{
		"no agents":   {},
		"bad digest":  {"agents": []any{map[string]any{"name": agentName, "token_sha256": "abc"}}},
		"bad name":    {"agents": []any{map[string]any{"name": "a/b", "token_sha256": digest(token)}}},
		"self peer":   {"agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{agentName}}}},
		"http wake":   {"agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token), "wake": map[string]any{"url": "http://runtime.example.com"}}}},
		"duplicate":   {"agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token)}, map[string]any{"name": agentName, "token_sha256": digest(token)}}},
		"no secret":   {"agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token), "wake": map[string]any{"url": "https://r.example.com", "token_url": "https://idp.example.com", "client_id": "x"}}}},
		"long owner":  {"agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token), "owner": strings.Repeat("o", 300)}}},
		"zero limits": {"max_inflight": 0, "agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token)}}},
	} {
		if _, err := ParseConfig(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestACancelGoesToTheConnectionHoldingTheTask(t *testing.T) {
	r := newRig(t, nil, nil)
	c1, c2 := r.connect(), r.connect()
	r.consume(c1)
	r.consume(c2)
	r.submit("T1", "tok:alice:T1", nil)
	holder, task := nextOf(t, typeTask, c1, c2)
	holder.send(map[string]any{"type": typeAck, "delivery": task.Delivery})
	for range 2 {
		r.cancel("T1")
		if f := holder.next(typeCancel); f.TaskID != "T1" {
			t.Fatalf("%s", f.Raw)
		}
	}
}

func TestAPeerCallThatCouldNotBePublishedIsReportedAndForgotten(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	r.brk.failTopic.Store(a2a.AgentRequestTopic(testNS, peerName))
	c.send(peerCall("S1", peerName, "Is policy P-9 active?"))
	c.refused(errCodePublishFailed, "S1")
	if hasSubtask(r, "S1") {
		t.Fatal("a peer call that was never sent is still open")
	}
	refused := r.brk.refusedPublish.Load()
	if refused == nil {
		t.Fatal("the refused peer request was not seen")
	}
	peerID := peerRequestID(t, refused)
	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, a2a.NewTextPart("Active")))
	c.quiet()
}

func TestDeliveryResumesAfterTheAgentReconnects(t *testing.T) {
	r, c := connectedRig(t, nil)
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })
	c = r.connect()
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	if f := c.next(typeTask); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAWokenAgentThatLeavesTasksUnacknowledgedIsWokenAgain(t *testing.T) {
	r, wakes := wakeRig(t)
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return wakes.Load() == 1 })

	c := r.connect()
	r.consume(c)
	c.next(typeTask)
	_ = c.ws.Close()
	waitFor(t, func() bool { return wakes.Load() == 2 })
}

func TestARequestAfterAWokenAgentLeftWakesItWithoutWaitingOutTheInterval(t *testing.T) {
	r, wakes := wakeRig(t)
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return wakes.Load() == 1 })
	c := r.connect()
	r.consume(c)
	task := c.next(typeTask)
	c.send(map[string]any{"type": typeAck, "delivery": task.Delivery})
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	time.Sleep(200 * time.Millisecond)
	_ = c.ws.Close()
	time.Sleep(300 * time.Millisecond)
	if wakes.Load() != 1 {
		t.Fatalf("woken again with nothing left: %d", wakes.Load())
	}
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return wakes.Load() == 2 })
}

func TestASignedStopTakesEffectAtOnceAfterAFloodOfUnsignedControlCommands(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.publishWait = time.Second
	const floodReplies = testNS + "/sam/v1/control/reply/flood"
	r.brk.stallTopic.Store(floodReplies)
	topic := a2a.ControlAppCustomTopic(testNS, agentName, "patch", "instances/"+agentName)
	for range 300 {
		if err := r.brk.Publish(t.Context(), topic, &broker.Message{ReplyTo: floodReplies, Payload: []byte(`{"enabled":false}`),
			UserProperties: map[string]string{a2a.PropAuthToken: "forged"}}); err != nil {
			t.Fatal(err)
		}
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		r.stop()
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !r.adapter.agent(agentName).isStopped() {
		if time.Now().After(deadline) {
			t.Fatal("a signed stop sent after a flood of unsigned ones did not take effect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	<-stopped
}
