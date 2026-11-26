// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func countingRuntime(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var wakes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		wakes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &wakes
}

func startedWithWake(t *testing.T, agentCfg map[string]any, lease any) *rig {
	t.Helper()
	r := newRigOn(t, []any{claimsAgent(agentCfg)}, fakeAuth{}, nil, func(b *trackingBroker) broker.Broker {
		serveLease(t, b, func(int32) any { return lease })
		return b
	})
	s := r.adapter.agent(agentName)
	select {
	case <-s.stateReady:
	case <-time.After(arrivalWait):
		t.Fatal("the stop lease was never read")
	}
	if s.wakeable() {
		r.awaitWakeWatch()
	}
	return r
}

func TestAWakeAgentIsWokenOnceWhenTheEntrypointStartsSoItIsListedAgain(t *testing.T) {
	url, wakes := countingRuntime(t)
	startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	wokenTimes(t, wakes, 1)
}

func TestAWakeAgentIsNotWokenAtStartWhenTurnedOffDisabledOrStopped(t *testing.T) {
	for name, tc := range map[string]struct {
		wake    map[string]any
		enabled bool
		lease   any
	}{
		"on_start false": {map[string]any{"on_start": false}, true, lease("running")},
		"disabled":       {nil, false, lease("running")},
		"stopped by SAM": {nil, true, lease("stopped")},
	} {
		t.Run(name, func(t *testing.T) {
			url, wakes := countingRuntime(t)
			wake := map[string]any{"url": url}
			for k, v := range tc.wake {
				wake[k] = v
			}
			startedWithWake(t, map[string]any{"wake": wake, "enabled": tc.enabled}, tc.lease)
			wokenTimes(t, wakes, 0)
		})
	}
}

func TestARequestQueuedForAStoppedAgentDoesNotWakeItBeforeTheStopStateIsRead(t *testing.T) {
	url, wakes := countingRuntime(t)
	r := newRigOn(t, []any{claimsAgent(map[string]any{"wake": map[string]any{"url": url, "on_start": false}})}, fakeAuth{}, nil,
		func(b *trackingBroker) broker.Broker {
			serveLease(t, b, func(int32) any {
				time.Sleep(80 * time.Millisecond)
				return lease("stopped")
			})
			return b
		})
	r.awaitWakeWatch()
	r.submit("T1", "tok:alice:T1", nil)
	select {
	case <-r.adapter.agent(agentName).stateReady:
	case <-time.After(arrivalWait):
		t.Fatal("the stop lease was never read")
	}
	if !r.adapter.agent(agentName).isStopped() {
		t.Fatal("the lease did not stop the agent")
	}
	wokenTimes(t, wakes, 0)
}

func answeringRuntime(t *testing.T, status func(wake int32) int) (string, *atomic.Int32) {
	t.Helper()
	var wakes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status(wakes.Add(1)))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &wakes
}

