// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const governorWiringNamespace = "governor-wiring"

func featureCatalog(t *testing.T) *features.Registry {
	t.Helper()
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatalf("load feature catalog: %v", err)
	}
	return reg
}

func governorWiringCore(t *testing.T, brk broker.Broker, db *store.DB) *PlatformCore {
	t.Helper()
	return governorWiringCoreLogging(t, brk, db, io.Discard)
}

func governorWiringCoreLogging(t *testing.T, brk broker.Broker, db *store.DB, logs io.Writer) *PlatformCore {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(logs, nil))
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(logger), runtime.WithFeatureRegistry(featureCatalog(t)))
	return &PlatformCore{
		cfg:    &PlatformConfig{Namespace: governorWiringNamespace},
		svc:    rt.Services(),
		logger: logger,
		db:     db,
	}
}

func TestNewGovernor_FilesSpend(t *testing.T) {
	db := store.TestDB(t)
	brk := connectedDevBroker(t)
	core := governorWiringCore(t, brk, db)

	g := core.newGovernor()

	if g == nil {
		t.Fatal("no governor")
	}
	if err := g.Start(t.Context()); err != nil {
		t.Fatalf("start governor: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })
	waitForPublishedSpendToBeFiled(t, brk, db)
}

func TestNewGovernor_BuildsNoneWhenItCannotRun(t *testing.T) {
	cases := map[string]struct {
		noDB     bool
		noBroker bool
		warning  string
	}{
		"no database":               {noDB: true, warning: "missing=[database]"},
		"no broker":                 {noBroker: true, warning: "missing=[broker]"},
		"no database and no broker": {noDB: true, noBroker: true, warning: `missing="[database broker]"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			db := store.TestDB(t)
			if c.noDB {
				db = nil
			}
			var brk broker.Broker = connectedDevBroker(t)
			if c.noBroker {
				brk = nil
			}
			var logs strings.Builder
			core := governorWiringCoreLogging(t, brk, db, &logs)

			if g := core.newGovernor(); g != nil {
				t.Error("a governor was built, want none")
			}
			warned := strings.Contains(logs.String(), "level=WARN") &&
				strings.Contains(logs.String(), "the spend governor cannot start")
			if !warned || !strings.Contains(logs.String(), c.warning) {
				t.Errorf("want a WARN naming %s:\n%s", c.warning, logs.String())
			}
		})
	}
}

func TestInit_StartsTheSpendGovernor(t *testing.T) {
	brk := connectedDevBroker(t)
	core := NewPlatformCore(map[string]any{
		"namespace":    governorWiringNamespace,
		"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
	}, ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.New(slog.DiscardHandler)),
		runtime.WithFeatureRegistry(featureCatalog(t)),
	).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("platform init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("platform start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	if core.governor == nil {
		t.Fatal("the platform started no spend governor")
	}
	waitForPublishedSpendToBeFiled(t, brk, core.DB())
}

func waitForPublishedSpendToBeFiled(t *testing.T, brk broker.Broker, db *store.DB) {
	t.Helper()
	e := spend.Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "user-1", TriggerKind: spend.TriggerChat,
		Surface: spend.SurfaceAgent, ModelName: "m", TaskID: "task-1", TraceID: "trace-1",
	}
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := brk.PublishGuaranteed(t.Context(), a2a.SpendEventTopic(governorWiringNamespace), &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM token_spend_ledger WHERE id = ?`, e.ID)
		if err != nil {
			t.Fatalf("read ledger: %v", err)
		}
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the wired governor did not file a published spend event")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func spendPurgeJobs(core *PlatformCore) []leader.Job {
	return leaderJobsNamed(core, leader.JobPlatformTokenGovernancePurge)
}

func leaderJobsNamed(core *PlatformCore, name leader.JobName) []leader.Job {
	var jobs []leader.Job
	for _, job := range core.LeaderJobs() {
		if job.Name == name {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func TestLeaderJobs_OffersTheBudgetStateMonitor(t *testing.T) {
	brk := connectedDevBroker(t)
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	core := governorWiringCore(t, brk, store.TestDB(t))
	if err := brk.Subscribe(q, a2a.BudgetStateTopic(core.cfg.Namespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	jobs := leaderJobsNamed(core, leader.JobPlatformBudgetState)
	if len(jobs) != 1 {
		t.Fatalf("budget state jobs = %d, want 1", len(jobs))
	}
	termCtx, endTerm := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- jobs[0].Run(termCtx, leader.Term{Number: 1}) }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if msg, err := brk.Receive(ctx, q); err != nil || msg == nil {
		t.Errorf("the wired monitor published nothing on the namespace's budget state topic: %v", err)
	}
	endTerm()
	if err := <-done; err != nil {
		t.Errorf("the monitor's run ended with %v when its term ended, want nil", err)
	}
}

func TestLeaderJobs_OffersTheTokenGovernancePurge(t *testing.T) {
	db := store.TestDB(t)
	core := governorWiringCore(t, connectedDevBroker(t), db)
	if _, err := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 5000 WHERE id = ?`, store.SeededMeshBudgetID); err != nil {
		t.Fatalf("limit the mesh budget: %v", err)
	}
	expired, err := store.NewBudgetTopUpsRepo(db).Grant(t.Context(), store.BudgetTopUpGrant{
		SubjectType: store.BudgetSubjectMesh, SubjectRef: store.BudgetMeshSubjectRef, Amount: 100,
		ExpiresTime: time.Now().Add(-time.Hour).UnixMilli(), Actor: "admin", NowMs: time.Now().Add(-2 * time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("seed an expired top-up: %v", err)
	}

	jobs := spendPurgeJobs(core)

	if len(jobs) != 1 {
		t.Fatalf("purge jobs = %d, want 1", len(jobs))
	}
	if jobs[0].Every <= 0 || jobs[0].Run == nil {
		t.Errorf("purge job = every %s, run set %t; want a recurring runnable job", jobs[0].Every, jobs[0].Run != nil)
	}
	if err = jobs[0].Run(t.Context(), leader.Term{Number: 1}); err != nil {
		t.Errorf("run the purge against the platform database: %v", err)
	}
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM budget_top_ups WHERE id = ?`, expired.TopUp.ID)
	if err != nil || n != 0 {
		t.Errorf("expired top-ups left after the purge = %d, %v; want none", n, err)
	}
}

func TestLeaderJobs_OffersNoTokenGovernancePurgeWithoutADatabase(t *testing.T) {
	core := governorWiringCore(t, connectedDevBroker(t), nil)
	if jobs := spendPurgeJobs(core); len(jobs) != 0 {
		t.Errorf("purge jobs = %d, want none", len(jobs))
	}
}

func TestLeaderJobs_OffersNoBudgetPeriodCheckWithoutADatabase(t *testing.T) {
	core := governorWiringCore(t, connectedDevBroker(t), nil)

	if jobs := leaderJobsNamed(core, leader.JobPlatformBudgetPeriodCheck); len(jobs) != 0 {
		t.Errorf("budget period check jobs = %d, want none", len(jobs))
	}
}

func spendRollupJobs(core *PlatformCore) []leader.Job {
	var jobs []leader.Job
	for _, job := range core.LeaderJobs() {
		if job.Name == leader.JobPlatformSpendRollup || job.Name == leader.JobPlatformSpendRollupReconcile {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func TestLeaderJobs_OffersTheSpendRollups(t *testing.T) {
	core := governorWiringCore(t, connectedDevBroker(t), store.TestDB(t))

	jobs := spendRollupJobs(core)

	if len(jobs) != 2 {
		t.Fatalf("spend rollup jobs = %d, want the recent and the reconcile job", len(jobs))
	}
	for _, job := range jobs {
		if job.Every <= 0 || job.Run == nil {
			t.Errorf("%s = every %s, run set %t; want a recurring runnable job", job.Name, job.Every, job.Run != nil)
		}
		if err := job.Run(t.Context(), leader.Term{Number: 1}); err != nil {
			t.Errorf("run %s against the platform database: %v", job.Name, err)
		}
	}
}

func TestLeaderJobs_OffersNoSpendRollupsWithoutADatabase(t *testing.T) {
	core := governorWiringCore(t, connectedDevBroker(t), nil)

	if jobs := spendRollupJobs(core); len(jobs) != 0 {
		t.Errorf("spend rollup jobs = %d, want none", len(jobs))
	}
}

func TestLeaderJobs_OffersTheBudgetPeriodCheck(t *testing.T) {
	core := governorWiringCore(t, connectedDevBroker(t), store.TestDB(t))

	jobs := leaderJobsNamed(core, leader.JobPlatformBudgetPeriodCheck)

	if len(jobs) != 1 {
		t.Fatalf("budget period check jobs = %d, want 1", len(jobs))
	}
	if err := jobs[0].Run(t.Context(), leader.Term{Number: 1}); err != nil {
		t.Errorf("run the budget period check against the platform database: %v", err)
	}
}

func connectedDevBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}
