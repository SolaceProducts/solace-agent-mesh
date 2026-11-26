// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const adaPlatformRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c45"

var allScopes = []string{"*:*:*"}

func matchedAda(scopes []string) *auth.Subject {
	return auth.ForGoogleChatSender("ada@example.com", "Ada", "users/1", auth.SenderAuthorization{
		Resolution: identity.LookupMatched, ResolvedUserID: adaPlatformRow, Scopes: scopes,
	})
}

func adaDM(text string) *inboundEvent {
	ev := dmJob(1, "spaces/DM1/messages/refusal", text).ev
	return &ev
}

func refusalAdapter(t *testing.T, agents []string, defaultAgent string, authz bool) *Adapter {
	t.Helper()
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	attachCore(t, a)
	a.cfg.DefaultAgentName = defaultAgent
	for _, name := range agents {
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: name})
	}
	a.core.SetAuthzEnabled(authz)
	return a
}

func TestRoutingRefusalsReportTheirReason(t *testing.T) {
	tests := []struct {
		name         string
		agents       []string
		defaultAgent string
		text         string
		authz        bool
		scopes       []string
		wantOutcome  inboundOutcome
		wantReason   string
	}{
		{name: "a misconfigured default agent is an agent that is not found", agents: []string{"agent"}, defaultAgent: "does-not-exist", text: "hello", scopes: allScopes, wantOutcome: outcomeDeclined, wantReason: taxonomy.RejectAgentNotFound},
		{name: "no agent to route to yet is an agent that is not found", defaultAgent: "", text: "hello", scopes: allScopes, wantOutcome: outcomeTransient, wantReason: taxonomy.RejectAgentNotFound},
		{name: "a mention of an unknown agent is an agent that is not found", agents: []string{"agent"}, defaultAgent: "agent", text: "@nobody hello", scopes: allScopes, wantOutcome: outcomeUnauthorized, wantReason: taxonomy.RejectAgentNotFound},
		{name: "a default agent the sender may not invoke is forbidden", agents: []string{"agent"}, defaultAgent: "agent", text: "hello", authz: true, wantOutcome: outcomeUnauthorized, wantReason: taxonomy.RejectForbidden},
		{name: "an accepted message reports no refusal", agents: []string{"agent"}, defaultAgent: "agent", text: "hello", scopes: allScopes, wantOutcome: outcomeSubmitted, wantReason: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := refusalAdapter(t, tc.agents, tc.defaultAgent, tc.authz)

			res, reason := a.trySubmit(adaDM(tc.text), matchedAda(tc.scopes))

			if res.outcome != tc.wantOutcome || reason != tc.wantReason {
				t.Errorf("outcome, reason = %v, %q; want %v, %q", res.outcome, reason, tc.wantOutcome, tc.wantReason)
			}
		})
	}
}

func TestASessionThatCannotBeResolvedIsAnInternalRefusal(t *testing.T) {
	a := refusalAdapter(t, []string{"agent"}, "agent", false)
	a.sessKeys = store.NewGatewaySessionKeyRepo(closedGatewayDB(t))

	res, reason := a.trySubmit(adaDM("hello"), matchedAda(allScopes))

	if res.outcome != outcomeTransient || reason != taxonomy.RejectInternal {
		t.Errorf("outcome, reason = %v, %q; want %v, %q", res.outcome, reason, outcomeTransient, taxonomy.RejectInternal)
	}
}

func TestATaskThatCannotBeRecordedIsAnInternalRefusal(t *testing.T) {
	a := refusalAdapter(t, []string{"agent"}, "agent", false)
	a.items = refusingItems{}

	res, reason := a.trySubmit(adaDM("hello"), matchedAda(allScopes))

	if res.outcome != outcomeTransient || reason != taxonomy.RejectInternal {
		t.Errorf("outcome, reason = %v, %q; want %v, %q", res.outcome, reason, outcomeTransient, taxonomy.RejectInternal)
	}
}

func TestATaskTheBrokerWillNotTakeIsAnInternalRefusal(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	brk := broker.NewDevBroker()
	attachCoreOn(t, a, brk, refusingPublisher{brk})
	readyToSubmit(a)

	res, reason := a.trySubmit(adaDM("hello"), matchedAda(allScopes))

	if res.outcome != outcomeTransient || reason != taxonomy.RejectInternal {
		t.Errorf("outcome, reason = %v, %q; want %v, %q", res.outcome, reason, outcomeTransient, taxonomy.RejectInternal)
	}
}

func TestASubmitTheCoreDeniesIsForbidden(t *testing.T) {
	a := newClickRig(t).replicaWithTaskLog(t, newFakeChat())
	a.items = &afterPutItems{taskItemStore: a.items, after: func() { a.core.SetAuthzEnabled(true) }}
	readyToSubmit(a)

	res, reason := a.trySubmit(adaDM("hello"), matchedAda(nil))

	if res.outcome != outcomeUnauthorized || reason != taxonomy.RejectForbidden {
		t.Errorf("outcome, reason = %v, %q; want %v, %q", res.outcome, reason, outcomeUnauthorized, taxonomy.RejectForbidden)
	}
}

