// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const watchlistChartPath = watchlistPath + "/chartData"

func replaceWatchlist(api *entrypointAPI, names ...string) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPut, watchlistPath, map[string]any{"agentNames": names})
}

func watchedNames(entries []WatchlistEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.AgentName
	}
	return names
}

func TestWatchlist_Lifecycle(t *testing.T) {
	api := newEntrypointAPI(t, nil)

	empty := api.call(http.MethodGet, watchlistPath, nil)
	expectStatus(t, empty, http.StatusOK)
	if got := decodeList[WatchlistEntry](t, empty); len(got.Data) != 0 || got.Meta.Pagination.Count != 0 {
		t.Fatalf("a new caller's watchlist = %+v, want empty", got)
	}

	put := replaceWatchlist(api, "bravo", "alpha")
	expectStatus(t, put, http.StatusOK)
	replaced := decodeList[WatchlistEntry](t, put)
	if !slices.Equal(watchedNames(replaced.Data), []string{"bravo", "alpha"}) || replaced.Data[0].Position != 0 || replaced.Data[1].Position != 1 {
		t.Errorf("PUT answered %+v, want the new list in the order sent, positions from 0", replaced.Data)
	}
	if first := decodeList[map[string]any](t, put).Data[0]; first["resolvedModelProvider"] != "general" || first["isBuiltIn"] != true || first["targetType"] != "agent" {
		t.Errorf("an agent with no platform record = %v, want an agent, provider general and isBuiltIn true", first)
	}

	got := api.call(http.MethodGet, watchlistPath, nil)
	expectStatus(t, got, http.StatusOK)
	if listed := decodeList[WatchlistEntry](t, got); !slices.Equal(watchedNames(listed.Data), []string{"bravo", "alpha"}) {
		t.Errorf("GET after PUT = %v, want the list PUT stored", watchedNames(listed.Data))
	}
	expectAbsent(t, decodeList[map[string]any](t, got).Data[0], "id", "type", "createdAt", "updatedAt", "created_time")

	shorter := decodeList[WatchlistEntry](t, replaceWatchlist(api, "charlie"))
	if !slices.Equal(watchedNames(shorter.Data), []string{"charlie"}) || shorter.Data[0].Position != 0 {
		t.Errorf("replacing with one agent = %+v, want only charlie, back at position 0", shorter.Data)
	}

	for name, body := range map[string]any{"an empty list": map[string]any{"agentNames": []string{}}, "null": map[string]any{"agentNames": nil}} {
		replaceWatchlist(api, "charlie")
		rec := api.call(http.MethodPut, watchlistPath, body)
		expectStatus(t, rec, http.StatusOK)
		if cleared := decodeList[WatchlistEntry](t, rec); len(cleared.Data) != 0 {
			t.Errorf("PUT of %s answered %+v, want the watchlist cleared", name, cleared.Data)
		}
		if listed := decodeList[WatchlistEntry](t, api.call(http.MethodGet, watchlistPath, nil)); len(listed.Data) != 0 {
			t.Errorf("GET after PUT of %s = %+v, want empty", name, listed.Data)
		}
	}
}

func TestWatchlist_ReplaceTrimsAndCollapsesDuplicates(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	rec := replaceWatchlist(api, "  alpha ", "bravo", "alpha", "charlie", "bravo", "delta", "echo")
	expectStatus(t, rec, http.StatusOK)
	got := decodeList[WatchlistEntry](t, rec)
	if want := []string{"alpha", "bravo", "charlie", "delta", "echo"}; !slices.Equal(watchedNames(got.Data), want) {
		t.Errorf("names = %v, want %v: trimmed, a repeat counted once at its first place, so 5 unique fit the cap", watchedNames(got.Data), want)
	}
}

