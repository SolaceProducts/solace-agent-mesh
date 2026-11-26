// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

var ackReasons = []string{"retries_exhausted", "unauthorized", "stalled", "declined", "ignored", "undecodable"}

func requireAckedWithoutTask(t *testing.T, metrics *countingMetrics, gatewayID, want string) {
	t.Helper()
	counted := map[string]int64{}
	for _, reason := range ackReasons {
		if n := metrics.count(metricAckedWithoutTask, "gatewayId", gatewayID, "reason", reason); n != 0 {
			counted[reason] = n
		}
	}
	if len(counted) != 1 || counted[want] != 1 {
		t.Errorf("acked without a task = %v, want one under reason=%s", counted, want)
	}
}

func countMetrics(a *Adapter) *countingMetrics {
	metrics := &countingMetrics{}
	a.metrics = metrics
	return metrics
}

func runUntilAcked(t *testing.T, a *Adapter, job func(attempt int) ingressJob, limit int) int {
	t.Helper()
	for attempt := 1; attempt <= limit; attempt++ {
		if got := runIngressJob(t, a, job(attempt)); got.acked {
			return attempt
		}
	}
	t.Fatalf("still redelivering after %d attempts", limit)
	return 0
}

func messageJobs(key, text string) func(int) ingressJob {
	return func(attempt int) ingressJob { return dmJob(attempt, key, text) }
}

func TestIngress_SpentRefusalIsCountedAsUnauthorized(t *testing.T) {
	a := adapterWithRunningCore(t, newFakeChat(), matchedAuthorizer())
	metrics := countMetrics(a)

	if n := runUntilAcked(t, a, messageJobs("spaces/DM1/messages/refused", "@nosuch hello"), maxTransientAttempts); n != maxAuthzDenialAttempts {
		t.Errorf("acked after %d attempts, want the %d-attempt refusal budget", n, maxAuthzDenialAttempts)
	}
	requireAckedWithoutTask(t, metrics, a.gatewayID, "unauthorized")
}

func TestIngress_SpentStallIsCountedAsStalled(t *testing.T) {
	a := adapterWithAuthorizer(t, panickingAuthorizer{})
	metrics := countMetrics(a)

	if n := runUntilAcked(t, a, messageJobs("spaces/DM1/messages/poison", "hello"), maxTransientAttempts); n != stalledRetryBudget {
		t.Errorf("acked after %d attempts, want the %d-attempt stall budget", n, stalledRetryBudget)
	}
	requireAckedWithoutTask(t, metrics, a.gatewayID, "stalled")
}

func TestIngress_SpentAuthzOutageIsCountedAsRetriesExhausted(t *testing.T) {
	a := adapterWithAuthorizer(t, &stubAuthorizer{err: errors.New("identity service down")})
	metrics := countMetrics(a)

	if n := runUntilAcked(t, a, messageJobs("spaces/DM1/messages/outage", "hello"), maxTransientAttempts); n != maxTransientAttempts {
		t.Errorf("acked after %d attempts, want the %d-attempt ceiling", n, maxTransientAttempts)
	}
	requireAckedWithoutTask(t, metrics, a.gatewayID, "retries_exhausted")
}

func TestIngress_RefusalAtTheCeilingIsCountedAsRetriesExhausted(t *testing.T) {
	a := adapterWithRunningCore(t, newFakeChat(), matchedAuthorizer())
	metrics := countMetrics(a)
	job := dmJob(maxTransientAttempts, "spaces/DM1/messages/ceiling", "@nosuch hello")
	_, transient := retryPolicy(outcomeTransient)
	spendAttempts(t, a, job.ev.EventKey, transient, maxTransientAttempts-1)

	if got := runIngressJob(t, a, job); !got.acked {
		t.Fatalf("last attempt settled %+v, want acked at the ceiling", got)
	}
	requireAckedWithoutTask(t, metrics, a.gatewayID, "retries_exhausted")
}

