// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const budgetTestAgent = "Agent"

var budgetResetsAt = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)

type budgetRefusalFixture struct {
	worker  *Worker
	adapter *EmailAdapter
	replier *fakeReplier
	refused *countingRefusals
	brk     broker.Broker
	agentQ  broker.Queue
}

func newBudgetRefusalFixture(t *testing.T) budgetRefusalFixture {
	t.Helper()
	return newBudgetRefusalFixtureWith(t, func(t testing.TB, gate *budget.Gate) { budgettest.BlockMesh(t, gate, budgetResetsAt) })
}

func newBudgetRefusalFixtureWith(t *testing.T, prepare func(testing.TB, *budget.Gate)) budgetRefusalFixture {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	prepare(t, core.BudgetGate())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: budgetTestAgent})
	core.SetScopeResolver(platformUserScopeResolver{scopes: []string{rbac.AgentInvokeScope(budgetTestAgent)}})

	agentQ := budgettest.TapAgentRequests(t, brk, "test-ns", budgetTestAgent)

	rep := newFakeReplier()
	adapter := &EmailAdapter{cfg: &EmailConfig{DefaultAgentName: budgetTestAgent}, logger: logger, core: core, replier: rep}
	refused := &countingRefusals{}
	w := &Worker{
		Resolver:           NewEmailIdentityResolver(logger, false),
		Policy:             &AuthPolicy{Mode: ModeLogOnly, Logger: logger},
		TrustedAuthservIDs: []string{"protection.outlook.com"},
		Dispatcher:         &gatewayDispatcher{adapter: adapter},
		Refusals:           refused,
		Logger:             logger,
	}
	return budgetRefusalFixture{worker: w, adapter: adapter, replier: rep, refused: refused, brk: brk, agentQ: agentQ}
}

func TestEmailRefusedForASpentMeshBudgetIsDroppedNotRetried(t *testing.T) {
	f := newBudgetRefusalFixture(t)

	got := f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})

	if got != HandleDropped {
		t.Fatalf("disposition = %v, want HandleDropped so the mail is not replayed every poll", got)
	}
	if len(f.refused.reasons) != 1 || f.refused.reasons[0] != taxonomy.RejectBudgetExhausted {
		t.Errorf("reported refusals = %v, want one %q", f.refused.reasons, taxonomy.RejectBudgetExhausted)
	}
	budgettest.AssertNotDelivered(t, f.brk, f.agentQ)
}

func TestEmailRefusedForASpentMeshBudgetBouncesTheSenderWithTheResetTime(t *testing.T) {
	f := newBudgetRefusalFixture(t)

	f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})

	if len(f.replier.calls) != 1 {
		t.Fatalf("bounces sent = %d, want 1", len(f.replier.calls))
	}
	bounce := f.replier.calls[0]
	if bounce.To != "alice@vendor.com" {
		t.Errorf("bounce To = %q, want the sender alice@vendor.com", bounce.To)
	}
	if !bounce.AutoReply {
		t.Error("the bounce is not marked as an auto-reply, so another auto-responder may answer it")
	}
	if bounce.OriginalMessageID != "<basic-text-001@vendor.com>" {
		t.Errorf("bounce OriginalMessageID = %q, want it threaded on the refused mail", bounce.OriginalMessageID)
	}
	if want := entrypoint.BudgetExhaustedText(budgettest.MeshExhausted(budgetResetsAt)); bounce.BodyText != want {
		t.Errorf("bounce body = %q, want %q", bounce.BodyText, want)
	}
	if !strings.Contains(bounce.BodyText, "1 November 2099") {
		t.Errorf("bounce body = %q, want it to name the reset date", bounce.BodyText)
	}
}

func TestEmailBudgetRefusalLeavesNoTaskBehind(t *testing.T) {
	f := newBudgetRefusalFixture(t)

	f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})

	f.adapter.taskMeta.Range(func(taskID, _ any) bool {
		t.Errorf("task metadata for %v survived the refusal", taskID)
		return true
	})
}

func TestEmailBudgetRefusalStillDropsWhenTheBounceCannotBeSent(t *testing.T) {
	f := newBudgetRefusalFixture(t)
	f.replier.sendErr = context.DeadlineExceeded

	got := f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})

	if got != HandleDropped {
		t.Errorf("disposition = %v, want HandleDropped even when the bounce fails", got)
	}
}

