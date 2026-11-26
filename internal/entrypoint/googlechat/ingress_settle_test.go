// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeSub struct {
	mu    sync.Mutex
	acks  []string
	nacks []fakeNack
}

type fakeNack struct {
	ackID string
	delay time.Duration
}

func (f *fakeSub) Pull(ctx context.Context, _ int) ([]pulledMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeSub) Ack(_ context.Context, ackIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, ackIDs...)
	return nil
}

func (f *fakeSub) Extend(context.Context, []string, time.Duration) error { return nil }

func (f *fakeSub) Nack(_ context.Context, ackIDs []string, delay time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ackIDs {
		f.nacks = append(f.nacks, fakeNack{ackID: id, delay: delay})
	}
	return nil
}

type settledOnce struct {
	acked  bool
	nacked bool
	delay  time.Duration
}

func runIngressJob(t *testing.T, a *Adapter, job ingressJob) settledOnce {
	t.Helper()
	sub := &fakeSub{}
	a.sub = sub
	jobs := make(chan ingressJob, 1)
	jobs <- job
	close(jobs)
	a.ingressWorker(t.Context(), jobs)

	sub.mu.Lock()
	defer sub.mu.Unlock()
	var got settledOnce
	for _, id := range sub.acks {
		if id == job.msg.AckID {
			got.acked = true
		}
	}
	for _, n := range sub.nacks {
		if n.ackID == job.msg.AckID {
			got.nacked, got.delay = true, n.delay
		}
	}
	if got.acked == got.nacked {
		t.Fatalf("message %s settled acked=%v nacked=%v, want exactly one", job.msg.AckID, got.acked, got.nacked)
	}
	return got
}

func dmJob(attempt int, key, text string) ingressJob {
	return ingressJob{
		msg: pulledMessage{AckID: fmt.Sprintf("%s#%d", key, attempt), MessageID: key},
		ev: inboundEvent{
			Kind: kindMessage, Addressed: true, IsDM: true, EventKey: key,
			SpaceName: "spaces/DM1", ThreadName: "spaces/DM1/threads/T1",
			SenderName: "users/1", SenderEmail: "ada@example.com", Text: text,
		},
	}
}

func matchedAuthorizer() *stubAuthorizer {
	return &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched, Scopes: []string{"*:*:*"}}}
}

type failingClaims struct{ err error }

func (f failingClaims) Claim(context.Context, string, string, time.Duration) (store.DedupClaim, error) {
	return store.DedupClaim{}, f.err
}

func (f failingClaims) MarkDone(context.Context, string, string, int64) (bool, error) {
	return false, f.err
}

func (f failingClaims) ReleaseForRetry(context.Context, string, string, int64, string) (bool, error) {
	return false, f.err
}

type panickingAuthorizer struct{}

func (panickingAuthorizer) AuthorizeSender(context.Context, auth.SenderAuthorizationRequest) (auth.SenderAuthorization, error) {
	panic("identity lookup exploded")
}

func adapterWithRunningCore(t *testing.T, chat *fakeChat, authorizer auth.SenderAuthorizer) *Adapter {
	t.Helper()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": a.gatewayID})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	core.SetIdentityAuthorizer(authorizer)
	a.core = core
	return a
}

func TestIngress_InFlightClaimIsNackedNotAcked(t *testing.T) {
	authorizer := matchedAuthorizer()
	a := adapterWithAuthorizer(t, authorizer)
	job := dmJob(1, "spaces/DM1/messages/inflight", "hello")
	if c, err := a.claims.Claim(t.Context(), a.gatewayID, job.ev.EventKey, claimLease); err != nil || c.Outcome != store.ClaimAcquired {
		t.Fatalf("pre-claim = %+v, %v", c, err)
	}

	got := runIngressJob(t, a, job)
	if !got.nacked || got.delay <= 0 {
		t.Errorf("in-flight message settled %+v, want a nack with a redelivery delay", got)
	}
	if authorizer.calls != 0 {
		t.Errorf("identity lookups = %d, want none for a message another replica holds", authorizer.calls)
	}
}

