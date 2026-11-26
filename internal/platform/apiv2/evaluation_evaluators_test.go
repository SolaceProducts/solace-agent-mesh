// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newEvaluatorAPI(t *testing.T) *entrypointAPI {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyEvalDecisionEvaluator), "true")
	return newEntrypointAPI(t, nil)
}

func llmJudgeBody(name string) map[string]any {
	return map[string]any{
		"name":           name,
		"description":    "Judge the test creates.",
		"model":          "general",
		"promptTemplate": "Is {{ Response }} correct?",
		"choiceScores":   map[string]any{"yes": 1, "no": 0},
	}
}

func decisionBody(name string) map[string]any {
	return map[string]any{
		"name":          name,
		"description":   "Decision evaluator the test creates.",
		"evaluatorType": "decision",
		"model":         "jev",
		"question": map[string]any{
			"type": "choice", "instructions": "Is the answer correct?",
			"criteria": []map[string]any{{"key": "yes", "description": "It is."}, {"key": "no"}},
		},
		"choiceScores": map[string]any{"yes": 1, "no": 0},
	}
}

func newEvaluator(api *entrypointAPI, body map[string]any) Evaluator {
	api.t.Helper()
	return expectCreated[Evaluator](api.t, api.call(http.MethodPost, evaluatorsPath, body), "evaluations/evaluators")
}

