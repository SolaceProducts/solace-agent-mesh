// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const rawRunFailure = "agent crashed: connection reset by 10.1.2.3"

func (f experimentFixture) createWithModels(name, targetAgent string, models ...string) Experiment {
	f.api.t.Helper()
	body := f.body(name, targetAgent)
	entries := make([]map[string]any, len(models))
	for i, model := range models {
		entries[i] = map[string]any{"model": model, "alias": model}
	}
	body["models"] = entries
	return expectCreated[Experiment](f.api.t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
}

func (f experimentFixture) trigger(experimentID string, body any) Operation {
	f.api.t.Helper()
	rec := f.api.call(http.MethodPut, experimentsPath+"/"+experimentID+"/trigger", body)
	expectStatus(f.api.t, rec, http.StatusAccepted)
	op := decodeData[Operation](f.api.t, rec)
	expectLocation(f.api.t, rec, experimentsPath+"/"+experimentID+"/operations/"+op.ID)
	return op
}

func (f experimentFixture) groupRuns(runGroupID string) []Run {
	f.api.t.Helper()
	rec := f.api.call(http.MethodGet, runGroupsPath+"/"+runGroupID, nil)
	expectStatus(f.api.t, rec, http.StatusOK)
	return decodeData[RunGroup](f.api.t, rec).Runs
}

func (f experimentFixture) finish(runID, status string) {
	f.api.t.Helper()
	repo := store.NewEvalRunRepo(f.api.core.DB())
	var err error
	switch status {
	case eval.StatusPending:
	case eval.StatusRunning, eval.StatusCancelled:
		_, err = repo.UpdateStatus(f.api.t.Context(), runID, status)
	default:
		var message *string
		if status == eval.StatusFailed {
			failure := rawRunFailure
			message = &failure
		}
		_, err = repo.UpdateSummaryScores(f.api.t.Context(), runID, fmt.Sprintf(`{%q:0.75}`, f.evaluators[0]), status, message)
	}
	if err != nil {
		f.api.t.Fatalf("move run %s to %s: %v", runID, status, err)
	}
}

func (f experimentFixture) finishAll(runGroupID, status string) {
	f.api.t.Helper()
	for _, run := range f.groupRuns(runGroupID) {
		f.finish(run.ID, status)
	}
}

func (f experimentFixture) exec(query string, args ...any) {
	f.api.t.Helper()
	if _, err := f.api.core.DB().ExecContext(f.api.t.Context(), query, args...); err != nil {
		f.api.t.Fatalf("%s: %v", query, err)
	}
}

func TestRuns_TriggerReturnsAnOperationForTheRuns(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.createWithModels("trigger-experiment", "support_agent", "gpt-4o", "claude")

	rec := f.api.call(http.MethodPut, experimentsPath+"/"+experiment.ID+"/trigger", map[string]any{"runName": "March run"})
	expectStatus(t, rec, http.StatusAccepted)
	op := decodeData[Operation](t, rec)
	expectLocation(t, rec, experimentsPath+"/"+experiment.ID+"/operations/"+op.ID)
	raw := rawData(t, rec)
	expectISOTimes(t, raw, "createdTime")
	expectNull(t, raw, "completedTime", "error")
	if op.Type != "operation" || *op.ResourceID != experiment.ID || op.OperationType != "run" || op.Status != "pending" || op.CreatedBy == "" {
		t.Errorf("operation = %+v, want a pending run operation on the experiment, created by the caller", op)
	}

	polled := f.api.call(http.MethodGet, experimentsPath+"/"+experiment.ID+"/operations/"+op.ID, nil)
	expectStatus(t, polled, http.StatusOK)
	if got := decodeData[Operation](t, polled); got.ID != op.ID || got.Status != op.Status || got.CreatedTime != op.CreatedTime {
		t.Errorf("GET at Location = %+v, want the operation the trigger returned %+v", got, op)
	}

	runs := f.groupRuns(op.ID)
	if len(runs) != 2 {
		t.Fatalf("the operation started %d runs, want one per model", len(runs))
	}
	models := []string{*runs[0].Model, *runs[1].Model}
	slices.Sort(models)
	if !slices.Equal(models, []string{"claude", "gpt-4o"}) {
		t.Errorf("run models = %v, want one run per model", models)
	}
	for _, run := range runs {
		if run.RunName == nil || *run.RunName != "March run" || run.RunGroupID != op.ID || run.ExperimentID != experiment.ID || run.Source != "manual" {
			t.Errorf("run = %+v, want the trigger's name, group and experiment, started manually", run)
		}
	}
}

func TestRuns_TriggerWithoutNameUsesTheExperimentName(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("unnamed-run-experiment", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})
	runs := f.groupRuns(op.ID)
	if len(runs) != 1 || runs[0].RunName == nil || *runs[0].RunName != "unnamed-run-experiment" {
		t.Errorf("runs = %+v, want one run named for the experiment", runs)
	}
}

