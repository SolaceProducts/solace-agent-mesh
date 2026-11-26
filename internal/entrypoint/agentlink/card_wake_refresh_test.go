// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var wakeAgentConfig = map[string]any{"wake": map[string]any{"url": "http://127.0.0.1:1/wake"}}

const wakeRefreshInterval = 50 * time.Millisecond

func wakeRefreshRig(t *testing.T, agentCfg map[string]any) (*rig, *client, <-chan *broker.Message) {
	t.Helper()
	r := newRig(t, agentCfg, nil)
	r.adapter.cardRepublish = wakeRefreshInterval
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "asleep"}})
	recv(t, cards)
	return r, c, cards
}

func disconnect(t *testing.T, r *rig, c *client) {
	t.Helper()
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })
}

func republished(cards <-chan *broker.Message) bool {
	for _, m := range cardsFor(cards, 10*wakeRefreshInterval) {
		if !removal(m) {
			return true
		}
	}
	return false
}

func TestAWokenAgentsCardIsRefreshedWithoutAConnection(t *testing.T) {
	r, c, cards := wakeRefreshRig(t, wakeAgentConfig)
	disconnect(t, r, c)
	cardsFor(cards, 10*wakeRefreshInterval)
	if !republished(cards) {
		t.Fatal("the card of a woken agent with no connection was not published again")
	}
}

func TestAnAgentThatIsNotWokenHasItsCardLeftToExpireWithoutAConnection(t *testing.T) {
	r, c, cards := wakeRefreshRig(t, nil)
	disconnect(t, r, c)
	waitFor(t, func() bool {
		var republishing bool
		r.locked(func(s *agentState) { republishing = s.republishing })
		return !republishing
	})
	drain(cards)
	if republished(cards) {
		t.Fatal("the card of an agent that is not woken was refreshed with no connection")
	}
}

func TestAStoppedWokenAgentsCardIsNotRefreshedUntilItIsReleased(t *testing.T) {
	r, c, cards := wakeRefreshRig(t, wakeAgentConfig)
	disconnect(t, r, c)
	r.stop()
	waitFor(t, func() bool {
		select {
		case m := <-cards:
			return removal(m)
		default:
			return false
		}
	})
	waitFor(t, func() bool {
		var republishing bool
		r.locked(func(s *agentState) { republishing = s.republishing })
		return !republishing
	})
	drain(cards)
	if got := cardsFor(cards, 10*wakeRefreshInterval); len(got) != 0 {
		t.Fatalf("published %v while SAM had the agent stopped", got[0].UserProperties)
	}
	r.release()
	waitFor(t, func() bool {
		select {
		case m := <-cards:
			return !removal(m)
		default:
			return false
		}
	})
	if !republished(cards) {
		t.Fatal("the card was not refreshed after the release")
	}
}

func TestARetiredWokenAgentsCardIsNotRefreshed(t *testing.T) {
	r, c, cards := wakeRefreshRig(t, wakeAgentConfig)
	c.send(map[string]any{"type": typeRetire})
	waitFor(t, func() bool {
		select {
		case m := <-cards:
			return removal(m)
		default:
			return false
		}
	})
	disconnect(t, r, c)
	waitFor(t, func() bool {
		var republishing bool
		r.locked(func(s *agentState) { republishing = s.republishing })
		return !republishing
	})
	drain(cards)
	if got := cardsFor(cards, 10*wakeRefreshInterval); len(got) != 0 {
		t.Fatalf("published %v after the agent retired", got[0].UserProperties)
	}
}