func builtinEvaluators(api *entrypointAPI) []Evaluator {
	api.t.Helper()
	listed := decodeList[Evaluator](api.t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=100", nil))
	var builtins []Evaluator
	for _, e := range listed.Data {
		if e.IsDefault {
			builtins = append(builtins, e)
		}
	}
	if len(builtins) == 0 {
		api.t.Fatal("no built-in evaluators listed")
	}
	return builtins
}

func TestEvaluators_LLMJudgeLifecycle(t *testing.T) {
	api := newEvaluatorAPI(t)
	created := newEvaluator(api, llmJudgeBody("lifecycle-judge"))
	if created.Type != "evaluator" || created.EvaluatorType != "llm_judge" || created.IsDefault || created.CreatedBy == "" ||
		created.ChoiceScores["yes"] != 1 || created.ExperimentCount != 0 {
		t.Errorf("created = %+v, want an llm_judge nothing uses", created)
	}
	if created.PromptTemplate == nil || *created.PromptTemplate != "Is {{Response}} correct?" || created.Model == nil || *created.Model != "general" {
		t.Errorf("created model %v and promptTemplate %v, want them as sent, the placeholder canonicalized", created.Model, created.PromptTemplate)
	}

	got := api.call(http.MethodGet, evaluatorsPath+"/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	raw := rawData(t, got)
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "createdAt", "updatedAt")
	expectNull(t, raw, "heuristicType", "question", "scoring", "reads", "minConfidence", "escalateToEvaluatorId")
	if raw["evaluatorType"] != "llm_judge" || raw["type"] != "evaluator" {
		t.Errorf("type %v evaluatorType %v, want evaluator and llm_judge", raw["type"], raw["evaluatorType"])
	}

	listed := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=100", nil))
	if !slices.ContainsFunc(listed.Data, func(e Evaluator) bool { return e.ID == created.ID }) {
		t.Errorf("list is missing the created evaluator")
	}

	expectStatus(t, api.call(http.MethodDelete, evaluatorsPath+"/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, evaluatorsPath+"/"+created.ID, nil), http.StatusNotFound)
}

func TestEvaluators_DecisionLifecycle(t *testing.T) {
	api := newEvaluatorAPI(t)
	created := newEvaluator(api, decisionBody("lifecycle-decision"))
	if created.EvaluatorType != "decision" || created.Question == nil || created.Question.Type != "choice" || len(created.Question.Criteria) != 2 {
		t.Fatalf("created = %+v, want a decision evaluator with its question", created)
	}
	if c := created.Question.Criteria; c[0].Key != "yes" || c[0].Description == nil || *c[0].Description != "It is." || c[1].Description != nil {
		t.Errorf("criteria = %+v, want the description of yes and none for no", c)
	}
	if created.Scoring == nil || *created.Scoring != "chosen" || !slices.Equal(created.Reads, []string{"prompt", "response", "expected_response"}) {
		t.Errorf("scoring %v reads %v, want the defaults", created.Scoring, created.Reads)
	}

	got := api.call(http.MethodGet, evaluatorsPath+"/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	expectNull(t, rawData(t, got), "heuristicType", "promptTemplate", "minConfidence", "escalateToEvaluatorId")
	if d := decodeData[Evaluator](t, got); d.Question == nil || d.ChoiceScores["no"] != 0 {
		t.Errorf("got = %+v, want the question and choiceScores back", d)
	}
	expectStatus(t, api.call(http.MethodDelete, evaluatorsPath+"/"+created.ID, nil), http.StatusNoContent)
}

func TestEvaluators_BuiltInsAreHeuristicAndJudgeEvaluators(t *testing.T) {
	api := newEvaluatorAPI(t)
	var heuristic *Evaluator
	builtins := builtinEvaluators(api)
	for i, e := range builtins {
		if e.EvaluatorType == "heuristic" {
			heuristic = &builtins[i]
		}
		if e.ExperimentCount < 0 || e.CreatedBy == "" {
			t.Errorf("built-in %+v has no creator or a negative count", e)
		}
	}
	if heuristic == nil || heuristic.HeuristicType == nil || heuristic.Model != nil || heuristic.PromptTemplate != nil || heuristic.ChoiceScores != nil {
		t.Fatalf("built-ins = %+v, want a heuristic with a heuristicType and no model, prompt or choiceScores", builtins)
	}
	raw := rawData(t, api.call(http.MethodGet, evaluatorsPath+"/"+heuristic.ID, nil))
	expectNull(t, raw, "model", "promptTemplate", "choiceScores", "question", "scoring", "reads")
}

func TestEvaluators_PatchMergesAndNullClears(t *testing.T) {
	api := newEvaluatorAPI(t)
	created := newEvaluator(api, llmJudgeBody("patch-judge"))
	path := evaluatorsPath + "/" + created.ID

	rec := api.call(http.MethodPatch, path, map[string]any{"name": "renamed-judge"})
	expectStatus(t, rec, http.StatusOK)
	if e := decodeData[Evaluator](t, rec); e.Name != "renamed-judge" || e.Model == nil || *e.Model != "general" || len(e.ChoiceScores) != 2 {
		t.Errorf("after renaming = %+v, want the new name and everything else kept", e)
	}

	rec = api.call(http.MethodPatch, path, map[string]any{
		"description": "Changed.", "model": "planning", "promptTemplate": "Right? {{response}}",
		"choiceScores": map[string]any{"pass": 1, "partial": 0.5, "fail": 0},
	})
	expectStatus(t, rec, http.StatusOK)
	e := decodeData[Evaluator](t, rec)
	if e.Description == nil || *e.Description != "Changed." || *e.Model != "planning" || len(e.ChoiceScores) != 3 || e.ChoiceScores["partial"] != 0.5 {
		t.Errorf("after replacing = %+v", e)
	}
	if e.PromptTemplate == nil || *e.PromptTemplate != "Right? {{Response}}" {
		t.Errorf("promptTemplate = %v, want the placeholder canonicalized", e.PromptTemplate)
	}
	if e.UpdatedTime < created.UpdatedTime || e.UpdatedBy == "" {
		t.Errorf("updatedTime %v updatedBy %q after an update", e.UpdatedTime, e.UpdatedBy)
	}
}

func TestEvaluators_DecisionPatchNullClears(t *testing.T) {
	api := newEvaluatorAPI(t)
	target := newEvaluator(api, llmJudgeBody("escalation-target"))
	body := decisionBody("patch-decision")
	body["escalateToEvaluatorId"], body["minConfidence"], body["scoring"] = target.ID, 0.7, "expected"
	body["reads"] = []string{"response", "transcript"}
	created := newEvaluator(api, body)
	path := evaluatorsPath + "/" + created.ID
	if created.MinConfidence == nil || *created.MinConfidence != 0.7 || created.EscalateToEvaluatorID == nil || *created.Scoring != "expected" {
		t.Fatalf("created = %+v, want the escalation settings as sent", created)
	}

	expectStatus(t, api.call(http.MethodDelete, evaluatorsPath+"/"+target.ID, nil), http.StatusUnprocessableEntity)

	rec := api.call(http.MethodPatch, path, map[string]any{"minConfidence": nil, "scoring": nil, "reads": nil})
	expectStatus(t, rec, http.StatusOK)
	e := decodeData[Evaluator](t, rec)
	if e.MinConfidence != nil || *e.Scoring != "chosen" || !slices.Equal(e.Reads, []string{"prompt", "response", "expected_response"}) {
		t.Errorf("after clearing = %+v, want no minConfidence, the default scoring and reads", e)
	}
	if e.EscalateToEvaluatorID == nil || *e.EscalateToEvaluatorID != target.ID || e.Question == nil {
		t.Errorf("after clearing = %+v, want the escalation evaluator and question kept", e)
	}

	rec = api.call(http.MethodPatch, path, map[string]any{"escalateToEvaluatorId": nil, "promptTemplate": nil})
	expectStatus(t, rec, http.StatusOK)
	expectNull(t, rawData(t, rec), "escalateToEvaluatorId", "promptTemplate")

	rec = api.call(http.MethodPatch, path, map[string]any{
		"question":     map[string]any{"type": "boolean", "instructions": "Is it right?"},
		"choiceScores": nil,
	})
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	expectNull(t, raw, "choiceScores")
	if q, _ := raw["question"].(map[string]any); q["type"] != "boolean" || q["criteria"] != nil {
		t.Errorf("question = %v, want a boolean question with null criteria", raw["question"])
	}
	expectStatus(t, api.call(http.MethodDelete, evaluatorsPath+"/"+target.ID, nil), http.StatusNoContent)
}

func TestEvaluators_PatchValidation(t *testing.T) {
	api := newEvaluatorAPI(t)
	judge := newEvaluator(api, llmJudgeBody("patch-validation-judge"))
	decision := newEvaluator(api, decisionBody("patch-validation-decision"))
	judgePath, decisionPath := evaluatorsPath+"/"+judge.ID, evaluatorsPath+"/"+decision.ID

	expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{"evaluatorType": "decision"}), http.StatusUnprocessableEntity, "evaluatorType")
	rec := api.call(http.MethodPatch, judgePath, map[string]any{"evaluatorType": "llm_judge", "name": "kept-name"})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "evaluatorType")
	if got := decodeData[Evaluator](t, api.call(http.MethodGet, judgePath, nil)); got.Name != judge.Name || got.EvaluatorType != "llm_judge" {
		t.Errorf("after a refused PATCH = %+v, want the evaluator unchanged", got)
	}

	for _, field := range []string{"name", "description", "model"} {
		for _, value := range []any{nil, ""} {
			expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{field: value}), http.StatusUnprocessableEntity, field)
		}
	}
	expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{"promptTemplate": nil}), http.StatusUnprocessableEntity, "promptTemplate")
	expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{"promptTemplate": "no placeholder"}), http.StatusUnprocessableEntity, "promptTemplate")
	expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{"choiceScores": nil}), http.StatusUnprocessableEntity, "choiceScores")
	expectDetailSaying(t, api.call(http.MethodPatch, judgePath, map[string]any{"choiceScores": map[string]any{"only": 1}}), "choiceScores", "choiceScores must define")
	expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{"name": strings.Repeat("n", 256)}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{"description": strings.Repeat("d", 1001)}), http.StatusUnprocessableEntity, "description")
	expectDetail(t, api.call(http.MethodPatch, judgePath, map[string]any{"question": map[string]any{"type": "boolean", "instructions": "?"}}), http.StatusUnprocessableEntity, "question")
	expectDetailSaying(t, api.call(http.MethodPatch, decisionPath, map[string]any{"question": nil}), "question", "cannot be cleared")
	if got := decodeData[Evaluator](t, api.call(http.MethodGet, decisionPath, nil)); !reflect.DeepEqual(got.Question, decision.Question) {
		t.Errorf("question after a refused null = %+v, want it kept as %+v", got.Question, decision.Question)
	}
	expectDetail(t, api.call(http.MethodPatch, decisionPath, map[string]any{"scoring": "sometimes"}), http.StatusUnprocessableEntity, "scoring")
	expectDetail(t, api.call(http.MethodPatch, decisionPath, map[string]any{"minConfidence": 0.8}), http.StatusUnprocessableEntity, "minConfidence")

	rec = api.call(http.MethodPatch, decisionPath, map[string]any{
		"evaluatorType": "llm_judge", "name": "", "description": strings.Repeat("d", 1001), "scoring": "sometimes",
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if details := decodeError(t, rec).ValidationDetails; len(details) != 4 {
		t.Errorf("validationDetails = %v, want evaluatorType, name, description and scoring", details)
	}

	for name, body := range map[string]string{
		"unknown field":   `{"colour":"red"}`,
		"wrong type":      `{"name":7}`,
		"truncated JSON":  `{"name":`,
		"read-only field": `{"isDefault":true}`,
		"bad choice type": `{"choiceScores":{"yes":"one"}}`,
	} {
		if rec := api.call(http.MethodPatch, judgePath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH with %s = %d, want 400", name, rec.Code)
		}
	}
	expectDetail(t, api.call(http.MethodPatch, judgePath, `{"id":"`+judge.ID+`"}`), http.StatusBadRequest, "id")
}

func TestEvaluators_CreateValidation(t *testing.T) {
	api := newEvaluatorAPI(t)
	for name, body := range map[string]string{
		"truncated JSON": `{"name":`,
		"unknown field":  `{"name":"a","colour":"red"}`,
		"wrong type":     `{"name":7}`,
		"bad choice":     `{"choiceScores":{"yes":"one"}}`,
		"read-only":      `{"type":"evaluator"}`,
	} {
		if rec := api.call(http.MethodPost, evaluatorsPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("create with %s = %d, want 400", name, rec.Code)
		}
	}

	rec := api.call(http.MethodPost, evaluatorsPath, map[string]any{"description": strings.Repeat("d", 1001)})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	for _, field := range []string{"name", "description"} {
		if details := decodeError(t, rec).ValidationDetails; len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %s: %v", field, details)
		}
	}
	expectDetail(t, api.call(http.MethodPost, evaluatorsPath, map[string]any{"name": strings.Repeat("n", 256), "description": "d"}), http.StatusUnprocessableEntity, "name")

	for _, c := range []struct {
		name   string
		change func(map[string]any)
		field  string
	}{
		{"no model", func(b map[string]any) { delete(b, "model") }, "model"},
		{"no prompt template", func(b map[string]any) { delete(b, "promptTemplate") }, "promptTemplate"},
		{"no placeholder", func(b map[string]any) { b["promptTemplate"] = "Is it correct?" }, "promptTemplate"},
		{"one choice", func(b map[string]any) { b["choiceScores"] = map[string]any{"yes": 1} }, "choiceScores"},
		{"no choices", func(b map[string]any) { delete(b, "choiceScores") }, "choiceScores"},
		{"a decision field", func(b map[string]any) { b["scoring"] = "chosen" }, "scoring"},
		{"a heuristic", func(b map[string]any) { b["evaluatorType"] = "heuristic" }, "evaluatorType"},
		{"an unknown type", func(b map[string]any) { b["evaluatorType"] = "bogus" }, "evaluatorType"},
	} {
		body := llmJudgeBody("invalid-judge")
		c.change(body)
		got := api.call(http.MethodPost, evaluatorsPath, body)
		if got.Code != http.StatusUnprocessableEntity || len(decodeError(t, got).ValidationDetails[c.field]) == 0 {
			t.Errorf("create with %s = %d %s, want 422 naming %s", c.name, got.Code, got.Body.String(), c.field)
		}
	}

	rec = api.call(http.MethodPost, evaluatorsPath, map[string]any{
		"name": "invalid-decision", "description": "d", "evaluatorType": "decision", "promptTemplate": "{{ Response }}",
		"minConfidence": 0.5, "reads": []string{"prompt", "colour"}, "scoring": "never",
		"question": map[string]any{"type": "choice", "instructions": "Right?", "criteria": []map[string]any{{"key": "a"}, {"key": "b"}}},
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"model", "promptTemplate", "choiceScores", "reads", "scoring", "minConfidence"} {
		if len(details[field]) == 0 {
			t.Errorf("a decision evaluator with many faults: validationDetails has nothing for %s: %v", field, details)
		}
	}
	for field := range details {
		if strings.Contains(field, "_") {
			t.Errorf("validationDetails names %q, want camelCase", field)
		}
	}
	expectDetail(t, api.call(http.MethodPost, evaluatorsPath, map[string]any{
		"name": "bad-escalation", "description": "d", "evaluatorType": "decision", "model": "jev",
		"question":              map[string]any{"type": "boolean", "instructions": "Right?"},
		"escalateToEvaluatorId": missingID,
	}), http.StatusUnprocessableEntity, "escalateToEvaluatorId")
}

func TestEvaluators_DecisionOffIs422(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyEvalDecisionEvaluator), "false")
	api := newEntrypointAPI(t, nil)
	rec := api.call(http.MethodPost, evaluatorsPath, decisionBody("flag-off-decision"))
	expectDetail(t, rec, http.StatusUnprocessableEntity, "evaluatorType")
	if message := decodeError(t, rec).ValidationDetails["evaluatorType"][0]; !strings.Contains(message, "SAM_FEATURE_EVAL_DECISION_EVALUATOR") {
		t.Errorf("message = %q, want it to name the env var that turns decision evaluators on", message)
	}
}