func TestRuns_TriggerWhileARunIsActiveIs409(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("busy-experiment", "support_agent")
	path := experimentsPath + "/" + experiment.ID + "/trigger"
	op := f.trigger(experiment.ID, map[string]any{})

	rec := f.api.call(http.MethodPut, path, map[string]any{})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)

	f.finishAll(op.ID, eval.StatusRunning)
	expectStatus(t, f.api.call(http.MethodPut, path, map[string]any{}), http.StatusConflict)

	for _, group := range decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath, nil)).Data {
		f.finishAll(group.ID, eval.StatusCompleted)
	}
	f.trigger(experiment.ID, map[string]any{})
}

func TestRuns_TriggerTheBrokerRefusesFailsTheRunsAndIs503(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.createWithModels("undispatched-experiment", "support_agent", "gpt-4o", "claude")
	path := experimentsPath + "/" + experiment.ID + "/trigger"
	brk := f.api.core.Services().Broker()
	if err := brk.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}

	rec := f.api.call(http.MethodPut, path, map[string]any{})
	expectStatus(t, rec, http.StatusServiceUnavailable)
	decodeError(t, rec)
	runs := decodeList[Run](t, f.api.call(http.MethodGet, experimentsPath+"/"+experiment.ID+"/runs", nil)).Data
	if len(runs) != 2 {
		t.Fatalf("the refused trigger left %d runs, want one per model", len(runs))
	}
	for _, run := range runs {
		if run.Status != eval.StatusFailed || run.ErrorMessage == nil || *run.ErrorMessage != "Could not start the run." {
			t.Errorf("run = %+v, want it failed with a SAM sentence, not left pending", run)
		}
	}

	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.trigger(experiment.ID, map[string]any{})
}

type publishLimitBroker struct {
	broker.Broker
	accept int
	seen   int
}

func (b *publishLimitBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	b.seen++
	if b.seen > b.accept {
		return errors.New("broker dropped")
	}
	return b.Broker.Publish(ctx, topic, msg)
}

func TestRuns_PartlySentTriggerIsAcceptedAndTheOperationIsInProgress(t *testing.T) {
	wrapTestBroker = func(b broker.Broker) broker.Broker { return &publishLimitBroker{Broker: b, accept: 1} }
	t.Cleanup(func() { wrapTestBroker = nil })
	f := newExperimentFixture(t)
	experiment := f.createWithModels("partly-sent-experiment", "support_agent", "gpt-4o", "claude")
	f.api.core.Services().Broker().(*publishLimitBroker).seen = 0

	op := f.trigger(experiment.ID, map[string]any{})

	if op.Status != "inProgress" {
		t.Errorf("operation status = %q, want inProgress while the sent run is pending", op.Status)
	}
	statuses := map[string]int{}
	for _, run := range f.groupRuns(op.ID) {
		statuses[run.Status]++
	}
	if statuses[eval.StatusPending] != 1 || statuses[eval.StatusFailed] != 1 {
		t.Errorf("run statuses = %v, want one pending (sent) and one failed (not sent)", statuses)
	}
}

func TestRuns_ConcurrentTriggersStartOneRunGroup(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("raced-experiment", "support_agent")
	path := experimentsPath + "/" + experiment.ID + "/trigger"

	const triggers = 8
	statuses := make(chan int, triggers)
	var wg sync.WaitGroup
	for range triggers {
		wg.Go(func() { statuses <- f.api.call(http.MethodPut, path, map[string]any{}).Code })
	}
	wg.Wait()
	close(statuses)

	accepted := 0
	for status := range statuses {
		switch status {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
		default:
			t.Errorf("trigger = %d, want 202 or 409", status)
		}
	}
	groups := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath, nil)).Data
	if accepted != 1 || len(groups) != 1 {
		t.Errorf("%d triggers accepted and %d run groups exist, want exactly one of each", accepted, len(groups))
	}
}

