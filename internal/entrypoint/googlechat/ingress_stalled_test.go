// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func killConsumer(t *testing.T, a *Adapter, db *store.DB, key string) {
	t.Helper()
	c, err := a.claims.Claim(t.Context(), a.gatewayID, key, claimLease)
	if err != nil || c.Outcome != store.ClaimAcquired {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	lapsed := min(c.Token, time.Now().UnixMilli()) - claimLease.Milliseconds() - 1
	if _, err := db.ExecContext(t.Context(),
		`UPDATE googlechat_processed_events SET claimed_at = ? WHERE gateway_id = ? AND event_key = ?`,
		lapsed, a.gatewayID, key); err != nil {
		t.Fatalf("lapse the claim's lease: %v", err)
	}
}

func killedConsumerRig(t *testing.T) (*Adapter, *store.DB, *fakeChat, *stubAuthorizer) {
	t.Helper()
	db := store.TestDB(t)
	chat := newFakeChat()
	a := newTestAdapter(t, db, chat)
	a.core = entrypoint.NewGatewayCore(a, nil)
	authorizer := &stubAuthorizer{err: errors.New("identity service down")}
	a.core.SetIdentityAuthorizer(authorizer)
	return a, db, chat, authorizer
}

func TestIngress_EventThatKeepsKillingItsConsumerIsDeclinedUnprocessed(t *testing.T) {
	a, db, chat, authorizer := killedConsumerRig(t)
	metrics := countMetrics(a)
	job := dmJob(stalledRetryBudget+1, "spaces/DM1/messages/oom", "hello")
	for range stalledRetryBudget {
		killConsumer(t, a, db, job.ev.EventKey)
	}

	if got := runIngressJob(t, a, job); !got.acked {
		t.Fatalf("settled %+v, want acked", got)
	}
	if authorizer.calls != 0 {
		t.Errorf("identity lookups = %d, want the event declined before processing", authorizer.calls)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgTemporaryFailure) })
	requireAckedWithoutTask(t, metrics, a.gatewayID, "stalled")
	if c, err := a.claims.Claim(t.Context(), a.gatewayID, job.ev.EventKey, claimLease); err != nil || c.Outcome != store.ClaimDuplicate {
		t.Errorf("claim after the decline = %+v, %v, want it done so a redelivery is dropped", c, err)
	}
}

func TestIngress_EventWithinItsStallBudgetIsProcessed(t *testing.T) {
	a, db, _, authorizer := killedConsumerRig(t)
	job := dmJob(stalledRetryBudget, "spaces/DM1/messages/oom-once", "hello")
	for range stalledRetryBudget - 1 {
		killConsumer(t, a, db, job.ev.EventKey)
	}

	if got := runIngressJob(t, a, job); !got.nacked {
		t.Fatalf("settled %+v, want held for retry after processing", got)
	}
	if authorizer.calls != 1 {
		t.Errorf("identity lookups = %d, want the event processed", authorizer.calls)
	}
}

func clickJob(t *testing.T, label string) ingressJob {
	t.Helper()
	ev := clickBy(dmMeta(), requesterName, dmMeta().SpaceName+"/messages/card", cancelClick(tid("task-"+label)))
	ev.EventKey = "click|" + ev.MessageName + "|" + actionCancelTask + "|" + label
	return ingressJob{msg: pulledMessage{AckID: label + "#press", MessageID: label}, ev: *ev}
}

func TestClickThatKeepsKillingItsConsumerIsDeclinedForGood(t *testing.T) {
	a, db, chat, authorizer := killedConsumerRig(t)
	metrics := countMetrics(a)
	job := clickJob(t, "oom")
	for range stalledRetryBudget {
		killConsumer(t, a, db, job.ev.EventKey)
	}

	if got := runIngressJob(t, a, job); !got.acked {
		t.Fatalf("settled %+v, want declined", got)
	}
	if got := runIngressJob(t, a, job); !got.acked {
		t.Fatalf("second press settled %+v, want dropped as a duplicate", got)
	}
	if authorizer.calls != 0 {
		t.Errorf("identity lookups = %d, want the click declined before processing", authorizer.calls)
	}
	writes, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgClickDeclined) })
	if len(writes) != 1 || anyTextContains(texts, msgClickFailed) {
		t.Errorf("messages = %q, want the one decline and no invitation to try again", texts)
	}
	requireAckedWithoutTask(t, metrics, a.gatewayID, "stalled")
}

func TestClickWithinItsStallBudgetIsProcessed(t *testing.T) {
	a, db, _, authorizer := killedConsumerRig(t)
	job := clickJob(t, "oom-short")
	for range stalledRetryBudget - 1 {
		killConsumer(t, a, db, job.ev.EventKey)
	}

	runIngressJob(t, a, job)
	if authorizer.calls != 1 {
		t.Errorf("identity lookups = %d, want the click processed", authorizer.calls)
	}
}

func TestClickThatDiedOnceAtItsCeilingIsProcessed(t *testing.T) {
	a, db, _, authorizer := killedConsumerRig(t)
	job := clickJob(t, "oom-ceiling")
	_, unavailable := retryPolicy(outcomeAuthzUnavailable)
	spendAttempts(t, a, job.ev.EventKey, unavailable, maxClickAttempts-1)
	killConsumer(t, a, db, job.ev.EventKey)

	runIngressJob(t, a, job)
	if authorizer.calls != 1 {
		t.Errorf("identity lookups = %d, want the click processed", authorizer.calls)
	}
}

func TestIngress_DeathsShortOfTheStallBudgetAtTheCeilingAreCountedAsRetriesExhausted(t *testing.T) {
	a, db, chat, authorizer := killedConsumerRig(t)
	metrics := countMetrics(a)
	logs := recordLogs(a)
	job := dmJob(maxTransientAttempts, "spaces/DM1/messages/oom-ceiling", "hello")
	_, transient := retryPolicy(outcomeTransient)
	deaths := stalledRetryBudget - 1
	spendAttempts(t, a, job.ev.EventKey, transient, maxTransientAttempts-deaths)
	for range deaths {
		killConsumer(t, a, db, job.ev.EventKey)
	}

	if got := runIngressJob(t, a, job); !got.acked {
		t.Fatalf("settled %+v, want declined at the ceiling", got)
	}
	if authorizer.calls != 0 {
		t.Errorf("identity lookups = %d, want the event declined before processing", authorizer.calls)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgTemporaryFailure) })
	requireAckedWithoutTask(t, metrics, a.gatewayID, "retries_exhausted")
	if n := logs.count(slog.LevelError, "google chat event keeps killing its consumer"); n != 0 {
		t.Error("the decline was logged as a payload that keeps killing its consumer")
	}
}
