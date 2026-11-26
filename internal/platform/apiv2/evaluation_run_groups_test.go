// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func runGroupIDs(groups []RunGroupSummary) []string {
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	return ids
}

func TestRunGroups_Lifecycle(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.createWithModels("group-lifecycle", "support_agent", "model-a", "model-b")
	op := f.trigger(experiment.ID, map[string]any{"runName": "March run"})

	rec := f.api.call(http.MethodGet, runGroupsPath+"/"+op.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	expectISOTimes(t, raw, "createdTime")
	expectNull(t, raw, "completedTime")
	expectAbsent(t, raw, "startedAt", "completedAt")
	group := decodeData[RunGroup](t, rec)
	if group.ID != op.ID || group.Type != "runGroup" || group.ExperimentID != experiment.ID || group.RunName == nil || *group.RunName != "March run" ||
		group.Status != eval.StatusPending || group.ProgressTotal != 2 || len(group.Runs) != 2 || group.ConfigOutdated {
		t.Errorf("run group = %+v, want a pending group of two runs named March run, config current", group)
	}

	expectStatus(t, f.api.call(http.MethodPatch, experimentsPath+"/"+experiment.ID, map[string]any{"name": "group-lifecycle-renamed"}), http.StatusOK)
	f.exec(`UPDATE eval_runs SET started_time = 1000 WHERE run_group_id = $1`, op.ID)
	if outdated := decodeData[RunGroup](t, f.api.call(http.MethodGet, runGroupsPath+"/"+op.ID, nil)); !outdated.ConfigOutdated {
		t.Error("configOutdated = false after the experiment changed since the runs started, want true")
	}

	f.finishAll(op.ID, eval.StatusCompleted)
	rec = f.api.call(http.MethodGet, runGroupsPath+"/"+op.ID, nil)
	expectISOTimes(t, rawData(t, rec), "completedTime")
	if done := decodeData[RunGroup](t, rec); done.Status != eval.StatusCompleted {
		t.Errorf("status = %q, want completed once every run is", done.Status)
	}

	summaries := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath, nil))
	if len(summaries.Data) != 1 {
		t.Fatalf("run groups = %+v, want the one group", summaries.Data)
	}
	summary := summaries.Data[0]
	if summary.ID != op.ID || summary.Type != "runGroup" || summary.ExperimentID != experiment.ID || summary.ExperimentName != "group-lifecycle-renamed" ||
		summary.TargetAgent != "support_agent" || summary.TargetType != "agent" || summary.RunCount != 2 || summary.Status != eval.StatusCompleted || summary.DurationSeconds == nil {
		t.Errorf("summary = %+v, want the group's experiment, agent, two runs and its duration", summary)
	}
	if flow := toRunGroupSummary(&eval.RunGroupSummaryResponse{TargetType: "workflow", TargetAgent: "order_flow"}); flow.TargetType != "workflow" || flow.TargetAgent != "order_flow" {
		t.Errorf("workflow summary = %+v, want targetType workflow naming the workflow", flow)
	}
	listRaw := decodeList[map[string]any](t, f.api.call(http.MethodGet, runGroupsPath, nil)).Data[0]
	expectISOTimes(t, listRaw, "createdTime", "completedTime")
	expectAbsent(t, listRaw, "startedAt", "completedAt", "runGroupId", "agent")

	expectStatus(t, f.api.call(http.MethodDelete, runGroupsPath+"/"+op.ID, nil), http.StatusNoContent)
	expectStatus(t, f.api.call(http.MethodGet, runGroupsPath+"/"+op.ID, nil), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodDelete, runGroupsPath+"/"+op.ID, nil), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodGet, runsPath+"/"+group.Runs[0].ID, nil), http.StatusNotFound)
}