func TestRuns_TriggerValidation(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("trigger-validation-experiment", "support_agent")
	path := experimentsPath + "/" + experiment.ID + "/trigger"

	expectDetail(t, f.api.call(http.MethodPut, experimentsPath+"/not-a-uuid/trigger", map[string]any{}), http.StatusBadRequest, "experimentId")
	for name, body := range map[string]string{
		"empty body":     "",
		"truncated JSON": `{"runName":`,
		"unknown field":  `{"runName":"a","colour":"red"}`,
		"wrong type":     `{"runName":7}`,
	} {
		if rec := f.api.call(http.MethodPut, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("trigger with %s = %d, want 400", name, rec.Code)
		}
	}
	rec := f.api.call(http.MethodPut, experimentsPath+"/"+missingID+"/trigger", map[string]any{})
	expectStatus(t, rec, http.StatusNotFound)
	decodeError(t, rec)

	dataset := newDataset(f.api, "empty-dataset")
	body := f.body("empty-dataset-experiment", "support_agent")
	body["datasetId"] = dataset.ID
	empty := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
	expectDetail(t, f.api.call(http.MethodPut, experimentsPath+"/"+empty.ID+"/trigger", map[string]any{}), http.StatusUnprocessableEntity, "datasetId")
}

func TestRuns_Wire(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("wire-experiment", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})
	run := f.groupRuns(op.ID)[0]

	rec := f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	expectISOTimes(t, raw, "createdTime")
	expectNull(t, raw, "completedTime", "errorMessage", "runAs", "model")
	expectEmptyList(t, raw, "summaryScores")
	expectAbsent(t, raw, "startedAt", "startedBy", "completedAt")
	got := decodeData[Run](t, rec)
	if got.Type != "run" || got.Status != eval.StatusPending || got.CreatedBy == "" || got.ProgressTotal != 1 || got.ProgressCurrent != 0 {
		t.Errorf("run = %+v, want a pending run of one trial created by the caller", got)
	}

	snapshot := got.ConfigSnapshot
	if snapshot == nil || snapshot.Experiment == nil || snapshot.Dataset == nil || snapshot.Agent == nil {
		t.Fatalf("configSnapshot = %+v, want the experiment, dataset and agent", snapshot)
	}
	if snapshot.Experiment.ID != experiment.ID || snapshot.Experiment.Name != "wire-experiment" || snapshot.Dataset.ExampleCount != 1 ||
		len(snapshot.Dataset.Examples) != 1 || snapshot.Dataset.Examples[0].Prompt != "What is 2 + 2?" || snapshot.Agent.Name != "support_agent" {
		t.Errorf("configSnapshot = %+v, want what the run started with", snapshot)
	}
	if len(got.EvaluatorSnapshot) != 1 || got.EvaluatorSnapshot[0].ID != f.evaluators[0] || got.EvaluatorSnapshot[0].EvaluatorType == "" {
		t.Errorf("evaluatorSnapshot = %+v, want the experiment's evaluator", got.EvaluatorSnapshot)
	}
	rawSnapshot := raw["configSnapshot"].(map[string]any)
	agent, _ := rawSnapshot["agent"].(map[string]any)
	expectEmptyList(t, agent, "toolsets", "tools", "connectors", "skills")
	expectNull(t, rawSnapshot, "workflow")
	example := rawSnapshot["dataset"].(map[string]any)["examples"].([]any)[0].(map[string]any)
	expectNull(t, example, "inputData", "expectedPath", "expectedFacts")
}

