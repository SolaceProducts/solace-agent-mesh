// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const testNamespace = "test-ns"

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

type ledgerRow struct {
	ID                  string  `db:"id"`
	CompletedTime       int64   `db:"completed_time"`
	UserID              *string `db:"user_id"`
	UserSub             *string `db:"user_sub"`
	AgentID             *string `db:"agent_id"`
	AgentName           *string `db:"agent_name"`
	WorkflowID          *string `db:"workflow_id"`
	WorkflowName        *string `db:"workflow_name"`
	RootKind            *string `db:"root_kind"`
	RootID              *string `db:"root_id"`
	RootName            *string `db:"root_name"`
	EntrypointID        *string `db:"entrypoint_id"`
	EntrypointName      *string `db:"entrypoint_name"`
	Channel             *string `db:"channel"`
	TriggerKind         *string `db:"trigger_kind"`
	TriggerID           *string `db:"trigger_id"`
	Surface             string  `db:"surface"`
	ModelConfigID       *string `db:"model_config_id"`
	ModelName           *string `db:"model_name"`
	TaskID              *string `db:"task_id"`
	TraceID             *string `db:"trace_id"`
	UncachedInputTokens int64   `db:"uncached_input_tokens"`
	OutputTokens        int64   `db:"output_tokens"`
	CacheReadTokens     int64   `db:"cache_read_tokens"`
	CacheWriteTokens    int64   `db:"cache_write_tokens"`
	UncachedInputCost   *int64  `db:"uncached_input_cost"`
	OutputCost          *int64  `db:"output_cost"`
	CacheReadCost       *int64  `db:"cache_read_cost"`
	CacheWriteCost      *int64  `db:"cache_write_cost"`
	TotalCost           *int64  `db:"total_cost"`
	AvoidedInputTokens  *int64  `db:"avoided_input_tokens"`
	AvoidedInputCost    *int64  `db:"avoided_input_cost"`
}

const ledgerColumns = `id, completed_time, user_id, user_sub, agent_id, agent_name, workflow_id, workflow_name,
	root_kind, root_id, root_name, entrypoint_id, entrypoint_name, channel, trigger_kind, trigger_id, surface,
	model_config_id, model_name, task_id, trace_id, uncached_input_tokens, output_tokens, cache_read_tokens,
	cache_write_tokens, uncached_input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost,
	avoided_input_tokens, avoided_input_cost`

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func readRow(t *testing.T, db *store.DB, id string) *ledgerRow {
	t.Helper()
	row, err := dbutil.QueryRow[ledgerRow](t.Context(), db,
		`SELECT `+ledgerColumns+` FROM token_spend_ledger WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("read ledger row %s: %v", id, err)
	}
	return row
}

func countRows(t *testing.T, db *store.DB) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM token_spend_ledger`)
	if err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	return n
}

const waitTimeout = 30 * time.Second

const pollInterval = 10 * time.Millisecond

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(pollInterval)
	}
}

func waitForCount(t *testing.T, db *store.DB, want int) {
	t.Helper()
	last, lastProgress := -1, time.Now()
	for {
		n := countRows(t, db)
		if n == want {
			return
		}
		if n != last {
			last, lastProgress = n, time.Now()
		} else if time.Since(lastProgress) > waitTimeout {
			t.Fatalf("ledger stalled at %d rows, want %d", n, want)
		}
		time.Sleep(pollInterval)
	}
}

func waitForRow(t *testing.T, db *store.DB, id string) *ledgerRow {
	t.Helper()
	var row *ledgerRow
	eventually(t, "event "+id+" to be filed", func() bool {
		row = readRow(t, db, id)
		return row != nil
	})
	return row
}

func amount(t *testing.T, text string) pricing.Amount {
	t.Helper()
	a, err := pricing.ParseAmount(text)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", text, err)
	}
	return a
}

type seededModels struct {
	cacheBrokenOut    store.ModelConfiguration
	cacheBrokenOutFor pricing.ModelPrice
	flat              store.ModelConfiguration
	flatFor           pricing.ModelPrice
	free              store.ModelConfiguration
	unpriced          store.ModelConfiguration
}

func seedModels(t *testing.T, db *store.DB) seededModels {
	t.Helper()
	cacheRead, cacheWrite := amount(t, "0.3"), amount(t, "3.75")
	m := seededModels{
		cacheBrokenOutFor: pricing.ModelPrice{
			Input: amount(t, "3"), Output: amount(t, "15"), CacheRead: &cacheRead, CacheWrite: &cacheWrite,
		},
		flatFor: pricing.ModelPrice{Input: amount(t, "1"), Output: amount(t, "2")},
	}
	m.cacheBrokenOut = createModel(t, db, "cache-priced", &m.cacheBrokenOutFor)
	m.flat = createModel(t, db, "flat-priced", &m.flatFor)
	m.free = createModel(t, db, "free", &pricing.ModelPrice{})
	m.unpriced = createModel(t, db, "unpriced", nil)
	return m
}

func createModel(t *testing.T, db *store.DB, alias string, price *pricing.ModelPrice) store.ModelConfiguration {
	t.Helper()
	stored := pricing.Store(price)
	mc := store.ModelConfiguration{
		Alias: alias, Provider: "openai", ModelName: alias + "-model", ModelAuthType: "apikey",
		ModelAuthConfig: `{}`, ModelParams: `{}`, CreatedBy: "test", UpdatedBy: "test",
		PriceInput: stored.Input, PriceOutput: stored.Output,
		PriceCacheRead: stored.CacheRead, PriceCacheWrite: stored.CacheWrite,
	}
	if err := store.NewModelConfigRepo(db).Create(t.Context(), &mc); err != nil {
		t.Fatalf("create model %s: %v", alias, err)
	}
	return mc
}

var fastTiming = timing{
	flushInterval:                   10 * time.Millisecond,
	retryBackoff:                    time.Millisecond,
	maxRetryBackoff:                 5 * time.Millisecond,
	attemptsBeforeIsolating:         3,
	failuresBeforeStoppingIsolation: 3,
	failuresBeforeNack:              5,
	writeTimeout:                    time.Minute,
	drainTimeout:                    time.Second,
	deliveredIdle:                   20 * time.Millisecond,
	outageAlertAfter:                time.Hour,
	intakeDownAfter:                 time.Hour,
	repeatWarnAfter:                 time.Hour,
}

type rig struct {
	db       *store.DB
	broker   *broker.DevBroker
	governor *Governor
	models   seededModels
}

type rigOption func(*Config)

func startRig(t *testing.T, opts ...rigOption) *rig {
	t.Helper()
	db := store.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	r := &rig{db: db, broker: brk, models: seedModels(t, db)}
	cfg := Config{
		Namespace: testNamespace,
		Broker:    brk,
		Ledger:    store.NewSpendLedgerRepo(db),
		Prices:    NewModelConfigPrices(store.NewModelConfigRepo(db), discardLogger),
		Logger:    discardLogger,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	r.governor = startGovernor(t, cfg)
	return r
}

func startGovernor(t *testing.T, cfg Config) *Governor {
	t.Helper()
	g := New(cfg)
	g.setTiming(fastTiming)
	if err := g.Start(t.Context()); err != nil {
		t.Fatalf("start governor: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })
	return g
}

type tappedBroker struct {
	broker.Broker
	tap func(*broker.Message, error) (*broker.Message, error)
}

func (b *tappedBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	return b.tap(b.Broker.Receive(ctx, q))
}

type deliveries struct {
	mu   sync.Mutex
	list []*delivery
}

type delivery struct {
	payload []byte
	acked   atomic.Bool
	nacked  atomic.Bool
}

func (d *deliveries) tap(msg *broker.Message, err error) (*broker.Message, error) {
	if msg == nil {
		return nil, err
	}
	rec := &delivery{payload: msg.Payload}
	d.mu.Lock()
	d.list = append(d.list, rec)
	d.mu.Unlock()
	return broker.NewDeliveredMessage(msg.ID, msg.Topic, msg.Payload,
		func() { rec.acked.Store(true) }, func() { rec.nacked.Store(true) }), nil
}

func (d *deliveries) snapshot() []*delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.list)
}

func (d *deliveries) allAcked(n int) bool {
	list := d.snapshot()
	return len(list) == n && !slices.ContainsFunc(list, func(rec *delivery) bool { return !rec.acked.Load() })
}

func recordDeliveries(d *deliveries) rigOption {
	return func(cfg *Config) { cfg.Broker = &tappedBroker{Broker: cfg.Broker, tap: d.tap} }
}

func (r *rig) publish(t *testing.T, payload []byte) {
	t.Helper()
	msg := &broker.Message{Payload: payload}
	if err := r.broker.PublishGuaranteed(t.Context(), a2a.SpendEventTopic(testNamespace), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func (r *rig) publishEvent(t *testing.T, e spend.Event) {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	r.publish(t, payload)
}

func newEvent(modelKey string) spend.Event {
	return spend.Event{
		ID:            idgen.New(),
		CompletedTime: time.Now().UnixMilli(),
		UserSub:       "user-1",
		TriggerKind:   spend.TriggerChat,
		Surface:       spend.SurfaceAgent,
		ModelKey:      modelKey,
		ModelName:     "provider-model",
		TaskID:        "task-1",
		TraceID:       "trace-1",
		Tokens:        spend.Tokens{UncachedInput: 400, Output: 50, CacheRead: 400, CacheWrite: 200},
	}
}

func assertCosts(t *testing.T, row *ledgerRow, want spend.Cost) {
	t.Helper()
	got := [4]*int64{row.UncachedInputCost, row.OutputCost, row.CacheReadCost, row.CacheWriteCost}
	wantCosts := [4]int64{want.UncachedInput, want.Output, want.CacheRead, want.CacheWrite}
	for i, name := range []string{"input", "output", "cache read", "cache write"} {
		if got[i] == nil || *got[i] != wantCosts[i] {
			t.Errorf("%s cost = %v, want %d", name, got[i], wantCosts[i])
		}
	}
	if wantTotal := want.UncachedInput + want.Output + want.CacheRead + want.CacheWrite; row.TotalCost == nil || *row.TotalCost != wantTotal {
		t.Errorf("total cost = %v, want %d", row.TotalCost, wantTotal)
	}
}

func assertUnpriced(t *testing.T, row *ledgerRow) {
	t.Helper()
	for name, cost := range map[string]*int64{
		"input": row.UncachedInputCost, "output": row.OutputCost, "cache read": row.CacheReadCost,
		"cache write": row.CacheWriteCost, "total": row.TotalCost,
	} {
		if cost != nil {
			t.Errorf("%s cost = %d, want NULL for an unpriced call", name, *cost)
		}
	}
}

func TestGovernor_FilesAnEventAttributedByUUIDWithItsCosts(t *testing.T) {
	r := startRig(t)
	c := seedComponents(t, r.db)
	e := newEvent(r.models.cacheBrokenOut.Alias)
	e.UserID, e.UserSub = idgen.New(), ""
	e.AgentID, e.WorkflowID, e.EntrypointID = c.agent, c.workflow, c.gateway
	e.RootKind, e.RootID = spend.RootWorkflow, c.rootWorkflow
	e.TriggerKind, e.TriggerID = spend.TriggerSchedule, "trigger-1"
	e.Surface, e.Channel = spend.SurfaceTool, spend.Channel(httputil.SurfaceSlack)

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	assertColumns(t, map[string][2]string{
		"user_id": {deref(row.UserID), e.UserID}, "user_sub": {deref(row.UserSub), "<nil>"},
		"agent_id": {deref(row.AgentID), c.agent}, "agent_name": {deref(row.AgentName), "<nil>"},
		"workflow_id": {deref(row.WorkflowID), c.workflow}, "workflow_name": {deref(row.WorkflowName), "<nil>"},
		"root_kind": {deref(row.RootKind), "workflow"}, "root_id": {deref(row.RootID), c.rootWorkflow},
		"root_name":     {deref(row.RootName), "<nil>"},
		"entrypoint_id": {deref(row.EntrypointID), c.gateway}, "entrypoint_name": {deref(row.EntrypointName), "<nil>"},
		"channel": {deref(row.Channel), "slack"}, "trigger_kind": {deref(row.TriggerKind), "schedule"},
		"trigger_id": {deref(row.TriggerID), "trigger-1"}, "surface": {row.Surface, "tool"},
		"model_config_id": {deref(row.ModelConfigID), r.models.cacheBrokenOut.ID}, "model_name": {deref(row.ModelName), "<nil>"},
		"task_id": {deref(row.TaskID), "task-1"}, "trace_id": {deref(row.TraceID), "trace-1"},
	})
	if row.CompletedTime != e.CompletedTime {
		t.Errorf("completed_time = %d, want %d", row.CompletedTime, e.CompletedTime)
	}
	if row.UncachedInputTokens != 400 || row.OutputTokens != 50 || row.CacheReadTokens != 400 || row.CacheWriteTokens != 200 {
		t.Errorf("tokens = %d/%d/%d/%d, want 400/50/400/200",
			row.UncachedInputTokens, row.OutputTokens, row.CacheReadTokens, row.CacheWriteTokens)
	}
	assertCosts(t, row, spend.Cost{UncachedInput: 12_000_000, Output: 7_500_000, CacheRead: 1_200_000, CacheWrite: 7_500_000})
}

func TestGovernor_FilesAnEventAttributedByNameUnderTheNameColumns(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)
	e.AgentName, e.WorkflowName, e.EntrypointName = "yaml-agent", "yaml-workflow", "builtin-gateway"
	e.RootKind, e.RootName = spend.RootAgent, "yaml-root"

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	assertColumns(t, map[string][2]string{
		"user_id": {deref(row.UserID), "<nil>"}, "user_sub": {deref(row.UserSub), "user-1"},
		"agent_id": {deref(row.AgentID), "<nil>"}, "agent_name": {deref(row.AgentName), "yaml-agent"},
		"workflow_id": {deref(row.WorkflowID), "<nil>"}, "workflow_name": {deref(row.WorkflowName), "yaml-workflow"},
		"root_kind": {deref(row.RootKind), "agent"}, "root_id": {deref(row.RootID), "<nil>"},
		"root_name":     {deref(row.RootName), "yaml-root"},
		"entrypoint_id": {deref(row.EntrypointID), "<nil>"}, "entrypoint_name": {deref(row.EntrypointName), "builtin-gateway"},
	})
}

func assertColumns(t *testing.T, columns map[string][2]string) {
	t.Helper()
	for column, gotWant := range columns {
		if gotWant[0] != gotWant[1] {
			t.Errorf("%s = %s, want %s", column, gotWant[0], gotWant[1])
		}
	}
}

func TestGovernor_FilesASessionCompactionWithNoTaskTraceOrTrigger(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)
	e.Surface, e.TaskID, e.TraceID, e.TriggerKind, e.SessionID = spend.SurfaceCompaction, "", "", "", "session-9"

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	if row.TaskID != nil || row.TraceID != nil || row.TriggerKind != nil {
		t.Errorf("task, trace and trigger = %v, %v, %v, want all NULL", row.TaskID, row.TraceID, row.TriggerKind)
	}
	if d := waitForDetail(t, r.db, e.ID); deref(d.SessionID) != "session-9" {
		t.Errorf("session_id = %s, want session-9", deref(d.SessionID))
	}
}

func TestGovernor_PricesEachModelAtItsOwnRatesResolvingTheKeyAsAliasOrId(t *testing.T) {
	r := startRig(t)
	cases := map[string]struct {
		key        string
		wantConfig string
		wantPrice  pricing.ModelPrice
	}{
		"alias, cache broken out":      {r.models.cacheBrokenOut.Alias, r.models.cacheBrokenOut.ID, r.models.cacheBrokenOutFor},
		"id, cache billed as input":    {r.models.flat.ID, r.models.flat.ID, r.models.flatFor},
		"alias, cache billed as input": {r.models.flat.Alias, r.models.flat.ID, r.models.flatFor},
		"id, cache broken out":         {r.models.cacheBrokenOut.ID, r.models.cacheBrokenOut.ID, r.models.cacheBrokenOutFor},
		"alias, free model":            {r.models.free.Alias, r.models.free.ID, pricing.ModelPrice{}},
	}
	events := make(map[string]spend.Event, len(cases))
	for name, c := range cases {
		e := newEvent(c.key)
		events[name] = e
		r.publishEvent(t, e)
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := events[name]
			row := waitForRow(t, r.db, e.ID)
			if deref(row.ModelConfigID) != c.wantConfig || row.ModelName != nil {
				t.Errorf("model_config_id, model_name = %s, %s; want %s and NULL",
					deref(row.ModelConfigID), deref(row.ModelName), c.wantConfig)
			}
			assertCosts(t, row, spend.CostOf(e.Tokens, c.wantPrice))
		})
	}
}

