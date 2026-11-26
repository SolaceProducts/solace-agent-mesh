// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const (
	evalNamespace      = "test-ns"
	resultsOfRunFormat = evaluationsPath + "/runs/%s/results"
	runResultsFormat   = evaluationsPath + "/runResults/%s"
)

type runResultFixture struct {
	api         *entrypointAPI
	storage     *eval.StorageService
	artifacts   *artifact.ScopedService
	exampleIDs  []string
	evaluatorID string
	runID       string
	results     []*store.EvalRunResult
}

func newRunResultFixture(t *testing.T, withStorage bool) *runResultFixture {
	t.Helper()
	dir := t.TempDir()
	f := &runResultFixture{}
	if withStorage {
		evalRoot := filepath.Join(dir, "eval")
		if err := os.MkdirAll(evalRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("OBJECT_STORAGE_FS_ROOT", evalRoot)
		client, err := storage.NewFilesystem(evalRoot)
		if err != nil {
			t.Fatal(err)
		}
		f.storage = eval.NewStorageService(client, evalNamespace)
	}
	artifactConfig := map[string]any{"type": "filesystem", "base_path": filepath.Join(dir, "data")}
	f.artifacts = samapp.CreateArtifactService(artifactConfig, map[string]any{"namespace": evalNamespace})
	f.api = newEntrypointAPI(t, map[string]any{"artifact_service": artifactConfig})

	datasetID := createDataset(f.api, "run-results-dataset")
	f.exampleIDs = []string{f.firstExampleID(datasetID)}
	for i := 2; i <= 3; i++ {
		f.exampleIDs = append(f.exampleIDs, newExample(f.api, datasetID, map[string]any{"prompt": fmt.Sprintf("Prompt %d", i)}).ID)
	}
	f.evaluatorID = builtinEvaluatorIDs(f.api)[0]
	expRec := f.api.call(http.MethodPost, experimentsPath, map[string]any{
		"name": "run-results-experiment", "datasetId": datasetID, "targetAgent": "support_agent", "evaluatorIds": []string{f.evaluatorID},
	})
	experiment := expectCreated[Experiment](t, expRec, "evaluations/experiments")
	experiments := experimentFixture{api: f.api}
	runs := experiments.groupRuns(experiments.trigger(experiment.ID, map[string]any{}).ID)
	if len(runs) != 1 {
		t.Fatalf("trigger made %d runs, want 1: %+v", len(runs), runs)
	}
	f.runID = runs[0].ID
	return f
}

func (f *runResultFixture) firstExampleID(datasetID string) string {
	f.api.t.Helper()
	rows, err := store.NewEvalExampleRepo(f.api.core.DB()).ListByDataset(f.api.t.Context(), datasetID)
	if err != nil || len(rows) != 1 {
		f.api.t.Fatalf("list the dataset's examples: %v, %d rows", err, len(rows))
	}
	return rows[0].ID
}

func ptr[T any](v T) *T { return &v }

func (f *runResultFixture) add(example, trial int, change func(*store.EvalRunResult)) *store.EvalRunResult {
	f.api.t.Helper()
	res := &store.EvalRunResult{RunID: f.runID, ExampleID: f.exampleIDs[example], TrialIndex: trial}
	if change != nil {
		change(res)
	}
	if err := store.NewEvalRunResultRepo(f.api.core.DB()).Create(f.api.t.Context(), res); err != nil {
		f.api.t.Fatalf("create run result: %v", err)
	}
	f.results = append(f.results, res)
	return res
}

func (f *runResultFixture) addScores(res *store.EvalRunResult) {
	f.api.t.Helper()
	repo := store.NewEvalRunScoreRepo(f.api.core.DB())
	for _, s := range []*store.EvalRunScore{
		{RunResultID: res.ID, EvaluatorID: f.evaluatorID, Score: 1, Passed: ptr(true), Reasoning: ptr("Matches the expected response.")},
		{RunResultID: res.ID, EvaluatorID: builtinEvaluatorIDs(f.api)[1], Score: 0.25, Choice: ptr("partly"), Details: ptr(decidedByDecision)},
	} {
		if err := repo.Create(f.api.t.Context(), s); err != nil {
			f.api.t.Fatalf("create score: %v", err)
		}
	}
}

const decidedByDecision = `{"decidedBy":"llm_judge","model":"decide-1","escalated":true,"escalationReason":"low_confidence",
	"answer":{"choice":"partly","confidence":0.4,"probabilities":{"wrong":0.35,"partly":0.4,"right":0.25},"scoringApplied":"chosen"},
	"usage":{"decision":{"inputTokens":900,"outputTokens":3},"llmJudge":{"inputTokens":1100,"outputTokens":60}},"durationMs":850}`

func (f *runResultFixture) save(sessionID, filename, mimeType, content string) {
	f.api.t.Helper()
	ref := f.artifacts.Resolve("", eval.EvalRunnerUserID, sessionID, filename)
	if _, err := f.artifacts.Inner.Save(f.api.t.Context(), ref, []byte(content), mimeType); err != nil {
		f.api.t.Fatalf("save artifact %s: %v", filename, err)
	}
}

func jsonString(t *testing.T, v any) *string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return ptr(string(raw))
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (f *runResultFixture) addRichResult() *store.EvalRunResult {
	t := f.api.t
	t.Helper()
	trialSession := func(res *store.EvalRunResult) string {
		return fmt.Sprintf("eval-%s-%s-%d", res.RunID, res.ExampleID, res.TrialIndex)
	}
	res := f.add(0, 0, func(r *store.EvalRunResult) {
		r.InputPrompt = ptr("Prompt 1")
		r.ExpectedResponse = ptr("An expected answer.")
		r.TargetAgent = ptr("support_agent")
		r.AgentResponse = ptr("The agent's answer.")
		r.ResolvedModelName = ptr("gpt-4o")
		r.DurationSeconds = ptr(3.5)
		r.InputTokens = ptr(int64(1200))
		r.TotalTokens = ptr(int64(1350))
		inputSession := "eval-example-" + r.ExampleID + "-input"
		expectedSession := "eval-example-" + r.ExampleID + "-expected"
		r.ArtifactsMetadata = jsonString(t, []map[string]any{
			{"filename": "out.txt", "version": 0, "mime_type": "text/plain", "size_bytes": 5},
			{"filename": "out.txt", "version": 1, "mime_type": "text/plain", "size_bytes": 6},
			{"filename": "dir/report.csv", "version": 0, "mime_type": "text/csv", "size_bytes": 8},
			{"filename": "page.html", "version": 0, "mime_type": "text/html", "size_bytes": 12},
		})
		r.InputArtifactMetadata = jsonString(t, map[string]any{
			"filename": "in.txt", "version": 0, "mime_type": "text/plain", "size_bytes": 5,
			"content_hash": sha256Hex("input"), "artifact_session_id": inputSession,
		})
		r.ExpectedArtifactMetadata = jsonString(t, map[string]any{
			"filename": "exp.txt", "version": 0, "mime_type": "text/plain", "size_bytes": 8,
			"content_hash": sha256Hex("replaced"), "artifact_session_id": expectedSession,
		})
		f.save(trialSession(r), "out.txt", "text/plain", "first")
		f.save(trialSession(r), "out.txt", "text/plain", "second")
		f.save(trialSession(r), "dir/report.csv", "text/csv", "a,b\n1,2\n")
		f.save(trialSession(r), "page.html", "text/html", "<b>hello</b>")
		f.save(inputSession, "in.txt", "text/plain", "input")
		f.save(expectedSession, "exp.txt", "text/plain", "the file was replaced afterwards")
	})
	f.addScores(res)
	return res
}

func TestRunResults_AnEmptyExampleFileIsSizeZero(t *testing.T) {
	f := newRunResultFixture(t, false)
	f.add(0, 0, func(r *store.EvalRunResult) {
		r.ArtifactsMetadata = jsonString(t, []map[string]any{{"filename": "out.txt", "version": 0}})
		r.InputArtifactMetadata = jsonString(t, map[string]any{"filename": "empty.txt", "version": 0, "size_bytes": 0})
	})
	result := decodeList[RunResult](t, f.api.call(http.MethodGet, fmt.Sprintf(resultsOfRunFormat, f.runID), nil)).Data[0]
	if result.InputArtifact == nil || result.InputArtifact.Size == nil || *result.InputArtifact.Size != 0 {
		t.Errorf("inputArtifact = %+v, want size 0 for the empty example file", result.InputArtifact)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Size != nil {
		t.Errorf("artifacts = %+v, want a null size for the agent's file that recorded none", result.Artifacts)
	}
}

func TestRunResults_Lifecycle(t *testing.T) {
	f := newRunResultFixture(t, true)
	rich := f.addRichResult()
	f.add(0, 1, nil)
	f.add(1, 0, func(r *store.EvalRunResult) {
		r.ErrorMessage = ptr("Sign-in is needed.")
		r.FailureKind = ptr(eval.FailureKindAuthRequired)
	})

	rec := f.api.call(http.MethodGet, fmt.Sprintf(resultsOfRunFormat, f.runID), nil)
	expectStatus(t, rec, http.StatusOK)
	listed := decodeList[RunResult](t, rec)
	if listed.Meta.Pagination.Count != 3 || len(listed.Data) != 3 {
		t.Fatalf("list = %d of %d, want the 3 results", len(listed.Data), listed.Meta.Pagination.Count)
	}
	first := listed.Data[0]
	if first.ID != rich.ID || first.Type != "runResult" || first.RunID != f.runID || first.ExampleID != f.exampleIDs[0] || first.TrialIndex != 0 {
		t.Errorf("first = %+v, want the rich result", first)
	}
	if len(first.Scores) != 2 || first.Scores[0].EvaluatorID == "" || first.Scores[1].Choice == nil || *first.Scores[1].Choice != "partly" {
		t.Errorf("scores = %+v, want both scores", first.Scores)
	}
	if len(first.Artifacts) != 4 || first.Artifacts[2].Filename != "dir/report.csv" || *first.Artifacts[2].MimeType != "text/csv" || *first.Artifacts[2].Size != 8 {
		t.Errorf("artifacts = %+v, want the four recorded files", first.Artifacts)
	}
	if first.InputArtifact == nil || first.InputArtifact.Filename != "in.txt" || first.ExpectedArtifact == nil || first.ExpectedArtifact.Filename != "exp.txt" {
		t.Errorf("input and expected artifacts = %+v, %+v", first.InputArtifact, first.ExpectedArtifact)
	}
	if u := first.TokenUsage; u == nil || *u.InputTokens != 1200 || *u.TotalTokens != 1350 || u.OutputTokens != nil || u.CachedInputTokens != nil || u.ReasoningTokens != nil {
		t.Errorf("tokenUsage = %+v, want input and total with the rest unknown", u)
	}

	items := decodeList[map[string]any](t, rec).Data
	expectISOTimes(t, items[0], "createdTime")
	expectAbsent(t, items[0], "createdAt", "executionDataKey", "taskEventsKey")
	score := items[0]["scores"].([]any)[0].(map[string]any)
	expectISOTimes(t, score, "createdTime")
	expectAbsent(t, score, "createdAt")
	expectNull(t, score, "details")
	judged := first.Scores[1].Details
	if judged == nil || *judged.DecidedBy != "llm_judge" || !judged.Escalated || *judged.EscalationReason != "low_confidence" || judged.ProviderError != nil ||
		*judged.DurationMs != 850 || judged.Usage == nil || judged.Usage.Decision.InputTokens != 900 || judged.Usage.LLMJudge.OutputTokens != 60 {
		t.Errorf("details = %+v, want the escalated decision as stored", judged)
	}
	if a := judged.Answer; a == nil || *a.Choice != "partly" || *a.Confidence != 0.4 || a.Probability != nil || a.Score != nil || *a.ScoringApplied != "chosen" ||
		!slices.Equal(a.Probabilities, []DecisionOptionProbability{{"partly", 0.4}, {"right", 0.25}, {"wrong", 0.35}}) {
		t.Errorf("answer = %+v, want the stored answer with its probabilities as a list by option", a)
	}
	expectNull(t, items[1], "inputPrompt", "expectedResponse", "targetAgent", "agentResponse", "resolvedModelName", "durationSeconds",
		"errorMessage", "failureKind", "tokenUsage", "inputArtifact", "expectedArtifact")
	expectEmptyList(t, items[1], "scores", "artifacts")
	if items[2]["failureKind"] != "auth_required" || items[2]["errorMessage"] != "Sign-in is needed." {
		t.Errorf("failed trial = %v, want failureKind auth_required and its message", items[2])
	}

	got := f.api.call(http.MethodGet, fmt.Sprintf(runResultsFormat, rich.ID), nil)
	expectStatus(t, got, http.StatusOK)
	if single := decodeData[RunResult](t, got); !reflect.DeepEqual(single, first) {
		t.Errorf("GET by id = %+v, want the list's item %+v", single, first)
	}
	expectISOTimes(t, rawData(t, got), "createdTime")
}

func TestRunResults_PagingAndOrder(t *testing.T) {
	f := newRunResultFixture(t, false)
	f.add(2, 0, nil)
	f.add(0, 1, nil)
	f.add(1, 0, nil)
	f.add(0, 0, nil)
	f.add(1, 1, nil)
	path := fmt.Sprintf(resultsOfRunFormat, f.runID)
	order := func(rows []RunResult) []string {
		keys := make([]string, len(rows))
		for i, r := range rows {
			keys[i] = fmt.Sprintf("%d/%d", slices.Index(f.exampleIDs, r.ExampleID), r.TrialIndex)
		}
		return keys
	}

	defaults := decodeList[RunResult](t, f.api.call(http.MethodGet, path, nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 5 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 5", p, defaultPageSize)
	}
	if got := order(defaults.Data); !slices.Equal(got, []string{"0/0", "0/1", "1/0", "1/1", "2/0"}) {
		t.Errorf("order = %v, want dataset order then trial order", got)
	}
	second := decodeList[RunResult](t, f.api.call(http.MethodGet, path+"?pageSize=2&pageNumber=2", nil))
	if got := order(second.Data); !slices.Equal(got, []string{"1/0", "1/1"}) || second.Meta.Pagination.TotalPages != 3 {
		t.Errorf("page 2 of size 2 = %v, pagination %+v", got, second.Meta.Pagination)
	}
	if beyond := decodeList[RunResult](t, f.api.call(http.MethodGet, path+"?pageNumber=9", nil)); len(beyond.Data) != 0 || beyond.Meta.Pagination.Count != 5 {
		t.Errorf("a page past the end = %d items, count %d, want none of 5", len(beyond.Data), beyond.Meta.Pagination.Count)
	}

	expectDetail(t, f.api.call(http.MethodGet, path+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, f.api.call(http.MethodGet, path+"?pageNumber=0", nil), http.StatusUnprocessableEntity, "pageNumber")
	expectStatus(t, f.api.call(http.MethodGet, path+"?pageNumber=ten", nil), http.StatusBadRequest)
}

func TestRunResults_EmptyRunIsAnEmptyList(t *testing.T) {
	f := newRunResultFixture(t, false)
	list := decodeList[RunResult](t, f.api.call(http.MethodGet, fmt.Sprintf(resultsOfRunFormat, f.runID), nil))
	if len(list.Data) != 0 || list.Meta.Pagination.Count != 0 {
		t.Errorf("a run with no results = %d items, count %d, want none", len(list.Data), list.Meta.Pagination.Count)
	}
}

func TestRunResults_UnknownIDs(t *testing.T) {
	f := newRunResultFixture(t, true)
	result := f.add(0, 0, nil)
	for _, route := range []struct {
		name, suffix, field string
		runPath             bool
	}{
		{"results", "/results", "runId", true},
		{"export", "/export", "runId", true},
		{"result", "", "id", false},
		{"executionData", "/executionData", "runResultId", false},
		{"taskEvents", "/taskEvents", "runResultId", false},
		{"artifact", "/artifacts/out.txt", "runResultId", false},
		{"inputArtifact", "/inputArtifact/in.txt", "runResultId", false},
		{"expectedArtifact", "/expectedArtifact/exp.txt", "runResultId", false},
	} {
		prefix := evaluationsPath + "/runResults/"
		if route.runPath {
			prefix = evaluationsPath + "/runs/"
		}
		expectDetail(t, f.api.call(http.MethodGet, prefix+"not-a-uuid"+route.suffix, nil), http.StatusBadRequest, route.field)
		rec := f.api.call(http.MethodGet, prefix+missingID+route.suffix, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s of a missing id = %d, want 404; body %s", route.name, rec.Code, rec.Body.String())
			continue
		}
		decodeError(t, rec)
	}
	expectDetail(t, f.api.call(http.MethodGet, fmt.Sprintf(runResultsFormat, result.ID)+"/artifacts/", nil), http.StatusBadRequest, "filename")
}

func TestRunResults_ExportCSVIsV1sBytes(t *testing.T) {
	f := newRunResultFixture(t, false)
	f.addRichResult()
	f.add(1, 0, func(r *store.EvalRunResult) { r.ErrorMessage = ptr("=1+1") })
	path := fmt.Sprintf(evaluationsPath+"/runs/%s/export", f.runID)

	v1 := "example_id,trial_index,input_prompt,expected_response,agent_response,duration_seconds,error_message,input_artifact,expected_artifact," +
		"score_Factuality,score_Closed QA,passed_Factuality,passed_Closed QA\n" +
		f.exampleIDs[0] + ",0,Prompt 1,An expected answer.,The agent's answer.,3.5,,in.txt@0,exp.txt@0,1,0.25,true,\n" +
		f.exampleIDs[1] + ",0,,,,,'=1+1,,,,,,\n"
	for _, query := range []string{"", "?format=csv"} {
		rec := f.api.call(http.MethodGet, path+query, nil)
		expectStatus(t, rec, http.StatusOK)
		if rec.Body.String() != v1 {
			t.Errorf("CSV for %q differs from v1's:\n%s\nv1:\n%s", query, rec.Body.String(), v1)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/csv" {
			t.Errorf("Content-Type = %q, want text/csv", got)
		}
		expectAttachment(t, rec, fmt.Sprintf("run-%s-results.csv", f.runID))
	}
}

func expectAttachment(t *testing.T, rec *httptest.ResponseRecorder, filename string) {
	t.Helper()
	disposition, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != filename {
		t.Errorf("Content-Disposition = %q, want an attachment named %q", rec.Header().Get("Content-Disposition"), filename)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestRunResults_ExportJSONIsTheV2Type(t *testing.T) {
	f := newRunResultFixture(t, false)
	f.addRichResult()
	f.add(0, 1, nil)
	path := fmt.Sprintf(evaluationsPath+"/runs/%s/export?format=json", f.runID)

	rec := f.api.call(http.MethodGet, path, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	expectAttachment(t, rec, fmt.Sprintf("run-%s-results.json", f.runID))

	var exported []RunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode export %s: %v", rec.Body.String(), err)
	}
	listed := decodeList[RunResult](t, f.api.call(http.MethodGet, fmt.Sprintf(resultsOfRunFormat, f.runID), nil))
	if !reflect.DeepEqual(exported, listed.Data) {
		t.Errorf("JSON export = %+v, want the list's items %+v", exported, listed.Data)
	}
	var raw []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	expectISOTimes(t, raw[0], "createdTime")
	expectAbsent(t, raw[0], "run_id", "example_id", "trial_index", "input_prompt", "input_artifact", "createdAt")
	if raw[0]["type"] != "runResult" || raw[0]["runId"] != f.runID {
		t.Errorf("exported item = %v, want camelCase keys and type runResult", raw[0])
	}

	empty := newRunResultFixture(t, false)
	rec = empty.api.call(http.MethodGet, fmt.Sprintf(evaluationsPath+"/runs/%s/export?format=json", empty.runID), nil)
	expectStatus(t, rec, http.StatusOK)
	var none []RunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &none); err != nil || none == nil || len(none) != 0 {
		t.Errorf("export of a run with no results = %s, want []", rec.Body.String())
	}
}

func TestRunResults_ExportFormatIs422(t *testing.T) {
	f := newRunResultFixture(t, false)
	path := fmt.Sprintf(evaluationsPath+"/runs/%s/export", f.runID)
	for _, format := range []string{"xml", "CSV", "jsonl"} {
		expectDetail(t, f.api.call(http.MethodGet, path+"?format="+format, nil), http.StatusUnprocessableEntity, "format")
	}
	expectDetail(t, f.api.call(http.MethodGet, fmt.Sprintf(evaluationsPath+"/runs/%s/export?format=xml", missingID), nil), http.StatusUnprocessableEntity, "format")
}

func TestRunResults_ExecutionData(t *testing.T) {
	f := newRunResultFixture(t, true)
	res := f.add(0, 0, nil)
	path := fmt.Sprintf(runResultsFormat, res.ID) + "/executionData"

	uncaptured := f.api.call(http.MethodGet, path, nil)
	expectStatus(t, uncaptured, http.StatusOK)
	if got := decodeData[RunResultExecutionData](t, uncaptured); got.RunID != f.runID || got.ExampleID != res.ExampleID || got.Messages == nil || len(got.Messages) != 0 {
		t.Errorf("execution data captured nothing = %+v, want the trial with no messages", got)
	}

	key, err := f.storage.UploadExecutionData(f.runID, res.ExampleID, 0, map[string]any{
		"run_id": f.runID, "example_id": res.ExampleID, "trial_index": 0,
		"messages": []map[string]any{
			{"topic": "test-ns/a2a/v1/agent/request/support_agent", "payload": map[string]any{"jsonrpc": "2.0", "id": "task-1", "method": "message/stream"}, "wall_time": "2026-03-01T10:15:30.123456789Z"},
			{"topic": "test-ns/a2a/v1/gateway/response/eval", "payload": map[string]any{"jsonrpc": "2.0", "id": "task-1"}, "wall_time": "2026-03-01T10:15:31.5Z"},
		},
		"agent_response": "The agent's answer.", "duration_seconds": 1.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewEvalRunResultRepo(f.api.core.DB()).SetExecutionDataKey(t.Context(), res.ID, key); err != nil {
		t.Fatal(err)
	}

	rec := f.api.call(http.MethodGet, path, nil)
	expectStatus(t, rec, http.StatusOK)
	data := decodeData[RunResultExecutionData](t, rec)
	if data.RunID != f.runID || data.ExampleID != res.ExampleID || data.TrialIndex != 0 || data.DurationSeconds != 1.5 || *data.AgentResponse != "The agent's answer." {
		t.Errorf("execution data = %+v, want the stored outcome", data)
	}
	if len(data.Messages) != 2 || data.Messages[0].Topic != "test-ns/a2a/v1/agent/request/support_agent" || data.Messages[0].Payload["method"] != "message/stream" {
		t.Errorf("messages = %+v, want both in order with their payloads as recorded", data.Messages)
	}
	raw := rawData(t, rec)
	expectAbsent(t, raw, "run_id", "example_id", "trial_index", "agent_response", "duration_seconds")
	message := raw["messages"].([]any)[0].(map[string]any)
	expectISOTimes(t, message, "capturedTime")
	expectAbsent(t, message, "wall_time")
	if message["capturedTime"] != "2026-03-01T10:15:30.123Z" {
		t.Errorf("capturedTime = %v, want the captured instant to the millisecond", message["capturedTime"])
	}
}

func TestRunResults_StorageDisabledIsEmpty(t *testing.T) {
	f := newRunResultFixture(t, false)
	res := f.add(0, 0, func(r *store.EvalRunResult) {
		r.ExecutionDataKey = ptr("test-ns/eval/runs/x/e_0.json")
		r.TaskEventsKey = ptr("test-ns/eval/runs/x/e_0_events.json")
	})
	base := fmt.Sprintf(runResultsFormat, res.ID)

	data := f.api.call(http.MethodGet, base+"/executionData", nil)
	expectStatus(t, data, http.StatusOK)
	if messages := rawData(t, data)["messages"]; messages == nil || len(messages.([]any)) != 0 {
		t.Errorf("messages = %v, want []", messages)
	}
	events := f.api.call(http.MethodGet, base+"/taskEvents", nil)
	expectStatus(t, events, http.StatusOK)
	if got := rawData(t, events)["events"]; got == nil || len(got.([]any)) != 0 {
		t.Errorf("events = %v, want []", got)
	}

	for _, suffix := range []string{"/executionData", "/taskEvents"} {
		rec := f.api.call(http.MethodGet, fmt.Sprintf(runResultsFormat, missingID)+suffix, nil)
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
}

func TestRunResults_TaskEventsAreOneObject(t *testing.T) {
	f := newRunResultFixture(t, true)
	res := f.add(0, 0, nil)
	path := fmt.Sprintf(runResultsFormat, res.ID) + "/taskEvents"

	unrecorded := f.api.call(http.MethodGet, path, nil)
	expectStatus(t, unrecorded, http.StatusOK)
	if got := decodeData[RunResultTaskEvents](t, unrecorded); got.Events == nil || len(got.Events) != 0 {
		t.Errorf("task events recorded none = %+v, want []", got)
	}

	events := []map[string]any{
		{"event_type": "a2a_message", "timestamp": "2026-03-01T10:15:30.1Z", "solace_topic": "test-ns/a2a/v1/agent/request/support_agent", "direction": "request",
			"task_id": "task-1", "payload_summary": map[string]any{"method": "message/stream"}, "full_payload": map[string]any{"jsonrpc": "2.0", "id": "task-1"}},
		{"event_type": "a2a_message", "timestamp": "2026-03-01T10:15:31.2Z", "direction": "response", "task_id": "task-1"},
	}
	key, err := f.storage.UploadTaskEvents(f.runID, res.ExampleID, 0, events)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.NewEvalRunResultRepo(f.api.core.DB()).SetTaskEventsKey(t.Context(), res.ID, key); err != nil {
		t.Fatal(err)
	}

	rec := f.api.call(http.MethodGet, path, nil)
	expectStatus(t, rec, http.StatusOK)
	var body map[string]any
	if err = json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, paged := body["meta"]; paged || len(body) != 1 {
		t.Errorf("task events body keys = %v, want data alone, not a page", reflect.ValueOf(body).MapKeys())
	}
	got := decodeData[RunResultTaskEvents](t, rec)
	if len(got.Events) != 2 || got.Events[0]["event_type"] != "a2a_message" || got.Events[0]["task_id"] != "task-1" {
		t.Errorf("events = %+v, want both as recorded with their snake_case keys", got.Events)
	}
	if payload, _ := got.Events[0]["full_payload"].(map[string]any); payload["jsonrpc"] != "2.0" {
		t.Errorf("full_payload = %v, want the A2A payload as recorded", got.Events[0]["full_payload"])
	}

	emptyKey, err := f.storage.UploadTaskEvents(f.runID, res.ExampleID, 1, []map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	other := f.add(0, 1, nil)
	if err := store.NewEvalRunResultRepo(f.api.core.DB()).SetTaskEventsKey(t.Context(), other.ID, emptyKey); err != nil {
		t.Fatal(err)
	}
	rec = f.api.call(http.MethodGet, fmt.Sprintf(runResultsFormat, other.ID)+"/taskEvents", nil)
	expectStatus(t, rec, http.StatusOK)
	expectEmptyList(t, rawData(t, rec), "events")
}

func TestRunResults_ArtifactDownloads(t *testing.T) {
	f := newRunResultFixture(t, false)
	res := f.addRichResult()
	base := fmt.Sprintf(runResultsFormat, res.ID)

	for _, tc := range []struct {
		name, path, content, mimeType, filename string
	}{
		{"highest version by default", "/artifacts/out.txt", "second", "text/plain", "out.txt"},
		{"pinned version 0", "/artifacts/out.txt?version=0", "first", "text/plain", "out.txt"},
		{"pinned version 1", "/artifacts/out.txt?version=1", "second", "text/plain", "out.txt"},
		{"a name with a slash", "/artifacts/dir/report.csv", "a,b\n1,2\n", "text/csv", "dir/report.csv"},
		{"input file", "/inputArtifact/in.txt", "input", "text/plain", "in.txt"},
		{"input file at its version", "/inputArtifact/in.txt?version=0", "input", "text/plain", "in.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.api.call(http.MethodGet, base+tc.path, nil)
			expectStatus(t, rec, http.StatusOK)
			if rec.Body.String() != tc.content {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.content)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.mimeType {
				t.Errorf("Content-Type = %q, want the stored %q", got, tc.mimeType)
			}
			expectAttachment(t, rec, tc.filename)
		})
	}

	html := f.api.call(http.MethodGet, base+"/artifacts/page.html", nil)
	expectStatus(t, html, http.StatusOK)
	if got := html.Header().Get("Content-Type"); got != "text/html" {
		t.Errorf("Content-Type = %q, want the stored text/html", got)
	}
	expectAttachment(t, html, "page.html")

	for name, path := range map[string]string{
		"an artifact the result does not list":    "/artifacts/other.txt",
		"a version the result did not record":     "/artifacts/out.txt?version=7",
		"an input file under another name":        "/inputArtifact/other.txt",
		"an input file at another version":        "/inputArtifact/in.txt?version=1",
		"an expected file that was replaced":      "/expectedArtifact/exp.txt",
		"an expected file with no recorded entry": "/expectedArtifact/in.txt",
	} {
		rec := f.api.call(http.MethodGet, base+path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404; body %s", name, rec.Code, rec.Body.String())
			continue
		}
		decodeError(t, rec)
	}
}

func TestRunResults_ArtifactVersionMustBeANonNegativeInteger(t *testing.T) {
	f := newRunResultFixture(t, false)
	res := f.addRichResult()
	base := fmt.Sprintf(runResultsFormat, res.ID)
	for _, route := range []string{"/artifacts/out.txt", "/inputArtifact/in.txt", "/expectedArtifact/exp.txt"} {
		for _, version := range []string{"abc", "1.5"} {
			expectDetail(t, f.api.call(http.MethodGet, base+route+"?version="+version, nil), http.StatusBadRequest, "version")
		}
		expectDetail(t, f.api.call(http.MethodGet, base+route+"?version=-1", nil), http.StatusUnprocessableEntity, "version")
	}
}

func TestRunResults_Scopes(t *testing.T) {
	f := newRunResultFixture(t, true)
	res := f.addRichResult()
	base := fmt.Sprintf(runResultsFormat, res.ID)
	for _, path := range []string{
		fmt.Sprintf(resultsOfRunFormat, f.runID),
		fmt.Sprintf(evaluationsPath+"/runs/%s/export", f.runID),
		base,
		base + "/artifacts/out.txt",
		base + "/inputArtifact/in.txt",
		base + "/executionData",
		base + "/taskEvents",
	} {
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationRead), http.StatusOK)
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	}
}

func TestRunResults_OnlyReadsAreRouted(t *testing.T) {
	f := newRunResultFixture(t, false)
	res := f.add(0, 0, nil)
	for _, path := range []string{fmt.Sprintf(resultsOfRunFormat, f.runID), fmt.Sprintf(runResultsFormat, res.ID)} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if rec := f.api.call(method, path, map[string]any{}); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 405 or 404", method, path, rec.Code)
			}
		}
	}
}

func TestRunResultCarriesAWorkflowTrialsBreakdownAndCost(t *testing.T) {
	cost := "0.0142000000"
	counts := func(base int64) eval.TokenCounts {
		return eval.TokenCounts{InputTokens: base + 1, CachedInputTokens: base + 2, OutputTokens: base + 3, ReasoningTokens: base + 4, TotalTokens: base + 5}
	}
	wireCounts := func(base int64) RunResultTokenCounts {
		return RunResultTokenCounts{InputTokens: base + 1, CachedInputTokens: base + 2, OutputTokens: base + 3, ReasoningTokens: base + 4, TotalTokens: base + 5}
	}
	r := toRunResult(&eval.RunResultResponse{
		TargetType: store.EvalTargetTypeWorkflow,
		Cost:       &cost,
		TokenBreakdown: &eval.TokenBreakdown{
			TraceID:      "trace-1",
			Incomplete:   true,
			ByNode:       []eval.NodeTokenUsage{{NodeID: "classify", Models: []string{"claude", "gpt"}, TokenCounts: counts(10)}},
			Unattributed: counts(20),
			ByModel:      map[string]eval.TokenCounts{"jev": counts(30), "claude": counts(40), "": counts(50)},
		},
	})

	if r.TargetType != store.EvalTargetTypeWorkflow || r.Cost == nil || *r.Cost != cost {
		t.Errorf("targetType %q, cost %v; want workflow and %q", r.TargetType, r.Cost, cost)
	}
	jev, claude := "jev", "claude"
	want := &RunResultTokenBreakdown{
		TraceID:      &[]string{"trace-1"}[0],
		Incomplete:   true,
		ByNode:       []RunResultNodeTokens{{NodeID: "classify", Models: []string{"claude", "gpt"}, Tokens: wireCounts(10)}},
		Unattributed: wireCounts(20),
		ByModel: []RunResultModelTokens{
			{Model: nil, Tokens: wireCounts(50)},
			{Model: &claude, Tokens: wireCounts(40)},
			{Model: &jev, Tokens: wireCounts(30)},
		},
	}
	if !reflect.DeepEqual(r.TokenBreakdown, want) {
		got, _ := json.Marshal(r.TokenBreakdown)
		wantJSON, _ := json.Marshal(want)
		t.Errorf("breakdown =\n%s\nwant (byModel sorted, the unnamed model null)\n%s", got, wantJSON)
	}

	if agent := toRunResult(&eval.RunResultResponse{TargetType: store.EvalTargetTypeAgent}); agent.TokenBreakdown != nil || agent.Cost != nil {
		t.Errorf("agent trial = %+v, want no breakdown and no cost", agent)
	}
}

func TestAnEmptyRunResultBreakdownMarshalsEmptyListsAndANullTrace(t *testing.T) {
	r := toRunResult(&eval.RunResultResponse{
		TargetType:     store.EvalTargetTypeWorkflow,
		TokenBreakdown: &eval.TokenBreakdown{ByNode: []eval.NodeTokenUsage{{NodeID: "classify"}}},
	})

	raw, err := json.Marshal(r.TokenBreakdown)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"traceId":null`, `"byModel":[]`, `"models":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("breakdown %s lacks %s", raw, want)
		}
	}
	empty, err := json.Marshal(toRunResult(&eval.RunResultResponse{TokenBreakdown: &eval.TokenBreakdown{}}).TokenBreakdown)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), `"byNode":[]`) {
		t.Errorf("breakdown %s lacks an empty byNode", empty)
	}
}

