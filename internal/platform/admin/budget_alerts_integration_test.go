// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	notifystore "github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/budgetstate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const budgetAlertsNamespace = "budget-alerts-it"

type budgetAdminsFixture struct {
	db        *store.DB
	users     *store.UsersRepo
	directory *budgetAdminDirectory
	logger    *slog.Logger

	viaGrant, viaClaim, reviewer, regular, readOnly, gone string
}

func newBudgetAdminsFixture(t *testing.T) *budgetAdminsFixture {
	t.Helper()
	ctx := t.Context()
	db := testOpenDB(t)
	users, rbacRepo := store.NewUsersRepo(db), store.NewRBACRepo(db)
	logger := slog.Default()
	composer := rbac.NewComposer(embeddedYAMLState(t), newRBACDBSource(rbacRepo), logger)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	f := &budgetAdminsFixture{db: db, users: users, directory: newBudgetAdminDirectory(users, composer, logger), logger: logger}

	updater := seedRoleWithScopes(ctx, t, rbacRepo, composer, "budget-updater", []string{rbac.ScopeBudgetUpdate})
	assigner := seedRoleWithScopes(ctx, t, rbacRepo, composer, "budget-assigner", []string{rbac.ScopeBudgetAssign})
	reader := seedRoleWithScopes(ctx, t, rbacRepo, composer, "budget-reader", []string{rbac.ScopeBudgetRead})
	reviewing := seedRoleWithScopes(ctx, t, rbacRepo, composer, "budget-reviewer", []string{rbac.ScopeBudgetUpdate, rbac.ScopeBudgetRead})
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{
		OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "budget-admins", Name: "Budget admins", CreatedBy: "x", UpdatedBy: "x",
	}, []string{assigner}); err != nil {
		t.Fatalf("claim mapping: %v", err)
	}

	f.viaGrant = seedUserRow(ctx, t, users, "s-grant", "Grant", "grant@example.com", "azure", "", nil)
	f.viaClaim = seedUserRow(ctx, t, users, "s-claim", "Claim", "claim@example.com", "azure", `{"groups":["budget-admins"]}`, []string{"budget-admins"})
	f.reviewer = seedUserRow(ctx, t, users, "s-review", "Reviewer", "reviewer@example.com", "azure", "", nil)
	f.regular = seedUserRow(ctx, t, users, "s-regular", "Regular", "regular@example.com", "azure", "", nil)
	f.readOnly = seedUserRow(ctx, t, users, "s-read", "Reader", "reader@example.com", "azure", "", nil)
	f.gone = seedUserRow(ctx, t, users, "s-gone", "Gone", "gone@example.com", "azure", "", nil)
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{f.viaGrant: updater, f.reviewer: reviewing, f.readOnly: reader, f.gone: updater})
	if _, err := db.ExecContext(ctx, `UPDATE users SET active = 0 WHERE id = ?`, f.gone); err != nil {
		t.Fatalf("deprovision: %v", err)
	}
	return f
}

func TestBudgetAdminDirectory_IsTheActiveHoldersOfEitherBudgetAdministrationScope(t *testing.T) {
	t.Parallel()
	f := newBudgetAdminsFixture(t)

	admins, err := f.directory.BudgetAdmins(t.Context())
	if err != nil {
		t.Fatalf("BudgetAdmins: %v", err)
	}
	got := make([]string, 0, len(admins))
	for _, a := range admins {
		got = append(got, a.PlatformUserID)
		if a.LoginID == "" {
			t.Errorf("admin %s has no login id", a.PlatformUserID)
		}
		if a.CanReviewBudgets != (a.PlatformUserID == f.reviewer) {
			t.Errorf("admin %s CanReviewBudgets = %v, want it only for the holder of %s", a.PlatformUserID, a.CanReviewBudgets, rbac.ScopeBudgetRead)
		}
	}
	slices.Sort(got)
	want := []string{f.viaGrant, f.viaClaim, f.reviewer}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("admins = %v, want the update holder by grant and the assign holder by IdP group %v; not the reader, the regular user, the deprovisioned holder or a system user", got, want)
	}
}