func TestGovernor_AFreeModelFilesZeroCostsNotNulls(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.free.Alias)

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	assertCosts(t, row, spend.Cost{})
}

func TestGovernor_AnUnpricedModelFilesNullCostsButKeepsItsConfig(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.unpriced.Alias)

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	assertUnpriced(t, row)
	if deref(row.ModelConfigID) != r.models.unpriced.ID {
		t.Errorf("model_config_id = %s, want %s", deref(row.ModelConfigID), r.models.unpriced.ID)
	}
}

func TestGovernor_AnInlineModelWithNoKeyFilesNoConfigAndNullCosts(t *testing.T) {
	r := startRig(t)
	e := newEvent("")

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	assertUnpriced(t, row)
	if row.ModelConfigID != nil || deref(row.ModelName) != "provider-model" {
		t.Errorf("model_config_id, model_name = %s, %s; want NULL and the provider model name when no configuration resolved",
			deref(row.ModelConfigID), deref(row.ModelName))
	}
}

func TestGovernor_AKeyMatchingNoConfigurationFilesUnpriced(t *testing.T) {
	logs := &logCapture{}
	r := startRig(t, func(cfg *Config) { cfg.Logger = logs.logger() })
	for name, key := range map[string]string{"unknown alias": "no-such-model", "unknown id": idgen.New()} {
		t.Run(name, func(t *testing.T) {
			e := newEvent(key)
			r.publishEvent(t, e)
			row := waitForRow(t, r.db, e.ID)
			assertUnpriced(t, row)
			if row.ModelConfigID != nil || deref(row.ModelName) != e.ModelName {
				t.Errorf("model_config_id, model_name = %s, %s; want NULL and the provider model name %q",
					deref(row.ModelConfigID), deref(row.ModelName), e.ModelName)
			}
			if logged := logs.String(); !strings.Contains(logged, "modelKey="+key) {
				t.Errorf("no log says model key %s matched no configuration:\n%s", key, logged)
			}
		})
	}
}

func eventStamped(key, configID string) spend.Event {
	e := newEvent(key)
	e.ModelConfigID = configID
	return e
}

func TestGovernor_AStampedConfigurationIdIsPricedInPreferenceToTheKey(t *testing.T) {
	r := startRig(t)
	e := eventStamped(r.models.flat.Alias, r.models.cacheBrokenOut.ID)

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	if deref(row.ModelConfigID) != r.models.cacheBrokenOut.ID {
		t.Errorf("model_config_id = %s, want the stamped %s, not the key's %s",
			deref(row.ModelConfigID), r.models.cacheBrokenOut.ID, r.models.flat.ID)
	}
	assertCosts(t, row, spend.CostOf(e.Tokens, r.models.cacheBrokenOutFor))
	d := waitForDetail(t, r.db, e.ID)
	if deref(d.ModelKey) != r.models.flat.Alias || deref(d.ResolvedModelName) != r.models.cacheBrokenOut.ModelName {
		t.Errorf("details model_key, resolved model = %s, %s; want the key as sent (%s) and the stamped model (%s)",
			deref(d.ModelKey), deref(d.ResolvedModelName), r.models.flat.Alias, r.models.cacheBrokenOut.ModelName)
	}
}

func TestGovernor_AStampedIdMatchingNoConfigurationFilesUnpricedWithoutFallingBackToTheKey(t *testing.T) {
	logs := &logCapture{}
	r := startRig(t, func(cfg *Config) { cfg.Logger = logs.logger() })
	unknown := idgen.New()
	e := eventStamped(r.models.flat.Alias, unknown)

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	assertUnpriced(t, row)
	if row.ModelConfigID != nil || deref(row.ModelName) != e.ModelName {
		t.Errorf("model_config_id, model_name = %s, %s; want NULL and the provider model name %q",
			deref(row.ModelConfigID), deref(row.ModelName), e.ModelName)
	}
	if logged := logs.String(); !strings.Contains(logged, "modelConfigID="+unknown) {
		t.Errorf("no log names the stamped id that matched no configuration:\n%s", logged)
	}
}

func TestGovernor_FilesAnEventPublishedTwiceOnce(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)

	r.publishEvent(t, e)
	r.publishEvent(t, e)
	waitForRow(t, r.db, e.ID)
	flushedFirst := newEvent("")
	r.publishEvent(t, flushedFirst)
	waitForRow(t, r.db, flushedFirst.ID)
	r.publishEvent(t, e)
	flushedSecond := newEvent("")
	r.publishEvent(t, flushedSecond)
	waitForRow(t, r.db, flushedSecond.ID)

	if n := countRows(t, r.db); n != 3 {
		t.Errorf("ledger rows = %d, want 3: the event once, plus the two events published to flush it", n)
	}
}

