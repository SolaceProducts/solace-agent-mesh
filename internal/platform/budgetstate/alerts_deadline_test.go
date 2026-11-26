// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type slowPublish struct {
	broker.Broker
	clock *fakeClock
	step  time.Duration
}

func (b *slowPublish) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	b.clock.advance(b.clock.Now().Add(b.step))
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

type cancellingPublish struct {
	broker.Broker
	cancel    context.CancelFunc
	delivered bool
	calls     atomic.Int32
}

func (b *cancellingPublish) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	b.calls.Add(1)
	b.cancel()
	if !b.delivered {
		return ctx.Err()
	}
	return b.Broker.PublishGuaranteed(context.WithoutCancel(ctx), topic, msg)
}

func recipientsOf(reqs []a2a.NotifyRequest) []string {
	out := make([]string, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, req.RecipientPlatformUserID)
	}
	slices.Sort(out)
	return out
}

func (f *fixture) sortedHumans(emails ...string) []string {
	ids := make([]string, 0, len(emails))
	for _, email := range emails {
		ids = append(ids, f.human(email))
	}
	slices.SortFunc(ids, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return ids
}

func TestAlerts_APassThatReachesItsDeadlineSendsTheRestOnTheNextRecomputeAndNothingTwice(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	ids := r.sortedHumans("b@example.com", "c@example.com", "d@example.com", "a@example.com")
	early, late, cutOff, alreadyAlerted := ids[0], ids[1], ids[2], ids[3]
	r.mon.broker = &slowPublish{Broker: r.brk, clock: r.clock, step: alertPassDeadline * 3 / 5}
	r.spendAs(alreadyAlerted, "", testNow, 100)
	r.start(1)
	r.recomputed()
	if got := recipientsOf(r.alerts()); !slices.Equal(got, []string{alreadyAlerted}) {
		t.Fatalf("the term's start alerted %v, want only %s", got, alreadyAlerted)
	}

	for _, id := range []string{early, late, cutOff} {
		r.spendAs(id, "", testNow, 100)
	}
	r.at = r.clock.Now()
	r.recomputeOnSignal()
	if got, want := recipientsOf(r.alerts()), []string{early, late}; !slices.Equal(got, want) {
		t.Fatalf("the pass that ran out of time alerted %v, want the two it started before its deadline %v", got, want)
	}
	if n := len(r.alertStates()); n != 3 {
		t.Fatalf("%d alert states recorded, want the three sent and not the one cut off", n)
	}

	r.at = r.clock.Now()
	r.recomputeOnSignal()
	if got := recipientsOf(r.alerts()); !slices.Equal(got, []string{cutOff}) {
		t.Fatalf("the next recompute alerted %v, want only the crossing the deadline cut off, %s", got, cutOff)
	}
	r.at = r.clock.Now()
	r.recomputeOnSignal()
	r.noAlert("once every crossing is recorded")
}

func TestAlerts_APassWhoseContextEndsStopsWithoutWarning(t *testing.T) {
	for _, tc := range []struct {
		name      string
		delivered bool
	}{
		{"before the publish lands", false},
		{"after the publish lands", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAlertRig(t)
			r.limitUserDefault(100)
			for _, id := range r.sortedHumans("a@example.com", "b@example.com", "c@example.com") {
				r.spendAs(id, "", testNow, 100)
			}
			ctx, cancel := context.WithCancel(t.Context())
			brk := &cancellingPublish{Broker: r.brk, cancel: cancel, delivered: tc.delivered}
			r.mon.broker = brk

			r.mon.alert(ctx, testNow, r.assess())

			if n := brk.calls.Load(); n != 1 {
				t.Errorf("%d alerts started, want the pass to stop after the one its context ended in", n)
			}
			if rows := r.alertStates(); len(rows) != 0 {
				t.Errorf("alert states = %+v, want nothing recorded once the context ended", rows)
			}
			r.logs.mu.Lock()
			logged := r.logs.buf.String()
			r.logs.mu.Unlock()
			if strings.Contains(logged, `"level":"WARN"`) {
				t.Errorf("the pass warned after its context ended:\n%s", logged)
			}

			r.alerts()
			r.mon.broker = r.brk
			r.mon.alert(t.Context(), testNow, r.assess())
			if got := len(r.alerts()); got != 3 {
				t.Errorf("the next pass sent %d alerts, want all three", got)
			}
		})
	}
}

func TestAlerts_APassStartedAfterItsContextEndedDoesNothingAndDoesNotWarn(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	r.spendAs(r.human("alice@example.com"), "", testNow, 100)
	a := r.assess()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	r.mon.alert(ctx, testNow, a)

	r.noAlert("a pass whose context had ended")
	r.logs.mu.Lock()
	logged := r.logs.buf.String()
	r.logs.mu.Unlock()
	if logged != "" {
		t.Errorf("the pass logged after its context ended:\n%s", logged)
	}
}