func TestAFailedListingWakeIsMadeAgainUntilTheAgentDescribesItself(t *testing.T) {
	url, wakes := answeringRuntime(t, func(wake int32) int {
		if wake == 1 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	waitFor(t, func() bool { return wakes.Load() == 2 })
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	waitFor(t, func() bool { return r.adapter.agent(agentName).listed() })
	wokenTimes(t, wakes, 2)
}

func TestAListingWakeTheRuntimeKeepsRefusingGivesUpWithAnError(t *testing.T) {
	logs := captureAllLogs(t)
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	waitFor(t, func() bool { return strings.Contains(logs.all(), "could not list agent") })
	if got := wakes.Load(); got != maxStartWakes {
		t.Fatalf("woken %d times, want %d", got, maxStartWakes)
	}
	if !strings.Contains(logs.all(), "level=ERROR") {
		t.Fatal("giving up was not an error")
	}
}

func TestAWakeAgentStoppedAtStartIsWokenToListItWhenSAMReleasesIt(t *testing.T) {
	url, wakes := countingRuntime(t)
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("stopped"))
	wokenTimes(t, wakes, 0)
	r.adapter.agent(agentName).setStopped(false)
	wokenTimes(t, wakes, 1)
}

func TestAReleaseWhileTheListingWakesRunGivesThemAFreshSetOfWakes(t *testing.T) {
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	waitFor(t, func() bool { return wakes.Load() >= 3 })
	r.adapter.agent(agentName).startListing(true)
	waitFor(t, func() bool { return wakes.Load() > maxStartWakes })
}

func TestAConnectionThatNeverDescribesItselfDoesNotListTheAgent(t *testing.T) {
	logs := captureAllLogs(t)
	url, wakes := countingRuntime(t)
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	waitFor(t, func() bool { return wakes.Load() == 1 })
	r.connect()
	waitFor(t, func() bool { return strings.Contains(logs.all(), "could not list agent") })
	if got := wakes.Load(); got != 1 {
		t.Fatalf("an agent with a connection was woken %d times", got)
	}
	if !strings.Contains(logs.all(), "wakes=1") || !strings.Contains(logs.all(), "connected=true") {
		t.Fatalf("the error does not say what was tried:\n%s", logs.all())
	}
}

func TestACardAnotherReplicaPublishesForTheAgentListsIt(t *testing.T) {
	logs := captureAllLogs(t)
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	waitFor(t, func() bool { return wakes.Load() == 1 })
	r.core.AgentRegistry().AddOrUpdateInstance(a2a.AgentCard{Name: agentName, URL: "solace:agent-link"}, "claims-assistant@agent-link")
	wokenTimes(t, wakes, 1)
	time.Sleep(4 * time.Second)
	if strings.Contains(logs.all(), "could not list agent") || wakes.Load() != 1 {
		t.Fatalf("woken %d times; logs:\n%s", wakes.Load(), logs.all())
	}
}

func TestTheSweepWakesTheAgentWhenACardLeftFromBeforeTheStartHasGone(t *testing.T) {
	url, wakes := countingRuntime(t)
	r := newRigOn(t, []any{claimsAgent(map[string]any{"wake": map[string]any{"url": url}})}, fakeAuth{}, nil,
		func(b *trackingBroker) broker.Broker {
			serveLease(t, b, func(int32) any {
				time.Sleep(80 * time.Millisecond)
				return lease("running")
			})
			return b
		})
	registry := r.core.AgentRegistry()
	registry.AddOrUpdateInstance(a2a.AgentCard{Name: agentName, URL: "solace:agent-link"}, "claims-assistant@agent-link")
	s := r.adapter.agent(agentName)
	select {
	case <-s.stateReady:
	case <-time.After(arrivalWait):
		t.Fatal("the stop lease was never read")
	}
	waitFor(t, func() bool { return !s.listing.Load() })
	wokenTimes(t, wakes, 0)

	registry.RemoveInstance(agentName, "claims-assistant@agent-link")
	s.sweep(time.Now())
	wokenTimes(t, wakes, 1)
}

func TestTheSweepWakesAnAgentAgainOnlyLongAfterItsListingWakesGaveUp(t *testing.T) {
	logs := captureAllLogs(t)
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	waitFor(t, func() bool { return strings.Contains(logs.all(), "could not list agent") })
	if !strings.Contains(logs.all(), "wakes=6") || !strings.Contains(logs.all(), "connected=false") ||
		!strings.Contains(logs.all(), "level=ERROR") {
		t.Fatalf("the first give-up is not an error saying what was tried:\n%s", logs.all())
	}
	s := r.adapter.agent(agentName)
	waitFor(t, func() bool { return !s.listing.Load() })
	gaveUp := time.Unix(0, s.listingGaveUpAt.Load())

	s.sweep(gaveUp.Add(relistAfterGiveUp - time.Minute))
	wokenTimes(t, wakes, maxStartWakes)
	s.sweep(gaveUp.Add(relistAfterGiveUp + time.Minute))
	waitFor(t, func() bool { return wakes.Load() > maxStartWakes })
	waitFor(t, func() bool { return strings.Count(logs.all(), "could not list agent") == 2 })
	if strings.Count(logs.all(), "level=ERROR") != 1 {
		t.Fatalf("a second give-up in the same outage was logged at ERROR:\n%s", logs.all())
	}
	waitFor(t, func() bool { return !s.listing.Load() })
	gaveUp = time.Unix(0, s.listingGaveUpAt.Load())
	before := wakes.Load()
	s.sweep(gaveUp.Add(2*relistAfterGiveUp - time.Minute))
	wokenTimes(t, wakes, before)
}

func TestTheWaitBeforeWakingAnAgentAgainDoublesWithEachGiveUpUpToADay(t *testing.T) {
	for giveUps, want := range map[int32]time.Duration{1: 30 * time.Minute, 2: time.Hour, 3: 2 * time.Hour, 6: 16 * time.Hour,
		7: 24 * time.Hour, 40: 24 * time.Hour} {
		if got := relistWait(giveUps); got != want {
			t.Errorf("after %d give-ups the wait is %s, want %s", giveUps, got, want)
		}
	}
}