func (r *rig) spendFiledSignals(t *testing.T) broker.Queue {
	t.Helper()
	q, err := r.broker.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := r.broker.Subscribe(q, a2a.SpendFiledTopic(testNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func (r *rig) nextSignal(t *testing.T, q broker.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
	defer cancel()
	msg, err := r.broker.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no spend filed signal: %v", err)
	}
	msg.Ack()
}

func TestGovernor_SignalsSpendFiledOnceItsBatchIsCommitted(t *testing.T) {
	r := startRig(t)
	signals := r.spendFiledSignals(t)
	e := newEvent(r.models.flat.Alias)

	r.publishEvent(t, e)
	r.nextSignal(t, signals)
	if readRow(t, r.db, e.ID) == nil {
		t.Fatal("the governor signalled before its batch was committed")
	}
}

func TestGovernor_LogsEachFiledBatchAtDebugWithItsSize(t *testing.T) {
	logs := &logCapture{}
	r := startRig(t, func(cfg *Config) { cfg.Logger = logs.logger() })
	signals := r.spendFiledSignals(t)
	e := newEvent(r.models.flat.Alias)

	r.publishEvent(t, e)
	r.nextSignal(t, signals)

	line := logLine(t, logs, `msg="spend batch filed"`)
	for _, want := range []string{"level=DEBUG", "events=1", "filed=1", "unpriced=0"} {
		if !strings.Contains(line, want) {
			t.Errorf("filed line %q lacks %s", line, want)
		}
	}
}

func TestGovernor_DropsAnUnfileableEventWithoutBlockingTheValidOnesAfterIt(t *testing.T) {
	logs := &logCapture{}
	r := startRig(t, func(cfg *Config) { cfg.Logger = logs.logger() })
	notJSON := []byte(`{"id": "truncated`)
	noUser := newEvent("")
	noUser.UserSub = ""
	negativeTokens := newEvent("")
	negativeTokens.Tokens.Output = -1
	negativeTokensPayload, _ := json.Marshal(negativeTokens)
	noUserPayload, _ := json.Marshal(noUser)
	valid := newEvent(r.models.flat.Alias)

	r.publish(t, notJSON)
	r.publish(t, noUserPayload)
	r.publish(t, negativeTokensPayload)
	r.publishEvent(t, valid)
	waitForRow(t, r.db, valid.ID)

	if n := countRows(t, r.db); n != 1 {
		t.Errorf("ledger rows = %d, want only the valid event", n)
	}
	for _, e := range []spend.Event{noUser, negativeTokens} {
		if readRow(t, r.db, e.ID) != nil {
			t.Errorf("invalid event %s was filed", e.ID)
		}
	}
	notDecodable := logLine(t, logs, "spend event dropped: not decodable")
	for _, want := range []string{"level=ERROR", fmt.Sprintf("payloadBytes=%d", len(notJSON))} {
		if !strings.Contains(notDecodable, want) {
			t.Errorf("undecodable drop log is missing %q:\n%s", want, notDecodable)
		}
	}
	if !strings.Contains(notDecodable, "syntaxOffset=") {
		t.Errorf("undecodable drop log does not say where the payload stopped parsing:\n%s", notDecodable)
	}
	if strings.Contains(notDecodable, "truncated") || strings.Contains(notDecodable, "error=") {
		t.Errorf("undecodable drop log carries more than where the payload failed to parse:\n%s", notDecodable)
	}
	invalid := logLine(t, logs, "eventID="+negativeTokens.ID)
	for _, want := range append([]string{
		"level=ERROR", `msg="spend event dropped: invalid"`, "reason=",
		"taskID=" + negativeTokens.TaskID, "traceID=" + negativeTokens.TraceID,
	}, spendFieldsOf(negativeTokens)...) {
		if !strings.Contains(invalid, want) {
			t.Errorf("invalid drop log is missing %q:\n%s", want, invalid)
		}
	}
}

func logLine(t *testing.T, logs *logCapture, want string) string {
	t.Helper()
	var found []string
	for line := range strings.Lines(logs.String()) {
		if strings.Contains(line, want) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one log line containing %q, got %d:\n%s", want, len(found), logs.String())
	}
	return found[0]
}

func spendFieldsOf(e spend.Event) []string {
	return []string{
		"modelName=" + e.ModelName, "modelKey=" + e.ModelKey, "surface=" + string(e.Surface),
		fmt.Sprintf("uncachedInputTokens=%d", e.Tokens.UncachedInput), fmt.Sprintf("outputTokens=%d", e.Tokens.Output),
		fmt.Sprintf("cacheReadTokens=%d", e.Tokens.CacheRead), fmt.Sprintf("cacheWriteTokens=%d", e.Tokens.CacheWrite),
	}
}

func TestGovernor_AnInvalidEventsUnstorableFieldsAreNotLogged(t *testing.T) {
	logs := &logCapture{}
	r := startRig(t, func(cfg *Config) { cfg.Logger = logs.logger() })
	e := newEvent("")
	e.Surface = ""
	e.ModelName = strings.Repeat("m", spend.MaxFieldBytes+1)
	e.TaskID = "task\x00-1"

	r.publishEvent(t, e)

	eventually(t, "the invalid event to be logged", func() bool {
		return strings.Contains(logs.String(), "spend event dropped: invalid")
	})
	line := logLine(t, logs, "spend event dropped: invalid")
	if strings.Contains(line, e.ModelName) || strings.Count(line, unloggableValue) != 2 {
		t.Errorf("want the oversized model name and the NUL-bearing task id replaced, not logged:\n%s", line)
	}
}

func TestGovernor_WarnsOfEachRepairOncePerInterval(t *testing.T) {
	logs := &logCapture{}
	r := startRig(t, func(cfg *Config) { cfg.Logger = logs.logger() })
	clamped := func() spend.Event {
		e := newEvent("")
		e.CompletedTime = time.Now().Add(time.Hour).UnixMilli()
		return e
	}
	first, second := clamped(), clamped()
	unpaired := clamped()
	unpaired.RootKind = spend.RootAgent

	for _, e := range []spend.Event{first, second, unpaired} {
		r.publishEvent(t, e)
	}
	waitForCount(t, r.db, 3)

	logged := logs.String()
	if n := strings.Count(logged, "spend event repaired"); n != 2 {
		t.Fatalf("repair warnings = %d, want 2: the clamp once, then only the new note:\n%s", n, logged)
	}
	if strings.Contains(logged, "eventID="+second.ID) {
		t.Errorf("a repeated repair was warned again within the interval:\n%s", logged)
	}
	if line := logLine(t, logs, "eventID="+unpaired.ID); strings.Contains(line, "completed_time") ||
		!strings.Contains(line, "root cleared") {
		t.Errorf("want only the repair not yet warned of:\n%s", line)
	}
}

func TestGovernor_AcksTheEventsItCannotFileSoTheyAreNotRedelivered(t *testing.T) {
	d := &deliveries{}
	r := startRig(t, recordDeliveries(d))
	negativeTokens := newEvent("")
	negativeTokens.Tokens.UncachedInput = -5
	negativeTokensPayload, err := json.Marshal(negativeTokens)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	valid := newEvent("")

	r.publish(t, []byte(`not json`))
	r.publish(t, negativeTokensPayload)
	r.publishEvent(t, valid)
	waitForRow(t, r.db, valid.ID)

	eventually(t, "all three deliveries to be acked", func() bool { return d.allAcked(3) })
}

func TestGovernor_EventsUnfiledWhenItStopsAreFiledOnceByTheNextGovernor(t *testing.T) {
	filedBeforeStop, inFlightAtStop := events(3), events(3)
	inFlight := make(map[string]bool, len(inFlightAtStop))
	for _, e := range inFlightAtStop {
		inFlight[e.ID] = true
	}
	writing := make(chan struct{})
	var signalWriting sync.Once
	d := &deliveries{}
	r := startRig(t, recordDeliveries(d), func(cfg *Config) {
		real := cfg.Ledger
		cfg.Ledger = ledgerFunc{
			insert: func(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error) {
				if !slices.ContainsFunc(entries, func(e store.SpendLedgerEntry) bool { return inFlight[e.ID] }) {
					return real.Insert(ctx, entries)
				}
				signalWriting.Do(func() { close(writing) })
				<-ctx.Done()
				if _, err := real.Insert(context.Background(), entries[:1]); err != nil {
					return 0, err
				}
				return 0, ctx.Err()
			},
			delete: real.DeleteOlderThan,
		}
	})
	for _, e := range filedBeforeStop {
		r.publishEvent(t, e)
	}
	eventually(t, "the first events to be filed and acked", func() bool { return d.allAcked(len(filedBeforeStop)) })
	for _, e := range inFlightAtStop {
		r.publishEvent(t, e)
	}
	select {
	case <-writing:
	case <-time.After(waitTimeout):
		t.Fatal("the in-flight events were never written")
	}

	if err := r.governor.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	var unacked [][]byte
	for _, rec := range d.snapshot() {
		if !rec.acked.Load() {
			unacked = append(unacked, rec.payload)
		}
	}
	if len(unacked) == 0 {
		t.Fatal("no event was left unacked by a governor stopped mid-write")
	}
	for _, payload := range unacked {
		var e spend.Event
		if err := json.Unmarshal(payload, &e); err != nil || !inFlight[e.ID] {
			t.Errorf("an event that was not in flight was left unacked: %s", payload)
		}
	}

	next := &deliveries{}
	startGovernor(t, Config{
		Namespace: testNamespace,
		Broker:    &tappedBroker{Broker: r.broker, tap: next.tap},
		Ledger:    store.NewSpendLedgerRepo(r.db),
		Prices:    NewModelConfigPrices(store.NewModelConfigRepo(r.db), discardLogger),
		Logger:    discardLogger,
	})
	for _, payload := range unacked {
		r.publish(t, payload)
	}

	all := slices.Concat(filedBeforeStop, inFlightAtStop)
	waitForCount(t, r.db, len(all))
	for _, e := range all {
		if readRow(t, r.db, e.ID) == nil {
			t.Errorf("event %s was not filed", e.ID)
		}
	}
	eventually(t, "the redelivered events to be acked", func() bool {
		return len(next.snapshot()) >= len(unacked) && next.allAcked(len(next.snapshot()))
	})
}

type failingReceives struct {
	broker.Broker
	failuresLeft atomic.Int32
	failed       atomic.Int32
}

func (b *failingReceives) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	if b.failuresLeft.Add(-1) >= 0 {
		b.failed.Add(1)
		return nil, errors.New("broker connection lost")
	}
	return b.Broker.Receive(ctx, q)
}

func TestGovernor_KeepsReceivingAfterTheBrokerFailsToDeliver(t *testing.T) {
	failing := &failingReceives{}
	failing.failuresLeft.Store(4)
	r := startRig(t, func(cfg *Config) {
		failing.Broker = cfg.Broker
		cfg.Broker = failing
	})
	e := newEvent("")

	r.publishEvent(t, e)

	waitForRow(t, r.db, e.ID)
	if got := failing.failed.Load(); got != 4 {
		t.Errorf("failed receives = %d, want all 4 sat through before filing", got)
	}
}

func TestGovernor_ReportsASpendQueueThatWillNotBind(t *testing.T) {
	failing := &failingReceives{}
	failing.failuresLeft.Store(1 << 30)
	logs := &logCapture{}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	failing.Broker = brk
	g := New(Config{Namespace: testNamespace, Broker: failing, Ledger: store.NewSpendLedgerRepo(store.TestDB(t)), Prices: &countingPrices{}, Logger: logs.logger()})
	quickIntake := fastTiming
	quickIntake.intakeDownAfter = 50 * time.Millisecond
	g.setTiming(quickIntake)
	if err := g.Start(t.Context()); err != nil {
		t.Fatalf("start governor: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) }) //nolint:contextcheck

	eventually(t, "the governor to report the unbound queue", func() bool {
		return errors.Is(g.Health(), runtime.ErrDegraded) && strings.Contains(logs.String(), "level=ERROR msg=\"spend queue is not bound")
	})

	failing.failuresLeft.Store(0)
	eventually(t, "the governor to recover once the queue binds", func() bool {
		return g.Health() == nil && strings.Contains(logs.String(), "spend queue is bound again")
	})
}

type countingPrices struct {
	mu      sync.Mutex
	lookups map[string]int
}

func (p *countingPrices) Lookup(_ context.Context, ref ModelRef) (Model, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lookups == nil {
		p.lookups = map[string]int{}
	}
	p.lookups[ref.Key]++
	return Model{ConfigID: ref.Key}, nil
}

func (p *countingPrices) counts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.lookups)
}

func TestWrite_ResolvesEachModelOncePerBatchAndAfreshForTheNext(t *testing.T) {
	prices := &countingPrices{}
	g := New(Config{Ledger: &scriptedLedger{}, Prices: prices, Logger: discardLogger})
	acks := &ackLog{}
	batch := []pendingEvent{
		acks.pending(newEvent("a")), acks.pending(newEvent("a")), acks.pending(newEvent("b")), acks.pending(newEvent("a")),
	}

	if _, err := g.write(t.Context(), batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := prices.counts(); got["a"] != 1 || got["b"] != 1 {
		t.Errorf("lookups after one batch = %v, want each model once", got)
	}
	if _, err := g.write(t.Context(), batch[:1]); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := prices.counts(); got["a"] != 2 {
		t.Errorf("lookups of a after a second batch = %d, want 2: a price change applies from the next batch", got["a"])
	}
}

func TestGovernor_FilesABurstLargerThanOneBatch(t *testing.T) {
	r := startRig(t)
	total := 2*maxBatch + 37
	ids := make(map[string]bool, total)
	for range total {
		e := newEvent(r.models.cacheBrokenOut.Alias)
		ids[e.ID] = true
		r.publishEvent(t, e)
	}

	waitForCount(t, r.db, total)

	for id := range ids {
		if readRow(t, r.db, id) == nil {
			t.Fatalf("event %s was not filed", id)
		}
	}
}

func TestGovernor_StopReturnsPromptly(t *testing.T) {
	r := startRig(t)
	e := newEvent("")
	r.publishEvent(t, e)
	waitForRow(t, r.db, e.ID)

	stopped := make(chan error, 1)
	go func() { stopped <- r.governor.Stop(t.Context()) }()

	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("Stop() = %v, want nil", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Stop did not return")
	}
}

func TestGovernor_AnEventPublishedAfterStopIsNotFiled(t *testing.T) {
	r := startRig(t)
	if err := r.governor.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	e := newEvent("")

	r.publishEvent(t, e)

	receiveCtx, cancel := context.WithTimeout(t.Context(), waitTimeout)
	defer cancel()
	queue, err := r.broker.CreateQueue(a2a.QueueName(testNamespace, "governance", "spend"), broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("bind the spend queue: %v", err)
	}
	msg, err := r.broker.Receive(receiveCtx, queue)
	if err != nil {
		t.Fatalf("the event is not waiting on the spend queue: %v", err)
	}
	var got spend.Event
	if err := json.Unmarshal(msg.Payload, &got); err != nil || got.ID != e.ID {
		t.Errorf("queued event = %q (%v), want %s still unconsumed", got.ID, err, e.ID)
	}
	if readRow(t, r.db, e.ID) != nil {
		t.Error("a stopped governor filed an event")
	}
}

func ledgerEntry(completed time.Time) store.SpendLedgerEntry {
	return store.SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: completed.UnixMilli(), UserSub: new("user-1"),
		TriggerKind: new("chat"), Surface: "agent", ModelName: new("m"), TaskID: new("task"), TraceID: new("trace"),
	}
}

type fakePrices struct{}

func (fakePrices) Lookup(context.Context, ModelRef) (Model, error) { return Model{}, nil }

var (
	errStatementTimeout = &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
	errInvalidText      = &pgconn.PgError{Code: "22021", Message: "invalid byte sequence for encoding \"UTF8\""}
)

type scriptedLedger struct {
	refuse func(attempt int, entries []store.SpendLedgerEntry) error

	mu       sync.Mutex
	attempts int
	filed    map[string]int
}

func (l *scriptedLedger) Insert(_ context.Context, entries []store.SpendLedgerEntry) (int64, error) {
	l.mu.Lock()
	l.attempts++
	attempt := l.attempts
	l.mu.Unlock()
	if l.refuse != nil {
		if err := l.refuse(attempt, entries); err != nil {
			return 0, err
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.filed == nil {
		l.filed = map[string]int{}
	}
	for _, e := range entries {
		l.filed[e.ID]++
	}
	return int64(len(entries)), nil
}

func (l *scriptedLedger) DeleteOlderThan(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (l *scriptedLedger) InsertDetails(_ context.Context, details []store.SpendLedgerDetail) (int64, error) {
	return int64(len(details)), nil
}

func (l *scriptedLedger) ResolveNames(context.Context, store.ComponentTable, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (l *scriptedLedger) timesFiled(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.filed[id]
}

func (l *scriptedLedger) attemptCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attempts
}

func containsEntry(entries []store.SpendLedgerEntry, id string) bool {
	return slices.ContainsFunc(entries, func(e store.SpendLedgerEntry) bool { return e.ID == id })
}

type ackLog struct {
	mu    sync.Mutex
	byID  map[string]int
	nacks map[string]int
}

func (a *ackLog) pending(e spend.Event) pendingEvent {
	record := func(counts *map[string]int) func() {
		return func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if *counts == nil {
				*counts = map[string]int{}
			}
			(*counts)[e.ID]++
		}
	}
	return pendingEvent{msg: broker.NewDeliveredMessage(e.ID, "t", nil, record(&a.byID), record(&a.nacks)), event: e}
}

func (a *ackLog) nackCount(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nacks[id]
}

func (a *ackLog) totalNacks() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.nacks {
		n += c
	}
	return n
}

func (a *ackLog) count(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.byID[id]
}

func (a *ackLog) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.byID {
		n += c
	}
	return n
}

type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *logCapture) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(c, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

const droppedMsg = "spend event dropped: the ledger refused it"

func runWriteLoop(ctx context.Context, t *testing.T, g *Governor, queued ...pendingEvent) <-chan struct{} {
	t.Helper()
	for _, p := range queued {
		g.pending <- p
	}
	done := make(chan struct{})
	go func() { g.writeLoop(ctx); close(done) }()
	t.Cleanup(func() { <-done })
	return done
}

func events(n int) []spend.Event {
	out := make([]spend.Event, n)
	for i := range out {
		out[i] = newEvent("")
	}
	return out
}

func newWriteLoopGovernor(ledger Ledger, prices Prices, logger *slog.Logger, tm timing) *Governor {
	g := New(Config{Ledger: ledger, Prices: prices, Logger: logger})
	g.setTiming(tm)
	return g
}

func TestWriteLoop_RetriesATransientRefusalOfALoneEventUntilTheLedgerRecovers(t *testing.T) {
	refusals := fastTiming.attemptsBeforeIsolating + 3
	ledger := &scriptedLedger{refuse: func(attempt int, _ []store.SpendLedgerEntry) error {
		if attempt <= refusals {
			return errStatementTimeout
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	e := newEvent("")
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the event to be acked", func() bool { return acks.count(e.ID) == 1 })
	if got := ledger.timesFiled(e.ID); got != 1 {
		t.Errorf("event filed %d times, want 1", got)
	}
	if got := ledger.attemptCount(); got <= refusals {
		t.Errorf("ledger saw %d attempts, want more than the %d it refused", got, refusals)
	}
	if strings.Contains(logs.String(), droppedMsg) {
		t.Error("a transiently refused event was logged as dropped")
	}
}

type pricesFunc func(context.Context, ModelRef) (Model, error)

func (f pricesFunc) Lookup(ctx context.Context, ref ModelRef) (Model, error) { return f(ctx, ref) }

func TestWriteLoop_RetriesAnEventWhosePriceCannotBeLookedUp(t *testing.T) {
	var lookups atomic.Int32
	prices := pricesFunc(func(context.Context, ModelRef) (Model, error) {
		if lookups.Add(1) <= int32(fastTiming.attemptsBeforeIsolating+1) {
			return Model{}, errors.New("model configuration store unavailable")
		}
		return Model{ConfigID: "config-1"}, nil
	})
	ledger := &scriptedLedger{}
	logs := &logCapture{}
	acks := &ackLog{}
	e := newEvent("some-model")
	g := newWriteLoopGovernor(ledger, prices, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the event to be acked", func() bool { return acks.count(e.ID) == 1 })
	if got := ledger.timesFiled(e.ID); got != 1 {
		t.Errorf("event filed %d times, want 1", got)
	}
	if strings.Contains(logs.String(), droppedMsg) {
		t.Error("an event whose price lookup failed was logged as dropped")
	}
}

func TestWriteLoop_HoldsALoneRefusedEventUntilAnotherEventFilesThenDropsIt(t *testing.T) {
	e := newEvent("")
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if containsEntry(entries, e.ID) {
			return errInvalidText
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(e))
	eventually(t, "the lone event to be refused more than once", func() bool { return ledger.attemptCount() > 2 })
	if acks.count(e.ID) != 0 || strings.Contains(logs.String(), droppedMsg) {
		t.Fatalf("a lone refused event was dropped before any other event filed:\n%s", logs.String())
	}
	if n := strings.Count(logs.String(), loneRefusalMsg); n != 1 {
		t.Errorf("hold of the lone refused event logged %d times across its retries, want 1:\n%s", n, logs.String())
	}
	if line := logLine(t, logs, loneRefusalMsg); !strings.Contains(line, "level=WARN") || !strings.Contains(line, "eventID="+e.ID) {
		t.Errorf("want a WARN naming the held event:\n%s", line)
	}
	if n := strings.Count(logs.String(), "spend batch not filed"); n != 1 {
		t.Errorf("the refused batch was logged %d times across its retries, want 1 within the interval:\n%s", n, logs.String())
	}

	healthy := newEvent("")
	g.pending <- acks.pending(healthy)

	eventually(t, "the refused event to be acked", func() bool { return acks.count(e.ID) == 1 })
	if got := ledger.timesFiled(e.ID); got != 0 {
		t.Errorf("refused event filed %d times, want 0", got)
	}
	if got := ledger.timesFiled(healthy.ID); got != 1 {
		t.Errorf("healthy event filed %d times, want 1", got)
	}
	logged := logs.String()
	for _, want := range append([]string{
		"level=ERROR", droppedMsg, "eventID=" + e.ID, "taskID=" + e.TaskID, "traceID=" + e.TraceID, "SQLSTATE 22021",
	}, spendFieldsOf(e)...) {
		if !strings.Contains(logged, want) {
			t.Errorf("drop log is missing %q:\n%s", want, logged)
		}
	}
}

const loneRefusalMsg = "spend event refused by the ledger with nothing filed beside it; holding it until another event files"

func TestWriteLoop_ALoneRefusedEventHeldPastTheOutageAlertIsNotReportedAsAnOutage(t *testing.T) {
	e := newEvent("")
	ledger := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error { return errInvalidText }}
	logs := &logCapture{}
	alertAtOnce := fastTiming
	alertAtOnce.outageAlertAfter = time.Nanosecond
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), alertAtOnce)

	runWriteLoop(t.Context(), t, g, (&ackLog{}).pending(e))
	eventually(t, "the lone event to be refused repeatedly", func() bool { return ledger.attemptCount() > 6 })

	if strings.Contains(logs.String(), outageMsg) {
		t.Errorf("a ledger that answers by refusing one event was reported as an outage:\n%s", logs.String())
	}
}

func TestWriteLoop_StoppingWhileHoldingALoneRefusedEventLeavesItForRedelivery(t *testing.T) {
	e := newEvent("")
	ledger := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error { return errInvalidText }}
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)
	ctx, cancel := context.WithCancel(t.Context())
	done := runWriteLoop(ctx, t, g, acks.pending(e))
	eventually(t, "the lone event to be held", func() bool { return strings.Contains(logs.String(), loneRefusalMsg) })

	cancel()
	<-done

	if acks.count(e.ID) != 0 {
		t.Error("a held event was acked at stop; it must stay unacked to be redelivered")
	}
	if strings.Contains(logs.String(), droppedMsg) {
		t.Errorf("a held event was dropped at stop:\n%s", logs.String())
	}
	if line := logLine(t, logs, "governor stopped with spend unfiled"); !strings.Contains(line, "events=1") {
		t.Errorf("want the held event counted as unfiled at stop:\n%s", line)
	}
}

