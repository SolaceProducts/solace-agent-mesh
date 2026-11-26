// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func runDueFileDeletes(t *testing.T, db *store.DB, files *FileDeletes) {
	t.Helper()
	outbox := store.NewOutboxRepo(db)
	events, err := outbox.GetDispatchable(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var published []string
	for _, e := range events {
		if e.EventType != eventEvalFilesDelete {
			continue
		}
		if err := files.PublishOutboxEvent(t.Context(), e); err != nil {
			t.Fatalf("file delete %s: %v", e.ID, err)
		}
		published = append(published, e.ID)
	}
	if err := outbox.MarkPublished(t.Context(), published); err != nil {
		t.Fatal(err)
	}
}

type hangingDeletes struct{ artifact.Service }

func (hangingDeletes) Delete(ctx context.Context, _ artifact.Locator) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestFileDeletes_AHangingStoreHoldsUpNeitherTheRequestNorThePoller(t *testing.T) {
	f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
	if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("bytes"), ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	f.scoped.Inner = hangingDeletes{f.saved}
	f.files.attemptBudget = 100 * time.Millisecond

	start := time.Now()
	if err := NewExampleService(f.examples, f.datasets, nil).WithFileDeletes(f.files).Delete(f.ctx, f.example.ID); err != nil {
		t.Fatalf("delete example: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the delete request took %s with a hanging store", took)
	}

	events, err := store.NewOutboxRepo(f.db).GetDispatchable(f.ctx, 100)
	if err != nil || len(events) != 1 || events[0].EventType != eventEvalFilesDelete {
		t.Fatalf("outbox = %v, %v; want the one recorded file delete", events, err)
	}
	start = time.Now()
	if err := f.files.PublishOutboxEvent(f.ctx, events[0]); err == nil {
		t.Fatal("an attempt against a hanging store reported success, so the outbox would not retry it")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the outbox attempt took %s, want it to stop at its 100ms budget", took)
	}
}

func hangingS3(t *testing.T) storage.Client {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	client, err := objectstore.NewFromConfig(map[string]any{
		"type": "s3", "bucket_name": "eval", "endpoint_url": srv.URL,
		"aws_access_key_id": "test", "aws_secret_access_key": "test", "retry_max_attempts": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := storage.NewFromObjectStore(client)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFileDeletes_AHangingRunDataStoreStopsAtTheBudget(t *testing.T) {
	files := NewFileDeletes(NewStorageService(hangingS3(t), "ns"), nil, nil)
	files.attemptBudget = 100 * time.Millisecond
	event, err := fileDeleteEvent(store.EvalFileDeletes{RunIDs: []string{"run-1"}})
	if err != nil || event == nil {
		t.Fatalf("event = %v, %v", event, err)
	}
	result := make(chan error, 1)
	go func() { result <- files.PublishOutboxEvent(t.Context(), *event) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("an attempt against a hanging run-data store reported success, so the outbox would not retry it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the outbox attempt did not stop at its 100ms budget")
	}
}

func TestFileDeletes_RunRunGroupAndExperimentDeletesRemoveTheirRunData(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	storageSvc := NewStorageService(storage.NewMemory(), "ns")
	files := NewFileDeletes(storageSvc, nil, nil)
	ds := &store.EvalDataset{Name: "run-data", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewEvalDatasetRepo(db).Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	experiments, runs := store.NewEvalExperimentRepo(db), store.NewEvalRunRepo(db)
	newExperiment := func(name string) *store.EvalExperiment {
		exp := &store.EvalExperiment{Name: name, TargetAgent: "agent", Models: "[]", RunsPerExample: 1, DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice"}
		if err := experiments.Create(ctx, exp, nil); err != nil {
			t.Fatal(err)
		}
		return exp
	}
	keys := map[string]string{}
	newRun := func(exp *store.EvalExperiment, group string) string {
		run := &store.EvalRun{ExperimentID: exp.ID, RunGroupID: group, Status: StatusCompleted, Model: "m", StartedBy: "alice"}
		if err := runs.Create(ctx, run); err != nil {
			t.Fatal(err)
		}
		key, err := storageSvc.UploadExecutionData(run.ID, "ex-1", 0, map[string]any{"a": 1})
		if err != nil {
			t.Fatal(err)
		}
		keys[run.ID] = key
		return run.ID
	}
	first, second := newExperiment("first"), newExperiment("second")
	single := newRun(first, "")
	group := idgen.New()
	grouped := []string{newRun(first, group), newRun(first, group)}
	other := newRun(second, "")

	runSvc := NewRunService(RunServiceParams{Runs: runs, Results: store.NewEvalRunResultRepo(db), Experiments: experiments,
		Examples: store.NewEvalExampleRepo(db), Evaluators: store.NewEvalEvaluatorRepo(db), FileDeletes: files})
	expSvc := NewExperimentService(experiments, store.NewEvalDatasetRepo(db), store.NewEvalEvaluatorRepo(db), runs, nil).WithFileDeletes(files)
	stored := func(runID string) bool {
		_, err := storageSvc.GetExecutionData(keys[runID])
		return err == nil
	}
	check := func(step string, gone, kept []string) {
		t.Helper()
		runDueFileDeletes(t, db, files)
		for _, id := range gone {
			if stored(id) {
				t.Errorf("after %s, run %s still has its data", step, id)
			}
		}
		for _, id := range kept {
			if !stored(id) {
				t.Errorf("after %s, run %s lost its data", step, id)
			}
		}
	}

	if err := runSvc.DeleteRun(ctx, single); err != nil {
		t.Fatal(err)
	}
	check("the run delete", []string{single}, append([]string{other}, grouped...))
	if err := runSvc.DeleteRunGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	check("the run group delete", grouped, []string{other})
	if err := expSvc.Delete(ctx, second.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	check("the experiment delete", []string{other}, nil)
}
