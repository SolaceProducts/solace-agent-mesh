// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func describedClaims() map[string]any {
	return map[string]any{
		"name":        "someone-else",
		"displayName": "Claims Assistant", "description": "Handles insurance claims", "version": "1.2.0",
		"skills":         []any{map[string]any{"id": "claims", "name": "Claims", "description": "Files claims"}},
		"tools":          []any{map[string]any{"name": "pay_claim", "description": "Pays a claim", "requiredScopes": []any{"tool:claims:pay"}}},
		"requiredScopes": []any{"agent:claims:use"},
		"inputSchema":    map[string]any{"type": "object"},
		"welcomeMessage": "Hello", "suggestions": []any{map[string]any{"title": "File a claim", "prompt": "I want to file a claim"}},
		"peers":     []any{peerName, "hr-directory"},
		"streaming": true,
		"declared": map[string]any{"framework": "Strands Agents", "owner": "mallory@example.com",
			"sdk":        map[string]any{"name": "sam-mesh", "version": "0.2.0"},
			"dataAccess": []any{map[string]any{"system": "Claims DB", "classification": "confidential", "access": "read-write"}}},
		"health": []any{map[string]any{"id": "db", "severity": "degraded", "detail": "slow"}},
	}
}

func extension(card a2a.AgentCard, uri string) *a2a.AgentExtension {
	for i := range card.Capabilities.Extensions {
		if card.Capabilities.Extensions[i].URI == uri {
			return &card.Capabilities.Extensions[i]
		}
	}
	return nil
}

func TestDescribePublishesTheCardANativeAgentWouldPlusWhatTheLinkStamps(t *testing.T) {
	r := newRig(t, map[string]any{"owner": "claims-ops@example.com", "wake": map[string]any{"url": "http://127.0.0.1:1/wake"}}, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": describedClaims()})
	m := recv(t, cards)
	var card a2a.AgentCard
	if err := json.Unmarshal(m.Payload, &card); err != nil {
		t.Fatal(err)
	}
	native := agent.BuildCard(&agent.AgentConfig{Name: agentName, Namespace: testNS, Description: "Handles insurance claims", Version: "1.2.0",
		DisplayName: agentName, InputSchema: map[string]any{"type": "object"}, RequiredScopes: []string{"agent:claims:use"},
		SupportsStreaming: true, Skills: []a2a.AgentSkill{{ID: "claims", Name: "Claims", Description: "Files claims", Tags: []string{}}},
		Welcome: &agent.WelcomeConfig{Message: "Hello", Suggestions: []agent.WelcomeSuggestion{{Label: "File a claim", Prompt: "I want to file a claim"}}}},
		agent.CardInputs{Tools: []agent.CardTool{{Name: "pay_claim", Description: "Pays a claim", RequiredScopes: []string{"tool:claims:pay"}}},
			PeerNames: []string{peerName}})
	nativeBody, _ := json.Marshal(native)
	var want a2a.AgentCard
	_ = json.Unmarshal(nativeBody, &want)
	for _, ext := range want.Capabilities.Extensions {
		if got := extension(card, ext.URI); got == nil || !reflect.DeepEqual(*got, ext) {
			t.Fatalf("extension %s: %+v, native %+v", ext.URI, got, ext)
		}
	}
	if card.Name != agentName || card.URL != want.URL || card.Version != "1.2.0" || !reflect.DeepEqual(card.Skills, want.Skills) ||
		m.UserProperties["instance_id"] != agentName+instanceSuffix || !a2a.IsOnDemandCard(card) {
		t.Fatalf("card %s", m.Payload)
	}
	provenance := extension(card, a2a.ExtensionURIProvenance)
	registration := extension(card, a2a.ExtensionURIRegistration)
	health := a2a.HealthProblemsFromCard(card)
	if provenance == nil || provenance.Params["origin"] != "sam-mesh" || provenance.Params["sdk_version"] != "0.2.0" ||
		provenance.Params["framework"] != "Strands Agents" || provenance.Params["owner"] != "mallory@example.com" ||
		registration == nil || registration.Params["owner"] != "claims-ops@example.com" ||
		extension(card, a2a.ExtensionURIArtifactAccess) == nil || len(health) != 1 || health[0].ID != "db" {
		t.Fatalf("card %s", m.Payload)
	}
}

func TestTheCardIsRepublishedWhileConnectedAndRetireTakesItDown(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cardRepublish = 50 * time.Millisecond
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "first"}})
	recv(t, cards)
	if m := recv(t, cards); !strings.Contains(string(m.Payload), "first") || m.UserProperties["removed"] != "" {
		t.Fatalf("republished %s", m.Payload)
	}
	c.send(map[string]any{"type": typeRetire})
	waitFor(t, func() bool {
		select {
		case m := <-cards:
			return m.UserProperties["removed"] == "true"
		default:
			return false
		}
	})
	drain(cards)
	if m := quietFor(cards, 200*time.Millisecond); m != nil {
		t.Fatalf("a retired card was published again: %v", m.UserProperties)
	}
}