func TestWriteLoop_HoldsARefusedEventBesideOneThatFailsTransientlyUntilAnEventFiles(t *testing.T) {
	refused, flaky := newEvent(""), newEvent("")
	var recovered atomic.Bool
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		switch {
		case containsEntry(entries, refused.ID):
			return errInvalidText
		case containsEntry(entries, flaky.ID) && !recovered.Load():
			return errStatementTimeout
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(refused), acks.pending(flaky))
	eventually(t, "both events to be retried", func() bool { return ledger.attemptCount() > 6 })
	if acks.total() != 0 || strings.Contains(logs.String(), droppedMsg) {
		t.Fatalf("an event was dropped although nothing filed:\n%s", logs.String())
	}
	if line := logLine(t, logs, loneRefusalMsg); !strings.Contains(line, "eventID="+refused.ID) {
		t.Errorf("want the hold to name the refused event:\n%s", line)
	}

	recovered.Store(true)

	eventually(t, "both events to be acked", func() bool { return acks.total() == 2 })
	if got := ledger.timesFiled(flaky.ID); got != 1 {
		t.Errorf("transiently failing event filed %d times, want 1", got)
	}
	if got := ledger.timesFiled(refused.ID); got != 0 {
		t.Errorf("refused event filed %d times, want 0", got)
	}
	if line := logLine(t, logs, droppedMsg); !strings.Contains(line, "eventID="+refused.ID) {
		t.Errorf("want the refused event dropped once the other filed:\n%s", line)
	}
}

func TestWriteLoop_EscalatesASecondRefusalBesideAHeldLoneEventToALedgerFault(t *testing.T) {
	first, second := newEvent(""), newEvent("")
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if containsEntry(entries, first.ID) || containsEntry(entries, second.ID) {
			return errInvalidText
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(first))
	eventually(t, "the lone event to be held", func() bool { return strings.Contains(logs.String(), loneRefusalMsg) })
	g.pending <- acks.pending(second)

	eventually(t, "the refusal of both to be reported as a ledger fault", func() bool {
		return strings.Contains(logs.String(), ledgerFaultMsg)
	})
	if line := logLine(t, logs, ledgerFaultMsg); !strings.Contains(line, "level=ERROR") || !strings.Contains(line, "events=2") {
		t.Errorf("want an ERROR counting both refused events:\n%s", line)
	}
	if acks.total() != 0 || strings.Contains(logs.String(), droppedMsg) {
		t.Fatalf("a refused event was dropped although nothing filed:\n%s", logs.String())
	}

	healthy := newEvent("")
	g.pending <- acks.pending(healthy)

	eventually(t, "every event to be acked", func() bool { return acks.total() == 3 })
	for _, e := range []spend.Event{first, second} {
		if got := ledger.timesFiled(e.ID); got != 0 {
			t.Errorf("refused event %s filed %d times, want 0", e.ID, got)
		}
		if !strings.Contains(logs.String(), "eventID="+e.ID) {
			t.Errorf("no log names dropped event %s:\n%s", e.ID, logs.String())
		}
	}
	if n := strings.Count(logs.String(), droppedMsg); n != 2 {
		t.Errorf("drops logged = %d, want 2:\n%s", n, logs.String())
	}
}

const ledgerFaultMsg = "the ledger refuses every event as invalid"

func TestWriteLoop_NamesEachLoneRefusedEventItHolds(t *testing.T) {
	first, second := newEvent(""), newEvent("")
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if containsEntry(entries, first.ID) || containsEntry(entries, second.ID) {
			return errInvalidText
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(first))
	eventually(t, "the first lone event to be held", func() bool { return strings.Contains(logs.String(), loneRefusalMsg) })
	g.pending <- acks.pending(newEvent(""))
	eventually(t, "the first lone event to be dropped", func() bool { return acks.count(first.ID) == 1 })
	g.pending <- acks.pending(second)

	eventually(t, "the second lone event to be held and named", func() bool {
		return strings.Count(logs.String(), loneRefusalMsg) == 2
	})
	var held []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, loneRefusalMsg) {
			held = append(held, line)
		}
	}
	if !strings.Contains(held[0], "eventID="+first.ID) || !strings.Contains(held[1], "eventID="+second.ID) {
		t.Errorf("want each held event named in turn:\n%s", strings.Join(held, "\n"))
	}
}

