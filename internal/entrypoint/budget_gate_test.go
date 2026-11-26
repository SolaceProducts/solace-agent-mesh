// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

var budgetGatePeriodEnd = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)

func budgetTestMessage(id string) a2a.Message {
	return a2a.Message{Kind: "message", MessageID: id, Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
}

func startCoreOnDB(t *testing.T, brk broker.Broker, dbURL string, gate *budget.Gate) *GatewayCore {
	t.Helper()
	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":       "test-ns",
		"gateway_id":      "test-gw",
		"session_service": map[string]any{"type": "sql", "database_url": dbURL},
	})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithBudgetGate(gate)).Services())
	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return core
}

func connectedDevBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	return brk
}

func published(t *testing.T, brk broker.Broker, q broker.Queue) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	msg, _ := brk.Receive(ctx, q)
	return msg != nil
}

func assertBudgetRefusal(t *testing.T, err error) {
	t.Helper()
	exhausted, ok := errors.AsType[*budget.ExhaustedError](err)
	if !ok {
		t.Fatalf("SubmitTask error = %v, want a budget refusal", err)
	}
	if exhausted.Tier != budget.TierMesh || !exhausted.ResetsAt.Equal(budgetGatePeriodEnd) {
		t.Errorf("refusal = %+v, want the mesh resetting at %s", exhausted, budgetGatePeriodEnd)
	}
}

func TestSubmitTask_RefusesWhileTheMeshBudgetIsSpentAndPublishesNothing(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	gate := core.BudgetGate()
	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	q := budgettest.TapAgentRequests(t, brk, "test-ns", "target-agent")

	gate.Apply(ctx, budgettest.MeshState(true, budgetGatePeriodEnd, time.Now()))
	assertBudgetRefusal(t, core.SubmitTask(ctx, "target-agent", idgen.New(), budgetTestMessage("m1"), auth.ForUserIDOnly("user-1")))
	if published(t, brk, q) {
		t.Fatal("a refused task was published to the agent")
	}

	gate.Apply(ctx, budgettest.MeshState(false, budgetGatePeriodEnd, time.Now().Add(time.Second)))
	if err := core.SubmitTask(ctx, "target-agent", idgen.New(), budgetTestMessage("m2"), auth.ForUserIDOnly("user-1")); err != nil {
		t.Fatalf("SubmitTask after the budget was lifted: %v", err)
	}
	if !published(t, brk, q) {
		t.Error("an admitted task was not published")
	}
}

func TestSubmitTask_ARestartedGatewayStillRefusesFromItsDatabase(t *testing.T) {
	brk := connectedDevBroker(t)
	dbURL := store.TestDBURL(t)

	before := budgettest.NewGate()
	first := startCoreOnDB(t, brk, dbURL, before)
	before.Apply(t.Context(), budgettest.MeshState(true, budgetGatePeriodEnd, time.Now()))
	_ = first.Stop(t.Context())

	restarted := startCoreOnDB(t, brk, dbURL, budgettest.NewGate())
	defer func() { _ = restarted.Stop(t.Context()) }()
	assertBudgetRefusal(t, restarted.SubmitTask(t.Context(), "target-agent", idgen.New(), budgetTestMessage("m3"), auth.ForUserIDOnly("user-1")))
}