func TestEvaluators_UnknownIDs(t *testing.T) {
	api := newEvaluatorAPI(t)
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		expectDetail(t, api.call(method, evaluatorsPath+"/not-a-uuid", map[string]any{}), http.StatusBadRequest, "id")
		rec := api.call(method, evaluatorsPath+"/"+missingID, map[string]any{"name": "missing"})
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
}

func TestEvaluators_NameTakenIs409(t *testing.T) {
	api := newEvaluatorAPI(t)
	newEvaluator(api, llmJudgeBody("taken-judge"))
	other := newEvaluator(api, llmJudgeBody("other-judge"))

	rec := api.call(http.MethodPost, evaluatorsPath, llmJudgeBody("Taken-Judge"))
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
	rec = api.call(http.MethodPatch, evaluatorsPath+"/"+other.ID, map[string]any{"name": "taken-judge"})
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)
}

func TestEvaluators_BuiltInsCannotChangeOrGo(t *testing.T) {
	api := newEvaluatorAPI(t)
	for _, builtin := range builtinEvaluators(api) {
		path := evaluatorsPath + "/" + builtin.ID
		for name, rec := range map[string]*httptest.ResponseRecorder{
			"rename":         api.call(http.MethodPatch, path, map[string]any{"name": "renamed-builtin"}),
			"retype":         api.call(http.MethodPatch, path, map[string]any{"evaluatorType": "llm_judge"}),
			"clear its name": api.call(http.MethodPatch, path, map[string]any{"name": nil}),
			"delete":         api.call(http.MethodDelete, path, nil),
		} {
			if rec.Code != http.StatusConflict {
				t.Errorf("%s of built-in %q = %d, want 409", name, builtin.Name, rec.Code)
			}
		}
		if got := decodeData[Evaluator](t, api.call(http.MethodGet, path, nil)); got.Name != builtin.Name || got.UpdatedTime != builtin.UpdatedTime {
			t.Errorf("built-in after refused changes = %+v, want %+v", got, builtin)
		}
	}
	rec := api.call(http.MethodPatch, evaluatorsPath+"/"+builtinEvaluators(api)[0].ID, map[string]any{"name": "renamed-builtin"})
	if message := decodeError(t, rec).Message; !strings.Contains(message, "Built-in") {
		t.Errorf("message = %q, want it to say built-in evaluators cannot be modified", message)
	}
}