func TestWriteLoop_ReportsAnOutageWhenIsolatedEventsFailTransientlyBesideAHeldRefusal(t *testing.T) {
	refused, flaky := newEvent(""), newEvent("")
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if containsEntry(entries, refused.ID) {
			return errInvalidText
		}
		return errStatementTimeout
	}}
	logs := &logCapture{}
	quickAlert := fastTiming
	quickAlert.outageAlertAfter = 20 * time.Millisecond
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), quickAlert)
	acks := &ackLog{}

	runWriteLoop(t.Context(), t, g, acks.pending(refused), acks.pending(flaky))

	eventually(t, "the outage to be reported although every batch error was a refusal", func() bool {
		return strings.Contains(logs.String(), outageMsg)
	})
	if acks.total() != 0 {
		t.Errorf("acks = %d, want none while nothing files", acks.total())
	}
}

func TestWriteLoop_NamesAndCountsEachRefusedEventHeldBesideATransientFailure(t *testing.T) {
	first, second, flaky := newEvent(""), newEvent(""), newEvent("")
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if containsEntry(entries, first.ID) || containsEntry(entries, second.ID) {
			return errInvalidText
		}
		return errStatementTimeout
	}}
	metrics := &recordedMetrics{}
	logs := &logCapture{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)
	g.metrics = metrics
	acks := &ackLog{}

	runWriteLoop(t.Context(), t, g, acks.pending(first), acks.pending(second), acks.pending(flaky))
	eventually(t, "both refused events to be held and named", func() bool {
		return strings.Count(logs.String(), loneRefusalMsg) == 2
	})
	eventually(t, "the events to be retried", func() bool { return ledger.attemptCount() > 12 })

	holds := map[string]int{}
	for line := range strings.Lines(logs.String()) {
		for _, e := range []spend.Event{first, second} {
			if strings.Contains(line, loneRefusalMsg) && strings.Contains(line, "eventID="+e.ID) {
				holds[e.ID]++
			}
		}
	}
	if holds[first.ID] != 1 || holds[second.ID] != 1 {
		t.Errorf("hold WARNs per event = %v across retries, want one naming each:\n%s", holds, logs.String())
	}
	if got := metrics.count(metricEventsRefusalHeld); got != 2 {
		t.Errorf("%s = %d, want 2", metricEventsRefusalHeld, got)
	}
	if strings.Contains(logs.String(), ledgerFaultMsg) {
		t.Errorf("refusals beside a transient failure were reported as a ledger fault:\n%s", logs.String())
	}
}

func TestWriteLoop_IsolatesAPermanentlyRefusedBatchWithoutBackingOff(t *testing.T) {
	batch := events(maxBatch)
	poison := batch[7].ID
	var wholeBatchAttempts atomic.Int32
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if len(entries) > 1 {
			wholeBatchAttempts.Add(1)
		}
		if containsEntry(entries, poison) {
			return errInvalidText
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	neverRetry := fastTiming
	neverRetry.flushInterval = time.Hour
	neverRetry.retryBackoff = time.Hour
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), neverRetry)
	queued := make([]pendingEvent, len(batch))
	for i, e := range batch {
		queued[i] = acks.pending(e)
	}

	runWriteLoop(t.Context(), t, g, queued...)

	eventually(t, "every event to be acked", func() bool { return acks.total() == len(batch) })
	assertIsolated(t, batch, poison, ledger, acks, logs)
	if got := wholeBatchAttempts.Load(); got != 1 {
		t.Errorf("whole-batch attempts = %d, want 1: a permanent refusal is isolated at once", got)
	}
}

func TestWriteLoop_EscalatesATransientlyFailingBatchToIsolation(t *testing.T) {
	batch := events(maxBatch)
	poison := batch[42].ID
	var wholeBatchAttempts atomic.Int32
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		switch {
		case !containsEntry(entries, poison):
			return nil
		case len(entries) > 1:
			wholeBatchAttempts.Add(1)
			return errStatementTimeout
		default:
			return errInvalidText
		}
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	oneBatch := fastTiming
	oneBatch.flushInterval = time.Hour
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), oneBatch)
	queued := make([]pendingEvent, len(batch))
	for i, e := range batch {
		queued[i] = acks.pending(e)
	}

	runWriteLoop(t.Context(), t, g, queued...)

	eventually(t, "every event to be acked", func() bool { return acks.total() == len(batch) })
	assertIsolated(t, batch, poison, ledger, acks, logs)
	if got, want := wholeBatchAttempts.Load(), int32(oneBatch.attemptsBeforeIsolating); got != want {
		t.Errorf("whole-batch attempts = %d, want %d before isolating", got, want)
	}
}

func assertIsolated(t *testing.T, batch []spend.Event, poison string, ledger *scriptedLedger, acks *ackLog, logs *logCapture) {
	t.Helper()
	for _, e := range batch {
		wantFiled := 1
		if e.ID == poison {
			wantFiled = 0
		}
		if got := ledger.timesFiled(e.ID); got != wantFiled {
			t.Errorf("event %s filed %d times, want %d", e.ID, got, wantFiled)
		}
		if got := acks.count(e.ID); got != 1 {
			t.Errorf("event %s acked %d times, want 1", e.ID, got)
		}
	}
	logged := logs.String()
	if strings.Count(logged, droppedMsg) != 1 || !strings.Contains(logged, "eventID="+poison) {
		t.Errorf("want exactly one drop, logged for %s:\n%s", poison, logged)
	}
}

func TestWriteLoop_StoppingMidWriteAcksOnlyTheEventsAlreadyFiled(t *testing.T) {
	cases := map[string]struct {
		cancelOn func(entries []store.SpendLedgerEntry, first string) bool
		wantAck  bool
	}{
		"during the batch write": {
			cancelOn: func(entries []store.SpendLedgerEntry, _ string) bool { return len(entries) > 1 },
		},
		"during isolation": {
			cancelOn: func(entries []store.SpendLedgerEntry, first string) bool {
				return len(entries) == 1 && entries[0].ID != first
			},
			wantAck: true,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			batch := events(maxBatch)
			first := batch[0].ID
			ctx, cancel := context.WithCancel(t.Context())
			ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
				switch {
				case c.cancelOn(entries, first):
					cancel()
					return errInvalidText
				case len(entries) > 1:
					return errInvalidText
				}
				return nil
			}}
			logs := &logCapture{}
			acks := &ackLog{}
			oneBatch := fastTiming
			oneBatch.flushInterval = time.Hour
			g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), oneBatch)
			queued := make([]pendingEvent, len(batch))
			for i, e := range batch {
				queued[i] = acks.pending(e)
			}

			<-runWriteLoop(ctx, t, g, queued...)

			if got := acks.count(first) == 1; got != c.wantAck {
				t.Errorf("first event acked = %t, want %t", got, c.wantAck)
			}
			if got, want := acks.total(), ackCount(c.wantAck); got != want {
				t.Errorf("acks = %d, want %d: an event not filed must stay unacked", got, want)
			}
			if strings.Contains(logs.String(), droppedMsg) {
				t.Error("an event refused while the governor stopped was logged as dropped")
			}
		})
	}
}

func ackCount(acked bool) int {
	if acked {
		return 1
	}
	return 0
}

func TestGovernor_FilesAnEventOnceTheLedgerRecoversAndLosesNothing(t *testing.T) {
	var failuresLeft atomic.Int32
	failuresLeft.Store(1)
	var inserts atomic.Int32
	var real Ledger
	r := startRig(t, func(cfg *Config) {
		real = cfg.Ledger
		cfg.Ledger = ledgerFunc{
			insert: func(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error) {
				inserts.Add(1)
				if failuresLeft.Add(-1) >= 0 {
					return 0, errStatementTimeout
				}
				return real.Insert(ctx, entries)
			},
			delete: func(ctx context.Context, before time.Time, batch int) (int64, error) {
				return real.DeleteOlderThan(ctx, before, batch)
			},
		}
	})
	e := newEvent(r.models.flat.Alias)

	r.publishEvent(t, e)
	row := waitForRow(t, r.db, e.ID)

	if got := inserts.Load(); got < 2 {
		t.Errorf("ledger saw %d attempts, want a failed one then a retry", got)
	}
	assertCosts(t, row, spend.CostOf(e.Tokens, r.models.flatFor))
	if n := countRows(t, r.db); n != 1 {
		t.Errorf("ledger rows = %d, want 1", n)
	}
}

type ledgerFunc struct {
	insert        func(context.Context, []store.SpendLedgerEntry) (int64, error)
	delete        func(context.Context, time.Time, int) (int64, error)
	insertDetails func(context.Context, []store.SpendLedgerDetail) (int64, error)
	resolveNames  func(context.Context, store.ComponentTable, []string) (map[string]string, error)
}

func (l ledgerFunc) Insert(ctx context.Context, e []store.SpendLedgerEntry) (int64, error) {
	return l.insert(ctx, e)
}

func (l ledgerFunc) InsertDetails(ctx context.Context, d []store.SpendLedgerDetail) (int64, error) {
	if l.insertDetails == nil {
		return 0, nil
	}
	return l.insertDetails(ctx, d)
}

func (l ledgerFunc) ResolveNames(ctx context.Context, table store.ComponentTable, ids []string) (map[string]string, error) {
	if l.resolveNames == nil {
		return map[string]string{}, nil
	}
	return l.resolveNames(ctx, table, ids)
}

func (l ledgerFunc) DeleteOlderThan(ctx context.Context, before time.Time, n int) (int64, error) {
	return l.delete(ctx, before, n)
}

func TestWriteLoop_AcksAnEventOnlyAfterItIsFiled(t *testing.T) {
	acks := &ackLog{}
	e := newEvent("")
	acksWhenAttempted := make(chan int, 8)
	ledger := &scriptedLedger{refuse: func(attempt int, _ []store.SpendLedgerEntry) error {
		acksWhenAttempted <- acks.total()
		if attempt == 1 {
			return errStatementTimeout
		}
		return nil
	}}
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the event to be filed and acked", func() bool { return acks.count(e.ID) == 1 })
	close(acksWhenAttempted)
	for seen := range acksWhenAttempted {
		if seen != 0 {
			t.Errorf("an attempt began with %d acks already sent, want 0 before the event is filed", seen)
		}
	}
	if ledger.timesFiled(e.ID) != 1 {
		t.Error("the event was acked without being filed")
	}
	if got := ledger.attemptCount(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestWriteLoop_ShutdownWhileTheLedgerIsDownLeavesTheEventUnacked(t *testing.T) {
	acks := &ackLog{}
	attempted := make(chan struct{}, 1)
	ledger := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error {
		select {
		case attempted <- struct{}{}:
		default:
		}
		return errStatementTimeout
	}}
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)
	ctx, cancel := context.WithCancel(t.Context())
	done := runWriteLoop(ctx, t, g, acks.pending(newEvent("")))
	select {
	case <-attempted:
	case <-time.After(waitTimeout):
		t.Fatal("the ledger was never attempted")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("the write loop did not stop while retrying")
	}
	if got := acks.total(); got != 0 {
		t.Errorf("acks = %d, want 0: the broker must redeliver an unfiled event", got)
	}
}

func TestModelConfigPrices_ADamagedStoredPriceFilesUnpricedWithItsConfig(t *testing.T) {
	db := store.TestDB(t)
	if db.Dialect() == "postgres" {
		t.Skip("postgres refuses to store a half-set price, so there is no damaged row to read")
	}
	half := int64(30_000)
	mc := store.ModelConfiguration{
		Alias: "damaged", Provider: "openai", ModelName: "m", ModelAuthType: "apikey",
		ModelAuthConfig: `{}`, ModelParams: `{}`, CreatedBy: "test", UpdatedBy: "test", PriceInput: &half,
	}
	if err := store.NewModelConfigRepo(db).Create(t.Context(), &mc); err != nil {
		t.Fatalf("create: %v", err)
	}
	logs := &logCapture{}
	prices := NewModelConfigPrices(store.NewModelConfigRepo(db), logs.logger().With("logger_name", "platform"))

	got, err := prices.Lookup(t.Context(), ModelRef{Key: "damaged"})

	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ConfigID != mc.ID || got.Price != nil {
		t.Errorf("Lookup = %+v, want the config id with no price", got)
	}
	logged := logs.String()
	if !strings.Contains(logged, "model price is invalid") || loggerNameOf(logged) != "logger_name=governor" {
		t.Errorf("want the invalid-price warning logged as the governor:\n%s", logged)
	}
}