func TestIngress_ClaimStoreErrorIsNackedNotAcked(t *testing.T) {
	authorizer := matchedAuthorizer()
	a := adapterWithAuthorizer(t, authorizer)
	a.claims = failingClaims{err: errors.New("database unavailable")}

	got := runIngressJob(t, a, dmJob(1, "spaces/DM1/messages/claimerr", "hello"))
	if !got.nacked || got.delay <= 0 {
		t.Errorf("claim-store failure settled %+v, want a nack with a redelivery delay", got)
	}
	if authorizer.calls != 0 {
		t.Errorf("identity lookups = %d, want none without a claim", authorizer.calls)
	}
}

func TestIngress_UnauthorizedRetriesThenSendsRefusalOnce(t *testing.T) {
	chat := newFakeChat()
	a := adapterWithRunningCore(t, chat, matchedAuthorizer())
	const key = "spaces/DM1/messages/unauthorized"

	for attempt := 1; attempt < maxAuthzDenialAttempts; attempt++ {
		got := runIngressJob(t, a, dmJob(attempt, key, "@nosuch hello"))
		if !got.nacked || got.delay <= 0 {
			t.Fatalf("attempt %d settled %+v, want a nack with a redelivery delay", attempt, got)
		}
	}
	if got := runIngressJob(t, a, dmJob(maxAuthzDenialAttempts, key, "@nosuch hello")); !got.acked {
		t.Fatalf("attempt %d settled %+v, want ack once the budget is spent", maxAuthzDenialAttempts, got)
	}

	const refusal = `No agent named "nosuch" is available to you.`
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, refusal) })
	a.bgWG.Wait()
	_, texts := chat.snapshot()
	if len(texts) != 1 {
		t.Errorf("messages = %q, want exactly one refusal", texts)
	}
	if anyTextContains(texts, msgTemporaryFailure) {
		t.Errorf("messages = %q, want the refusal copy, not the generic failure", texts)
	}
	if !strings.HasPrefix(texts[0], refusal) {
		t.Errorf("refusal = %q, want it to start %q", texts[0], refusal)
	}
}

func TestIngress_PanicIsRecoveredAndReleasedAsStalled(t *testing.T) {
	a := adapterWithAuthorizer(t, panickingAuthorizer{})
	job := dmJob(1, "spaces/DM1/messages/panic", "hello")

	got := runIngressJob(t, a, job)
	if !got.nacked || got.delay <= 0 {
		t.Errorf("panicking event settled %+v, want a nack with a redelivery delay", got)
	}
	claim, err := a.claims.Claim(t.Context(), a.gatewayID, job.ev.EventKey, claimLease)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Outcome != store.ClaimAcquired || claim.FailureKind != store.StalledClaimFailure || claim.Attempts != 1 {
		t.Errorf("claim after the panic = %+v, want it released for retry as %q with one attempt", claim, store.StalledClaimFailure)
	}
}

func TestIngress_BotAuthoredMessageIsIgnored(t *testing.T) {
	for name, userType := range map[string]string{"user human": `"type":"HUMAN",`, "user type absent": ""} {
		t.Run(name, func(t *testing.T) {
			payload := []byte(`{"type":"MESSAGE",
				"space":{"name":"spaces/DM1","type":"DM","spaceType":"DIRECT_MESSAGE","singleUserBotDm":true},
				"user":{"name":"users/1",` + userType + `"email":"ada@example.com"},
				"message":{"name":"spaces/DM1/messages/bot-1","text":"hello",
					"sender":{"name":"users/bot","type":"BOT"},
					"thread":{"name":"spaces/DM1/threads/T1"}}}`)
			ev, err := parseEvent(payload)
			if err != nil {
				t.Fatal(err)
			}
			if ev.Kind != kindIgnored || ev.IgnoreReason != "sender_is_bot" {
				t.Errorf("parsed kind=%v reason=%q, want ignored as sender_is_bot", ev.Kind, ev.IgnoreReason)
			}

			authorizer := matchedAuthorizer()
			a := adapterWithAuthorizer(t, authorizer)
			got := runIngressJob(t, a, ingressJob{msg: pulledMessage{AckID: "bot#1", MessageID: "bot", Data: payload}, ev: ev})
			if !got.acked {
				t.Errorf("bot message settled %+v, want ack", got)
			}
			if authorizer.calls != 0 {
				t.Errorf("identity lookups = %d, want none for a bot's message", authorizer.calls)
			}
			if state, _ := a.claims.(*store.GoogleChatProcessedRepo).State(t.Context(), a.gatewayID, "spaces/DM1/messages/bot-1"); state != "" {
				t.Errorf("claim state = %q, want no claim for an ignored event", state)
			}
		})
	}
}