type cancelOnLog struct{ cancel context.CancelFunc }

func (cancelOnLog) Enabled(context.Context, slog.Level) bool { return true }
func (h cancelOnLog) Handle(context.Context, slog.Record) error {
	h.cancel()
	return nil
}
func (h cancelOnLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h cancelOnLog) WithGroup(string) slog.Handler      { return h }

func TestBudgetAdminDirectory_AContextThatEndsMidwayFailsRatherThanReturningAShortenedList(t *testing.T) {
	t.Parallel()
	f := newBudgetAdminsFixture(t)
	seedUserRow(t.Context(), t, f.users, "s-bad-claims", "Bad", "bad@example.com", "azure", "not json", nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.directory.logger = slog.New(cancelOnLog{cancel: cancel})

	admins, err := f.directory.BudgetAdmins(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("BudgetAdmins = %+v, %v; want the context's error, not the admins found before it ended", admins, err)
	}
}

type budgetAlertsChain struct {
	*budgetAdminsFixture
	model     store.ModelConfiguration
	publisher *spend.Publisher
	inbox     *notifystore.NotificationRepo
	prefs     *notifystore.CategoryPreferenceRepo
	notifyOn  atomic.Bool
	notifyBrk *ackCountingBroker
	published broker.Queue
	brk       *broker.DevBroker

	publishedTotal int64
}

type ackCountingBroker struct {
	broker.Broker
	requestPrefix string
	handled       atomic.Int64
}

func (b *ackCountingBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	msg, err := b.Broker.Receive(ctx, q)
	if err != nil || msg == nil || !strings.HasPrefix(msg.Topic, b.requestPrefix) {
		return msg, err
	}
	counted := broker.NewDeliveredMessage(msg.ID, msg.Topic, msg.Payload, func() {
		msg.Ack()
		b.handled.Add(1)
	}, msg.Nack)
	counted.UserProperties, counted.ReplyTo, counted.CorrelationID = msg.UserProperties, msg.ReplyTo, msg.CorrelationID
	counted.Redelivered, counted.ReplicationGroupMessageID = msg.Redelivered, msg.ReplicationGroupMessageID
	return counted, nil
}

func startBudgetAlertsChain(t *testing.T, notifyOn bool) *budgetAlertsChain {
	t.Helper()
	ctx := t.Context()
	c := &budgetAlertsChain{budgetAdminsFixture: newBudgetAdminsFixture(t)}
	c.notifyOn.Store(notifyOn)
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	c.brk = brk
	published, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err = brk.Subscribe(published, a2a.NotifyRequestSubscription(budgetAlertsNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	c.published = published
	c.notifyBrk = &ackCountingBroker{Broker: brk, requestPrefix: strings.TrimSuffix(a2a.NotifyRequestSubscription(budgetAlertsNamespace), ">")}

	c.model = seedOutputPricedModel(t, c.db)
	gov := governor.New(governor.Config{
		Namespace:     budgetAlertsNamespace,
		Broker:        brk,
		Ledger:        store.NewSpendLedgerRepo(c.db),
		Prices:        governor.NewModelConfigPrices(store.NewModelConfigRepo(c.db), c.logger),
		Logger:        c.logger,
		FlushInterval: 20 * time.Millisecond,
	})
	if err = gov.Start(ctx); err != nil {
		t.Fatalf("start governor: %v", err)
	}
	t.Cleanup(func() { _ = gov.Stop(context.Background()) }) //nolint:contextcheck
	c.publisher = spend.NewPublisher(brk, budgetAlertsNamespace, c.logger)
	t.Cleanup(func() { _ = c.publisher.Close(context.Background()) }) //nolint:contextcheck

	dbURL := "sqlite://" + t.TempDir() + "/notify.db"
	core := notify.NewNotifyCore(map[string]any{"namespace": budgetAlertsNamespace, "database_url": dbURL}, notify.NotifyExtensions{})
	core.SetServices(runtime.NewRuntime(c.notifyBrk, nil, runtime.WithLogger(c.logger)).Services())
	if err = core.Init(ctx, nil); err != nil {
		t.Fatalf("init notify: %v", err)
	}
	if err = core.Start(ctx); err != nil {
		t.Fatalf("start notify: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) }) //nolint:contextcheck
	notifyDB, err := notifystore.Open(dbURL, c.logger)
	if err != nil {
		t.Fatalf("open notify db: %v", err)
	}
	t.Cleanup(func() { _ = notifyDB.Close() })
	c.inbox, c.prefs = notifystore.NewNotificationRepo(notifyDB), notifystore.NewCategoryPreferenceRepo(notifyDB)

	job := budgetstate.MonitorJob(budgetstate.NewStore(c.db), brk, budgetAlertsNamespace, c.logger, budgetstate.Alerting{
		Admins:        c.directory,
		NotifyEnabled: c.notifyOn.Load,
	})
	monitorCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = job.Run(monitorCtx, leader.Term{Number: 1})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return c
}

func seedOutputPricedModel(t *testing.T, db *store.DB) store.ModelConfiguration {
	t.Helper()
	output, err := pricing.ParseAmount("15")
	if err != nil {
		t.Fatalf("parse price: %v", err)
	}
	stored := pricing.Store(&pricing.ModelPrice{Output: output})
	mc := store.ModelConfiguration{
		Alias: "priced", Provider: "openai", ModelName: "gpt-test", ModelAuthType: "apikey", ModelAuthConfig: `{}`, ModelParams: `{}`,
		CreatedBy: "test", UpdatedBy: "test",
		PriceInput: stored.Input, PriceOutput: stored.Output, PriceCacheRead: stored.CacheRead, PriceCacheWrite: stored.CacheWrite,
	}
	if err := store.NewModelConfigRepo(db).Create(t.Context(), &mc); err != nil {
		t.Fatalf("create model: %v", err)
	}
	return mc
}

func (c *budgetAlertsChain) limit(t *testing.T, subjectType, amount string) {
	t.Helper()
	parsed, err := budget.ParseAmount(amount)
	if err != nil {
		t.Fatalf("parse amount: %v", err)
	}
	query := `UPDATE budgets SET amount = ? WHERE subject_type = 'mesh'`
	if subjectType == store.BudgetSubjectUser {
		query = `UPDATE budgets SET amount = ? WHERE is_default = 1 AND subject_type = 'user'`
	}
	if _, err := c.db.ExecContext(t.Context(), query, parsed); err != nil {
		t.Fatalf("limit %s: %v", subjectType, err)
	}
}

func (c *budgetAlertsChain) spendMillionOutputTokens(userID string) {
	c.publisher.Record(spend.Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserID: userID, TriggerKind: spend.TriggerChat,
		Surface: spend.SurfaceAgent, ModelConfigID: c.model.ID, ModelName: c.model.ModelName, TaskID: "task-1", TraceID: idgen.New(),
		Tokens: spend.Tokens{Output: 1_000_000},
	})
}

func (c *budgetAlertsChain) waitForInbox(t *testing.T, userID string, want int) []notifystore.Notification {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := c.inbox.ListByUser(t.Context(), userID, notifystore.InboxFilter{}, 10, 0)
		if err != nil {
			t.Fatalf("read inbox: %v", err)
		}
		if len(rows) >= want || time.Now().After(deadline) {
			return rows
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (c *budgetAlertsChain) waitForNotifyIdle(t *testing.T) {
	t.Helper()
	c.drainPublished(t)
	if c.publishedTotal == 0 {
		t.Fatal("no notify request was published, so an empty inbox proves nothing")
	}
	deadline := time.Now().Add(10 * time.Second)
	for c.notifyBrk.handled.Load() < c.publishedTotal {
		if time.Now().After(deadline) {
			t.Fatalf("notify handled %d of %d requests", c.notifyBrk.handled.Load(), c.publishedTotal)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *budgetAlertsChain) drainPublished(t *testing.T) int64 {
	t.Helper()
	var published int64
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		msg, err := c.brk.Receive(ctx, c.published)
		cancel()
		if err != nil || msg == nil {
			c.publishedTotal += published
			return published
		}
		msg.Ack()
		published++
	}
}

func (c *budgetAlertsChain) waitForAlertStates(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := store.NewBudgetAlertStatesRepo(c.db).AlertStates(t.Context(), budget.PeriodStart(time.Now()).UnixMilli())
		if err != nil {
			t.Fatalf("read alert states: %v", err)
		}
		if len(rows) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("alert states = %+v, want %d", rows, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestBudgetAlerts_FiledSpendReachesTheInboxesOfTheUserAndTheBudgetAdmins(t *testing.T) {
	logs := captureDefaultLogsSafe(t)
	c := startBudgetAlertsChain(t, true)
	if err := c.prefs.SetCategory(t.Context(), c.viaClaim, a2a.CategoryBudgetMeshLimit, false); err != nil {
		t.Fatalf("mute: %v", err)
	}
	c.limit(t, store.BudgetSubjectMesh, "20")
	c.limit(t, store.BudgetSubjectUser, "10")
	alice := seedUserRow(t.Context(), t, c.users, "s-alice", "Alice", "alice@example.com", "azure", "", nil)

	c.spendMillionOutputTokens(alice)
	c.spendMillionOutputTokens(c.regular)
	c.waitForAlertStates(t, 3)

	own := c.waitForInbox(t, alice, 1)
	if len(own) != 1 || own[0].Title != "You've reached your budget" {
		t.Errorf("alice's inbox = %+v, want her own limit alert", own)
	}
	mesh := c.waitForInbox(t, c.viaGrant, 1)
	if len(mesh) != 1 || mesh[0].Title != "Agent Mesh budget reached" {
		t.Errorf("the budget admin's inbox = %+v, want the mesh limit alert", mesh)
	}
	if rows := c.waitForInbox(t, c.regular, 1); len(rows) != 1 {
		t.Errorf("the regular user's inbox = %+v, want their own limit alert", rows)
	}
	c.waitForNotifyIdle(t)
	for who, id := range map[string]string{"the admin who muted mesh limit alerts": c.viaClaim, "the read-only holder": c.readOnly, "the deprovisioned admin": c.gone} {
		if rows := c.waitForInbox(t, id, 0); len(rows) != 0 {
			t.Errorf("%s received %+v, want nothing", who, rows)
		}
	}
	if rows := c.waitForInbox(t, c.viaGrant, 0); len(rows) != 1 {
		t.Errorf("the admin received %d alerts, want only the mesh's: users' crossings are not copied to admins", len(rows))
	}
	logged := logs.String()
	if logged == "" {
		t.Fatal("nothing was logged, so the absence of an email from the logs proves nothing")
	}
	for _, email := range []string{"alice@example.com", "grant@example.com", "claim@example.com", "reviewer@example.com", "regular@example.com"} {
		if strings.Contains(logged, email) {
			t.Errorf("the logs name %s", email)
		}
	}
}

func TestBudgetAlerts_WithNotifyOffNothingReachesAnInboxButTheCrossingIsRecorded(t *testing.T) {
	t.Parallel()
	c := startBudgetAlertsChain(t, false)
	c.limit(t, store.BudgetSubjectUser, "10")
	alice := seedUserRow(t.Context(), t, c.users, "s-alice", "Alice", "alice@example.com", "azure", "", nil)

	c.spendMillionOutputTokens(alice)
	c.waitForAlertStates(t, 1)
	if n := c.drainPublished(t); n != 0 {
		t.Errorf("%d notify requests published with notify off, want none", n)
	}
	if rows := c.waitForInbox(t, alice, 0); len(rows) != 0 {
		t.Errorf("alice's inbox = %+v with notify off, want empty", rows)
	}
}