func loggerNameOf(line string) string {
	idx := strings.LastIndex(line, "logger_name=")
	if idx < 0 {
		return ""
	}
	return strings.Fields(line[idx:])[0]
}

func TestGovernor_SurvivesAnIdlePollOnTheNetworkDevBroker(t *testing.T) {
	db := store.TestDB(t)
	models := seedModels(t, db)
	inner := broker.NewDevBroker()
	if err := inner.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	server := broker.NewDevBrokerServer(inner, discardLogger)
	addr, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start dev broker server: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })

	consumer := broker.NewNetworkDevBroker(addr, "governor", "governor-q", discardLogger)
	publisher := broker.NewNetworkDevBroker(addr, "publisher", "publisher-q", discardLogger)
	for _, c := range []*broker.NetworkDevBroker{consumer, publisher} {
		if connErr := c.Connect(t.Context()); connErr != nil {
			t.Fatalf("connect: %v", connErr)
		}
		t.Cleanup(func() { _ = c.Disconnect(context.Background()) })
	}

	idlePolled := make(chan struct{})
	var signalIdlePoll sync.Once
	observed := &tappedBroker{Broker: consumer, tap: func(msg *broker.Message, err error) (*broker.Message, error) {
		if msg == nil && err == nil {
			signalIdlePoll.Do(func() { close(idlePolled) })
		}
		return msg, err
	}}
	startGovernor(t, Config{
		Namespace: testNamespace,
		Broker:    observed,
		Ledger:    store.NewSpendLedgerRepo(db),
		Prices:    NewModelConfigPrices(store.NewModelConfigRepo(db), discardLogger),
		Logger:    discardLogger,
	})

	select {
	case <-idlePolled:
	case <-time.After(waitTimeout):
		t.Fatal("the governor never sat through an idle poll")
	}

	e := newEvent(models.flat.Alias)
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := publisher.PublishGuaranteed(t.Context(), a2a.SpendEventTopic(testNamespace), &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	waitForRow(t, db, e.ID)
}