func TestRuns_ScoresAndFailuresReadAsStored(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("scores-experiment", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})
	run := f.groupRuns(op.ID)[0]

	f.finish(run.ID, eval.StatusCompletedWithWarnings)
	rec := f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil)
	expectISOTimes(t, rawData(t, rec), "completedTime")
	got := decodeData[Run](t, rec)
	if got.Status != eval.StatusCompletedWithWarnings || len(got.SummaryScores) != 1 ||
		got.SummaryScores[0] != (RunScore{EvaluatorID: f.evaluators[0], Score: 0.75}) {
		t.Errorf("run = %+v, want completed_with_warnings with the evaluator's score as {evaluatorId, score}", got)
	}

	f.exec(`UPDATE eval_runs SET evaluator_stats = $1 WHERE id = $2`, `{"`+f.evaluators[0]+`":{"scored":10,"escalated":2,"withConfidence":9,
		"escalatedPercent":20,"meanConfidence":0.91,"judgeUsage":{"decision":{"inputTokens":9000,"outputTokens":30}}}}`, run.ID)
	statsRec := f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil)
	stats := decodeData[Run](t, statsRec).EvaluatorStats
	if len(stats) != 1 || stats[0].EvaluatorID != f.evaluators[0] || stats[0].Scored != 10 || stats[0].Escalated != 2 || stats[0].WithConfidence != 9 ||
		*stats[0].EscalatedPercent != 20 || *stats[0].MeanConfidence != 0.91 || stats[0].JudgeUsage.Decision.InputTokens != 9000 {
		t.Errorf("evaluatorStats = %+v, want the stored stats as {evaluatorId, …}", stats)
	}
	expectNull(t, rawData(t, statsRec)["evaluatorStats"].([]any)[0].(map[string]any)["judgeUsage"].(map[string]any), "llmJudge")
	if got := rawData(t, rec)["evaluatorStats"]; got == nil || len(got.([]any)) != 0 {
		t.Errorf("evaluatorStats before any were stored = %v, want []", got)
	}
}

func TestRuns_LegacyFailureTextReadsAsStored(t *testing.T) {
	const legacyRunFailure = "dispatch: publish to agent topic: broker timeout"
	f := newExperimentFixture(t)
	op := f.trigger(f.create("legacy-failure-experiment", "support_agent").ID, map[string]any{})
	run := f.groupRuns(op.ID)[0]

	f.exec(`UPDATE eval_runs SET status = 'failed', error_message = $1 WHERE id = $2`, legacyRunFailure, run.ID)
	failed := decodeData[Run](t, f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil))
	if failed.Status != eval.StatusFailed || failed.ErrorMessage == nil || *failed.ErrorMessage != legacyRunFailure {
		t.Errorf("run = %+v, want failed with the stored error message", failed)
	}
}

func TestRuns_SnapshotsDecodeWhatTheyCan(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("snapshot-experiment", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})
	run := f.groupRuns(op.ID)[0]

	f.exec(`UPDATE eval_runs SET config_snapshot = $1, evaluator_snapshot = $2 WHERE id = $3`,
		`{"agent":{"name":"discovered_agent","origin":"discovered","skills":["not-an-object"],"toolsets":["web"],
		   "tools":[{"id":"t1","name":"search"}],"modelConfig":{"alias":"general","modelParams":{"temperature":0.2}}},
		  "dataset":{"id":"d1","name":"set","example_count":1,"examples":[{"id":"e1","sequence_number":1,"prompt":"hi",
		   "artifact":{"filename":"a.pdf","version":2,"mime_type":"application/pdf","size_bytes":9}}]}}`,
		`[{"id":"ev1","name":"judge","type":"llm_judge","is_default":true,"choice_scores":{"pass":1},"resolved_model_name":"gpt-4o",
		  "modelConfig":{"alias":"general"}}]`,
		run.ID)
	got := decodeData[Run](t, f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil))
	agent := got.ConfigSnapshot.Agent
	if agent == nil || agent.Name != "discovered_agent" || agent.Origin == nil || *agent.Origin != "discovered" || len(agent.Skills) != 1 || agent.Skills[0].Name != "" ||
		!slices.Equal(agent.Toolsets, []string{"web"}) || len(agent.Tools) != 1 || agent.Tools[0].Name != "search" ||
		agent.ModelConfig == nil || agent.ModelConfig.ModelParams["temperature"] != 0.2 {
		t.Errorf("agent snapshot = %+v, want the parts that decode kept and a skill of the wrong shape read as empty", agent)
	}
	example := got.ConfigSnapshot.Dataset.Examples[0]
	if example.InputArtifact == nil || example.InputArtifact.Version != 2 || *example.InputArtifact.MimeType != "application/pdf" || example.ExpectedArtifact != nil {
		t.Errorf("example = %+v, want its input file and no expected file", example)
	}
	judge := got.EvaluatorSnapshot[0]
	if judge.EvaluatorType != "llm_judge" || !judge.IsDefault || judge.ChoiceScores["pass"] != 1 || judge.ResolvedModelName == nil || judge.ModelConfig == nil {
		t.Errorf("evaluator snapshot = %+v, want the stored fields under their v2 names", judge)
	}

	f.exec(`UPDATE eval_runs SET config_snapshot = 'not json', evaluator_snapshot = '{"a":' WHERE id = $1`, run.ID)
	rec := f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	expectNull(t, raw, "configSnapshot")
	expectEmptyList(t, raw, "evaluatorSnapshot")
}