func TestSubmitTask_ARestartedGatewayWithACorruptStoredStateRefusesAsUnconfirmed(t *testing.T) {
	brk := connectedDevBroker(t)
	dbURL := store.TestDBURL(t)
	before := budgettest.NewGate()
	first := startCoreOnDB(t, brk, dbURL, before)
	before.Apply(t.Context(), budgettest.MeshState(true, budgetGatePeriodEnd, time.Now()))
	_ = first.Stop(t.Context())

	db, err := store.Open(dbURL, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE gateway_budget_state SET state = '{"meshBlocked": tru' WHERE id = 1`); err != nil {
		t.Fatalf("corrupt the stored state: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	gate := budgettest.NewGate()
	restarted := startCoreOnDB(t, brk, dbURL, gate)
	defer func() { _ = restarted.Stop(t.Context()) }()
	if err := restarted.SubmitTask(t.Context(), "target-agent", idgen.New(), budgetTestMessage("m7"), auth.ForUserIDOnly("user-1")); !IsBudgetUnconfirmed(err) {
		t.Fatalf("SubmitTask with a corrupt stored state = %v, want an unconfirmed-state refusal", err)
	}

	gate.Apply(t.Context(), budgettest.MeshState(false, budgetGatePeriodEnd, time.Now()))
	if err := restarted.SubmitTask(t.Context(), "target-agent", idgen.New(), budgetTestMessage("m8"), auth.ForUserIDOnly("user-1")); err != nil {
		t.Errorf("SubmitTask once the platform answered: %v", err)
	}
}

func TestSubmitTask_AGatewayWhoseGateHasHeardNothingAdmits(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	if err := core.SubmitTask(ctx, "target-agent", idgen.New(), budgetTestMessage("m4"), auth.ForUserIDOnly("user-1")); err != nil {
		t.Errorf("SubmitTask on a gate that has heard nothing: %v", err)
	}
}

func TestGatewayStop_TheSharedGateStopsSavingToTheStoppedCoresDatabase(t *testing.T) {
	brk := connectedDevBroker(t)
	var logs safeBuffer
	gate := budget.NewGate(slog.New(slog.NewTextHandler(&logs, nil)))
	core := startCoreOnDB(t, brk, store.TestDBURL(t), gate)
	_ = core.Stop(t.Context())

	gate.Apply(t.Context(), budgettest.MeshState(true, budgetGatePeriodEnd, time.Now()))
	if strings.Contains(logs.String(), "could not store the budget state") {
		t.Errorf("the gate still saved to a stopped core's closed database: %s", logs.String())
	}
}

func TestSubmitTask_ARestartedGatewayRefusesUntilThePlatformConfirmsBudgetsInUse(t *testing.T) {
	brk := connectedDevBroker(t)
	dbURL := store.TestDBURL(t)

	before := budgettest.NewGate()
	first := startCoreOnDB(t, brk, dbURL, before)
	before.Apply(t.Context(), budgettest.MeshState(false, budgetGatePeriodEnd, time.Now()))
	_ = first.Stop(t.Context())

	gate := budgettest.NewGate()
	restarted := startCoreOnDB(t, brk, dbURL, gate)
	defer func() { _ = restarted.Stop(t.Context()) }()
	err := restarted.SubmitTask(t.Context(), "target-agent", idgen.New(), budgetTestMessage("m5"), auth.ForUserIDOnly("user-1"))
	if !IsBudgetUnconfirmed(err) {
		t.Fatalf("SubmitTask before the platform answered = %v, want an unconfirmed-state refusal", err)
	}
	if text := restarted.SubmitFailureText(err, false); text != BudgetUnconfirmedText {
		t.Errorf("refusal text = %q, want %q", text, BudgetUnconfirmedText)
	}

	gate.Apply(t.Context(), budgettest.MeshState(false, budgetGatePeriodEnd, time.Now()))
	if err := restarted.SubmitTask(t.Context(), "target-agent", idgen.New(), budgetTestMessage("m6"), auth.ForUserIDOnly("user-1")); err != nil {
		t.Errorf("SubmitTask once the platform confirmed the budget is not spent: %v", err)
	}
}

func TestSubmitTask_RefusesOnlyAUserWhoseOwnBudgetIsSpent(t *testing.T) {
	gate := budgettest.NewGate()
	gate.Apply(context.Background(), budgettest.UserBlockedState(budgetGatePeriodEnd, time.Now(), budget.UserKey("", "spent-user")))
	core := startedCoreWithGate(t, gate)

	err := core.SubmitTask(context.Background(), "target-agent", "task-spent-user", budgetTestMessage("m5"), auth.ForUserIDOnly("spent-user"))
	if exhausted, ok := errors.AsType[*budget.ExhaustedError](err); !ok || exhausted.Tier != budget.TierUser {
		t.Errorf("SubmitTask as the spent user = %v, want their own budget refusing", err)
	}
	if err := core.SubmitTask(context.Background(), "target-agent", "task-other-user", budgetTestMessage("m6"), auth.ForUserIDOnly("other-user")); err != nil {
		t.Errorf("SubmitTask as another user = %v, want admitted", err)
	}
}

func TestSubmitTask_ASpentSystemUsersRefusalNamesTheSystemUserNotTheReader(t *testing.T) {
	gate := budgettest.NewGate()
	gate.Apply(context.Background(), budgettest.UserBlockedState(budgetGatePeriodEnd, time.Now(), budget.UserKey(rbac.DefaultSystemUserID, "")))
	core := startedCoreWithGate(t, gate)

	err := core.SubmitTask(context.Background(), "target-agent", "task-run-as", budgetTestMessage("m7"), auth.ForUserIDOnly(rbac.DefaultSystemUserSub))

	text := BudgetRefusalText(err)
	if !strings.Contains(text, `usage budget is used up until 1 November 2099 00:00 UTC for the service account "default" this runs as`) {
		t.Errorf("refusal text = %q, want it to name the service account the work runs as", text)
	}
	if strings.Contains(text, "from you") || strings.Contains(text, "your usage budget") {
		t.Errorf("refusal text = %q, want no claim that the reader's own budget is spent", text)
	}
}

type trustTaskSigner struct{ signer *trust.Signer }

func (s trustTaskSigner) SignTask(identity *auth.Subject, taskID string) (string, error) {
	return s.signer.Sign(identity.TaskSignClaims(taskID))
}

func newTaskTrust(t *testing.T) (TaskSigner, *trust.Verifier) {
	t.Helper()
	km, err := trust.NewKeyManager("test-gw")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	registry := trust.NewRegistry()
	registry.AddOrUpdate(&trust.TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		Namespace:     "test-ns",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	return trustTaskSigner{trust.NewSigner(km, "test-gw", time.Hour, 24*time.Hour)}, trust.NewVerifier(registry, 5*time.Second, slog.Default())
}

func TestBudgetUser_KeysTheUserAgentsBookSpendTo(t *testing.T) {
	const row = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"
	core, brk, _ := setupTestGateway(t)
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	q := budgettest.TapAgentRequests(t, brk, "test-ns", "target-agent")
	signer, verifier := newTaskTrust(t)

	for _, tc := range []struct {
		name     string
		identity *auth.Subject
		signed   bool
		runAs    map[string]string
		userID   string
	}{
		{"a user with a platform row", auth.NewTestSubject(auth.SubjectFields{UserID: "alice", PlatformUserID: row}), true, nil, "alice"},
		{"the default service account's seeded row", nil, false, nil, rbac.DefaultSystemUserSub},
		{"a custom run_as service account's rendered row", nil, false, map[string]string{"system:reports": row}, "system:reports"},
		{"a custom run_as service account with no rendered row", nil, false, nil, "system:reports"},
		{"a task SAM submits for itself", auth.NewTestSubject(auth.SubjectFields{UserID: "alice", PlatformUserID: row, SystemInitiated: true}), true, nil, "system:sentinel"},
		{"a user with no row", nil, false, nil, "bob"},
		{"a row id that is not canonical", auth.NewTestSubject(auth.SubjectFields{UserID: "bob", PlatformUserID: "not-a-uuid"}), true, nil, "bob"},
		{"a signed identity unlike the submitted user", auth.NewTestSubject(auth.SubjectFields{UserID: "carol"}), true, nil, "bob"},
		{"an unsigned identity unlike the submitted user", auth.NewTestSubject(auth.SubjectFields{UserID: "carol"}), false, nil, "bob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core.taskSigner = nil
			if tc.signed {
				core.SetTaskSigner(signer)
			}
			core.cfg.RunAsUserUUIDs = tc.runAs
			ctx := t.Context()
			caller := tc.identity
			if caller == nil {
				caller = auth.ForUserIDOnly(tc.userID)
			}

			taskID := idgen.New()
			if err := core.SubmitTaskAttributedTo(ctx, "target-agent", taskID, budgetTestMessage(idgen.New()), caller, tc.userID); err != nil {
				t.Fatalf("SubmitTask: %v", err)
			}
			received, err := brk.Receive(ctx, q)
			if err != nil || received == nil {
				t.Fatalf("the agent received no request: %v", err)
			}
			props := a2a.DecodeUserProperties(received.UserProperties)
			var verified *coreauth.Identity
			if props.AuthToken != "" {
				if verified, err = verifier.Authenticate(ctx, props.AuthToken, taskID); err != nil {
					t.Fatalf("verify the task token: %v", err)
				}
			}
			booked := budget.UserKey(spend.SpendUser(props, verified))

			if refused, _ := core.budgetUser(caller, tc.userID); refused != booked {
				t.Errorf("gateway refuses by %q, agent books spend to %q", refused, booked)
			}
		})
	}
}