func TestWatchlist_ReplaceValidation(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	expectStatus(t, replaceWatchlist(api, "kept"), http.StatusOK)

	for name, body := range map[string]string{
		"truncated JSON": `{"agentNames":`,
		"unknown field":  `{"agentNames":[],"colour":"red"}`,
		"wrong type":     `{"agentNames":"alpha"}`,
		"wrong element":  `{"agentNames":[7]}`,
		"empty body":     ``,
	} {
		if rec := api.call(http.MethodPut, watchlistPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT with %s = %d, want 400", name, rec.Code)
		}
	}
	expectDetail(t, api.call(http.MethodPut, watchlistPath, `{"agentNames":"alpha"}`), http.StatusBadRequest, "agentNames")

	rec := replaceWatchlist(api, "", "   ", strings.Repeat("a", 256), "one", "two", "three", "four", "five", "six")
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	problems := decodeError(t, rec).ValidationDetails["agentNames"]
	if len(problems) != 4 {
		t.Errorf("agentNames problems = %q, want a blank, a blank, an over-long name and the cap, all at once", problems)
	}

	tooMany := replaceWatchlist(api, "a", "b", "c", "d", "e", "f")
	expectDetail(t, tooMany, http.StatusUnprocessableEntity, "agentNames")
	if _, ok := decodeError(t, tooMany).ValidationDetails["agent_names"]; ok {
		t.Errorf("validationDetails names agent_names: %s", tooMany.Body.String())
	}

	if kept := decodeList[WatchlistEntry](t, api.call(http.MethodGet, watchlistPath, nil)); !slices.Equal(watchedNames(kept.Data), []string{"kept"}) {
		t.Errorf("after rejected PUTs the watchlist = %v, want it unchanged", watchedNames(kept.Data))
	}
}