func TestADroppedAgentsCardIsLeftToExpire(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cardRepublish = 50 * time.Millisecond
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	recv(t, cards)
	_ = c.ws.Close()
	waitFor(t, func() bool {
		s := r.adapter.agent(agentName)
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.republishing
	})
	drain(cards)
	if m := quietFor(cards, 200*time.Millisecond); m != nil {
		t.Fatalf("published %v after the agent left", m.UserProperties)
	}
}

func TestADescribeThatIsMalformedOrTooLargeIsRefused(t *testing.T) {
	r := newRig(t, nil, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	for _, agentObject := range []any{
		"not an object",
		map[string]any{"description": strings.Repeat("d", maxCardBytes)},
		map[string]any{"health": []any{map[string]any{"id": "db", "severity": "on fire"}}},
	} {
		c.send(map[string]any{"type": typeDescribe, "agent": agentObject})
		c.refused(errCodeBadMessage, "")
	}
	none(t, cards)
}

func drain(ch <-chan *broker.Message) {
	for range 50 {
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func quietFor(ch <-chan *broker.Message, d time.Duration) *broker.Message {
	select {
	case m := <-ch:
		return m
	case <-time.After(d):
		return nil
	}
}

func removal(m *broker.Message) bool { return m.UserProperties["removed"] == "true" }

func cardsFor(cards <-chan *broker.Message, d time.Duration) []*broker.Message {
	var got []*broker.Message
	deadline := time.After(d)
	for {
		select {
		case m := <-cards:
			got = append(got, m)
		case <-deadline:
			return got
		}
	}
}

func cardsUntilQuiet(cards <-chan *broker.Message, d time.Duration) []*broker.Message {
	var got []*broker.Message
	for m := quietFor(cards, d); m != nil; m = quietFor(cards, d) {
		got = append(got, m)
	}
	return got
}

func TestARetireFromOneConnectionLeavesTheAgentListedForTheOthers(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cardRepublish = 50 * time.Millisecond
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	leaving, staying := r.connect(), r.connect()
	leaving.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	recv(t, cards)

	leaving.send(map[string]any{"type": typeRetire})
	_ = leaving.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 1 })
	for _, m := range cardsFor(cards, 200*time.Millisecond) {
		if removal(m) {
			t.Fatal("one connection's retire took the card down while another stays")
		}
	}
	if m := recv(t, cards); removal(m) || !strings.Contains(string(m.Payload), "claims") {
		t.Fatalf("not republished for the staying connection: %v", m.UserProperties)
	}

	staying.send(map[string]any{"type": typeRetire})
	waitFor(t, func() bool {
		m := quietFor(cards, time.Second)
		return m != nil && removal(m)
	})
	if got := cardsUntilQuiet(cards, 200*time.Millisecond); len(got) != 0 {
		t.Fatalf("published %v after the last connection retired", got[0].UserProperties)
	}
	staying.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims again"}})
	if m := recv(t, cards); removal(m) || !strings.Contains(string(m.Payload), "claims again") {
		t.Fatalf("a describe after retire did not list the agent: %v", m.UserProperties)
	}
}

func TestARetireIsNeverFollowedByTheCardItTookDown(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cardRepublish = time.Millisecond
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	for range 20 {
		c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
		c.send(map[string]any{"type": typeRetire})
		got := cardsUntilQuiet(cards, 100*time.Millisecond)
		if len(got) == 0 || !removal(got[len(got)-1]) {
			t.Fatal("the card was published after its removal")
		}
	}
}

func stopAndRelease(t *testing.T, r *rig, cards <-chan *broker.Message, whileStopped func()) (onStop, onRelease []*broker.Message) {
	t.Helper()
	r.stop()
	onStop = cardsUntilQuiet(cards, 300*time.Millisecond)
	whileStopped()
	if got := cardsUntilQuiet(cards, 200*time.Millisecond); len(got) != 0 {
		t.Fatalf("published %v while stopped", got[0].UserProperties)
	}
	r.release()
	onRelease = cardsFor(cards, 300*time.Millisecond)
	return onStop, onRelease
}

func TestTheLinkTakesAStoppedAgentsCardDownAndListsItAgainOnRelease(t *testing.T) {
	for _, connections := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d connections", connections), func(t *testing.T) {
			r := newRig(t, nil, nil)
			r.adapter.cardRepublish = 100 * time.Millisecond
			cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
			var cs []*client
			for range connections {
				cs = append(cs, r.connect())
			}
			cs[0].send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "before"}})
			recv(t, cards)

			onStop, onRelease := stopAndRelease(t, r, cards, func() {
				cs[len(cs)-1].send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "while stopped"}})
				waitFor(t, func() bool {
					var description string
					r.locked(func(s *agentState) { description = s.described.card.Description })
					return description == "while stopped"
				})
			})
			if len(onStop) == 0 || !removal(onStop[len(onStop)-1]) {
				t.Fatal("the stop did not take the card down")
			}
			if len(onRelease) == 0 || removal(onRelease[0]) || !strings.Contains(string(onRelease[0].Payload), "while stopped") {
				t.Fatal("the release did not list the agent with its latest description")
			}
		})
	}
}