func TestRunGroups_PartlyFinished(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.createWithModels("group-partly-finished", "support_agent", "model-a", "model-b")
	op := f.trigger(experiment.ID, map[string]any{})
	runs := f.groupRuns(op.ID)
	f.finish(runs[0].ID, eval.StatusCompleted)
	f.exec(`UPDATE eval_runs SET completed_time = 1700000100000 WHERE id = $1`, runs[0].ID)

	detail := f.api.call(http.MethodGet, runGroupsPath+"/"+op.ID, nil)
	expectStatus(t, detail, http.StatusOK)
	expectNull(t, rawData(t, detail), "completedTime")

	summaries := decodeList[map[string]any](t, f.api.call(http.MethodGet, runGroupsPath, nil))
	if len(summaries.Data) != 1 {
		t.Fatalf("run groups = %+v, want the one group", summaries.Data)
	}
	expectISOTimes(t, summaries.Data[0], "completedTime")
	if got := summaries.Data[0]["completedTime"]; got != "2023-11-14T22:15:00.000Z" {
		t.Errorf("summary completedTime = %v, want the latest completion so far", got)
	}
}

func TestRunGroups_ListFiltersAndPaging(t *testing.T) {
	f := newExperimentFixture(t)
	support := f.create("filter-support", "support_agent")
	billing := f.create("filter-billing", "billing_agent")

	var ids []string
	for i, target := range []struct {
		experiment Experiment
		name       string
	}{{support, "Alpha Run"}, {billing, "Beta run"}, {support, "alpha again"}} {
		op := f.trigger(target.experiment.ID, map[string]any{"runName": target.name})
		f.finishAll(op.ID, eval.StatusCompleted)
		f.exec(`UPDATE eval_runs SET started_time = $1 WHERE run_group_id = $2`, 1_700_000_000_000+int64(i)*1000, op.ID)
		ids = append([]string{op.ID}, ids...)
	}

	all := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath, nil))
	if got := runGroupIDs(all.Data); !slices.Equal(got, ids) || all.Meta.Pagination.Count != 3 {
		t.Errorf("run groups = %v with count %d, want %v newest first", got, all.Meta.Pagination.Count, ids)
	}

	byAgent := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?targetAgent=support_agent", nil))
	if got := runGroupIDs(byAgent.Data); !slices.Equal(got, []string{ids[0], ids[2]}) {
		t.Errorf("targetAgent filter = %v, want the two support groups", got)
	}
	byName := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?name=ALPHA", nil))
	if got := runGroupIDs(byName.Data); !slices.Equal(got, []string{ids[0], ids[2]}) {
		t.Errorf("name filter = %v, want both alpha groups regardless of case", got)
	}
	both := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?name=beta&targetAgent=support_agent", nil))
	if len(both.Data) != 0 || both.Meta.Pagination.Count != 0 {
		t.Errorf("name and targetAgent together = %+v, want no group", both.Data)
	}
	if v1Name := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?agent=support_agent", nil)); len(v1Name.Data) != 3 {
		t.Errorf("v1's agent filter matched %d groups, want it ignored", len(v1Name.Data))
	}

	f.exec(`UPDATE eval_experiments SET target_type = 'workflow' WHERE id = $1`, billing.ID)
	workflows := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?targetType=workflow", nil))
	if got := runGroupIDs(workflows.Data); !slices.Equal(got, []string{ids[1]}) {
		t.Errorf("targetType=workflow = %v, want only the billing group", got)
	}
	agents := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?targetType=agent", nil))
	if got := runGroupIDs(agents.Data); !slices.Equal(got, []string{ids[0], ids[2]}) {
		t.Errorf("targetType=agent = %v, want the two support groups", got)
	}
	expectDetail(t, f.api.call(http.MethodGet, runGroupsPath+"?targetType=robot", nil), http.StatusUnprocessableEntity, "targetType")

	second := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?pageSize=2&pageNumber=2", nil))
	if got := runGroupIDs(second.Data); !slices.Equal(got, ids[2:]) || second.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 = %v with %+v, want the oldest group", got, second.Meta.Pagination)
	}
	if beyond := decodeList[RunGroupSummary](t, f.api.call(http.MethodGet, runGroupsPath+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}
	expectDetail(t, f.api.call(http.MethodGet, runGroupsPath+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
}

func TestRunGroups_AgentsAreDistinctAndPaged(t *testing.T) {
	f := newExperimentFixture(t)
	for i, agent := range []string{"zeta_agent", "alpha_agent", "zeta_agent", "mid_agent"} {
		experiment := f.create(fmt.Sprintf("agents-%d", i), agent)
		f.trigger(experiment.ID, map[string]any{})
		f.exec(`UPDATE eval_runs SET status = 'completed' WHERE experiment_id = $1`, experiment.ID)
	}

	rec := f.api.call(http.MethodGet, runGroupAgentsPath, nil)
	expectStatus(t, rec, http.StatusOK)
	agents := decodeList[RunGroupAgent](t, rec)
	var names []string
	for _, a := range agents.Data {
		names = append(names, a.TargetAgent)
	}
	if !slices.Equal(names, []string{"alpha_agent", "mid_agent", "zeta_agent"}) || agents.Meta.Pagination.Count != 3 {
		t.Errorf("agents = %v with count %d, want each agent once, alphabetically", names, agents.Meta.Pagination.Count)
	}
	for _, item := range decodeList[map[string]any](t, rec).Data {
		if len(item) != 2 || item["targetType"] != "agent" {
			t.Errorf("agent item = %v, want only targetAgent and targetType agent", item)
		}
	}
	second := decodeList[RunGroupAgent](t, f.api.call(http.MethodGet, runGroupAgentsPath+"?pageSize=2&pageNumber=2", nil))
	if len(second.Data) != 1 || second.Data[0].TargetAgent != "zeta_agent" || second.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 = %+v, want the last agent", second)
	}
	expectDetail(t, f.api.call(http.MethodGet, runGroupAgentsPath+"?pageNumber=0", nil), http.StatusUnprocessableEntity, "pageNumber")
}

func TestRunGroups_AgentsCarryTheirTargetType(t *testing.T) {
	f := newExperimentFixture(t)
	var flowID string
	for i, target := range []string{"support_agent", "order_flow", "order_flow"} {
		experiment := f.create(fmt.Sprintf("targets-%d", i), target)
		f.trigger(experiment.ID, map[string]any{})
		if i == 1 {
			flowID = experiment.ID
		}
	}
	f.exec(`UPDATE eval_experiments SET target_type = 'workflow' WHERE id = $1`, flowID)

	all := decodeList[RunGroupAgent](t, f.api.call(http.MethodGet, runGroupAgentsPath, nil))
	want := []RunGroupAgent{{TargetAgent: "order_flow", TargetType: "agent"}, {TargetAgent: "order_flow", TargetType: "workflow"}, {TargetAgent: "support_agent", TargetType: "agent"}}
	if !slices.Equal(all.Data, want) || all.Meta.Pagination.Count != 3 {
		t.Errorf("targets = %+v with count %d, want %+v: a name evaluated as both kinds once for each", all.Data, all.Meta.Pagination.Count, want)
	}

	workflows := decodeList[RunGroupAgent](t, f.api.call(http.MethodGet, runGroupAgentsPath+"?targetType=workflow", nil))
	if !slices.Equal(workflows.Data, want[1:2]) || workflows.Meta.Pagination.Count != 1 {
		t.Errorf("targetType=workflow = %+v with count %d, want only the workflow", workflows.Data, workflows.Meta.Pagination.Count)
	}
	agents := decodeList[RunGroupAgent](t, f.api.call(http.MethodGet, runGroupAgentsPath+"?targetType=agent", nil))
	if !slices.Equal(agents.Data, []RunGroupAgent{want[0], want[2]}) || agents.Meta.Pagination.Count != 2 {
		t.Errorf("targetType=agent = %+v with count %d, want the two agents", agents.Data, agents.Meta.Pagination.Count)
	}
	expectDetail(t, f.api.call(http.MethodGet, runGroupAgentsPath+"?targetType=robot", nil), http.StatusUnprocessableEntity, "targetType")
}

func TestRunGroups_ResultsOnePerModel(t *testing.T) {
	f := newExperimentFixture(t)
	body := f.body("group-results", "support_agent")
	body["models"] = []map[string]any{
		{"modelConfigId": missingID, "model": "gpt-4o", "alias": "general"},
		{"model": "plain-model", "alias": "plain"},
	}
	experiment := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
	op := f.trigger(experiment.ID, map[string]any{})
	runs := f.groupRuns(op.ID)
	path := runGroupsPath + "/" + op.ID + "/results"

	rec := f.api.call(http.MethodGet, path, nil)
	expectStatus(t, rec, http.StatusOK)
	results := decodeList[RunGroupModelResult](t, rec)
	if results.Meta.Pagination.Count != 2 || len(results.Data) != 2 {
		t.Fatalf("results = %+v, want one per model", results)
	}
	var configured, named *RunGroupModelResult
	for i := range results.Data {
		if results.Data[i].ModelConfigID != nil {
			configured = &results.Data[i]
		} else {
			named = &results.Data[i]
		}
	}
	if configured == nil || *configured.ModelConfigID != missingID || named == nil {
		t.Fatalf("results = %+v, want one result with the model configuration's id and one without", results.Data)
	}
	runModels := map[string]string{}
	for _, run := range runs {
		runModels[run.ID] = *run.Model
	}
	if runModels[configured.RunID] != missingID || runModels[named.RunID] != "plain-model" {
		t.Errorf("runIds = %q and %q, want each result to point at its model's run", configured.RunID, named.RunID)
	}
	for _, item := range decodeList[map[string]any](t, rec).Data {
		expectAbsent(t, item, "id", "type", "model")
		expectNull(t, item["tokenSummary"].(map[string]any), "meanInputTokens", "meanCachedInputTokens", "meanOutputTokens", "meanReasoningTokens", "meanTotalTokens")
		if item["tokenSummary"].(map[string]any)["usageResultCount"] != float64(0) {
			t.Errorf("tokenSummary = %v, want no usage for runs without results", item["tokenSummary"])
		}
	}

	second := decodeList[RunGroupModelResult](t, f.api.call(http.MethodGet, path+"?pageSize=1&pageNumber=2", nil))
	if len(second.Data) != 1 || second.Meta.Pagination.Count != 2 || second.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 = %+v, want the second model", second)
	}
	if beyond := decodeList[RunGroupModelResult](t, f.api.call(http.MethodGet, path+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}
}

func TestRunGroups_UnknownIDs(t *testing.T) {
	f := newExperimentFixture(t)
	for _, call := range []struct{ method, suffix, field string }{
		{http.MethodGet, "", "id"}, {http.MethodDelete, "", "id"}, {http.MethodGet, "/results", "runGroupId"},
	} {
		expectDetail(t, f.api.call(call.method, runGroupsPath+"/not-a-uuid"+call.suffix, nil), http.StatusBadRequest, call.field)
		rec := f.api.call(call.method, runGroupsPath+"/"+missingID+call.suffix, nil)
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
}

func TestRunGroups_Scopes(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("group-scopes", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})

	for _, path := range []string{runGroupsPath, runGroupAgentsPath, runGroupsPath + "/" + op.ID, runGroupsPath + "/" + op.ID + "/results"} {
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationRead), http.StatusOK)
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationInvoke), http.StatusForbidden)
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	}
	deletePath := runGroupsPath + "/" + op.ID
	expectStatus(t, f.api.call(http.MethodDelete, deletePath, nil, rbac.ScopeEvaluationRead), http.StatusForbidden)
	expectStatus(t, f.api.call(http.MethodDelete, deletePath, nil, rbac.ScopeEvaluationInvoke), http.StatusForbidden)
	expectStatus(t, f.api.call(http.MethodDelete, deletePath, nil, rbac.ScopeEvaluationDelete), http.StatusUnprocessableEntity)
	for _, run := range f.groupRuns(op.ID) {
		expectStatus(t, f.api.call(http.MethodPut, runsPath+"/"+run.ID+"/cancel", nil), http.StatusOK)
	}
	expectStatus(t, f.api.call(http.MethodDelete, deletePath, nil, rbac.ScopeEvaluationDelete), http.StatusNoContent)
}
