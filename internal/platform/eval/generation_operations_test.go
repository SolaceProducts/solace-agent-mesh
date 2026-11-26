// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newGenerationService(t *testing.T) (*DatasetService, *store.EvalGenerationOperationRepo) {
	t.Helper()
	db := store.TestDB(t)
	ops := store.NewEvalGenerationOperationRepo(db)
	svc := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil).
		WithAIGen(AIGenConfig{Client: &fakeGatewayClient{}, Timeout: time.Minute}).
		WithGenerationOperations(ops)
	return svc, ops
}

func createStaleGeneration(t *testing.T, ops *store.EvalGenerationOperationRepo, operationType string, datasetID *string) *store.EvalGenerationOperation {
	t.Helper()
	deadline := time.Now().Add(-time.Minute).UnixMilli()
	stale := &store.EvalGenerationOperation{
		ID: idgen.New(), OperationType: operationType, DatasetID: datasetID, Status: store.EvalGenerationInProgress,
		CreatedBy: "alice", CreatedTime: deadline - 1000, Deadline: deadline,
	}
	if err := ops.Create(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	return stale
}

func awaitGeneration(t *testing.T, svc *DatasetService, id string) *store.EvalGenerationOperation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		op, err := svc.GetGeneration(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status != store.EvalGenerationInProgress {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("generation %s still in progress", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGetGeneration_ExpiresAnOperationPastItsDeadline(t *testing.T) {
	svc, ops := newGenerationService(t)
	stale := createStaleGeneration(t, ops, store.EvalGenerationDataset, nil)
	deadline := stale.Deadline

	got, err := svc.GetGeneration(t.Context(), stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.EvalGenerationFailed || got.ErrorMessage == nil || *got.ErrorMessage != msgGenerationExpired {
		t.Errorf("expired operation = %+v, want failed with %q", got, msgGenerationExpired)
	}
	if got.ErrorID == nil || *got.ErrorID != stale.ID || got.CompletedTime == nil || *got.CompletedTime != deadline {
		t.Errorf("expired operation = %+v, want its own id as errorId and its deadline as completedTime", got)
	}
}

func TestStartGeneration_AStaleBatchDoesNotBlockTheNext(t *testing.T) {
	svc, ops := newGenerationService(t)
	dataset, err := svc.Create(t.Context(), CreateDatasetRequest{Name: "stale-batch"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	stale := createStaleGeneration(t, ops, store.EvalGenerationExamples, &dataset.ID)

	started, err := svc.startGeneration(t.Context(), store.EvalGenerationExamples, &dataset.ID, "alice", generationRequest{},
		func(ctx context.Context, operationID string) error {
			return svc.examples.SaveGeneratedExamples(ctx, operationID, dataset.ID, nil)
		})
	if err != nil {
		t.Fatalf("start after a stale batch: %v", err)
	}
	if got := awaitGeneration(t, svc, stale.ID); got.Status != store.EvalGenerationFailed {
		t.Errorf("stale batch status = %s, want failed", got.Status)
	}
	if got := awaitGeneration(t, svc, started.ID); got.Status != store.EvalGenerationSucceeded {
		t.Errorf("new batch status = %s, want succeeded", got.Status)
	}
}

func TestStartGeneration_APanicFailsTheOperation(t *testing.T) {
	svc, _ := newGenerationService(t)
	started, err := svc.startGeneration(t.Context(), store.EvalGenerationDataset, nil, "alice", generationRequest{},
		func(context.Context, string) error { panic("parser bug") })
	if err != nil {
		t.Fatal(err)
	}
	got := awaitGeneration(t, svc, started.ID)
	if got.Status != store.EvalGenerationFailed || got.ErrorMessage == nil || *got.ErrorMessage != msgGenerationError {
		t.Errorf("panicked generation = %+v, want failed with %q", got, msgGenerationError)
	}
}

func TestStartGeneration_NotConfiguredIsFeatureDisabled(t *testing.T) {
	db := store.TestDB(t)
	newService := func() *DatasetService {
		return NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil)
	}
	for name, svc := range map[string]*DatasetService{
		"no AI generation":     newService().WithGenerationOperations(store.NewEvalGenerationOperationRepo(db)),
		"no operations record": newService().WithAIGen(AIGenConfig{Client: &fakeGatewayClient{}}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.StartDatasetGeneration(t.Context(), GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1}, "alice", "alice-id", "http://gw", "")
			if _, ok := errors.AsType[*FeatureDisabledError](err); !ok {
				t.Errorf("StartDatasetGeneration err = %v, want FeatureDisabledError", err)
			}
		})
	}
}

func TestGenerationFailureMessage(t *testing.T) {
	capErr := exampleCapError(4990, 20, MaxExamplesPerDataset)
	invalid := &BusinessRuleViolationError{Code: CodeDatasetGenInvalidResponse, Message: "The AI's response had no examples."}
	upstream := &BusinessRuleViolationError{Code: "DATASET_GEN_FAILED", Message: "upstream text"}
	for name, tc := range map[string]struct {
		err           error
		operationType string
		want          string
	}{
		"the cap":                      {capErr, store.EvalGenerationExamples, capErr.(*BusinessRuleViolationError).Message},
		"an unreadable answer":         {invalid, store.EvalGenerationDataset, invalid.Message},
		"an orchestrator error":        {upstream, store.EvalGenerationExamples, msgExamplesGenerationFailed},
		"a dataset orchestrator error": {upstream, store.EvalGenerationDataset, msgDatasetGenerationFailed},
		"anything else":                {errors.New("database is down"), store.EvalGenerationDataset, msgGenerationError},
	} {
		if got := generationFailureMessage(tc.err, tc.operationType); got != tc.want {
			t.Errorf("%s: message = %q, want %q", name, got, tc.want)
		}
	}
}

func TestStartGeneration_AnOperationThatEndedSavesNothing(t *testing.T) {
	svc, ops := newGenerationService(t)
	saved := make(chan error, 1)
	started, err := svc.startGeneration(t.Context(), store.EvalGenerationDataset, nil, "alice", generationRequest{}, func(ctx context.Context, operationID string) error {
		if failErr := ops.Fail(ctx, operationID, "expired", operationID, time.Now().UnixMilli()); failErr != nil {
			saved <- failErr
			return failErr
		}
		err := svc.examples.SaveGeneratedDataset(ctx, operationID, &store.EvalDataset{Name: "ended", CreatedBy: "alice", UpdatedBy: "alice"},
			[]*store.EvalExample{{Prompt: "q"}})
		saved <- err
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-saved; !errors.Is(err, store.ErrEvalGenerationNotInProgress) {
		t.Fatalf("save after the operation ended = %v, want ErrEvalGenerationNotInProgress", err)
	}
	awaitNoDataset(t, svc, "ended")
	if got := awaitGeneration(t, svc, started.ID); got.ErrorMessage == nil || *got.ErrorMessage != "expired" {
		t.Errorf("operation = %+v, want the failure written before the save kept", got)
	}
}

func TestGenerateDataset_AFailedExampleWriteLeavesNoDataset(t *testing.T) {
	db := store.TestDB(t)
	fake := &fakeGatewayClient{taskID: "t", result: stubResponseFromText(`{"name":"Half done","examples":[{"prompt":"q"}]}`)}
	svc := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil).
		WithAIGen(AIGenConfig{
			Client:        fake,
			AgentRegistry: &stubRegistry{card: &a2a.AgentCard{Name: "AgentX"}},
			Orchestrator:  "Orchestrator",
			Timeout:       5 * time.Second,
		})
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE eval_examples RENAME TO eval_examples_gone`); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.GenerateDataset(t.Context(), GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1}, "alice", "http://gw", ""); err == nil {
		t.Fatal("GenerateDataset succeeded with no examples table")
	}
	awaitNoDataset(t, svc, "Half done")
}

func awaitNoDataset(t *testing.T, svc *DatasetService, name string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		taken, err := svc.datasets.NameExists(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		if !taken {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dataset %q was left behind", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type breakingGateway struct {
	*fakeGatewayClient
	db     *store.DB
	called chan struct{}
	once   sync.Once
}

func (g *breakingGateway) SubmitTask(ctx context.Context, gatewayURL, agentName, prompt, auth string) (string, error) {
	if _, err := g.db.ExecContext(ctx, `ALTER TABLE eval_generation_operations RENAME TO eval_generation_operations_gone`); err != nil {
		return "", err
	}
	taskID, err := g.fakeGatewayClient.SubmitTask(ctx, gatewayURL, agentName, prompt, auth)
	g.once.Do(func() { close(g.called) })
	return taskID, err
}

func TestStartExampleGeneration_ExamplesItCannotRecordAreRemoved(t *testing.T) {
	db := store.TestDB(t)
	body := `{"examples":[{"prompt":"q1","expected_response":"r1"},{"prompt":"q2"}]}`
	gateway := &breakingGateway{fakeGatewayClient: &fakeGatewayClient{taskID: "t", result: stubResponseFromText(body)}, db: db, called: make(chan struct{})}
	svc := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil).
		WithAIGen(AIGenConfig{Client: gateway, Timeout: time.Minute}).
		WithGenerationOperations(store.NewEvalGenerationOperationRepo(db))
	dataset, err := svc.Create(t.Context(), CreateDatasetRequest{Name: "examples-unrecorded"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.examples.BulkCreate(t.Context(), []*store.EvalExample{{DatasetID: dataset.ID, SequenceNumber: 1, Prompt: "already here"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.StartExampleGeneration(t.Context(), dataset.ID, GenerateExamplesRequest{TargetAgent: "AgentX", NumExamples: 2}, "alice", "alice-id", "http://gw.example", "Bearer abc"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	select {
	case <-gateway.called:
	case <-time.After(10 * time.Second):
		t.Fatal("the orchestrator was never called, so the test proves nothing")
	}
	for {
		n, countErr := svc.examples.CountByDataset(t.Context(), dataset.ID)
		if countErr != nil {
			t.Fatal(countErr)
		}
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dataset holds %d examples, want only the 1 that was there before the generation", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