func TestAnOnDemandAgentIsListedAgainOnReleaseWithoutAConnection(t *testing.T) {
	r := newRig(t, map[string]any{"wake": map[string]any{"url": "http://127.0.0.1:1/wake"}}, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	recv(t, cards)
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })
	cardsUntilQuiet(cards, 100*time.Millisecond)
	onStop, onRelease := stopAndRelease(t, r, cards, func() {})
	if len(onStop) != 1 || !removal(onStop[0]) || len(onRelease) != 1 || removal(onRelease[0]) {
		t.Fatalf("on stop %d publishes, on release %d", len(onStop), len(onRelease))
	}
}

func TestAnAgentThatReconnectsWithoutDescribingIsListedAgain(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cardRepublish = 50 * time.Millisecond
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	recv(t, cards)
	_ = c.ws.Close()
	waitFor(t, func() bool {
		var republishing bool
		r.locked(func(s *agentState) { republishing = s.republishing })
		return !republishing
	})
	cardsUntilQuiet(cards, 100*time.Millisecond)

	r.connect()
	if m := recv(t, cards); removal(m) || !strings.Contains(string(m.Payload), "claims") {
		t.Fatalf("not listed on reconnect: %v", m.UserProperties)
	}
	if m := recv(t, cards); removal(m) {
		t.Fatal("not republished after reconnect")
	}
}

func TestConsumeIsRefusedUntilTheAgentHasDescribedItself(t *testing.T) {
	r := newRig(t, nil, nil)
	c := r.connect()
	c.send(map[string]any{"type": typeConsume})
	if f := c.refused(errCodeBadMessage, ""); f.Frame != typeConsume {
		t.Fatalf("a refused consume does not say which frame was refused: %s", f.Raw)
	}
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	if f := c.refused(errCodeBadMessage, ""); f.Frame != typeDescribe {
		t.Fatalf("a refused describe does not say which frame was refused: %s", f.Raw)
	}
	if r.adapter.agent(agentName).hasConsumer() {
		t.Fatal("a connection consumes before the agent described itself")
	}
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	r.startConsuming(c, agentName)

	later := r.connect()
	r.startConsuming(later, agentName)
}

func TestAConnectionWhoseDescribeWasRefusedGetsNoTasksUntilOneIsAccepted(t *testing.T) {
	r := newRig(t, nil, nil)
	earlier := r.connect()
	earlier.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	earlier.send(map[string]any{"type": "no-such-type"})
	earlier.refused(errCodeUnknownType, "")

	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c.send(map[string]any{"type": typeConsume})
	if f := c.refused(errCodeBadMessage, ""); f.Frame != typeDescribe {
		t.Fatalf("%s", f.Raw)
	}
	if f := c.refused(errCodeBadMessage, ""); f.Frame != typeConsume {
		t.Fatalf("a consume after a refused describe was accepted under the earlier description: %s", f.Raw)
	}
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	c.quiet()
	none(t, answers)

	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	c.send(map[string]any{"type": typeConsume})
	if f := c.next(typeTask); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestADescribeRefusedOnAConsumingConnectionStopsItsTasks(t *testing.T) {
	r, c := connectedRig(t, nil)
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	if f := c.refused(errCodeBadMessage, ""); f.Frame != typeDescribe {
		t.Fatalf("%s", f.Raw)
	}
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	c.quiet()
	none(t, answers)
}

func TestAStoppedAgentMayDescribeAndConsumeButGetsNoTask(t *testing.T) {
	r := newRig(t, nil, nil)
	r.stop()
	answers := r.listen(responseTopic("T1"))
	c := r.connect()
	c.next(typeControl)
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAgentStopped) {
		t.Fatalf("refusal %s", m.Payload)
	}
	c.quiet()
}

