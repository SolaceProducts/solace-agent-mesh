// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func TestAWEStatusTracker_JudgesADeploymentOnEveryLiveAWE(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tr := newAWEStatusTracker(nil, "ns")
	tr.now = func() time.Time { return now }
	report := func(process, deploymentID, failure string) {
		tr.observe(control.AWEStatus{ProcessID: process, Agents: []control.AgentStatus{{Name: "agent_x", DeploymentID: deploymentID, Error: failure}}})
	}
	check := func(step string, wantDone bool, wantFailure string, wantLive bool) {
		t.Helper()
		if done, failure := tr.outcome("agent_x", "d1"); done != wantDone || failure != wantFailure {
			t.Fatalf("%s: outcome = %v, %q, want %v, %q", step, done, failure, wantDone, wantFailure)
		}
		if live := tr.anyLive(); live != wantLive {
			t.Fatalf("%s: anyLive = %v, want %v", step, live, wantLive)
		}
	}

	report("A", "d1", "")
	check("not listening yet", false, "", false)
	tr.listening = now
	report("A", "d1", "")
	check("before one report interval", false, "", true)

	now = now.Add(aweStatusSettle)
	report("A", "d1", "")
	check("one AWE runs it", true, "", true)
	report("B", "d0", "")
	check("a second AWE still runs the previous deployment", false, "", true)
	report("B", "d1", "queue create refused")
	check("the second AWE could not start it", false, "queue create refused", true)
	report("B", "d1", "")
	check("both AWEs run it", true, "", true)

	report("B", "d0", "")
	now = now.Add(control.AWEStatusExpiry + time.Second)
	report("A", "d1", "")
	check("an AWE silent past the expiry", true, "", true)

	tr.observe(control.AWEStatus{ProcessID: "A", Leaving: true})
	check("no live AWE", false, "", false)
}

func TestAWEStatusTracker_RollOutEndsWithTheReasonTheUserSees(t *testing.T) {
	cases := []struct {
		name    string
		reports []control.AWEStatus
		want    string
	}{
		{name: "no AWE reports", want: "the agent deployment was not confirmed by every AWE within 200ms"},
		{name: "an AWE still runs the previous deployment", reports: []control.AWEStatus{{ProcessID: "A", Agents: []control.AgentStatus{{Name: "agent_x", DeploymentID: "d0"}}}},
			want: "the agent deployment was not confirmed by every AWE within 200ms"},
		{name: "an AWE could not start it", reports: []control.AWEStatus{{ProcessID: "A", Agents: []control.AgentStatus{{Name: "agent_x", DeploymentID: "d1", Error: "queue create refused"}}}},
			want: "agent failed to start: queue create refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1_000_000, 0)
			brk := broker.NewDevBroker()
			if err := brk.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			tr := newAWEStatusTracker(brk, "ns")
			tr.now = func() time.Time { return now }
			tr.wait = 200 * time.Millisecond
			tr.listening = now.Add(-aweStatusSettle)
			for _, r := range tc.reports {
				tr.observe(r)
			}
			err := tr.rollOut(t.Context(), "agent_x", "d1")
			var runtime *runtimeError
			if !errors.As(err, &runtime) || err.Error() != tc.want {
				t.Fatalf("rollOut = %v, want the runtime error %q", err, tc.want)
			}
		})
	}
}

func TestRolloutWithoutAWEStatus(t *testing.T) {
	if err := rollOutOnAWEs(t.Context(), nil, "agent_x", "d1"); !errors.Is(err, errNoAWEStatus) {
		t.Fatalf("rollOut with no AWE status = %v, want %v", err, errNoAWEStatus)
	}
}

func TestAWEStatusTracker_AwaitLiveWaitsForTheFirstReport(t *testing.T) {
	cases := []struct {
		name     string
		reportIn time.Duration
		want     error
	}{
		{name: "an AWE reports during the wait", reportIn: 50 * time.Millisecond, want: nil},
		{name: "no AWE reports", reportIn: 0, want: errNoLiveAWE},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := newAWEStatusTracker(nil, "ns")
			tr.wait = 300 * time.Millisecond
			tr.listening = tr.now().Add(-aweStatusSettle)
			if c.reportIn > 0 {
				time.AfterFunc(c.reportIn, func() { tr.observe(control.AWEStatus{ProcessID: "A"}) })
			}
			if err := tr.awaitLive(t.Context()); !errors.Is(err, c.want) {
				t.Fatalf("awaitLive = %v, want %v", err, c.want)
			}
		})
	}
}

func TestAWEAnswerSize_ReportsOnlyTheCrossingsOfTheLimit(t *testing.T) {
	var a aweAnswerSize
	steps := []struct {
		bytes       int
		crossed     bool
		back        bool
		wantTooLong int64
	}{
		{bytes: 10, crossed: false, back: false, wantTooLong: 0},
		{bytes: broker.MaxMessageSize + 1, crossed: true, back: false, wantTooLong: broker.MaxMessageSize + 1},
		{bytes: broker.MaxMessageSize + 2, crossed: false, back: false, wantTooLong: broker.MaxMessageSize + 2},
		{bytes: 10, crossed: false, back: true, wantTooLong: 0},
		{bytes: 10, crossed: false, back: false, wantTooLong: 0},
	}
	for i, s := range steps {
		crossed, back := a.record(s.bytes)
		if crossed != s.crossed || back != s.back || a.tooLarge() != s.wantTooLong {
			t.Fatalf("step %d (%d bytes): crossed %v back %v tooLarge %d, want %v %v %d", i, s.bytes, crossed, back, a.tooLarge(), s.crossed, s.back, s.wantTooLong)
		}
	}
}

func TestAWEStatusTracker_ADeployTimeoutNamesAnAnswerOverTheSizeLimit(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect the broker: %v", err)
	}
	tr := newAWEStatusTracker(brk, "ns")
	tr.wait = 100 * time.Millisecond
	tr.answer = &aweAnswerSize{}
	tr.answer.record(broker.MaxMessageSize + 1)

	err := tr.rollOut(t.Context(), "agent_a", "d1")

	if err == nil || !strings.Contains(err.Error(), "message limit") {
		t.Fatalf("rollOut = %v, want the timeout to name the agent list over the broker's message limit", err)
	}
}