func TestClickGiveUpIsCountedByWhatRanOut(t *testing.T) {
	for name, tc := range map[string]struct {
		build      func(t *testing.T) (*Adapter, *inboundEvent)
		wantTries  int
		wantReason string
	}{
		"refused": {
			build: func(t *testing.T) (*Adapter, *inboundEvent) {
				rig := newClickRig(t)
				chat := newFakeChat()
				unscoped := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
				a := rig.replica(t, store.TestDB(t), chat, unscoped)
				a.core.SetAuthzEnabled(true)
				submitForTest(t, a, tid("task-z"), dmMeta())
				return a, clickBy(dmMeta(), requesterName, postCard(t, chat, dmMeta()), hilClick(actionHILSubmit, tid("task-z")))
			},
			wantTries:  maxAuthzDenialAttempts,
			wantReason: "unauthorized",
		},
		"crashing": {
			build: func(t *testing.T) (*Adapter, *inboundEvent) {
				a := adapterWithAuthorizer(t, panickingAuthorizer{})
				return a, clickBy(dmMeta(), requesterName, dmMeta().SpaceName+"/messages/card", cancelClick(tid("task-p")))
			},
			wantTries:  stalledRetryBudget,
			wantReason: "stalled",
		},
		"identity service down": {
			build: func(t *testing.T) (*Adapter, *inboundEvent) {
				a := adapterWithAuthorizer(t, &stubAuthorizer{err: errors.New("identity service down")})
				return a, clickBy(dmMeta(), requesterName, dmMeta().SpaceName+"/messages/card", cancelClick(tid("task-d")))
			},
			wantTries:  maxClickAttempts,
			wantReason: "retries_exhausted",
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, ev := tc.build(t)
			metrics := countMetrics(a)
			ev.EventKey = "click|" + ev.MessageName + "|" + ev.Action[actionMethodParam] + "|" + name
			job := func(n int) ingressJob {
				return ingressJob{msg: pulledMessage{AckID: name + "#" + strconv.Itoa(n), MessageID: name}, ev: *ev}
			}

			if n := runUntilAcked(t, a, job, maxClickAttempts); n != tc.wantTries {
				t.Errorf("given up after %d attempts, want %d", n, tc.wantTries)
			}
			requireAckedWithoutTask(t, metrics, a.gatewayID, tc.wantReason)
		})
	}
}

type releaseRefusingClaims struct{ eventClaims }

func (releaseRefusingClaims) ReleaseForRetry(context.Context, string, string, int64, string) (bool, error) {
	return false, errors.New("claim store unavailable")
}

func TestClickRefusedAtTheCeilingIsAnsweredWithTheRefusal(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	unscoped := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := rig.replica(t, store.TestDB(t), chat, unscoped)
	a.core.SetAuthzEnabled(true)
	submitForTest(t, a, tid("task-c"), dmMeta())
	ev := clickBy(dmMeta(), requesterName, postCard(t, chat, dmMeta()), hilClick(actionHILSubmit, tid("task-c")))
	ev.EventKey = "click|" + ev.MessageName + "|" + ev.Action[actionMethodParam] + "|ceiling"
	_, unavailable := retryPolicy(outcomeAuthzUnavailable)
	spendAttempts(t, a, ev.EventKey, unavailable, maxClickAttempts-1)
	a.claims = releaseRefusingClaims{a.claims}
	metrics := countMetrics(a)
	logs := recordLogs(a)
	_, before := chat.snapshot()

	if got := runIngressJob(t, a, ingressJob{msg: pulledMessage{AckID: "ceiling#last", MessageID: "ceiling"}, ev: *ev}); !got.acked {
		t.Fatalf("settled %+v, want given up", got)
	}
	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return len(texts) > len(before) })
	if notice := texts[len(texts)-1]; notice == msgClickFailedLater || notice == msgClickFailed {
		t.Errorf("notice = %q, want the refusal", notice)
	}
	requireAckedWithoutTask(t, metrics, a.gatewayID, "retries_exhausted")
	if logs.count(slog.LevelError, "google chat card click refused after retries") != 1 {
		t.Error("the refusal at the ceiling was not logged at ERROR, as the loss it is counted as")
	}
}