func TestWatchlist_PagingAndOrder(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	expectStatus(t, replaceWatchlist(api, "e", "d", "c", "b", "a"), http.StatusOK)

	defaults := decodeList[WatchlistEntry](t, api.call(http.MethodGet, watchlistPath, nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 5 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 5", p, defaultPageSize)
	}
	if !slices.Equal(watchedNames(defaults.Data), []string{"e", "d", "c", "b", "a"}) {
		t.Errorf("order = %v, want the order the caller chose", watchedNames(defaults.Data))
	}

	third := decodeList[WatchlistEntry](t, api.call(http.MethodGet, watchlistPath+"?pageSize=2&pageNumber=3", nil))
	if !slices.Equal(watchedNames(third.Data), []string{"a"}) || third.Data[0].Position != 4 || third.Meta.Pagination.TotalPages != 3 {
		t.Errorf("page 3 of size 2 = %+v, pagination %+v, want the last entry at position 4 of 3 pages", third.Data, third.Meta.Pagination)
	}
	if beyond := decodeList[WatchlistEntry](t, api.call(http.MethodGet, watchlistPath+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}

	put := api.call(http.MethodPut, watchlistPath+"?pageSize=2", map[string]any{"agentNames": []string{"p", "q", "r"}})
	expectStatus(t, put, http.StatusOK)
	if got := decodeList[WatchlistEntry](t, put); !slices.Equal(watchedNames(got.Data), []string{"p", "q"}) || got.Meta.Pagination.Count != 3 {
		t.Errorf("PUT with pageSize=2 = %v, pagination %+v, want the first page of 3", watchedNames(got.Data), got.Meta.Pagination)
	}

	for _, path := range []string{watchlistPath, watchlistChartPath} {
		expectDetail(t, api.call(http.MethodGet, path+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
		expectDetail(t, api.call(http.MethodGet, path+"?pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
		expectDetail(t, api.call(http.MethodGet, path+"?pageNumber=ten", nil), http.StatusBadRequest, "pageNumber")
	}
	expectDetail(t, api.call(http.MethodPut, watchlistPath+"?pageSize=101", map[string]any{"agentNames": []string{}}), http.StatusUnprocessableEntity, "pageSize")
}

func TestWatchlist_EachCallerHasTheirOwn(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	others := store.NewEvalWatchlistRepo(api.core.DB())
	if _, err := others.SetForUser(t.Context(), "someone-else", []string{"theirs", "also-theirs"}); err != nil {
		t.Fatal(err)
	}

	if got := decodeList[WatchlistEntry](t, api.call(http.MethodGet, watchlistPath, nil)); len(got.Data) != 0 {
		t.Errorf("list = %+v, want none of another user's entries", got.Data)
	}
	if got := decodeList[WatchlistChartSeries](t, api.call(http.MethodGet, watchlistChartPath, nil)); len(got.Data) != 0 {
		t.Errorf("chart data = %+v, want none of another user's agents", got.Data)
	}
	if got := decodeList[WatchlistEntry](t, replaceWatchlist(api, "mine")); got.Meta.Pagination.Count != 1 {
		t.Errorf("after PUT = %+v, want only the caller's entry", got.Data)
	}
	remaining, err := others.ListForUser(t.Context(), "someone-else")
	if err != nil || len(remaining) != 2 {
		t.Errorf("the other user's watchlist = %d entries (err %v), want 2 untouched", len(remaining), err)
	}
}

type chartFixture struct {
	api          *entrypointAPI
	experimentID string
	evaluatorID  string
	runs         *store.EvalRunRepo
}

func newChartFixture(t *testing.T, agentName string) chartFixture {
	t.Helper()
	f := newExperimentFixture(t)
	body := f.body("chart-"+agentName, agentName)
	body["primaryEvaluatorId"] = f.evaluators[0]
	body["models"] = []map[string]any{{"model": "general", "alias": "general"}}
	created := expectCreated[Experiment](t, f.api.call(http.MethodPost, experimentsPath, body), "evaluations/experiments")
	return chartFixture{api: f.api, experimentID: created.ID, evaluatorID: f.evaluators[0], runs: store.NewEvalRunRepo(f.api.core.DB())}
}

func (f chartFixture) addRun(t *testing.T, status string, startedTime int64, score *float64) string {
	t.Helper()
	run := &store.EvalRun{
		ID: idgen.New(), ExperimentID: f.experimentID, RunGroupID: idgen.New(), Status: status,
		Model: "general", StartedBy: "test", StartedTime: startedTime,
	}
	if score != nil {
		scores := fmt.Sprintf(`{%q: %v}`, f.evaluatorID, *score)
		run.SummaryScores = &scores
	}
	if err := f.runs.Create(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	return run.RunGroupID
}

func TestWatchlist_ChartData(t *testing.T) {
	f := newChartFixture(t, "charted_agent")
	const first, second, third = int64(1_760_000_000_000), int64(1_760_000_100_000), int64(1_760_000_200_000)
	score := func(v float64) *float64 { return &v }
	groupThird := f.addRun(t, "completed", third, score(0.5))
	groupFirst := f.addRun(t, "completed", first, score(0.25))
	groupSecond := f.addRun(t, "completed_with_warnings", second, nil)
	f.addRun(t, "failed", third+1000, score(0.9))
	f.addRun(t, "running", third+2000, nil)
	expectStatus(t, replaceWatchlist(f.api, "charted_agent", "idle_agent"), http.StatusOK)

	rec := f.api.call(http.MethodGet, watchlistChartPath, nil)
	expectStatus(t, rec, http.StatusOK)
	series := decodeList[WatchlistChartSeries](t, rec)
	if p := series.Meta.Pagination; p.Count != 2 || len(series.Data) != 2 {
		t.Fatalf("chart = %d series, pagination %+v, want one per watched agent", len(series.Data), p)
	}
	charted, idle := series.Data[0], series.Data[1]
	if charted.AgentName != "charted_agent" || charted.TargetType != "agent" || charted.Position != 0 || charted.ResolvedModelProvider == nil || *charted.ResolvedModelProvider != "general" || !charted.IsBuiltIn {
		t.Errorf("first series = %+v, want charted_agent at position 0 under general", charted)
	}
	if len(charted.Experiments) != 1 || charted.Experiments[0].ExperimentID != f.experimentID || charted.Experiments[0].PrimaryEvaluatorID != f.evaluatorID {
		t.Fatalf("charted experiments = %+v, want the fixture's experiment on its primary evaluator", charted.Experiments)
	}
	if name := charted.Experiments[0].PrimaryEvaluatorName; name == nil || *name == "" {
		t.Errorf("primaryEvaluatorName = %v, want the evaluator's name", name)
	}

	points := charted.Experiments[0].Points
	wantGroups := []string{groupFirst, groupSecond, groupThird}
	gotGroups := make([]string, len(points))
	for i, p := range points {
		gotGroups[i] = p.RunGroupID
	}
	if !slices.Equal(gotGroups, wantGroups) {
		t.Fatalf("points are run groups %v, want %v: completed runs only, oldest first", gotGroups, wantGroups)
	}
	if points[0].Score == nil || *points[0].Score != 0.25 || points[1].Score != nil || points[2].Score == nil || *points[2].Score != 0.5 {
		t.Errorf("scores = %v %v %v, want 0.25, null, 0.5", points[0].Score, points[1].Score, points[2].Score)
	}
	if points[0].StartedTime != Instant(first) || points[2].StartedTime != Instant(third) {
		t.Errorf("startedTime = %d..%d, want %d..%d", points[0].StartedTime, points[2].StartedTime, first, third)
	}

	if len(idle.Experiments) != 0 || idle.AgentName != "idle_agent" || idle.Position != 1 {
		t.Errorf("second series = %+v, want idle_agent at position 1 with no experiments", idle)
	}
}

func TestWatchlist_ChartDataWire(t *testing.T) {
	f := newChartFixture(t, "wire_agent")
	f.addRun(t, "completed", 1_760_000_000_000, nil)
	expectStatus(t, replaceWatchlist(f.api, "wire_agent", "idle_agent"), http.StatusOK)

	rec := f.api.call(http.MethodGet, watchlistChartPath, nil)
	items := decodeList[map[string]any](t, rec).Data
	expectAbsent(t, items[0], "id", "type")
	expectEmptyList(t, items[1], "experiments")

	experiments, _ := items[0]["experiments"].([]any)
	series, _ := experiments[0].(map[string]any)
	expectAbsent(t, series, "id", "type", "startedAt")
	point := series["points"].([]any)[0].(map[string]any)
	expectISOTimes(t, point, "startedTime")
	expectNull(t, point, "score")
	expectAbsent(t, point, "startedAt")
}

func TestWatchlist_ChartDataPaging(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	expectStatus(t, replaceWatchlist(api, "one", "two", "three"), http.StatusOK)

	second := decodeList[WatchlistChartSeries](t, api.call(http.MethodGet, watchlistChartPath+"?pageSize=1&pageNumber=2", nil))
	if len(second.Data) != 1 || second.Data[0].AgentName != "two" || second.Meta.Pagination.TotalPages != 3 || second.Meta.Pagination.Count != 3 {
		t.Errorf("page 2 of size 1 = %+v, pagination %+v, want the second agent of 3 pages", second.Data, second.Meta.Pagination)
	}
	if beyond := decodeList[WatchlistChartSeries](t, api.call(http.MethodGet, watchlistChartPath+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d series", len(beyond.Data))
	}
}

func TestWatchlist_Scopes(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	expectStatus(t, replaceWatchlist(api, "alpha"), http.StatusOK)

	for _, path := range []string{watchlistPath, watchlistChartPath} {
		expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationRead), http.StatusOK)
		expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	}
	put := map[string]any{"agentNames": []string{"alpha", "bravo"}}
	expectStatus(t, api.call(http.MethodPut, watchlistPath, put, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodPut, watchlistPath, put, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	if kept := decodeList[WatchlistEntry](t, api.call(http.MethodGet, watchlistPath, nil)); !slices.Equal(watchedNames(kept.Data), []string{"alpha", "bravo"}) {
		t.Errorf("watchlist = %v, want the reader's PUT applied and the forbidden one not", watchedNames(kept.Data))
	}
}