func TestASpentBudgetIsReportedAsBudgetExhausted(t *testing.T) {
	a := refusalAdapter(t, []string{"agent"}, "agent", false)
	blockMeshBudget(t, a.core)

	res, reason := a.trySubmit(adaDM("hello"), matchedAda(allScopes))

	if res.outcome != outcomeDeclined || reason != taxonomy.RejectBudgetExhausted {
		t.Errorf("outcome, reason = %v, %q; want %v, %q", res.outcome, reason, outcomeDeclined, taxonomy.RejectBudgetExhausted)
	}
}

func TestTheGoogleChatAdapterDeclaresTheClientItsRefusalsReport(t *testing.T) {
	a := &Adapter{}
	if got := a.RelayedSurface(); got != httputil.SurfaceGoogleChat {
		t.Fatalf("RelayedSurface = %q, want %q", got, httputil.SurfaceGoogleChat)
	}
	if !slices.Contains(httputil.SubmitterSurfaces, a.RelayedSurface()) {
		t.Errorf("submittedFrom does not accept %q, so a refused Google Chat message would name no client", a.RelayedSurface())
	}
}

func TestASubmittedTaskLogsAUsageLineAtInfoNamingThePersonByPlatformIDNotEmail(t *testing.T) {
	tests := []struct {
		name        string
		ev          inboundEvent
		wantSwapped bool
	}{
		{name: "direct message", ev: dmJob(1, "spaces/DM1/messages/usage", "hello").ev, wantSwapped: false},
		{name: "shared space under run_as from an add-on app", ev: spaceEvent("spaces/S1/messages/usage", "hello", true), wantSwapped: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAdapter(nil)
			a.chatOverride = newFakeChat()
			core, brk := eventCore(t, a, store.TestDBURL(t))
			requests := budgettest.TapAgentRequests(t, brk, clickNamespace, "agent")
			readyToSubmitTo(a, "agent")
			authorizer := matchedAuthorizer()
			authorizer.res.ResolvedUserID = adaPlatformRow
			core.SetIdentityAuthorizer(authorizer)
			logs := &syncBuffer{}
			a.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

			ev := tc.ev
			if res := a.process(t.Context(), &ev); res.outcome != outcomeSubmitted {
				t.Fatalf("outcome = %v, want submitted; logs:\n%s", res.outcome, logs.String())
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err := brk.Receive(ctx, requests); err != nil {
				t.Fatalf("no task reached the agent: %v", err)
			}

			raw := logs.String()
			line := findLogLine(t, raw, "google chat task submitted")
			want := map[string]any{
				"level":          "INFO",
				"resolvedUserId": adaPlatformRow,
				"runAsSwapped":   tc.wantSwapped,
				"isDm":           tc.ev.IsDM,
				"addOn":          tc.ev.AddOn,
				"delivery":       DeliveryPubSub,
				"agentName":      "agent",
			}
			for key, v := range want {
				if line[key] != v {
					t.Errorf("%s = %v, want %v", key, line[key], v)
				}
			}
			for _, key := range []string{"taskID", "traceID"} {
				if v, _ := line[key].(string); v == "" {
					t.Errorf("%s is empty, want it set", key)
				}
			}
			if strings.Contains(raw, "ada@example.com") {
				t.Errorf("a log line carries the sender's email:\n%s", raw)
			}
		})
	}
}

func readyToSubmitTo(a *Adapter, agent string) {
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agent})
	a.cfg.DefaultAgentName = agent
}

func spaceEvent(key, text string, addOn bool) inboundEvent {
	return inboundEvent{
		Kind: kindMessage, Addressed: true, IsDM: false, AddOn: addOn, EventKey: key,
		SpaceName: "spaces/S1", ThreadName: "spaces/S1/threads/T1",
		SenderName: "users/1", SenderEmail: "ada@example.com", Text: text,
	}
}

func findLogLine(t *testing.T, raw, msg string) logLine {
	t.Helper()
	for _, line := range logLines(t, bytes.NewBufferString(raw)) {
		if line["msg"] == msg {
			return line
		}
	}
	t.Fatalf("no %q line in:\n%s", msg, raw)
	return nil
}

func closedGatewayDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return db
}

var errStoreDown = errors.New("task item store is down")

type refusingItems struct{}

func (refusingItems) Put(context.Context, string, string, string) error { return errStoreDown }
func (refusingItems) Delete(context.Context, string, string) error      { return nil }
func (refusingItems) List(context.Context, string) (map[string]string, error) {
	return nil, errStoreDown
}

type refusingPublisher struct{ *broker.DevBroker }

func (refusingPublisher) PublishGuaranteed(context.Context, string, *broker.Message) error {
	return errors.New("broker refused the publish")
}
