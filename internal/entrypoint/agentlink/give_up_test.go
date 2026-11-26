// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func giveUpTimes(t *testing.T, s *agentState, wakes interface{ Load() int32 }, n int32) time.Time {
	t.Helper()
	for {
		waitFor(t, func() bool { return !s.listing.Load() && s.listingGiveUps.Load() >= 1 })
		gaveUp := time.Unix(0, s.listingGaveUpAt.Load())
		if s.listingGiveUps.Load() >= n {
			return gaveUp
		}
		s.sweep(gaveUp.Add(relistWait(s.listingGiveUps.Load()) + time.Minute))
		cur := s.listingGiveUps.Load()
		waitFor(t, func() bool { return s.listingGiveUps.Load() > cur && !s.listing.Load() })
	}
}

func TestTheRelistWaitStaysAtADayForAnyNumberOfGiveUps(t *testing.T) {
	for _, n := range []int32{53, 100, 1 << 20, 1<<31 - 1} {
		if got := relistWait(n); got != maxRelistWait {
			t.Errorf("relistWait(%d)=%v", n, got)
		}
	}
}

func TestTheSweepWakesTheAgentAgainAfterTheDoubledWait(t *testing.T) {
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	s := r.adapter.agent(agentName)
	gaveUp := giveUpTimes(t, s, wakes, 2)
	before := wakes.Load()
	s.sweep(gaveUp.Add(2*relistAfterGiveUp + time.Minute))
	waitFor(t, func() bool { return wakes.Load() > before })
}

func TestASecondGiveUpIsAWarnWithTheGiveUpsAndTheNextWait(t *testing.T) {
	logs := captureAllLogs(t)
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	s := r.adapter.agent(agentName)
	giveUpTimes(t, s, wakes, 2)
	var lines []string
	for _, l := range strings.Split(logs.all(), "\n") {
		if strings.Contains(l, "could not list agent") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("%d lines", len(lines))
	}
	for i, want := range [][]string{{"level=ERROR", "giveUps=1", "retryAfter=30m0s"}, {"level=WARN", "giveUps=2", "retryAfter=1h0m0s"}} {
		for _, w := range want {
			if !strings.Contains(lines[i], w) {
				t.Errorf("give-up %d lacks %q: %s", i+1, w, lines[i])
			}
		}
	}
}

func TestAReleaseAfterGiveUpsStartsTheCountAgain(t *testing.T) {
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	s := r.adapter.agent(agentName)
	giveUpTimes(t, s, wakes, 2)
	s.setStopped(true)
	s.setStopped(false)
	waitFor(t, func() bool { return !s.listing.Load() && s.listingGiveUps.Load() >= 1 })
	if got := s.listingGiveUps.Load(); got != 1 {
		t.Fatalf("after a release and one more give-up the count is %d, want 1", got)
	}
}

func TestAnAgentListedWhileTheWakesRunStartsTheCountAgain(t *testing.T) {
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	s := r.adapter.agent(agentName)
	gaveUp := giveUpTimes(t, s, wakes, 2)
	s.sweep(gaveUp.Add(2*relistAfterGiveUp + time.Minute))
	waitFor(t, func() bool { return s.listing.Load() })
	registry := r.core.AgentRegistry()
	registry.AddOrUpdateInstance(a2a.AgentCard{Name: agentName, URL: "solace:agent-link"}, "claims-assistant@agent-link")
	waitFor(t, func() bool { return !s.listing.Load() })
	registry.RemoveInstance(agentName, "claims-assistant@agent-link")
	waitFor(t, func() bool { return s.listingGiveUps.Load() == 0 })
	before := wakes.Load()
	s.sweep(time.Now().Add(relistAfterGiveUp + time.Minute))
	waitFor(t, func() bool { return wakes.Load() > before })
	waitFor(t, func() bool { return !s.listing.Load() && s.listingGiveUps.Load() >= 1 })
	if got := s.listingGiveUps.Load(); got != 1 {
		t.Fatalf("a new outage after recovery counts %d give-ups, want 1", got)
	}
}

func TestAnAgentAnotherReplicaListsAfterGiveUpsStartsTheCountAgain(t *testing.T) {
	logs := captureAllLogs(t)
	url, wakes := answeringRuntime(t, func(int32) int { return http.StatusServiceUnavailable })
	r := startedWithWake(t, map[string]any{"wake": map[string]any{"url": url}}, lease("running"))
	s := r.adapter.agent(agentName)
	gaveUp := giveUpTimes(t, s, wakes, 2)

	registry := r.core.AgentRegistry()
	registry.AddOrUpdateInstance(a2a.AgentCard{Name: agentName, URL: "solace:agent-link"}, "claims-assistant@agent-link")
	s.sweep(gaveUp.Add(time.Minute))
	if s.listingGiveUps.Load() != 0 || s.listingGaveUpAt.Load() != 0 {
		t.Fatalf("a listed agent kept its give-ups: %d", s.listingGiveUps.Load())
	}
	registry.RemoveInstance(agentName, "claims-assistant@agent-link")
	before := wakes.Load()
	s.sweep(time.Now())
	waitFor(t, func() bool { return wakes.Load() > before })
	waitFor(t, func() bool { return !s.listing.Load() && s.listingGiveUps.Load() == 1 })
	if got := strings.Count(logs.all(), "level=ERROR"); got != 2 {
		t.Fatalf("the new outage's first give-up was not an error (%d errors):\n%s", got, logs.all())
	}
}