func TestRunResultCarriesEveryV1RunResultField(t *testing.T) {
	cases := []struct {
		v1, v2  any
		renamed map[string]string
		leftOut map[string]string
	}{
		{
			v1: eval.RunResultResponse{}, v2: RunResult{},
			renamed: map[string]string{"createdAt": "createdTime"},
			leftOut: map[string]string{
				"executionDataKey": "a storage key; v2 serves the execution data from its own route",
				"taskEventsKey":    "a storage key; v2 serves the task events from its own route",
			},
		},
		{v1: eval.TokenBreakdown{}, v2: RunResultTokenBreakdown{}},
		{v1: eval.TokenCounts{}, v2: RunResultTokenCounts{}},
		{v1: eval.TokenUsage{}, v2: RunResultTokenUsage{}},
		{v1: eval.ScoreResponse{}, v2: RunResultScore{}, renamed: map[string]string{"id": "key", "createdAt": "createdTime"}},
		{v1: eval.ArtifactResponse{}, v2: RunResultArtifact{}},
		{
			v1: eval.NodeTokenUsage{}, v2: RunResultNodeTokens{},
			leftOut: map[string]string{
				"inputTokens": "nested under tokens", "cachedInputTokens": "nested under tokens", "outputTokens": "nested under tokens",
				"reasoningTokens": "nested under tokens", "totalTokens": "nested under tokens",
			},
		},
	}
	for _, tc := range cases {
		t.Run(reflect.TypeOf(tc.v1).Name(), func(t *testing.T) {
			v1, v2 := jsonFieldNames(reflect.TypeOf(tc.v1)), jsonFieldNames(reflect.TypeOf(tc.v2))
			for name := range tc.renamed {
				if !slices.Contains(v1, name) {
					t.Errorf("renamed lists %q, which v1 no longer has", name)
				}
			}
			for name := range tc.leftOut {
				if !slices.Contains(v1, name) {
					t.Errorf("leftOut lists %q, which v1 no longer has", name)
				}
			}
			for _, name := range v1 {
				if _, ok := tc.leftOut[name]; ok {
					continue
				}
				want := name
				if renamed, ok := tc.renamed[name]; ok {
					want = renamed
				}
				if !slices.Contains(v2, want) {
					t.Errorf("v1 field %q has no v2 field %q; map it, or list it as left out with why", name, want)
				}
			}
		})
	}
}