func TestEvaluators_BuiltInJudgeModelCanBeSetAndReset(t *testing.T) {
	api := newEvaluatorAPI(t)
	judgeModel := createModel(api, "builtin-judge-model", apiKeyAuth()).Alias
	decisionModel := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "builtin-decision-model", "provider": "typesafe", "modelName": "jev-1.13.0",
		"modelType": "decision", "authConfig": apiKeyAuth(),
	}), "models").Alias
	for _, builtin := range builtinEvaluators(api) {
		path := evaluatorsPath + "/" + builtin.ID
		if builtin.EvaluatorType != "llm_judge" {
			expectStatus(t, api.call(http.MethodPatch, path, map[string]any{"model": judgeModel}), http.StatusConflict)
			continue
		}

		set := decodeData[Evaluator](t, api.call(http.MethodPatch, path, map[string]any{"model": judgeModel}))
		if set.Model == nil || *set.Model != judgeModel || set.Name != builtin.Name || !set.IsDefault {
			t.Errorf("built-in %q after setting its model = %+v, want model %s and nothing else changed", builtin.Name, set, judgeModel)
		}
		expectStatus(t, api.call(http.MethodPatch, path, map[string]any{"model": "general", "name": "renamed-builtin"}), http.StatusConflict)
		expectDetail(t, api.call(http.MethodPatch, path, map[string]any{"model": ""}), http.StatusUnprocessableEntity, "model")
		expectDetail(t, api.call(http.MethodPatch, path, map[string]any{"model": "no-such-model"}), http.StatusUnprocessableEntity, "model")
		expectDetail(t, api.call(http.MethodPatch, path, map[string]any{"model": decisionModel}), http.StatusUnprocessableEntity, "model")
		again := decodeData[Evaluator](t, api.call(http.MethodPatch, path, map[string]any{"model": judgeModel}))
		got := decodeData[Evaluator](t, api.call(http.MethodGet, path, nil))
		if got.Name != builtin.Name || got.Model == nil || *got.Model != judgeModel || got.UpdatedTime != set.UpdatedTime || again.UpdatedTime != set.UpdatedTime {
			t.Errorf("built-in %q after refused and unchanged updates = %+v, want it as the model change left it (%+v)", builtin.Name, got, set)
		}

		reset := decodeData[Evaluator](t, api.call(http.MethodPatch, path, map[string]any{"model": nil}))
		if reset.Model != nil {
			t.Errorf("built-in %q model after reset = %q, want null", builtin.Name, *reset.Model)
		}
	}
}