func TestEmailBudgetRefusalSendsNoBounceToAnAutoSubmittedMessage(t *testing.T) {
	f := newBudgetRefusalFixture(t)
	autoReply := append([]byte("Auto-Submitted: auto-replied\r\n"), mustReadFixture(t, "synthetic/basic_text.eml")...)

	got := f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: autoReply})

	if got != HandleDropped {
		t.Errorf("disposition = %v, want HandleDropped", got)
	}
	if len(f.replier.calls) != 0 {
		t.Errorf("bounces sent = %d, want none: bouncing an auto-responder loops until the budget resets", len(f.replier.calls))
	}
}

func TestEmailBudgetRefusalSendsNoBounceToMailingListMail(t *testing.T) {
	for name, header := range map[string]string{
		"a list precedence": "Precedence: list\r\n",
		"a bulk precedence": "Precedence: bulk\r\n",
		"a list id":         "List-Id: <team.lists.vendor.com>\r\n",
		"an unsubscribe":    "List-Unsubscribe: <mailto:leave@vendor.com>\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newBudgetRefusalFixture(t)
			listMail := append([]byte(header), mustReadFixture(t, "synthetic/basic_text.eml")...)

			f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: listMail})

			if len(f.replier.calls) != 0 {
				t.Errorf("bounces sent = %d, want none to mailing-list mail", len(f.replier.calls))
			}
		})
	}
}

func TestEmailRefusedForAnUnconfirmedBudgetStateIsRetriedNotBounced(t *testing.T) {
	f := newBudgetRefusalFixtureWith(t, budgettest.Unconfirm)

	got := f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})

	if got != HandleRetry {
		t.Errorf("disposition = %v, want HandleRetry: the state clears once the platform answers", got)
	}
	if len(f.replier.calls) != 0 {
		t.Errorf("bounces sent = %d, want none for a transient refusal", len(f.replier.calls))
	}
}

func TestIsAutomatedMail_RecognizesDeliveryReportsWithoutAutoSubmitted(t *testing.T) {
	for name, tc := range map[string]struct {
		headers   map[string][]string
		automated bool
	}{
		"a null sender":             {map[string][]string{"Return-Path": {"<>"}}, true},
		"a delivery status report":  {map[string][]string{"Content-Type": {`multipart/report; report-type=delivery-status; boundary="b"`}}, true},
		"the mailer daemon":         {map[string][]string{"From": {"Mail Delivery System <MAILER-DAEMON@mx.vendor.com>"}}, true},
		"the postmaster":            {map[string][]string{"From": {"postmaster@vendor.com"}}, true},
		"an auto-reply precedence":  {map[string][]string{"Precedence": {"auto_reply"}}, true},
		"an x-autoreply header":     {map[string][]string{"X-Autoreply": {"yes"}}, true},
		"a graph bounce":            {map[string][]string{"X-Auto-Response-Suppress": {"All"}}, true},
		"an auto-reply suppressor":  {map[string][]string{"X-Auto-Response-Suppress": {"DR, AutoReply"}}, true},
		"a read-receipt suppressor": {map[string][]string{"X-Auto-Response-Suppress": {"RN, NRN"}}, false},
		"a person":                  {map[string][]string{"From": {"Alice <alice@vendor.com>"}, "Return-Path": {"<alice@vendor.com>"}, "Content-Type": {"text/plain"}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isAutomatedMail(tc.headers); got != tc.automated {
				t.Errorf("isAutomatedMail = %v, want %v", got, tc.automated)
			}
		})
	}
}

func TestEmailBudgetRefusalSendsNoBounceToANullSender(t *testing.T) {
	f := newBudgetRefusalFixture(t)
	report := append([]byte("Return-Path: <>\r\n"), mustReadFixture(t, "synthetic/basic_text.eml")...)

	f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: report})

	if len(f.replier.calls) != 0 {
		t.Errorf("bounces sent = %d, want none: answering a delivery report bounces our own bounce back", len(f.replier.calls))
	}
}

func TestEmailBudgetRefusalSendsNoBounceToItsOwnMailbox(t *testing.T) {
	f := newBudgetRefusalFixture(t)
	f.adapter.cfg.Mailbox = "Alice@Vendor.com"

	f.worker.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})

	if len(f.replier.calls) != 0 {
		t.Errorf("bounces sent = %d, want none: a bounce to the gateway's own mailbox comes straight back", len(f.replier.calls))
	}
}