func jsonFieldNames(typ reflect.Type) []string {
	var names []string
	for f := range typ.Fields() {
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		switch {
		case tag == "-", !f.IsExported() && !f.Anonymous:
		case f.Anonymous && tag == "":
			names = append(names, jsonFieldNames(f.Type)...)
		case tag != "":
			names = append(names, tag)
		default:
			names = append(names, f.Name)
		}
	}
	return names
}

func TestRunResultTokenUsageCarriesEveryCount(t *testing.T) {
	in, cached, out, reasoning, total := int64(1), int64(2), int64(3), int64(4), int64(5)

	got := toRunResult(&eval.RunResultResponse{TokenUsage: &eval.TokenUsage{
		InputTokens: &in, CachedInputTokens: &cached, OutputTokens: &out, ReasoningTokens: &reasoning, TotalTokens: &total,
	}}).TokenUsage

	want := &RunResultTokenUsage{InputTokens: &in, CachedInputTokens: &cached, OutputTokens: &out, ReasoningTokens: &reasoning, TotalTokens: &total}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		t.Errorf("tokenUsage = %s, want 1/2/3/4/5", gotJSON)
	}
}

func TestRunResultsRouteReadsAWorkflowTrialsCostFromTheSpendLedger(t *testing.T) {
	f := newRunResultFixture(t, false)
	traceID := "trace-wf"
	breakdown := `{"traceId":"trace-wf","incomplete":false,"byNode":[],"unattributed":{},` +
		`"byModel":{"claude":{"inputTokens":1000,"outputTokens":50,"totalTokens":1050}}}`
	f.add(0, 0, func(r *store.EvalRunResult) {
		r.TargetType = ptr(store.EvalTargetTypeWorkflow)
		r.TokenBreakdown = &breakdown
	})
	agentID := idgen.New()
	if _, err := store.NewSpendLedgerRepo(f.api.core.DB()).Insert(t.Context(), []store.SpendLedgerEntry{{
		ID: idgen.New(), CompletedTime: 1_760_000_000_000, UserID: ptr(idgen.New()), AgentID: &agentID,
		RootKind: ptr("workflow"), RootID: &agentID, Channel: ptr("eval"), TriggerKind: ptr("eval"),
		Surface: string(spend.SurfaceAgent), ModelConfigID: ptr(idgen.New()), TaskID: ptr("task-1"), TraceID: &traceID,
		UncachedInputTokens: 400, OutputTokens: 50, CacheReadTokens: 400, CacheWriteTokens: 200,
		Cost: &spend.Cost{UncachedInput: 12_000_000, Output: 7_500_000, CacheRead: 1_200_000, CacheWrite: 7_500_000},
	}}); err != nil {
		t.Fatalf("file spend: %v", err)
	}

	rec := f.api.call(http.MethodGet, fmt.Sprintf(resultsOfRunFormat, f.runID), nil, rbac.ScopeEvaluationRead)

	expectStatus(t, rec, http.StatusOK)
	var body struct {
		Data []RunResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Data) != 1 {
		t.Fatalf("body = %s (%v), want one result", rec.Body, err)
	}
	if got := body.Data[0].Cost; got == nil || *got != "0.0028200000" {
		t.Errorf("cost = %v, want the ledger's 0.0028200000", got)
	}
}