func TestEvaluators_CustomModelCannotBeCleared(t *testing.T) {
	api := newEvaluatorAPI(t)
	custom := newEvaluator(api, llmJudgeBody("clear-model-judge"))
	expectDetail(t, api.call(http.MethodPatch, evaluatorsPath+"/"+custom.ID, map[string]any{"model": nil}), http.StatusUnprocessableEntity, "model")
}

func TestEvaluators_PagingDefaultOrderAndSort(t *testing.T) {
	api := newEvaluatorAPI(t)
	for _, name := range []string{"paging-bravo", "paging-Alpha", "paging-charlie"} {
		newEvaluator(api, llmJudgeBody(name))
		time.Sleep(3 * time.Millisecond)
	}
	builtins := len(builtinEvaluators(api))
	total := builtins + 3

	all := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=100", nil))
	if p := all.Meta.Pagination; p.Count != total || len(all.Data) != total {
		t.Fatalf("pagination %+v with %d items, want a count of %d", p, len(all.Data), total)
	}
	for i, e := range all.Data {
		if wantBuiltin := i < builtins; e.IsDefault != wantBuiltin {
			t.Fatalf("item %d is %q (isDefault %t), want the built-ins first", i, e.Name, e.IsDefault)
		}
	}
	if names := evaluatorNames(all.Data[builtins:]); !slices.Equal(names, []string{"paging-bravo", "paging-Alpha", "paging-charlie"}) {
		t.Errorf("user evaluators = %v, want oldest first", names)
	}

	defaults := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath, nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != total {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count %d", p, defaultPageSize, total)
	}

	var paged []Evaluator
	for pageNumber := 1; ; pageNumber++ {
		page := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=2&pageNumber="+strconv.Itoa(pageNumber), nil))
		if len(page.Data) == 0 {
			break
		}
		if p := page.Meta.Pagination; p.TotalPages != (total+1)/2 || p.Count != total {
			t.Errorf("page %d pagination = %+v, want %d pages over %d", pageNumber, p, (total+1)/2, total)
		}
		paged = append(paged, page.Data...)
	}
	if !slices.Equal(evaluatorNames(paged), evaluatorNames(all.Data)) {
		t.Errorf("pages of 2 = %v, want the same order as one page %v", evaluatorNames(paged), evaluatorNames(all.Data))
	}

	byName := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=100&sort=name:asc", nil))
	if names := evaluatorNames(byName.Data); !slices.IsSortedFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }) {
		t.Errorf("sort=name:asc = %v, want case-insensitive name order", names)
	}
	byNameDesc := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=100&sort=name:desc", nil))
	if names := evaluatorNames(byNameDesc.Data); !slices.Equal(names, reversed(evaluatorNames(byName.Data))) {
		t.Errorf("sort=name:desc = %v, want the reverse of name:asc", names)
	}
	newest := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=1&sort=createdTime:desc", nil))
	if names := evaluatorNames(newest.Data); !slices.Equal(names, []string{"paging-charlie"}) {
		t.Errorf("newest = %v, want the last created", names)
	}
	if beyond := decodeList[Evaluator](t, api.call(http.MethodGet, evaluatorsPath+"?pageNumber=99", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}

	expectDetail(t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, evaluatorsPath+"?pageNumber=0", nil), http.StatusUnprocessableEntity, "pageNumber")
	expectStatus(t, api.call(http.MethodGet, evaluatorsPath+"?pageSize=ten", nil), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodGet, evaluatorsPath+"?sort=description:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectStatus(t, api.call(http.MethodGet, evaluatorsPath+"?sort=name:up", nil), http.StatusBadRequest)
}

