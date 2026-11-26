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

type fakeGenerationTrigger struct {
	mu        sync.Mutex
	published []string
	err       error
}

func (f *fakeGenerationTrigger) PublishGenerationTrigger(_ context.Context, operationID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, operationID)
	return f.err
}

type fakeGenerationAuth struct {
	header      string
	err         error
	gotUser     string
	gotPlatform string
}

func (f *fakeGenerationAuth) AuthHeader(_ context.Context, userID, platformUserID string) (string, error) {
	f.gotUser, f.gotPlatform = userID, platformUserID
	return f.header, f.err
}

type dispatchFixture struct {
	svc     *DatasetService
	ops     *store.EvalGenerationOperationRepo
	client  *fakeGatewayClient
	trigger *fakeGenerationTrigger
	auth    *fakeGenerationAuth
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	f := &dispatchFixture{
		client: &fakeGatewayClient{taskID: "task-1", result: stubResponseFromText(
			`{"name":"Generated","examples":[{"prompt":"q1","expected_response":"r1"},{"prompt":"q2"}]}`)},
		trigger: &fakeGenerationTrigger{},
		auth:    &fakeGenerationAuth{header: "Bearer signed"},
	}
	db := store.TestDB(t)
	f.ops = store.NewEvalGenerationOperationRepo(db)
	f.svc = NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil).
		WithAIGen(AIGenConfig{
			Client:        f.client,
			AgentRegistry: &stubRegistry{card: &a2a.AgentCard{Name: "AgentX", Description: "test agent"}},
			Orchestrator:  "Orchestrator",
			Timeout:       5 * time.Second,
		}).
		WithGenerationOperations(f.ops).
		WithGenerationDispatch(f.trigger, f.auth)
	return f
}

func (f *dispatchFixture) createOperation(t *testing.T, operationType string, datasetID, request *string, age time.Duration) *store.EvalGenerationOperation {
	t.Helper()
	created := time.Now().Add(-age)
	op := &store.EvalGenerationOperation{
		ID: idgen.New(), OperationType: operationType, DatasetID: datasetID, Status: store.EvalGenerationInProgress,
		CreatedBy: "alice", CreatedTime: created.UnixMilli(), Deadline: time.Now().Add(time.Minute).UnixMilli(), Request: request,
	}
	if err := f.ops.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op
}

func encodedRequest(t *testing.T, req generationRequest) *string {
	t.Helper()
	encoded, err := encodeGenerationRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func (f *dispatchFixture) operation(t *testing.T, id string) *store.EvalGenerationOperation {
	t.Helper()
	op, err := f.ops.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestGenerationDispatch_StartPublishesTheTriggerAndTheActiveGWERunsIt(t *testing.T) {
	f := newDispatchFixture(t)
	started, err := f.svc.StartDatasetGeneration(t.Context(), GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 2},
		"alice", "alice-id", "http://gw.example", "Bearer caller")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.trigger.published) != 1 || f.trigger.published[0] != started.ID {
		t.Fatalf("published triggers = %v, want [%s]", f.trigger.published, started.ID)
	}
	if f.client.calls != 0 {
		t.Fatalf("the GWE that took the request called the orchestrator %d times, want 0", f.client.calls)
	}

	f.svc.RunGeneration(t.Context(), started.ID, false)

	if got := f.operation(t, started.ID); got.Status != store.EvalGenerationSucceeded {
		t.Fatalf("operation = %+v, want succeeded", got)
	}
	if f.client.gotURL != "http://gw.example" || f.client.gotAuth != "Bearer signed" || f.auth.gotUser != "alice" || f.auth.gotPlatform != "alice-id" {
		t.Errorf("orchestrator call url=%q auth=%q user=%q platform user=%q, want the stored gateway URL and the token signed for alice",
			f.client.gotURL, f.client.gotAuth, f.auth.gotUser, f.auth.gotPlatform)
	}
}