func TestARetiredConnectionLeftAloneDoesNotKeepTheCardListed(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cardRepublish = 50 * time.Millisecond
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	retired, dropping := r.connect(), r.connect()
	retired.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	recv(t, cards)
	retired.send(map[string]any{"type": typeRetire})
	recv(t, cards)

	_ = dropping.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 1 })
	cardsFor(cards, 200*time.Millisecond)
	if got := cardsFor(cards, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("a retired connection kept the card republished: %d publishes", len(got))
	}
}

func TestDescribesRacingOnEveryConnectionNeverNameMoreScopesThanTheLimit(t *testing.T) {
	r := newRig(t, nil, nil)
	conns := make([]*client, defaultMaxConnectionsPerAgent)
	for i := range conns {
		conns[i] = r.connect()
	}
	const describes = 20
	for round := range describes {
		for i, c := range conns {
			scopes := make([]any, maxToolScopes/2)
			for j := range scopes {
				scopes[j] = fmt.Sprintf("tool:claims:c%d-r%d-s%d", i, round, j)
			}
			c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"tools": []any{map[string]any{"name": "x", "requiredScopes": scopes}}}})
		}
	}
	refused := 0
	for _, c := range conns {
		refused += refusalsUntilQuiet(c)
	}
	if want := len(conns)*describes - 2; refused != want {
		t.Fatalf("%d describes refused, want %d", refused, want)
	}
	r.locked(func(s *agentState) {
		if len(s.toolScopesNamed) > maxToolScopes {
			t.Fatalf("the agent's tools named %d scopes", len(s.toolScopesNamed))
		}
	})
	conns[0].send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "no tools"}})
	conns[0].quiet()
}

func TestDescribesPublishTheCardAtMostOncePerInterval(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.describeInterval = 500 * time.Millisecond
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	describe := func(description string) {
		c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": description}})
	}
	describe("claims v1")
	recv(t, cards)
	describe("claims v1")
	describe("claims v2")
	describe("claims v3")
	if got := cardsFor(cards, 200*time.Millisecond); len(got) != 0 {
		t.Fatalf("published %d cards within the interval", len(got))
	}
	got := cardsUntilQuiet(cards, 700*time.Millisecond)
	if len(got) != 1 || !strings.Contains(string(got[0].Payload), "claims v3") {
		t.Fatalf("published %d cards after the interval", len(got))
	}
	describe("claims v3")
	if got := cardsFor(cards, 700*time.Millisecond); len(got) != 0 {
		t.Fatalf("an unchanged describe published %d cards", len(got))
	}
}

func refusalsUntilQuiet(c *client) int {
	refused := 0
	for {
		select {
		case f := <-c.in:
			if f.Type == typeError {
				refused++
			}
		case <-time.After(300 * time.Millisecond):
			return refused
		}
	}
}

func TestADescribeSentAgainAfterPublishFailedPublishesTheCard(t *testing.T) {
	r := newRig(t, nil, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	r.brk.failTopic.Store(a2a.DiscoveryAgentCardsTopic(testNS))
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	c.refused(errCodePublishFailed, "")
	r.brk.failTopic.Store("")
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	if m := recv(t, cards); removal(m) || !strings.Contains(string(m.Payload), "claims") {
		t.Fatalf("%v", m.UserProperties)
	}
}

func TestConnectionsThatOpenAndCloseInALoopDoNotRepublishTheCard(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.describeInterval = time.Hour
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	staying := r.connect()
	staying.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	recv(t, cards)
	s := r.adapter.agent(agentName)
	cycle := func(remaining int) {
		_ = r.connect().ws.Close()
		waitFor(t, func() bool { return len(s.connections()) == remaining })
	}
	for range 30 {
		cycle(1)
	}
	_ = staying.ws.Close()
	waitFor(t, func() bool { return len(s.connections()) == 0 })
	for range 10 {
		cycle(0)
	}
	if got := cardsFor(cards, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("40 connections published %d cards", len(got))
	}
}

func TestConnectionsBeyondTheAgentsRatePerMinuteAreRefused(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cfg.MaxConnectsPerMinute = 3
	for range 3 {
		_ = r.connect().ws.Close()
	}
	_, resp, err := r.dial(agentName, token)
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a fourth connection in the minute: %v %v", resp, err)
	}
}