func evaluatorNames(evaluators []Evaluator) []string {
	names := make([]string, len(evaluators))
	for i, e := range evaluators {
		names[i] = e.Name
	}
	return names
}

func reversed(s []string) []string {
	out := slices.Clone(s)
	slices.Reverse(out)
	return out
}

func TestEvaluators_ExperimentCount(t *testing.T) {
	f := newExperimentFixture(t)
	judge := newEvaluator(f.api, llmJudgeBody("counted-judge"))
	path := evaluatorsPath + "/" + judge.ID
	attach := func(name string) {
		body := f.body(name, "support_agent")
		body["evaluatorIds"] = []string{judge.ID}
		expectStatus(t, f.api.call(http.MethodPost, experimentsPath, body), http.StatusCreated)
	}

	if judge.ExperimentCount != 0 {
		t.Errorf("created experimentCount = %d, want 0", judge.ExperimentCount)
	}
	attach("counted-experiment-one")
	attach("counted-experiment-two")

	if got := decodeData[Evaluator](t, f.api.call(http.MethodGet, path, nil)); got.ExperimentCount != 2 {
		t.Errorf("get experimentCount = %d, want 2", got.ExperimentCount)
	}
	patched := decodeData[Evaluator](t, f.api.call(http.MethodPatch, path, map[string]any{"description": "Counted."}))
	if patched.ExperimentCount != 2 {
		t.Errorf("update experimentCount = %d, want 2", patched.ExperimentCount)
	}
	listed := decodeList[Evaluator](t, f.api.call(http.MethodGet, evaluatorsPath+"?pageSize=100", nil))
	i := slices.IndexFunc(listed.Data, func(e Evaluator) bool { return e.ID == judge.ID })
	if i < 0 || listed.Data[i].ExperimentCount != 2 {
		t.Errorf("list = %v, want the evaluator with experimentCount 2", evaluatorNames(listed.Data))
	}
}