func TestIsPermanent_RefusesOnlyWhatTheEventItselfCaused(t *testing.T) {
	pg := func(code string) error { return fmt.Errorf("insert: %w", &pgconn.PgError{Code: code}) }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"NUL byte in text (data exception)", pg("22021"), true},
		{"malformed uuid (data exception)", pg("22P02"), true},
		{"unique violation (integrity: the table changed under the governor)", pg("23505"), false},
		{"check violation (integrity: the table changed under the governor)", pg("23514"), false},
		{"not-null violation (integrity: a column added mid-rollout)", pg("23502"), false},
		{"price lookup that failed with a data exception", priceLookupError{pg("22P02")}, false},
		{"index row too large (program limit)", pg("54000"), true},
		{"undefined table after a bad deploy", pg("42P01"), false},
		{"insufficient privilege", pg("42501"), false},
		{"statement timeout", pg("57014"), false},
		{"connection failure", pg("08006"), false},
		{"serialization failure", pg("40001"), false},
		{"unrecognised plain error", errors.New("something unforeseen"), false},
		{"context cancelled", context.Canceled, false},
		{"no error", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanent(tc.err); got != tc.want {
				t.Errorf("isPermanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsPermanent_ARealSQLiteConstraintViolationIsPermanent(t *testing.T) {
	db := store.TestDB(t)
	if db.Dialect() != "sqlite" {
		t.Skip("exercises the SQLite error type")
	}
	repo := store.NewSpendLedgerRepo(db)
	entry := ledgerEntry(time.Now())
	if _, err := repo.Insert(t.Context(), []store.SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err := db.ExecContext(t.Context(), `INSERT INTO token_spend_ledger (id) VALUES (?)`, entry.ID)
	if err == nil {
		t.Fatal("inserting a row missing required columns succeeded")
	}
	if !isPermanent(err) {
		t.Errorf("isPermanent(%v) = false, want true for a SQLite constraint violation", err)
	}
}

func TestIsPermanent_ARealPostgresCheckViolationIsRetried(t *testing.T) {
	db := store.TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("exercises the error the pgx driver returns")
	}
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO token_spend_ledger (id, completed_time, user_sub, surface, model_name,
			uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, uncached_input_cost)
		 VALUES (?, 1, 'u', 'agent', 'm', 1, 1, 0, 0, 1)`, idgen.New())

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23514" {
		t.Fatalf("insert of a partly priced row: err = %v, want SQLSTATE 23514 from the cost-together check", err)
	}
	if isPermanent(err) {
		t.Errorf("isPermanent(%v) = true, want a check violation retried: the table changed under the governor", err)
	}
}

func TestWriteLoop_RetriesALoneEventTheLedgerRefusesForAnUnforeseenReason(t *testing.T) {
	undefinedTable := &pgconn.PgError{Code: "42P01", Message: "relation token_spend_ledger does not exist"}
	refusals := fastTiming.attemptsBeforeIsolating + 3
	ledger := &scriptedLedger{refuse: func(attempt int, _ []store.SpendLedgerEntry) error {
		if attempt <= refusals {
			return undefinedTable
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	e := newEvent("")
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the event to be acked once the ledger recovers", func() bool { return acks.count(e.ID) == 1 })
	if got := ledger.timesFiled(e.ID); got != 1 {
		t.Errorf("event filed %d times, want 1", got)
	}
	if strings.Contains(logs.String(), droppedMsg) {
		t.Error("an event refused for an unforeseen reason was dropped instead of retried")
	}
}

const nackedMsg = "spend event nacked: it keeps failing while other events file"

func TestWriteLoop_NacksAnEventThatKeepsFailingWhileTheRestOfTheStreamFiles(t *testing.T) {
	db := store.TestDB(t)
	real := store.NewSpendLedgerRepo(db)
	poison := newEvent("")
	poison.ModelName = "poison-model"
	ledger := ledgerFunc{
		insert: func(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error) {
			if containsEntry(entries, poison.ID) {
				return 0, errStatementTimeout
			}
			return real.Insert(ctx, entries)
		},
		delete: real.DeleteOlderThan,
	}
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runWriteLoop(ctx, t, g, acks.pending(poison))
	var stream []spend.Event
	for acks.nackCount(poison.ID) == 0 {
		if len(stream) == 10_000 {
			t.Fatal("the failing event was never nacked")
		}
		e := newEvent("")
		select {
		case g.pending <- acks.pending(e):
			stream = append(stream, e)
		case <-time.After(waitTimeout):
			t.Fatal("the failing event stopped the rest of the stream being received")
		}
		time.Sleep(time.Millisecond)
	}

	eventually(t, "the rest of the stream to be filed and acked", func() bool { return acks.total() == len(stream) })
	if n := countRows(t, db); n != len(stream) {
		t.Errorf("ledger rows = %d, want the %d streamed events", n, len(stream))
	}
	if acks.count(poison.ID) != 0 || acks.nackCount(poison.ID) != 1 {
		t.Errorf("failing event acked %d and nacked %d times, want 0 and 1", acks.count(poison.ID), acks.nackCount(poison.ID))
	}
	line := logLine(t, logs, nackedMsg)
	for _, want := range append([]string{
		"level=ERROR", "eventID=" + poison.ID, "SQLSTATE 57014", fmt.Sprintf("attempts=%d", fastTiming.failuresBeforeNack),
	}, spendFieldsOf(poison)...) {
		if !strings.Contains(line, want) {
			t.Errorf("nack log is missing %q:\n%s", want, line)
		}
	}
}

func TestWriteLoop_ALedgerOutageIsReportedOnceAndNacksNothing(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	var batchWrites, failedSingleWrites atomic.Int32
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if len(entries) > 1 {
			batchWrites.Add(1)
		}
		if !down.Load() {
			return nil
		}
		if len(entries) == 1 {
			failedSingleWrites.Add(1)
		}
		return errStatementTimeout
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	quickAlert := fastTiming
	quickAlert.outageAlertAfter = 20 * time.Millisecond
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), quickAlert)
	batch := events(quickAlert.failuresBeforeStoppingIsolation + 1)
	queued := make([]pendingEvent, len(batch))
	for i, e := range batch {
		queued[i] = acks.pending(e)
	}

	runWriteLoop(t.Context(), t, g, queued...)
	isolationPasses := 2 * quickAlert.failuresBeforeNack
	eventually(t, "the ledger outage to outlast the alert and the nack limit", func() bool {
		return int(batchWrites.Load()) >= quickAlert.attemptsBeforeIsolating+isolationPasses &&
			strings.Contains(logs.String(), outageMsg)
	})
	down.Store(false)
	eventually(t, "every event to be filed once the ledger recovers", func() bool { return acks.total() == len(batch) })

	if n := acks.totalNacks(); n != 0 {
		t.Errorf("nacks = %d, want none: an outage is no event's fault", n)
	}
	passes := int(batchWrites.Load()) - quickAlert.attemptsBeforeIsolating + 1
	if limit := passes * quickAlert.failuresBeforeStoppingIsolation; int(failedSingleWrites.Load()) > limit {
		t.Errorf("failed single writes = %d over %d isolation passes, want at most %d: a pass stops once writes keep failing",
			failedSingleWrites.Load(), passes, limit)
	}
	logged := logs.String()
	if n := strings.Count(logged, outageMsg); n != 1 {
		t.Errorf("outage errors = %d, want 1 for the whole outage:\n%s", n, logged)
	}
	if line := logLine(t, logs, outageMsg); !strings.Contains(line, "level=ERROR") || !strings.Contains(line, "elapsed=") {
		t.Errorf("want the outage at ERROR with how long it lasted:\n%s", line)
	}
	recovered := logLine(t, logs, "spend ledger is filing again")
	if !strings.Contains(recovered, "level=INFO") {
		t.Errorf("want the recovery at INFO:\n%s", recovered)
	}
	if strings.Index(logged, outageMsg) > strings.Index(logged, "spend ledger is filing again") {
		t.Error("the recovery was logged before the outage")
	}
}

const outageMsg = "spend has not been filed for a while; unfiled spend stays in a durable queue, a dev broker drops it once its queue fills"

const unknownKeyMsg = "model matches no configuration; its spend is filed unpriced"

func TestWriteLoop_WarnsOfAnUnknownModelKeyOncePerInterval(t *testing.T) {
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, logs.logger(), fastTiming)
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	g.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	runWriteLoop(t.Context(), t, g)
	file := func(key string) spend.Event {
		e := newEvent(key)
		g.pending <- acks.pending(e)
		eventually(t, "the event to be filed", func() bool { return acks.count(e.ID) == 1 })
		return e
	}

	first := file("ghost")
	file("ghost")
	file("ghost")
	file("phantom")
	clock.Add(fastTiming.repeatWarnAfter.Milliseconds())
	afterInterval := file("ghost")

	logged := logs.String()
	if n := strings.Count(logged, "modelKey=ghost"); n != 2 {
		t.Errorf("warnings for ghost = %d, want 2: once, then again after the interval:\n%s", n, logged)
	}
	if n := strings.Count(logged, "modelKey=phantom"); n != 1 {
		t.Errorf("warnings for phantom = %d, want 1: each key is throttled on its own:\n%s", n, logged)
	}
	for _, e := range []spend.Event{first, afterInterval} {
		if line := logLine(t, logs, "eventID="+e.ID); !strings.Contains(line, "level=WARN") ||
			!strings.Contains(line, unknownKeyMsg) || !strings.Contains(line, "traceID="+e.TraceID) {
			t.Errorf("want the warning at WARN with the event context:\n%s", line)
		}
	}
}

func TestLogThrottle_ForgetsAKeyOnlyOnceItsIntervalHasPassed(t *testing.T) {
	throttle := newLogThrottle(time.Minute)
	start := time.Now()
	for i := range maxThrottledKeys {
		if !throttle.allow(fmt.Sprint(i), start) {
			t.Fatalf("key %d was held back before the throttle filled", i)
		}
	}

	if !throttle.allow("one too many", start.Add(time.Second)) {
		t.Error("the first key past the limit was held back: keys past the limit share one overflow key, logged once per interval")
	}
	if ok, _ := throttle.admit("another past the limit", start.Add(2*time.Second)); ok {
		t.Error("a second key past the limit was let through within the overflow key's interval")
	}
	if ok, suppressed := throttle.admit("one too many", start.Add(time.Minute)); !ok || suppressed != 0 {
		t.Errorf("admit after the remembered keys aged out = %v, %d; want the key let through on its own", ok, suppressed)
	}
	if throttle.allow("one too many", start.Add(time.Minute+time.Second)) {
		t.Error("the new key was let through again within its own interval")
	}
}

func (a *ackLog) delivered(t *testing.T, e spend.Event) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	p := a.pending(e)
	p.msg.Payload = payload
	return p.msg
}

func TestWriteLoop_ANackedEventRedeliveredIsIsolatedAtOnceAndNackedAfterOneMorePass(t *testing.T) {
	poison := newEvent("")
	var wholeBatchAttemptsWithPoison atomic.Int32
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if !containsEntry(entries, poison.ID) {
			return nil
		}
		if len(entries) > 1 {
			wholeBatchAttemptsWithPoison.Add(1)
		}
		return errStatementTimeout
	}}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)
	g.nacked.add(poison.ID)
	healthy := events(3)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runWriteLoop(ctx, t, g)

	enqueue := g.enqueue(ctx)
	enqueue(acks.delivered(t, poison))
	for _, e := range healthy {
		enqueue(acks.delivered(t, e))
	}

	eventually(t, "the redelivered event to be nacked again", func() bool { return acks.nackCount(poison.ID) == 1 })
	eventually(t, "the healthy events to be filed", func() bool { return acks.total() == len(healthy) })
	if got := wholeBatchAttemptsWithPoison.Load(); got > 1 {
		t.Errorf("whole-batch attempts with the returning event = %d, want at most 1: it is isolated at once", got)
	}
}

func TestWriteLoop_HoldsABatchTheLedgerRefusesEntirelyAsAFaultRatherThanDroppingIt(t *testing.T) {
	var accepting atomic.Bool
	ledger := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error {
		if accepting.Load() {
			return nil
		}
		return errInvalidText
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)
	batch := events(3)
	queued := make([]pendingEvent, len(batch))
	for i, e := range batch {
		queued[i] = acks.pending(e)
	}

	runWriteLoop(t.Context(), t, g, queued...)
	eventually(t, "the refusal of every event to be reported", func() bool {
		return strings.Contains(logs.String(), "the ledger refuses every event as invalid")
	})
	accepting.Store(true)

	eventually(t, "every event to be filed once the ledger accepts again", func() bool { return acks.total() == len(batch) })
	if strings.Contains(logs.String(), droppedMsg) {
		t.Errorf("an event was dropped although the ledger refused every one:\n%s", logs.String())
	}
	for _, e := range batch {
		if got := ledger.timesFiled(e.ID); got != 1 {
			t.Errorf("event %s filed %d times, want 1", e.ID, got)
		}
	}
}

func TestWriteLoop_AFlushFilesEveryEventAlreadyWaiting(t *testing.T) {
	busyTicker := fastTiming
	busyTicker.flushInterval = time.Microsecond
	for range 20 {
		var mu sync.Mutex
		var sizes []int
		ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
			mu.Lock()
			sizes = append(sizes, len(entries))
			mu.Unlock()
			return nil
		}}
		acks := &ackLog{}
		g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, busyTicker)
		batch := events(3)
		queued := make([]pendingEvent, len(batch))
		for i, e := range batch {
			queued[i] = acks.pending(e)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := runWriteLoop(ctx, t, g, queued...)

		eventually(t, "every event to be filed", func() bool { return acks.total() == len(batch) })
		cancel()
		<-done
		mu.Lock()
		writes := slices.Clone(sizes)
		mu.Unlock()
		if !slices.Equal(writes, []int{len(batch)}) {
			t.Fatalf("writes = %v, want one write of all %d waiting events", writes, len(batch))
		}
	}
}

func TestWriteLoop_FilesALoneEventOnceALedgerThatRefusedEverythingAcceptsAgain(t *testing.T) {
	var accepting atomic.Bool
	ledger := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error {
		if accepting.Load() {
			return nil
		}
		return errInvalidText
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	e := newEvent("")
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(e))
	eventually(t, "the lone event to be refused more than once", func() bool { return ledger.attemptCount() > 2 })
	accepting.Store(true)

	eventually(t, "the event to be filed once the ledger accepts again", func() bool { return acks.count(e.ID) == 1 })
	if got := ledger.timesFiled(e.ID); got != 1 {
		t.Errorf("event filed %d times, want 1", got)
	}
	if strings.Contains(logs.String(), droppedMsg) {
		t.Errorf("a lone event was dropped while the ledger refused everything:\n%s", logs.String())
	}
}

func TestCountFailure_RestartsTheCountWhenTheErrorChanges(t *testing.T) {
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, discardLogger, fastTiming)
	f := failedWrite{err: errStatementTimeout}

	g.countFailure(&f, true)
	g.countFailure(&f, true)
	f.err = errors.New("connection pool exhausted")
	g.countFailure(&f, true)
	g.countFailure(&f, false)

	if f.failures != 1 {
		t.Errorf("failures = %d, want 1: a new error restarts the count and a pass where nothing filed adds none", f.failures)
	}
}

func TestWriteLoop_APriceLookupDataExceptionIsRetriedNotDropped(t *testing.T) {
	var lookups atomic.Int32
	prices := pricesFunc(func(context.Context, ModelRef) (Model, error) {
		if lookups.Add(1) <= 2 {
			return Model{}, &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type uuid"}
		}
		return Model{ConfigID: "config-1"}, nil
	})
	ledger := &scriptedLedger{}
	logs := &logCapture{}
	acks := &ackLog{}
	e := newEvent("some-model")
	g := newWriteLoopGovernor(ledger, prices, logs.logger(), fastTiming)

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the event to be filed", func() bool { return acks.count(e.ID) == 1 })
	if ledger.timesFiled(e.ID) != 1 || strings.Contains(logs.String(), droppedMsg) {
		t.Errorf("filed %d times, logs:\n%s\nwant filed once and never dropped", ledger.timesFiled(e.ID), logs.String())
	}
	if !strings.Contains(logs.String(), "resolve model price") {
		t.Errorf("the retry does not name the price lookup as the cause:\n%s", logs.String())
	}
}

func TestWriteLoop_StoppingWithTheLedgerDownReportsHowMuchIsUnfiled(t *testing.T) {
	logs := &logCapture{}
	never := fastTiming
	never.flushInterval = time.Hour
	down := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error { return errors.New("database unavailable") }}
	g := newWriteLoopGovernor(down, fakePrices{}, logs.logger(), never)
	acks := &ackLog{}
	ctx, cancel := context.WithCancel(t.Context())
	done := runWriteLoop(ctx, t, g, acks.pending(newEvent("")), acks.pending(newEvent("")))

	cancel()
	<-done

	line := logLine(t, logs, "governor stopped with spend unfiled")
	if !strings.Contains(line, "level=WARN") || !strings.Contains(line, "events=2") {
		t.Errorf("want a WARN counting the 2 unfiled events:\n%s", line)
	}
	if acks.total() != 0 {
		t.Errorf("acked %d events at stop, want none: unfiled events must be redelivered", acks.total())
	}
}

func TestGovernor_KeepsFilingAfterTheContextItStartedWithIsCancelled(t *testing.T) {
	db := store.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	g := New(Config{
		Namespace: testNamespace, Broker: brk, Logger: discardLogger,
		Ledger: store.NewSpendLedgerRepo(db), Prices: fakePrices{},
	})
	g.setTiming(fastTiming)
	startCtx, cancelStart := context.WithCancel(t.Context())
	if err := g.Start(startCtx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })
	cancelStart()

	r := &rig{db: db, broker: brk, governor: g}
	e := newEvent("")
	r.publishEvent(t, e)
	waitForRow(t, db, e.ID)
}

func TestWriteLoop_ADropOfTheSameKindIsLoggedOncePerIntervalAndCounted(t *testing.T) {
	logs := &logCapture{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, logs.logger(), fastTiming)
	acks := &ackLog{}
	ownerless := func() *broker.Message {
		e := newEvent("")
		e.UserSub = ""
		return acks.delivered(t, e)
	}

	for range 3 {
		g.enqueue(t.Context())(ownerless())
	}

	if n := strings.Count(logs.String(), "spend event dropped: invalid"); n != 1 {
		t.Errorf("drop errors = %d, want 1 within the interval:\n%s", n, logs.String())
	}
	if acks.total() != 3 {
		t.Errorf("acked %d of 3 invalid events, want every one", acks.total())
	}
	g.dropWarnings = newLogThrottle(0)
	g.enqueue(t.Context())(ownerless())
	g.enqueue(t.Context())(ownerless())
	if !strings.Contains(logs.String(), "suppressed=0") {
		t.Errorf("want a suppressed count on the drop log:\n%s", logs.String())
	}
}

func TestEntryFor_ModelNameFallsBackToTheKeyWhenNothingElseNamesTheModel(t *testing.T) {
	e := newEvent("unknown-alias")
	e.ModelName = ""

	if got := entryFor(e, Model{}); got.ModelConfigID != nil || deref(got.ModelName) != "unknown-alias" {
		t.Errorf("model_config_id, model_name = %s, %s; want NULL and the model key", deref(got.ModelConfigID), deref(got.ModelName))
	}
}

func TestWriteLoop_WarnsOncePerIntervalOfAModelFiledUnpricedForHavingNoKey(t *testing.T) {
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, logs.logger(), fastTiming)
	batch := events(3)
	queued := make([]pendingEvent, len(batch))
	for i, e := range batch {
		queued[i] = acks.pending(e)
	}

	runWriteLoop(t.Context(), t, g, queued...)

	eventually(t, "every event to be filed", func() bool { return acks.total() == len(batch) })
	line := logLine(t, logs, "spend names no model key")
	if n := strings.Count(logs.String(), "spend names no model key"); n != 1 || !strings.Contains(line, "level=WARN") ||
		!strings.Contains(line, "modelName=provider-model") {
		t.Errorf("want one WARN naming the model:\n%s", logs.String())
	}
}

func TestWriteLoop_ARetriedBatchToppedUpWithWaitingEventsNeverExceedsTheBatchLimit(t *testing.T) {
	poison := newEvent("")
	var largest atomic.Int32
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		for n := int32(len(entries)); n > largest.Load(); {
			if largest.CompareAndSwap(largest.Load(), n) {
				break
			}
		}
		if containsEntry(entries, poison.ID) {
			return errStatementTimeout
		}
		return nil
	}}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)
	queued := []pendingEvent{acks.pending(poison)}
	for _, e := range events(2*maxBatch - 1) {
		queued = append(queued, acks.pending(e))
	}

	runWriteLoop(t.Context(), t, g, queued...)

	eventually(t, "every other event to be filed", func() bool { return acks.total() == len(queued)-1 })
	if got := largest.Load(); got > maxBatch {
		t.Errorf("largest write = %d entries, want at most the batch limit %d", got, maxBatch)
	}
}

func TestWriteLoop_IsolationThatFilesEventsIsNotReportedAsAnOutage(t *testing.T) {
	poison := newEvent("")
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if containsEntry(entries, poison.ID) {
			return errStatementTimeout
		}
		return nil
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	slowPasses := fastTiming
	slowPasses.outageAlertAfter = 35 * time.Millisecond
	slowPasses.retryBackoff = 10 * time.Millisecond
	slowPasses.maxRetryBackoff = 10 * time.Millisecond
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), slowPasses)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runWriteLoop(ctx, t, g, acks.pending(poison))

	for acks.nackCount(poison.ID) == 0 {
		select {
		case g.pending <- acks.pending(newEvent("")):
		case <-time.After(waitTimeout):
			t.Fatal("the failing event stopped the stream being received")
		}
		time.Sleep(time.Millisecond)
	}

	if strings.Contains(logs.String(), outageMsg) {
		t.Errorf("isolation that kept filing events was reported as an outage:\n%s", logs.String())
	}
}

type nackRig struct {
	g      *Governor
	acks   *ackLog
	poison spend.Event

	poisonAttempts   atomic.Int32
	wholeBatchPoison atomic.Int32
}

func newNackRig(t *testing.T) *nackRig {
	t.Helper()
	r := &nackRig{acks: &ackLog{}, poison: newEvent("")}
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if !containsEntry(entries, r.poison.ID) {
			return nil
		}
		r.poisonAttempts.Add(1)
		if len(entries) > 1 {
			r.wholeBatchPoison.Add(1)
		}
		r.g.pending <- r.acks.pending(newEvent(""))
		return errStatementTimeout
	}}
	r.g = newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)
	return r
}

func (r *nackRig) fileUntilNacked(ctx context.Context) {
	r.g.file(ctx, []pendingEvent{r.acks.pending(r.poison), r.acks.pending(newEvent(""))})
}

func TestWriteLoop_AnEventItNackedIsIsolatedAtOnceWhenTheBrokerRedeliversIt(t *testing.T) {
	r := newNackRig(t)
	r.fileUntilNacked(t.Context())
	if got := r.acks.nackCount(r.poison.ID); got != 1 {
		t.Fatalf("failing event nacked %d times, want 1", got)
	}
	wholeBatchBefore := r.wholeBatchPoison.Load()

	enqueue := r.g.enqueue(t.Context())
	enqueue(r.acks.delivered(t, r.poison))
	enqueue(r.acks.delivered(t, newEvent("")))
	r.g.file(t.Context(), r.g.topUp(nil, maxBatch))

	if got := r.acks.nackCount(r.poison.ID); got != 2 {
		t.Errorf("redelivered event nacked %d times in total, want 2", got)
	}
	if got := r.wholeBatchPoison.Load() - wholeBatchBefore; got > 1 {
		t.Errorf("whole-batch attempts with the redelivered event = %d, want at most 1: it is isolated at once", got)
	}
}

type recordedMetrics struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (m *recordedMetrics) Counter(name string, delta int64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = map[string]int64{}
	}
	m.counts[name+"{"+strings.Join(labels, ",")+"}"] += delta
}

func (m *recordedMetrics) Gauge(string, float64, ...string)           {}
func (m *recordedMetrics) Histogram(string, time.Duration, ...string) {}

func (m *recordedMetrics) count(name string, labels ...string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[name+"{"+strings.Join(labels, ",")+"}"]
}

func TestGovernor_CountsEachKindOfDroppedEventByReason(t *testing.T) {
	metrics := &recordedMetrics{}
	acks := &ackLog{}
	refused := newEvent("")
	ledger := &scriptedLedger{refuse: func(_ int, entries []store.SpendLedgerEntry) error {
		if containsEntry(entries, refused.ID) {
			return errInvalidText
		}
		return nil
	}}
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)
	g.metrics = metrics
	enqueue := g.enqueue(t.Context())

	notJSON := acks.delivered(t, newEvent(""))
	notJSON.Payload = []byte(`{"id": "truncated`)
	enqueue(notJSON)
	noUser := newEvent("")
	noUser.UserSub = ""
	enqueue(acks.delivered(t, noUser))
	enqueue(acks.delivered(t, refused))
	enqueue(acks.delivered(t, newEvent("")))
	g.file(t.Context(), g.topUp(nil, maxBatch))

	for reason, want := range map[string]int64{"not_decodable": 1, "invalid": 1, "refused": 1} {
		if got := metrics.count(metricEventsDropped, "reason", reason); got != want {
			t.Errorf("%s{reason=%s} = %d, want %d", metricEventsDropped, reason, got, want)
		}
	}
}

func TestGovernor_CountsEachEventItHoldsForANewerGovernor(t *testing.T) {
	metrics := &recordedMetrics{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, discardLogger, fastTiming)
	g.metrics = metrics
	e := newEvent("")
	msg := acks.delivered(t, e)
	msg.Payload = withSchemaVersion(t, e, spend.EventSchemaVersion+1)

	g.enqueue(t.Context())(msg)
	g.enqueue(t.Context())(msg)

	if got := metrics.count(metricEventsHeld); got != 2 {
		t.Errorf("%s = %d, want 2", metricEventsHeld, got)
	}
}

func TestGovernor_CountsALoneRefusedEventItHoldsOnce(t *testing.T) {
	metrics := &recordedMetrics{}
	e := newEvent("")
	ledger := &scriptedLedger{refuse: func(int, []store.SpendLedgerEntry) error { return errInvalidText }}
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)
	g.metrics = metrics

	runWriteLoop(t.Context(), t, g, (&ackLog{}).pending(e))
	eventually(t, "the lone event to be refused repeatedly", func() bool { return ledger.attemptCount() > 6 })

	if got := metrics.count(metricEventsRefusalHeld); got != 1 {
		t.Errorf("%s = %d across the event's retries, want 1", metricEventsRefusalHeld, got)
	}
}

func TestGovernor_CountsAnEventItHandsBackToTheBroker(t *testing.T) {
	metrics := &recordedMetrics{}
	r := newNackRig(t)
	r.g.metrics = metrics

	r.fileUntilNacked(t.Context())

	if got := metrics.count(metricEventsNacked); got != 1 {
		t.Errorf("%s = %d, want 1", metricEventsNacked, got)
	}
}

func TestGovernor_CountsTheEventsItFilesUnpriced(t *testing.T) {
	metrics := &recordedMetrics{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, discardLogger, fastTiming)
	g.metrics = metrics
	inline, alsoInline := newEvent(""), newEvent("")

	g.file(t.Context(), []pendingEvent{acks.pending(inline), acks.pending(alsoInline)})

	if got := metrics.count(metricEventsUnpriced); got != 2 {
		t.Errorf("%s = %d, want the 2 events with no model key", metricEventsUnpriced, got)
	}
}

func TestGovernor_AnUndecodableEventsLogNamesTheFieldButNeverItsValue(t *testing.T) {
	const secret = "SECRET-TOKEN-COUNT"
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(&scriptedLedger{}, fakePrices{}, logs.logger(), fastTiming)
	e := newEvent("")
	msg := acks.delivered(t, e)
	msg.Payload = []byte(fmt.Sprintf(`{"id":%q,"tokens":{"uncached_input":%q}}`, e.ID, secret))

	g.enqueue(t.Context())(msg)

	if acks.count(e.ID) != 1 {
		t.Error("an undecodable event was not acked away")
	}
	line := logLine(t, logs, "spend event dropped: not decodable")
	for _, want := range []string{"level=ERROR", "uncached_input", "wantType=int64"} {
		if !strings.Contains(line, want) {
			t.Errorf("undecodable drop log is missing %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, secret) || strings.Contains(line, "error=") {
		t.Errorf("undecodable drop log echoes the payload:\n%s", line)
	}
}

func TestWriteLoop_StoppingWithTheLedgerUpFilesAndAcksWhatIsPending(t *testing.T) {
	logs := &logCapture{}
	never := fastTiming
	never.flushInterval = time.Hour
	ledger := &scriptedLedger{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), never)
	acks := &ackLog{}
	pending := events(3)
	var queued []pendingEvent
	for _, e := range pending {
		queued = append(queued, acks.pending(e))
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := runWriteLoop(ctx, t, g, queued...)

	cancel()
	<-done

	for _, e := range pending {
		if ledger.timesFiled(e.ID) != 1 || acks.count(e.ID) != 1 {
			t.Errorf("event %s filed %d times, acked %d times; want once each", e.ID, ledger.timesFiled(e.ID), acks.count(e.ID))
		}
	}
	if strings.Contains(logs.String(), "governor stopped with spend unfiled") {
		t.Errorf("a stop that filed everything warned of unfiled spend:\n%s", logs.String())
	}
}

func TestGovernor_StopFilesAnEventReceivedBeforeTheFlushInterval(t *testing.T) {
	db := store.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	d := &deliveries{}
	slow := fastTiming
	slow.flushInterval = time.Hour
	g := New(Config{
		Namespace: testNamespace,
		Broker:    &tappedBroker{Broker: brk, tap: d.tap},
		Ledger:    store.NewSpendLedgerRepo(db),
		Prices:    NewModelConfigPrices(store.NewModelConfigRepo(db), discardLogger),
		Logger:    discardLogger,
	})
	g.setTiming(slow)
	if err := g.Start(t.Context()); err != nil {
		t.Fatalf("start governor: %v", err)
	}
	e := newEvent("")
	r := &rig{broker: brk}
	r.publishEvent(t, e)
	eventually(t, "the event to be received", func() bool { return len(d.snapshot()) == 1 })
	time.Sleep(100 * time.Millisecond)
	stopCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if err := g.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if readRow(t, db, e.ID) == nil {
		t.Error("an event received before Stop was not filed by it")
	}
	if !d.allAcked(1) {
		t.Error("the filed event was not acked")
	}
}

type stoppingReceive struct{}

type receivesOnlyWhileStopping struct {
	broker.Broker
}

func (b receivesOnlyWhileStopping) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	if ctx.Value(stoppingReceive{}) == nil {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return b.Broker.Receive(ctx, q)
}

func TestGovernor_StopFilesTheEventsAlreadyDeliveredToItsQueue(t *testing.T) {
	db := store.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	r := &rig{db: db, broker: brk, models: seedModels(t, db)}
	patient := fastTiming
	patient.deliveredIdle = 2 * time.Second
	patient.drainTimeout = 10 * time.Second
	r.governor = New(Config{
		Namespace: testNamespace,
		Broker:    receivesOnlyWhileStopping{Broker: brk},
		Ledger:    store.NewSpendLedgerRepo(db),
		Prices:    NewModelConfigPrices(store.NewModelConfigRepo(db), discardLogger),
		Logger:    discardLogger,
	})
	r.governor.setTiming(patient)
	if err := r.governor.Start(t.Context()); err != nil {
		t.Fatalf("start governor: %v", err)
	}
	delivered := events(3)
	for _, e := range delivered {
		r.publishEvent(t, e)
	}

	if err := r.governor.Stop(context.WithValue(t.Context(), stoppingReceive{}, true)); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	for _, e := range delivered {
		if readRow(t, r.db, e.ID) == nil {
			t.Errorf("event %s waiting on the queue at Stop was not filed", e.ID)
		}
	}
}

func TestWriteLoop_AHungLedgerWriteFailsAtTheWriteTimeoutAndIsRetried(t *testing.T) {
	var hung atomic.Bool
	hung.Store(true)
	inner := &scriptedLedger{}
	ledger := ledgerFunc{insert: func(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error) {
		if hung.Load() {
			<-ctx.Done()
			return 0, ctx.Err()
		}
		return inner.Insert(ctx, entries)
	}}
	logs := &logCapture{}
	acks := &ackLog{}
	quick := fastTiming
	quick.writeTimeout = 20 * time.Millisecond
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), quick)
	e := newEvent("")

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the hung write to fail at its deadline", func() bool {
		return strings.Contains(logs.String(), "spend batch not filed") && strings.Contains(logs.String(), "context deadline exceeded")
	})
	if acks.total() != 0 {
		t.Error("an event was acked before it was filed")
	}
	hung.Store(false)
	eventually(t, "the event to be filed once the ledger stops hanging", func() bool { return acks.count(e.ID) == 1 })
	if inner.timesFiled(e.ID) != 1 {
		t.Errorf("event filed %d times, want 1", inner.timesFiled(e.ID))
	}
}