func TestAnAcceptedDescribeAfterARefusedOneResumesTasksWithoutAnotherConsume(t *testing.T) {
	r, c := connectedRig(t, nil)
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"health": []any{map[string]any{"id": "db", "severity": "critical"}}}})
	if f := c.refused(errCodeBadMessage, ""); f.Frame != typeDescribe {
		t.Fatalf("%s", f.Raw)
	}
	r.submit("T1", "tok:alice:T1", nil)
	c.quiet()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"health": []any{map[string]any{"id": "db", "severity": "degraded"}}}})
	if f := c.next(typeTask); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestADescribeRefusedWhileATaskIsUnacknowledgedLeavesItsAcknowledgementSettlingTheTask(t *testing.T) {
	for name, newRig := range map[string]func(*testing.T) *rig{
		"queue kept bound":     func(t *testing.T) *rig { return newRig(t, nil, nil) },
		"queue let go of idle": newRigReleasing,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			c := r.connect()
			r.consume(c)
			r.submit("T1", "tok:alice:T1", nil)
			task := c.next(typeTask)
			c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
			c.refused(errCodeBadMessage, "")
			c.send(map[string]any{"type": typeAck, "delivery": task.Delivery})
			waitFor(t, func() bool { return r.brk.acks.Load() == 1 })
		})
	}
}

func TestADescribeOverTheValueLimitIsRefusedAsAnyRefusedDescribeIs(t *testing.T) {
	r := newRig(t, nil, nil)
	earlier := r.connect()
	earlier.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	earlier.send(map[string]any{"type": "no-such-type"})
	earlier.refused(errCodeUnknownType, "")

	c := r.connect()
	padding := strings.TrimSuffix(strings.Repeat("1,", maxFrameValues), ",")
	if err := c.ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"describe","agent":{"skills":[`+padding+`]}}`)); err != nil {
		t.Fatal(err)
	}
	if f := c.refused(errCodeLimit, ""); f.Frame != typeDescribe {
		t.Fatalf("%s", f.Raw)
	}
	if err := c.ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"consume","padding":[`+padding+`]}`)); err != nil {
		t.Fatal(err)
	}
	if f := c.refused(errCodeLimit, ""); f.Frame != typeConsume {
		t.Fatalf("%s", f.Raw)
	}
	c.send(map[string]any{"type": typeConsume})
	c.refused(errCodeBadMessage, "")
	r.submit("T1", "tok:alice:T1", nil)
	c.quiet()
}

func TestADescribeIsRefusedWhenItsTextForNativeAgentsIsOverItsBounds(t *testing.T) {
	r := newRig(t, nil, nil)
	c := r.connect()
	skill := func(name, description string) map[string]any {
		return map[string]any{"id": name, "name": name, "description": description}
	}
	manySkills := make([]any, maxSkills+1)
	for i := range manySkills {
		manySkills[i] = skill(fmt.Sprintf("s%d", i), "x")
	}
	for _, over := range []map[string]any{
		{"description": strings.Repeat("d", maxDescriptionBytes+1)},
		{"skills": manySkills},
		{"skills": []any{skill("triage", strings.Repeat("d", maxSkillDescriptionBytes+1))}},
		{"skills": []any{skill(strings.Repeat("n", maxNameBytes+1), "x")}},
	} {
		c.send(map[string]any{"type": typeDescribe, "agent": over})
		if f := c.refused(errCodeLimit, ""); f.Frame != typeDescribe {
			t.Fatalf("%s", f.Raw)
		}
	}
	atBounds := []any{skill("triage", strings.Repeat("d", maxSkillDescriptionBytes))}
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": strings.Repeat("d", maxDescriptionBytes), "skills": atBounds}})
	c.quiet()
}