func TestEvaluators_Scopes(t *testing.T) {
	api := newEvaluatorAPI(t)
	created := newEvaluator(api, llmJudgeBody("scoped-judge"))
	path := evaluatorsPath + "/" + created.ID

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, evaluatorsPath, llmJudgeBody("denied-judge")},
		{http.MethodPatch, path, map[string]any{"name": "denied"}},
		{http.MethodDelete, path, nil},
	} {
		if rec := api.call(call.method, call.path, call.body, rbac.ScopeEvaluationRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a reader = %d, want 403", call.method, call.path, rec.Code)
		}
	}
	expectStatus(t, api.call(http.MethodGet, evaluatorsPath, nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, evaluatorsPath, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPost, evaluatorsPath, llmJudgeBody("creator-judge"), rbac.ScopeEvaluationCreate), http.StatusCreated)
	expectStatus(t, api.call(http.MethodPatch, path, map[string]any{"name": "updated-judge"}, rbac.ScopeEvaluationUpdate), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, path, nil, rbac.ScopeEvaluationDelete), http.StatusNoContent)
}

func TestEvaluators_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, evaluatorsTag)
	want := []string{
		"DELETE /evaluations/evaluators/{id}", "GET /evaluations/evaluators", "GET /evaluations/evaluators/{id}",
		"PATCH /evaluations/evaluators/{id}", "POST /evaluations/evaluators",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}