func TestRuns_SnapshotsLeaveOutModelCredentials(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("credentials-experiment", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})
	run := f.groupRuns(op.ID)[0]
	secrets := []string{"SECRET-api-key", "SECRET-apiKey", "SECRET-header", "SECRET-auth-config"}

	f.exec(`UPDATE eval_runs SET config_snapshot = $1, evaluator_snapshot = $2 WHERE id = $3`,
		`{"agent":{"name":"support_agent","modelConfig":{"alias":"general","modelParams":{"temperature":0.2},
		   "api_key":"`+secrets[0]+`","apiKey":"`+secrets[1]+`","headers":{"Authorization":"`+secrets[2]+`"},"authConfig":{"api_key":"`+secrets[3]+`"}}}}`,
		`[{"id":"ev1","name":"judge","type":"llm_judge","modelConfig":{"alias":"general","api_key":"`+secrets[0]+`","apiKey":"`+secrets[1]+`",
		   "headers":{"Authorization":"`+secrets[2]+`"},"authConfig":{"api_key":"`+secrets[3]+`"}}}]`,
		run.ID)
	rec := f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil)
	expectStatus(t, rec, http.StatusOK)

	got := decodeData[Run](t, rec)
	if got.ConfigSnapshot.Agent.ModelConfig == nil || got.EvaluatorSnapshot[0].ModelConfig == nil {
		t.Fatalf("snapshots = %+v and %+v, want the model configurations kept", got.ConfigSnapshot.Agent, got.EvaluatorSnapshot)
	}
	for _, secret := range secrets {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("run = %s, must not carry %q from a stored model configuration", rec.Body.String(), secret)
		}
	}
}

func TestRuns_CancelAndDelete(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.createWithModels("cancel-experiment", "support_agent", "gpt-4o", "claude")
	op := f.trigger(experiment.ID, map[string]any{})
	runs := f.groupRuns(op.ID)

	rec := f.api.call(http.MethodPut, runsPath+"/"+runs[0].ID+"/cancel", nil)
	expectStatus(t, rec, http.StatusOK)
	expectISOTimes(t, rawData(t, rec), "completedTime")
	if got := decodeData[Run](t, rec); got.ID != runs[0].ID || got.Status != eval.StatusCancelled {
		t.Errorf("cancelled run = %+v, want that run, cancelled", got)
	}
	rec = f.api.call(http.MethodPut, runsPath+"/"+runs[0].ID+"/cancel", nil)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)

	f.finish(runs[1].ID, eval.StatusCompleted)
	expectStatus(t, f.api.call(http.MethodPut, runsPath+"/"+runs[1].ID+"/cancel", nil), http.StatusUnprocessableEntity)

	expectStatus(t, f.api.call(http.MethodDelete, runsPath+"/"+runs[0].ID, nil), http.StatusNoContent)
	expectStatus(t, f.api.call(http.MethodGet, runsPath+"/"+runs[0].ID, nil), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodDelete, runsPath+"/"+runs[0].ID, nil), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodGet, runsPath+"/"+runs[1].ID, nil), http.StatusOK)
}