func TestWriteLoop_AHungPriceLookupFailsAtTheWriteTimeoutAndIsRetried(t *testing.T) {
	var hung atomic.Bool
	hung.Store(true)
	prices := pricesFunc(func(ctx context.Context, _ ModelRef) (Model, error) {
		if hung.Load() {
			<-ctx.Done()
			return Model{}, ctx.Err()
		}
		return Model{}, nil
	})
	ledger := &scriptedLedger{}
	logs := &logCapture{}
	acks := &ackLog{}
	quick := fastTiming
	quick.writeTimeout = 20 * time.Millisecond
	g := newWriteLoopGovernor(ledger, prices, logs.logger(), quick)
	e := newEvent("hung-model")

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the hung lookup to fail at its deadline", func() bool {
		return strings.Contains(logs.String(), "spend batch not filed") && strings.Contains(logs.String(), "context deadline exceeded")
	})
	if acks.total() != 0 || ledger.attemptCount() != 0 {
		t.Errorf("acks = %d, ledger attempts = %d; want neither while the price cannot be resolved", acks.total(), ledger.attemptCount())
	}
	hung.Store(false)
	eventually(t, "the event to be filed once the lookup recovers", func() bool { return acks.count(e.ID) == 1 })
	if ledger.timesFiled(e.ID) != 1 {
		t.Errorf("event filed %d times, want 1", ledger.timesFiled(e.ID))
	}
}