func TestGenerationDispatch_ALostTriggerLeavesTheOperationForTakeover(t *testing.T) {
	f := newDispatchFixture(t)
	f.trigger.err = errors.New("broker down")
	started, err := f.svc.StartDatasetGeneration(t.Context(), GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 2},
		"alice", "alice-id", "http://gw.example", "")
	if err != nil {
		t.Fatalf("start with a failed trigger: %v", err)
	}
	if got := f.operation(t, started.ID); got.Status != store.EvalGenerationInProgress {
		t.Fatalf("operation = %+v, want in progress", got)
	}
}

func TestGenerationDispatch_ExampleGenerationRunsOnTheActiveGWE(t *testing.T) {
	f := newDispatchFixture(t)
	f.client.result = stubResponseFromText(`{"examples":[{"prompt":"q3"},{"prompt":"q4"}]}`)
	dataset, err := f.svc.Create(t.Context(), CreateDatasetRequest{Name: "examples-dispatch"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.svc.StartExampleGeneration(t.Context(), dataset.ID,
		GenerateExamplesRequest{TargetAgent: "AgentX", NumExamples: 2}, "alice", "alice-id", "http://gw.example", "")
	if err != nil {
		t.Fatal(err)
	}
	f.svc.RunGeneration(t.Context(), started.ID, false)
	if got := f.operation(t, started.ID); got.Status != store.EvalGenerationSucceeded {
		t.Fatalf("operation = %+v, want succeeded", got)
	}
}

func TestGenerationDispatch_TakeoverCountsAndCaps(t *testing.T) {
	f := newDispatchFixture(t)
	request := encodedRequest(t, generationRequest{TargetAgent: "AgentX", NumExamples: 2, GatewayURL: "http://gw.example"})

	taken := f.createOperation(t, store.EvalGenerationDataset, nil, request, time.Minute)
	f.svc.RunGeneration(t.Context(), taken.ID, true)
	if got := f.operation(t, taken.ID); got.Status != store.EvalGenerationSucceeded || got.Takeovers != 1 {
		t.Fatalf("taken over operation = %+v, want succeeded after 1 takeover", got)
	}

	atCap := f.createOperation(t, store.EvalGenerationDataset, nil, request, time.Minute)
	for range maxGenerationTakeovers - 1 {
		if _, ok, err := f.ops.TakeOver(t.Context(), atCap.ID, time.Now().Add(time.Minute).UnixMilli()); err != nil || !ok {
			t.Fatalf("TakeOver = %v, %v", ok, err)
		}
	}
	f.svc.RunGeneration(t.Context(), atCap.ID, true)
	if got := f.operation(t, atCap.ID); got.Status != store.EvalGenerationSucceeded || got.Takeovers != maxGenerationTakeovers {
		t.Fatalf("operation at the cap = %+v, want succeeded after %d takeovers", got, maxGenerationTakeovers)
	}

	capped := f.createOperation(t, store.EvalGenerationDataset, nil, request, time.Minute)
	for range maxGenerationTakeovers {
		if _, ok, err := f.ops.TakeOver(t.Context(), capped.ID, time.Now().Add(time.Minute).UnixMilli()); err != nil || !ok {
			t.Fatalf("TakeOver = %v, %v", ok, err)
		}
	}
	f.svc.RunGeneration(t.Context(), capped.ID, true)
	got := f.operation(t, capped.ID)
	if got.Status != store.EvalGenerationFailed || got.ErrorMessage == nil || *got.ErrorMessage != msgGenerationTakeoverCap {
		t.Fatalf("operation over the cap = %+v, want failed with %q", got, msgGenerationTakeoverCap)
	}
	if f.client.calls != 2 {
		t.Errorf("orchestrator calls = %d, want 2 (none for the capped operation)", f.client.calls)
	}
}

func TestGenerationDispatch_FailuresBeforeTheOrchestratorCall(t *testing.T) {
	for name, tc := range map[string]struct {
		operationType string
		request       string
		authErr       error
	}{
		"an unreadable request":         {store.EvalGenerationDataset, "{", nil},
		"a token that cannot be signed": {store.EvalGenerationDataset, `{"targetAgent":"AgentX","numExamples":2}`, errors.New("no scopes")},
		"examples without a dataset":    {store.EvalGenerationExamples, `{"targetAgent":"AgentX","numExamples":2}`, nil},
		"an unknown operation type":     {"unknown", `{"targetAgent":"AgentX","numExamples":2}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDispatchFixture(t)
			f.auth.err = tc.authErr
			request := tc.request
			op := f.createOperation(t, tc.operationType, nil, &request, 0)
			f.svc.RunGeneration(t.Context(), op.ID, false)
			if got := f.operation(t, op.ID); got.Status != store.EvalGenerationFailed {
				t.Fatalf("operation = %+v, want failed", got)
			}
			if f.client.calls != 0 {
				t.Errorf("orchestrator calls = %d, want 0", f.client.calls)
			}
		})
	}
}

func TestGenerationDispatch_RunGenerationSkipsWhatItMustNotRun(t *testing.T) {
	f := newDispatchFixture(t)
	request := encodedRequest(t, generationRequest{TargetAgent: "AgentX", NumExamples: 2})

	noRequest := f.createOperation(t, store.EvalGenerationDataset, nil, nil, 0)
	f.svc.RunGeneration(t.Context(), noRequest.ID, false)

	running := f.createOperation(t, store.EvalGenerationDataset, nil, request, 0)
	if !f.svc.dispatch.claim(running.ID) {
		t.Fatal("first claim refused")
	}
	f.svc.RunGeneration(t.Context(), running.ID, false)
	f.svc.dispatch.release(running.ID)

	ended := f.createOperation(t, store.EvalGenerationDataset, nil, request, 0)
	if err := f.ops.Fail(t.Context(), ended.ID, "stopped", ended.ID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.svc.RunGeneration(t.Context(), ended.ID, false)
	f.svc.RunGeneration(t.Context(), ended.ID, true)

	f.svc.RunGeneration(t.Context(), idgen.New(), false)

	if f.client.calls != 0 {
		t.Errorf("orchestrator calls = %d, want 0", f.client.calls)
	}
	for _, id := range []string{noRequest.ID, running.ID} {
		if got := f.operation(t, id); got.Status != store.EvalGenerationInProgress {
			t.Errorf("operation %s = %s, want in progress", id, got.Status)
		}
	}
}

func TestGenerationsToTakeOver_ReturnsOverdueOperationsThisGWEIsNotRunning(t *testing.T) {
	f := newDispatchFixture(t)
	request := encodedRequest(t, generationRequest{TargetAgent: "AgentX", NumExamples: 2})
	overdue := f.createOperation(t, store.EvalGenerationDataset, nil, request, time.Minute)
	f.createOperation(t, store.EvalGenerationDataset, nil, request, 0)
	f.createOperation(t, store.EvalGenerationDataset, nil, nil, time.Minute)
	runningHere := f.createOperation(t, store.EvalGenerationDataset, nil, request, time.Minute)
	f.svc.dispatch.claim(runningHere.ID)

	got := f.svc.GenerationsToTakeOver(t.Context())
	if len(got) != 1 || got[0] != overdue.ID {
		t.Fatalf("GenerationsToTakeOver = %v, want [%s]", got, overdue.ID)
	}
}

func TestGenerationDispatch_NotWiredDoesNothing(t *testing.T) {
	svc, ops := newGenerationService(t)
	op := createStaleGeneration(t, ops, store.EvalGenerationDataset, nil)
	if got := svc.GenerationsToTakeOver(t.Context()); got != nil {
		t.Errorf("GenerationsToTakeOver without dispatch = %v, want nil", got)
	}
	svc.RunGeneration(t.Context(), op.ID, true)
	got, err := ops.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Takeovers != 0 {
		t.Errorf("takeovers = %d, want 0", got.Takeovers)
	}
}
