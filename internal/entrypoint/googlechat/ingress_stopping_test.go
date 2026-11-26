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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type stoppingAuthorizer struct{ stop context.CancelFunc }

func (s stoppingAuthorizer) AuthorizeSender(context.Context, auth.SenderAuthorizationRequest) (auth.SenderAuthorization, error) {
	s.stop()
	return auth.SenderAuthorization{}, errors.New("identity service unreachable")
}

func spendAttempts(t *testing.T, a *Adapter, key, kind string, n int) {
	t.Helper()
	for i := range n {
		c, err := a.claims.Claim(t.Context(), a.gatewayID, key, claimLease)
		if err != nil || c.Outcome != store.ClaimAcquired {
			t.Fatalf("claim %d = %+v, %v", i+1, c, err)
		}
		if _, err := a.claims.ReleaseForRetry(t.Context(), a.gatewayID, key, c.Token, kind); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeliver_StoppingReplicaHandsTheEventBackUntold(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	chat := newFakeChat()
	a := adapterWithAuthorizer(t, stoppingAuthorizer{stop: stop})
	a.writer = newChatWriter(chat, newSpaceThrottle(0), a.logger)
	job := dmJob(1, "spaces/DM1/messages/stopping", "hello")
	spendAttempts(t, a, job.ev.EventKey, "transient", maxTransientAttempts-1)

	s := a.deliver(ctx, job)
	if s.ack || s.delay != 0 {
		t.Errorf("settlement on a stopping replica = %+v, want an immediate nack", s)
	}
	a.bgWG.Wait()
	if _, texts := chat.snapshot(); len(texts) != 0 {
		t.Errorf("messages = %q, want the sender told nothing", texts)
	}
	c, err := a.claims.Claim(t.Context(), a.gatewayID, job.ev.EventKey, claimLease)
	if err != nil || c.Outcome != store.ClaimAcquired {
		t.Errorf("claim after the stop = %+v, %v, want it released for the next replica", c, err)
	}
}

type releaseOverride struct {
	eventClaims
	released bool
	err      error
}

func (r releaseOverride) ReleaseForRetry(context.Context, string, string, int64, string) (bool, error) {
	return r.released, r.err
}

func TestClickGiveUpOnlyInvitesAPressItCouldReopen(t *testing.T) {
	for name, tc := range map[string]struct {
		release    releaseOverride
		wantNotice string
		wantLog    string
	}{
		"release failed": {
			release:    releaseOverride{err: errors.New("claims unreachable")},
			wantNotice: msgClickFailedLater,
			wantLog:    `level=ERROR msg="google chat card click given up after retries, and the click could not be reopened"`,
		},
		"taken over": {
			release: releaseOverride{},
			wantLog: `level=DEBUG msg="google chat card click give-up skipped: another replica took the click over"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, chat, logs, taskID := feedbackReplica(t)
			if _, err := a.core.Store().ExecContext(t.Context(), `ALTER TABLE feedback RENAME TO feedback_hidden`); err != nil {
				t.Fatal(err)
			}
			ev := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, nil)
			ev.Action[paramRequestID] = "req-submit"
			ev.EventKey = "click|" + ev.MessageName + "|" + actionFeedbackDetails + "|req-submit|" + requesterName
			_, kind := retryPolicy(outcomeTransient)
			spendAttempts(t, a, ev.EventKey, kind, maxClickAttempts-1)
			tc.release.eventClaims = a.claims
			a.claims = tc.release

			if s := a.deliver(t.Context(), ingressJob{msg: pulledMessage{AckID: "fb#last", MessageID: "fb"}, ev: *ev}); !s.ack {
				t.Fatalf("settlement = %+v, want the click given up", s)
			}
			a.bgWG.Wait()
			_, texts := chat.snapshot()
			switch {
			case tc.wantNotice == "" && len(texts) != 0:
				t.Errorf("messages = %q, want none", texts)
			case tc.wantNotice != "" && !anyTextContains(texts, tc.wantNotice):
				t.Errorf("messages = %q, want %q", texts, tc.wantNotice)
			}
			if anyTextContains(texts, msgFeedbackNotRecorded) {
				t.Errorf("messages = %q, the user was asked to click again on a click that cannot be reopened", texts)
			}
			if !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("logs lack %s:\n%s", tc.wantLog, logs.String())
			}
		})
	}
}

func TestClickGiveUpLetsTheUserPressAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		action     map[string]string
		wantNotice string
	}{
		"cancel": {action: cancelClick(tid("task-r")), wantNotice: msgClickFailed},
		"answer": {action: hilClick(actionHILSubmit, tid("task-r")), wantNotice: msgClickFailed},
	} {
		t.Run(name, func(t *testing.T) {
			a, chat, logs, _ := feedbackReplica(t)
			a.items = &flakyItems{taskItemStore: a.items, listFailures: maxClickAttempts}
			ev := clickBy(dmMeta(), requesterName, dmMeta().SpaceName+"/messages/card", tc.action)
			ev.EventKey = "click|" + ev.MessageName + "|" + tc.action[actionMethodParam] + "|" + tc.action[paramRequestID] + "|" + requesterName
			job := func(n int) ingressJob {
				return ingressJob{msg: pulledMessage{AckID: name + "#" + strconv.Itoa(n), MessageID: name}, ev: *ev}
			}
			for attempt := 1; attempt < maxClickAttempts; attempt++ {
				if got := runIngressJob(t, a, job(attempt)); !got.nacked {
					t.Fatalf("attempt %d settled %+v, want held for retry", attempt, got)
				}
			}
			if got := runIngressJob(t, a, job(maxClickAttempts)); !got.acked {
				t.Fatalf("last attempt settled %+v, want given up", got)
			}
			chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, tc.wantNotice) })
			if !strings.Contains(logs.String(), `level=ERROR msg="google chat card click given up after retries; the user is asked to try again"`) {
				t.Error("the give-up was not logged at ERROR")
			}

			if got := runIngressJob(t, a, job(maxClickAttempts+1)); !got.acked {
				t.Fatalf("the next press settled %+v, want it processed", got)
			}
			chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgRequestNotFound) })
		})
	}
}

func TestRefusedClickGiveUpKeepsTheRefusal(t *testing.T) {
	for name, release := range map[string]*releaseOverride{"reopened": nil, "not reopened": {err: errors.New("claims unreachable")}} {
		t.Run(name, func(t *testing.T) {
			rig := newClickRig(t)
			chat := newFakeChat()
			unscoped := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
			a := rig.replica(t, store.TestDB(t), chat, unscoped)
			a.core.SetAuthzEnabled(true)
			logs := recordLogs(a)
			submitForTest(t, a, tid("task-z"), dmMeta())
			card := postCard(t, chat, dmMeta())
			ev := clickBy(dmMeta(), requesterName, card, hilClick(actionHILSubmit, tid("task-z")))
			ev.EventKey = "click|" + card + "|" + actionHILSubmit + "|req-1|" + requesterName
			_, kind := retryPolicy(outcomeUnauthorized)
			spendAttempts(t, a, ev.EventKey, kind, maxAuthzDenialAttempts-1)
			if release != nil {
				release.eventClaims = a.claims
				a.claims = release
			}
			_, before := chat.snapshot()

			if s := a.deliver(t.Context(), ingressJob{msg: pulledMessage{AckID: "z#last", MessageID: "z"}, ev: *ev}); !s.ack {
				t.Fatalf("settlement = %+v, want the click given up", s)
			}
			a.bgWG.Wait()
			_, texts := chat.snapshot()
			notices := texts[len(before):]
			if len(notices) != 1 || notices[0] == msgClickFailed || notices[0] == msgClickFailedLater || notices[0] == msgHILSubmitFailed {
				t.Errorf("notices = %q, want the refusal wording alone", notices)
			}
			if logs.count(slog.LevelWarn, "google chat card click refused after retries") != 1 {
				t.Error("the refused give-up was not logged at WARN")
			}
			if n := logs.count(slog.LevelError, "google chat card click given up"); n != 0 {
				t.Errorf("refused give-up logged %d ERROR lines, want none", n)
			}
			rig.mustReceiveNothing(t)
		})
	}
}
