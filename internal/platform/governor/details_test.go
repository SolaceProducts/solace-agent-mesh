// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type detailRow struct {
	ID                     string  `db:"id"`
	ModelKey               *string `db:"model_key"`
	SentModelName          *string `db:"sent_model_name"`
	ToolName               *string `db:"tool_name"`
	WorkflowNodeID         *string `db:"workflow_node_id"`
	ParentTaskID           *string `db:"parent_task_id"`
	SessionID              *string `db:"session_id"`
	ResolvedModelName      *string `db:"resolved_model_name"`
	ResolvedModelProvider  *string `db:"resolved_model_provider"`
	ResolvedAgentName      *string `db:"resolved_agent_name"`
	ResolvedWorkflowName   *string `db:"resolved_workflow_name"`
	ResolvedRootName       *string `db:"resolved_root_name"`
	ResolvedEntrypointName *string `db:"resolved_entrypoint_name"`
	AttemptCount           *int    `db:"attempt_count"`
	CacheWritesReported    *bool   `db:"cache_writes_reported"`
}

const detailColumns = `id, model_key, sent_model_name, tool_name, workflow_node_id,
	parent_task_id, session_id, resolved_model_name, resolved_model_provider, resolved_agent_name,
	resolved_workflow_name, resolved_root_name, resolved_entrypoint_name, attempt_count,
	cache_writes_reported`

func readDetail(t *testing.T, db *store.DB, id string) *detailRow {
	t.Helper()
	row, err := dbutil.QueryRow[detailRow](t.Context(), db,
		`SELECT `+detailColumns+` FROM token_spend_ledger_details WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("read detail row %s: %v", id, err)
	}
	return row
}

func countDetails(t *testing.T, db *store.DB) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM token_spend_ledger_details`)
	if err != nil {
		t.Fatalf("count detail rows: %v", err)
	}
	return n
}

func waitForDetail(t *testing.T, db *store.DB, id string) *detailRow {
	t.Helper()
	var row *detailRow
	eventually(t, "details of "+id+" to be written", func() bool {
		row = readDetail(t, db, id)
		return row != nil
	})
	return row
}

type components struct {
	agent, rootAgent, workflow, rootWorkflow, gateway string
}