func TestRuns_UnknownIDs(t *testing.T) {
	f := newExperimentFixture(t)
	for _, call := range []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodDelete, ""}, {http.MethodPut, "/cancel"},
	} {
		expectDetail(t, f.api.call(call.method, runsPath+"/not-a-uuid"+call.suffix, nil), http.StatusBadRequest, "id")
		rec := f.api.call(call.method, runsPath+"/"+missingID+call.suffix, nil)
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
	expectDetail(t, f.api.call(http.MethodGet, experimentsPath+"/not-a-uuid/runs", nil), http.StatusBadRequest, "experimentId")
	rec := f.api.call(http.MethodGet, experimentsPath+"/"+missingID+"/runs", nil)
	expectStatus(t, rec, http.StatusNotFound)
	decodeError(t, rec)
}

func TestRuns_PagingNewestFirst(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("paging-runs-experiment", "support_agent")
	other := f.create("other-runs-experiment", "support_agent")
	f.trigger(other.ID, map[string]any{})

	var ids []string
	for i := range 3 {
		op := f.trigger(experiment.ID, map[string]any{})
		run := f.groupRuns(op.ID)[0]
		f.finish(run.ID, eval.StatusCompleted)
		f.exec(`UPDATE eval_runs SET started_time = $1 WHERE id = $2`, 1_700_000_000_000+int64(i)*1000, run.ID)
		ids = append([]string{run.ID}, ids...)
	}
	path := experimentsPath + "/" + experiment.ID + "/runs"

	defaults := decodeList[Run](t, f.api.call(http.MethodGet, path, nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 3 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 3, the other experiment's run left out", p, defaultPageSize)
	}
	if got := runIDs(defaults.Data); !slices.Equal(got, ids) {
		t.Errorf("order = %v, want newest first %v", got, ids)
	}
	second := decodeList[Run](t, f.api.call(http.MethodGet, path+"?pageSize=2&pageNumber=2", nil))
	if got := runIDs(second.Data); !slices.Equal(got, ids[2:]) || second.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 of size 2 = %v, pagination %+v, want the oldest run", got, second.Meta.Pagination)
	}
	if beyond := decodeList[Run](t, f.api.call(http.MethodGet, path+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}
	expectDetail(t, f.api.call(http.MethodGet, path+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectStatus(t, f.api.call(http.MethodGet, path+"?pageNumber=ten", nil), http.StatusBadRequest)
}

func runIDs(runs []Run) []string {
	ids := make([]string, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	return ids
}

func TestRuns_Scopes(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("scoped-runs-experiment", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})
	run := f.groupRuns(op.ID)[0]
	triggerPath := experimentsPath + "/" + experiment.ID + "/trigger"

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, triggerPath, map[string]any{}},
		{http.MethodPut, runsPath + "/" + run.ID + "/cancel", nil},
		{http.MethodDelete, runsPath + "/" + run.ID, nil},
	} {
		if rec := f.api.call(call.method, call.path, call.body, rbac.ScopeEvaluationRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a reader = %d, want 403", call.method, call.path, rec.Code)
		}
	}
	expectStatus(t, f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, f.api.call(http.MethodGet, experimentsPath+"/"+experiment.ID+"/runs", nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, f.api.call(http.MethodGet, runsPath+"/"+run.ID, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)

	expectStatus(t, f.api.call(http.MethodPut, runsPath+"/"+run.ID+"/cancel", nil, rbac.ScopeEvaluationInvoke), http.StatusOK)
	expectStatus(t, f.api.call(http.MethodDelete, runsPath+"/"+run.ID, nil, rbac.ScopeEvaluationDelete), http.StatusNoContent)
	expectStatus(t, f.api.call(http.MethodPut, triggerPath, map[string]any{}, rbac.ScopeEvaluationInvoke), http.StatusAccepted)
}

func TestRuns_RefusedCancelCarriesNoServiceText(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("refused-cancel-experiment", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})
	run := f.groupRuns(op.ID)[0]
	f.finish(run.ID, eval.StatusFailed)

	rec := f.api.call(http.MethodPut, runsPath+"/"+run.ID+"/cancel", nil)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if body := decodeError(t, rec); body.Message != "Unable to cancel run" || strings.Contains(rec.Body.String(), run.ID) {
		t.Errorf("refused cancel = %s, want the handler's sentence and no service text", rec.Body.String())
	}
}
