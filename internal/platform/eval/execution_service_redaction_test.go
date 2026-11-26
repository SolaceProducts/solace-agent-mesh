// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	evalstorage "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	leakVerifier = "pkce-verifier-must-not-leak"
	leakAuthURI  = "https://idp.example.com/authorize?state=state-must-not-leak&code_challenge=challenge-must-not-leak"
)

var leakSecrets = []string{leakVerifier, "state-must-not-leak", "challenge-must-not-leak"}

func authRequiredPayload(t *testing.T, taskID string) []byte {
	t.Helper()
	raw, err := json.Marshal(a2a.AuthenticationRequiredData{
		Type:            "authentication_required",
		TargetAgent:     "Orchestrator",
		UserID:          rbac.EvalSystemUserSub,
		AuthURI:         leakAuthURI,
		CredentialKey:   "atlassian:system-eval",
		CodeVerifier:    leakVerifier,
		ToolDisplayName: "Atlassian",
	})
	if err != nil {
		t.Fatalf("marshal auth data: %v", err)
	}
	var authData map[string]any
	if err = json.Unmarshal(raw, &authData); err != nil {
		t.Fatalf("unmarshal auth data: %v", err)
	}
	return inputRequiredEnvelope("rpc-1", taskID, map[string]any{"kind": "data", "data": authData})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func TestHandleResponseRedactsOAuthSecretsFromEverySink(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "redaction", CreatedBy: "a", UpdatedBy: "a"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "search the wiki"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "redaction-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "a", UpdatedBy: "a",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, ProgressTotal: 1, Source: "manual", StartedBy: "a"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	storageSvc := NewStorageService(evalstorage.NewMemory(), "ns")
	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "ns", Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo, Storage: storageSvc,
		BrokerCli: NewBrokerClient(nil, "ns", nil, nil),
	})

	taskID := "task-oauth-1"
	task := &pendingTask{exampleID: ex.ID, trialIndex: 0, example: ex, startedAt: time.Now()}
	state := &runState{
		pending: map[string]*pendingTask{taskID: task},
	}

	raw := authRequiredPayload(t, taskID)
	for _, secret := range leakSecrets {
		if !strings.Contains(string(raw), secret) {
			t.Fatalf("fixture does not carry %q; the assertions for it would pass vacuously", secret)
		}
	}
	svc.handleResponse(ctx, run.ID, exp, "Orchestrator", nil, BrokerResponse{Topic: "ns/status/" + taskID, Payload: raw}, state)
	state.scoreWG.Wait()

	results, err := resultRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	row := results[0]

	if row.AgentResponse != nil {
		t.Errorf("AgentResponse = %q, want nil (auth wall produces no gradeable output)", *row.AgentResponse)
	}
	if row.FailureKind == nil || *row.FailureKind != FailureKindAuthRequired {
		t.Errorf("FailureKind = %v, want %q", row.FailureKind, FailureKindAuthRequired)
	}
	if row.ErrorMessage == nil {
		t.Fatal("ErrorMessage is nil; want the auth-required attribution")
	}
	assertNoSecrets(t, "error_message", *row.ErrorMessage)
	if want := "Atlassian"; !strings.Contains(*row.ErrorMessage, want) {
		t.Errorf("ErrorMessage = %q, want it to name %q so the operator knows which tool to connect", *row.ErrorMessage, want)
	}
	if len(row.Scores) != 0 {
		t.Errorf("scores = %d, want 0 (auth-walled trials are never scored)", len(row.Scores))
	}

	assertNoSecrets(t, "task.messages", mustJSON(t, task.messages))

	stored, err := resultRepo.GetByID(ctx, row.ID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if stored.ExecutionDataKey == nil || *stored.ExecutionDataKey == "" {
		t.Fatal("execution_data_key not set; the blob assertion would be vacuous")
	}
	execData, err := storageSvc.GetExecutionData(*stored.ExecutionDataKey)
	if err != nil {
		t.Fatalf("get execution data: %v", err)
	}
	assertNoSecrets(t, "executionData blob", mustJSON(t, execData))

	if stored.TaskEventsKey == nil || *stored.TaskEventsKey == "" {
		t.Fatal("task_events_key not set; the blob assertion would be vacuous")
	}
	events, err := storageSvc.GetTaskEvents(*stored.TaskEventsKey)
	if err != nil {
		t.Fatalf("get task events: %v", err)
	}
	assertNoSecrets(t, "taskEvents blob", mustJSON(t, events))
}

func assertNoSecrets(t *testing.T, sink, content string) {
	t.Helper()
	for _, secret := range leakSecrets {
		if strings.Contains(content, secret) {
			t.Errorf("%s leaked %q:\n%s", sink, secret, content)
		}
	}
}