func seedComponents(t *testing.T, db *store.DB) components {
	t.Helper()
	ctx := t.Context()
	agent := func(name string) string {
		a := &store.Agent{Name: name, CreatedBy: "test", UpdatedBy: "test"}
		if err := store.NewAgentRepo(db).Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", name, err)
		}
		return a.ID
	}
	workflow := func(name string) string {
		w := &store.Workflow{Name: name, AppConfig: "{}", CreatedBy: "test", UpdatedBy: "test"}
		if err := store.NewWorkflowRepo(db).Create(ctx, w, store.WorkflowBindings{}); err != nil {
			t.Fatalf("create workflow %s: %v", name, err)
		}
		return w.ID
	}
	gw := &store.Gateway{Name: "the-gateway", Type: "webhook", CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewGatewayRepo(db).Create(ctx, gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return components{
		agent: agent("the-agent"), rootAgent: agent("the-root-agent"),
		workflow: workflow("the-workflow"), rootWorkflow: workflow("the-root-workflow"),
		gateway: gw.ID,
	}
}

func TestGovernor_WritesEveryDetailAndResolvesTheNamesOfUUIDKeyedComponents(t *testing.T) {
	r := startRig(t)
	c := seedComponents(t, r.db)
	e := newEvent(r.models.cacheBrokenOut.Alias)
	e.AgentID, e.WorkflowID, e.EntrypointID = c.agent, c.workflow, c.gateway
	e.RootKind, e.RootID = spend.RootAgent, c.rootAgent
	e.WorkflowNodeID, e.RootNodeID, e.ToolName = "node-1", "root-node", "search"
	e.ParentTaskID, e.SessionID = "parent-task-1", "session-1"
	rootedAtAWorkflow := newEvent(r.models.flat.Alias)
	rootedAtAWorkflow.RootKind, rootedAtAWorkflow.RootID = spend.RootWorkflow, c.rootWorkflow

	r.publishEvent(t, e)
	r.publishEvent(t, rootedAtAWorkflow)
	d := waitForDetail(t, r.db, e.ID)

	assertColumns(t, map[string][2]string{
		"model_key":       {deref(d.ModelKey), r.models.cacheBrokenOut.Alias},
		"sent_model_name": {deref(d.SentModelName), "provider-model"},
		"tool_name":       {deref(d.ToolName), "search"}, "workflow_node_id": {deref(d.WorkflowNodeID), "node-1"},
		"parent_task_id":           {deref(d.ParentTaskID), "parent-task-1"},
		"session_id":               {deref(d.SessionID), "session-1"},
		"resolved_model_name":      {deref(d.ResolvedModelName), r.models.cacheBrokenOut.ModelName},
		"resolved_model_provider":  {deref(d.ResolvedModelProvider), "openai"},
		"resolved_agent_name":      {deref(d.ResolvedAgentName), "the-agent"},
		"resolved_workflow_name":   {deref(d.ResolvedWorkflowName), "the-workflow"},
		"resolved_root_name":       {deref(d.ResolvedRootName), "the-root-agent"},
		"resolved_entrypoint_name": {deref(d.ResolvedEntrypointName), "the-gateway"},
	})
	rootNode, err := dbutil.QueryVal[string](t.Context(), r.db, `SELECT root_node_id FROM token_spend_ledger WHERE id = ?`, e.ID)
	if err != nil || rootNode != "root-node" {
		t.Errorf("ledger root_node_id = %q (err %v), want root-node", rootNode, err)
	}
	if got := waitForDetail(t, r.db, rootedAtAWorkflow.ID).ResolvedRootName; deref(got) != "the-root-workflow" {
		t.Errorf("resolved_root_name of a workflow root = %s, want it resolved against workflows", deref(got))
	}
}

func TestGovernor_AForgedAttemptCountCostsNoOtherEventItsDetails(t *testing.T) {
	r := startRig(t)
	forged := newEvent(r.models.flat.Alias)
	forged.AttemptCount = math.MaxInt32 + 1
	retried := newEvent(r.models.flat.Alias)
	retried.AttemptCount = 3

	r.publishEvent(t, forged)
	r.publishEvent(t, retried)

	if got := waitForDetail(t, r.db, retried.ID).AttemptCount; got == nil || *got != 3 {
		t.Errorf("attempt_count of the valid event = %v, want 3", got)
	}
	if got := waitForDetail(t, r.db, forged.ID).AttemptCount; got != nil {
		t.Errorf("attempt_count of the forged event = %d, want NULL (unknown)", *got)
	}
}

func TestGovernor_FilesWhetherAnEventsCacheWritesWereReported(t *testing.T) {
	r := startRig(t)
	cases := map[string]*bool{"reported": new(true), "folded into input": new(false), "unknown": nil}
	ids := make(map[string]string, len(cases))
	for name, reported := range cases {
		event := newEvent(r.models.flat.Alias)
		event.CacheWritesReported = reported
		ids[name] = event.ID
		r.publishEvent(t, event)
	}

	for name, want := range cases {
		got := waitForDetail(t, r.db, ids[name]).CacheWritesReported
		if (got == nil) != (want == nil) || (want != nil && *got != *want) {
			t.Errorf("%s: cache_writes_reported = %v, want %v", name, got, want)
		}
	}
}

func TestGovernor_LeavesANameNullWhenTheCoreRowHoldsTheNameOrTheIDHasNoRow(t *testing.T) {
	r := startRig(t)
	byName := newEvent("")
	byName.AgentName, byName.WorkflowName, byName.EntrypointName = "yaml-agent", "yaml-workflow", "builtin-gateway"
	byName.RootKind, byName.RootName = spend.RootAgent, "yaml-agent"
	noRow := newEvent("")
	noRow.AgentID, noRow.WorkflowID, noRow.EntrypointID = idgen.New(), idgen.New(), idgen.New()
	noRow.RootKind, noRow.RootID = spend.RootWorkflow, idgen.New()

	for _, e := range []spend.Event{byName, noRow} {
		r.publishEvent(t, e)
	}

	for name, e := range map[string]spend.Event{"name-keyed": byName, "no such row": noRow} {
		d := waitForDetail(t, r.db, e.ID)
		for column, got := range map[string]*string{
			"resolved_agent_name": d.ResolvedAgentName, "resolved_workflow_name": d.ResolvedWorkflowName,
			"resolved_root_name": d.ResolvedRootName, "resolved_entrypoint_name": d.ResolvedEntrypointName,
			"resolved_model_name": d.ResolvedModelName, "resolved_model_provider": d.ResolvedModelProvider,
		} {
			if got != nil {
				t.Errorf("%s: %s = %q, want NULL", name, column, *got)
			}
		}
		if deref(d.SentModelName) != "provider-model" {
			t.Errorf("%s: sent_model_name = %s, want the event's model name", name, deref(d.SentModelName))
		}
	}
}

func TestGovernor_WritesDetailsForARedeliveredEventTheLedgerAlreadyHeld(t *testing.T) {
	r := startRig(t)
	e := newEvent(r.models.flat.Alias)
	e.SessionID = "session-1"
	filedBeforeACrash := entryFor(e, Model{ConfigID: r.models.flat.ID})
	if _, err := store.NewSpendLedgerRepo(r.db).Insert(t.Context(), []store.SpendLedgerEntry{filedBeforeACrash}); err != nil {
		t.Fatalf("seed the ledger row: %v", err)
	}

	r.publishEvent(t, e)

	if d := waitForDetail(t, r.db, e.ID); deref(d.SessionID) != "session-1" {
		t.Errorf("session_id = %s, want the redelivered event's", deref(d.SessionID))
	}
	if n := countRows(t, r.db); n != 1 {
		t.Errorf("ledger rows = %d, want the one row, not a second", n)
	}
}

func detailsLedger(real *store.SpendLedgerRepo, insertErr, resolveErr error) ledgerFunc {
	return ledgerFunc{
		insert: real.Insert,
		delete: real.DeleteOlderThan,
		insertDetails: func(ctx context.Context, d []store.SpendLedgerDetail) (int64, error) {
			if insertErr != nil {
				return 0, insertErr
			}
			return real.InsertDetails(ctx, d)
		},
		resolveNames: func(ctx context.Context, table store.ComponentTable, ids []string) (map[string]string, error) {
			if resolveErr != nil {
				return nil, resolveErr
			}
			return real.ResolveNames(ctx, table, ids)
		},
	}
}

func TestGovernor_AFailedDetailWriteNeverCostsTheSpendOrItsAck(t *testing.T) {
	logs := &logCapture{}
	d := &deliveries{}
	r := startRig(t, recordDeliveries(d), func(cfg *Config) {
		cfg.Logger = logs.logger()
		cfg.Ledger = detailsLedger(cfg.Ledger.(*store.SpendLedgerRepo), errors.New("details table locked"), nil)
	})
	batch := events(3)

	for _, e := range batch {
		r.publishEvent(t, e)
	}

	waitForCount(t, r.db, len(batch))
	eventually(t, "every event to be acked", func() bool { return d.allAcked(len(batch)) })
	eventually(t, "the lost details to be reported", func() bool {
		return strings.Contains(logs.String(), `reason="write failed"`)
	})
	if n := countDetails(t, r.db); n != 0 {
		t.Errorf("detail rows = %d, want none from a failing write", n)
	}
	line := logLine(t, logs, `reason="write failed"`)
	if !strings.Contains(line, "level=WARN") || !strings.Contains(line, "details table locked") {
		t.Errorf("want a WARN carrying the error:\n%s", line)
	}
}

func TestGovernor_AFailedNameLookupStillWritesTheDetailsWithTheNamesNull(t *testing.T) {
	logs := &logCapture{}
	r := startRig(t, func(cfg *Config) {
		cfg.Logger = logs.logger()
		cfg.Ledger = detailsLedger(cfg.Ledger.(*store.SpendLedgerRepo), nil, errors.New("agents table unavailable"))
	})
	c := seedComponents(t, r.db)
	e := newEvent(r.models.flat.Alias)
	e.AgentID = c.agent

	r.publishEvent(t, e)

	d := waitForDetail(t, r.db, e.ID)
	if d.ResolvedAgentName != nil || deref(d.ResolvedModelName) != r.models.flat.ModelName {
		t.Errorf("resolved agent, model = %s, %s; want NULL and the model's name", deref(d.ResolvedAgentName), deref(d.ResolvedModelName))
	}
	eventually(t, "the unresolved names to be reported", func() bool {
		return strings.Contains(logs.String(), `reason="names unresolved"`)
	})
	if line := logLine(t, logs, `reason="names unresolved"`); !strings.Contains(line, "events=1") {
		t.Errorf("want the one event missing a name counted:\n%s", line)
	}
}

type gatedLedger struct {
	ledgerFunc
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedLedger(real *store.SpendLedgerRepo) *gatedLedger {
	g := &gatedLedger{
		ledgerFunc: detailsLedger(real, nil, nil),
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	inner := g.insert
	g.insert = func(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error) {
		first := false
		g.once.Do(func() { first = true; close(g.entered) })
		if first {
			<-g.release
		}
		return inner(ctx, entries)
	}
	return g
}

func TestWriteLoop_SkipsDetailsWhileTheQueueIsMoreThanHalfFull(t *testing.T) {
	db := store.TestDB(t)
	ledger := newGatedLedger(store.NewSpendLedgerRepo(db))
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)
	first := newEvent("")
	runWriteLoop(t.Context(), t, g, acks.pending(first))
	<-ledger.entered
	backlog := events(cap(g.pending)/2 + 1)
	for _, e := range backlog {
		g.pending <- acks.pending(e)
	}

	close(ledger.release)

	eventually(t, "every event to be filed", func() bool { return acks.total() == 1+len(backlog) })
	if n := countRows(t, db); n != 1+len(backlog) {
		t.Errorf("ledger rows = %d, want every event", n)
	}
	if readDetail(t, db, first.ID) != nil {
		t.Error("details were written for a batch filed while the queue was more than half full")
	}
	eventually(t, "the skipped details to be reported", func() bool {
		return strings.Contains(logs.String(), `reason=overloaded`)
	})
	if line := logLine(t, logs, "reason=overloaded"); !strings.Contains(line, "level=WARN") || !strings.Contains(line, "events=1") {
		t.Errorf("want a WARN counting the one event:\n%s", line)
	}
	if n := countDetails(t, db); n == 0 {
		t.Error("no details were written once the backlog drained below half")
	}
}

func TestWriteLoop_WritesDetailsWhileTheQueueIsExactlyHalfFull(t *testing.T) {
	db := store.TestDB(t)
	ledger := newGatedLedger(store.NewSpendLedgerRepo(db))
	logs := &logCapture{}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, logs.logger(), fastTiming)
	first := newEvent("")
	runWriteLoop(t.Context(), t, g, acks.pending(first))
	<-ledger.entered
	backlog := events(cap(g.pending) / 2)
	for _, e := range backlog {
		g.pending <- acks.pending(e)
	}

	close(ledger.release)

	eventually(t, "every event to be filed", func() bool { return acks.total() == 1+len(backlog) })
	eventually(t, "the first event's details to be written", func() bool { return readDetail(t, db, first.ID) != nil })
	if strings.Contains(logs.String(), "reason=overloaded") {
		t.Errorf("details were skipped with the queue exactly half full:\n%s", logs.String())
	}
}

func TestWriteLoop_WritesDetailsForEventsFiledBesideAnEventThatKeepsFailing(t *testing.T) {
	db := store.TestDB(t)
	real := store.NewSpendLedgerRepo(db)
	poison := newEvent("")
	ledger := detailsLedger(real, nil, nil)
	ledger.insert = func(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error) {
		if containsEntry(entries, poison.ID) {
			return 0, errStatementTimeout
		}
		return real.Insert(ctx, entries)
	}
	acks := &ackLog{}
	oneBatch := fastTiming
	oneBatch.flushInterval = time.Hour
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, oneBatch)
	healthy := events(maxBatch - 1)
	queued := []pendingEvent{acks.pending(poison)}
	for _, e := range healthy {
		queued = append(queued, acks.pending(e))
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runWriteLoop(ctx, t, g, queued...)

	eventually(t, "the healthy events to be filed", func() bool { return acks.total() == len(healthy) })
	if n := countRows(t, db); n != len(healthy) {
		t.Errorf("ledger rows = %d, want the %d healthy events", n, len(healthy))
	}
	eventually(t, "a detail row for each healthy event", func() bool { return countDetails(t, db) == len(healthy) })
	if readDetail(t, db, poison.ID) != nil {
		t.Error("details were written for an event that was never filed")
	}
}

func TestWriteLoop_WritesDetailsForABatchFiledOnARetry(t *testing.T) {
	db := store.TestDB(t)
	real := store.NewSpendLedgerRepo(db)
	var failed atomic.Bool
	ledger := detailsLedger(real, nil, nil)
	ledger.insert = func(ctx context.Context, entries []store.SpendLedgerEntry) (int64, error) {
		if failed.CompareAndSwap(false, true) {
			return 0, errStatementTimeout
		}
		return real.Insert(ctx, entries)
	}
	acks := &ackLog{}
	g := newWriteLoopGovernor(ledger, fakePrices{}, discardLogger, fastTiming)
	e := newEvent("")

	runWriteLoop(t.Context(), t, g, acks.pending(e))

	eventually(t, "the event to be filed", func() bool { return acks.count(e.ID) == 1 })
	eventually(t, "its detail row", func() bool { return readDetail(t, db, e.ID) != nil })
}
