// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func ptr[T any](v T) *T { return &v }

func scoreFor(
	ev *store.EvalEvaluator,
	prompt, response string,
	expected *string,
	llmClient LLMJudgeClient,
	artifacts []ArtifactBlock,
) ScoreOutput {
	return scoreForWithArtifacts(ev, prompt, response, expected, llmClient, artifacts, nil, nil)
}

func scoreForWithArtifacts(
	ev *store.EvalEvaluator,
	prompt, response string,
	expected *string,
	llmClient LLMJudgeClient,
	artifacts, inputArtifacts, expectedArtifacts []ArtifactBlock,
) ScoreOutput {
	return ScoreResult(context.Background(), nil, ScoreInputs{
		Evaluator:         ev,
		Prompt:            prompt,
		Response:          response,
		Expected:          expected,
		Artifacts:         artifacts,
		InputArtifacts:    inputArtifacts,
		ExpectedArtifacts: expectedArtifacts,
	}, llmClient)
}

func TestValidJSONScorer(t *testing.T) {
	t.Parallel()
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype}
	out := scoreFor(ev, "p", `{"k": 1}`, nil, nil, nil)
	if out.Score != 1.0 || !out.Passed {
		t.Fatalf("valid json: score=%v passed=%v", out.Score, out.Passed)
	}
	out = scoreFor(ev, "p", `not json`, nil, nil, nil)
	if out.Score != 0.0 || out.Passed {
		t.Fatalf("invalid json: score=%v passed=%v", out.Score, out.Passed)
	}
	out = scoreFor(ev, "p", "```json\n{\"k\":1}\n```", nil, nil, nil)
	if out.Score != 1.0 {
		t.Fatalf("fenced json should pass: %v", out.Score)
	}
}

func TestJSONDiffScorer(t *testing.T) {
	t.Parallel()
	htype := "json_diff"
	ev := &store.EvalEvaluator{Name: "jd", Type: "heuristic", HeuristicType: &htype}
	out := scoreFor(ev, "p", `{"a":1,"b":2}`, ptr(`{"a":1,"b":2}`), nil, nil)
	if out.Score != 1.0 {
		t.Fatalf("identical: %v", out.Score)
	}
	out = scoreFor(ev, "p", `{"a":1}`, ptr(`{"a":1,"b":2}`), nil, nil)
	if out.Score != 0.5 {
		t.Fatalf("partial: %v", out.Score)
	}
	out = scoreFor(ev, "p", `not json`, ptr(`{"a":1}`), nil, nil)
	if out.Score != 0.0 {
		t.Fatalf("invalid: %v", out.Score)
	}
}

func TestLevenshteinScorer(t *testing.T) {
	t.Parallel()
	htype := "levenshtein"
	ev := &store.EvalEvaluator{Name: "lv", Type: "heuristic", HeuristicType: &htype}
	out := scoreFor(ev, "p", "kitten", ptr("kitten"), nil, nil)
	if out.Score != 1.0 {
		t.Fatalf("identical: %v", out.Score)
	}
	out = scoreFor(ev, "p", "kitten", ptr("sitting"), nil, nil)
	if out.Score < 0.4 || out.Score > 0.7 {
		t.Fatalf("kitten vs sitting score = %v (want ~0.57)", out.Score)
	}
}

func TestEmptyResponseSkipsScoring(t *testing.T) {
	t.Parallel()
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype}
	out := scoreFor(ev, "p", "", nil, nil, nil)
	if out.Score != 0 || out.Passed {
		t.Fatalf("empty response: %+v", out)
	}
}

type stubLLM struct {
	content string
	err     error
}

func (s *stubLLM) JudgeScore(_ context.Context, _ string, _, _ string, _ *llm.SpendAttribution) (string, error) {
	return s.content, s.err
}

func TestLLMJudgeMapsLetterToScore(t *testing.T) {
	t.Parallel()
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{
		Name:           "qa",
		Type:           "llm_judge",
		Model:          &model,
		PromptTemplate: &tmpl,
		ChoiceScores:   &cs,
	}
	llm := &stubLLM{content: `{"choice": "A", "reasoning": "looks correct"}`}
	out := scoreFor(ev, "Q?", "A response.", nil, llm, nil)
	if out.Score != 1.0 || !out.Passed {
		t.Fatalf("expected score 1.0 / passed: %+v", out)
	}
	if out.Choice == nil || *out.Choice != "Yes" {
		t.Fatalf("expected choice 'Yes', got %v", out.Choice)
	}
	if out.Reasoning == nil || *out.Reasoning != "looks correct" {
		t.Fatalf("expected reasoning, got %v", out.Reasoning)
	}
}

func TestLLMJudgeProviderErrorMarksErrored(t *testing.T) {
	t.Parallel()
	cs := `{"Yes":1.0,"No":0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs}

	failing := &stubLLM{err: errors.New("dial tcp 127.0.0.1:59999: connect: connection refused")}
	out := scoreFor(ev, "Q?", "A response.", nil, failing, nil)
	if !out.Errored {
		t.Fatalf("provider error should mark ScoreOutput.Errored, got %+v", out)
	}
	if out.Passed {
		t.Fatalf("provider error must not pass: %+v", out)
	}

	unparseable := &stubLLM{content: "not json at all"}
	out2 := scoreFor(ev, "Q?", "A response.", nil, unparseable, nil)
	if out2.Errored {
		t.Fatalf("an unparseable reply is a grade failure, not Errored: %+v", out2)
	}
}

func TestLLMJudgeMapsLabelDirectly(t *testing.T) {
	t.Parallel()
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{
		Name:           "qa",
		Type:           "llm_judge",
		Model:          &model,
		PromptTemplate: &tmpl,
		ChoiceScores:   &cs,
	}
	llm := &stubLLM{content: `{"choice":"no","reasoning":"wrong"}`}
	out := scoreFor(ev, "Q?", "Some text.", nil, llm, nil)
	if out.Score != 0.0 || out.Passed {
		t.Fatalf("expected zero score: %+v", out)
	}
	if out.Choice == nil || *out.Choice != "No" {
		t.Fatalf("expected choice 'No', got %v", out.Choice)
	}
}

func TestLLMJudgeStripsCodeFence(t *testing.T) {
	t.Parallel()
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs}
	llm := &stubLLM{content: "```json\n{\"choice\":\"A\",\"reasoning\":\"ok\"}\n```"}
	out := scoreFor(ev, "Q?", "answer", nil, llm, nil)
	if out.Score != 1.0 {
		t.Fatalf("fenced JSON should parse: %+v", out)
	}
}

func TestLLMJudgeRecoversJSONAfterProse(t *testing.T) {
	t.Parallel()
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs}
	llm := &stubLLM{content: "Looking at the agent's response, it correctly created the deck.\n\n{\"choice\":\"A\",\"reasoning\":\"deck created\"}"}
	out := scoreFor(ev, "Q?", "answer", nil, llm, nil)
	if out.Score != 1.0 || !out.Passed {
		t.Fatalf("prose-then-JSON should parse to Yes/1.0: %+v", out)
	}
}

func TestExtractJSONObject(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"bare", `{"choice":"A"}`, `{"choice":"A"}`},
		{"leading prose", "Looking at it.\n{\"choice\":\"A\"}", `{"choice":"A"}`},
		{"trailing prose", `{"choice":"A"} — done`, `{"choice":"A"}`},
		{"nested", `pre {"a":{"b":1}} post`, `{"a":{"b":1}}`},
		{"brace in string", `x {"reasoning":"use } carefully","choice":"A"} y`, `{"reasoning":"use } carefully","choice":"A"}`},
		{"escaped quote in string", `{"reasoning":"say \"hi\" }","choice":"A"}`, `{"reasoning":"say \"hi\" }","choice":"A"}`},
		{"no object", "no json here", "no json here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSONObject(tc.in); got != tc.want {
				t.Errorf("extractJSONObject(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLLMJudgeUnmappableChoice(t *testing.T) {
	t.Parallel()
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs}
	llm := &stubLLM{content: `{"choice": "Z"}`}
	out := scoreFor(ev, "Q?", "answer", nil, llm, nil)
	if out.Score != 0.0 || out.Passed {
		t.Fatalf("expected score 0 on unmappable choice: %+v", out)
	}
	if out.Reasoning == nil {
		t.Fatalf("expected reasoning for failure")
	}
}

func TestLLMJudgeError(t *testing.T) {
	t.Parallel()
	model := "gpt-4o"
	cs := `{"Yes": 1.0, "No": 0.0}`
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs}
	llm := &stubLLM{err: errors.New("network")}
	out := scoreFor(ev, "Q?", "answer", nil, llm, nil)
	if out.Score != 0.0 || out.Passed {
		t.Fatalf("network error should yield zero: %+v", out)
	}
}

func TestLLMJudgeNoClient(t *testing.T) {
	t.Parallel()
	model := "gpt-4o"
	cs := `{"Yes": 1.0, "No": 0.0}`
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs}
	out := scoreFor(ev, "Q?", "answer", nil, nil, nil)
	if out.Score != 0.0 {
		t.Fatalf("no client must fail-soft: %+v", out)
	}
}

func TestLLMJudgeInfraFaultsMarkErrored(t *testing.T) {
	t.Parallel()
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	goodCS := `{"Yes":1.0,"No":0.0}`

	noClientEv := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &goodCS}
	if out := scoreFor(noClientEv, "Q?", "A.", nil, nil, nil); !out.Errored {
		t.Errorf("no LLM client must mark Errored: %+v", out)
	}

	badCS := `{"Yes":`
	badChoiceEv := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &badCS}
	if out := scoreFor(badChoiceEv, "Q?", "A.", nil, &stubLLM{content: `{"choice":"A"}`}, nil); !out.Errored {
		t.Errorf("malformed choice_scores must mark Errored: %+v", out)
	}

	noModelEv := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", PromptTemplate: &tmpl, ChoiceScores: &goodCS}
	if out := scoreFor(noModelEv, "Q?", "A.", nil, &stubLLM{content: `{"choice":"A"}`}, nil); !out.Errored {
		t.Errorf("no model configured must mark Errored: %+v", out)
	}
}

type modelRecordingLLM struct {
	content string
	model   string
}

func (r *modelRecordingLLM) JudgeScore(_ context.Context, model, _, _ string, _ *llm.SpendAttribution) (string, error) {
	r.model = model
	return r.content, nil
}

func TestLLMJudgeModelResolutionFallback(t *testing.T) {
	cs := `{"Yes":1.0,"No":0.0}`
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	evModel := "evaluator-model"

	run := func(ev *store.EvalEvaluator, defaultModel string) (ScoreOutput, string) {
		stub := &modelRecordingLLM{content: `{"choice":"A"}`}
		out := ScoreResult(context.Background(), nil, ScoreInputs{
			Evaluator:    ev,
			Prompt:       "Q?",
			Response:     "A.",
			DefaultModel: defaultModel,
		}, stub)
		return out, stub.model
	}

	t.Run("default model used when the evaluator has none", func(t *testing.T) {
		ev := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", PromptTemplate: &tmpl, ChoiceScores: &cs}
		out, gotModel := run(ev, "openai/general-from-store")
		if out.Errored {
			t.Fatalf("default model should grade, got errored: %+v", out)
		}
		if out.Score != 1.0 {
			t.Errorf("score = %v, want 1.0", out.Score)
		}
		if gotModel != "openai/general-from-store" {
			t.Errorf("judge model = %q, want %q", gotModel, "openai/general-from-store")
		}
	})

	t.Run("evaluator model wins over default", func(t *testing.T) {
		ev := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", Model: &evModel, PromptTemplate: &tmpl, ChoiceScores: &cs}
		if _, gotModel := run(ev, "openai/general-from-store"); gotModel != evModel {
			t.Errorf("judge model = %q, want evaluator model %q", gotModel, evModel)
		}
	})

	t.Run("LLM_SERVICE_GENERAL_MODEL_NAME is ignored", func(t *testing.T) {
		t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "env-model")
		ev := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", PromptTemplate: &tmpl, ChoiceScores: &cs}
		if _, gotModel := run(ev, "openai/general-from-store"); gotModel != "openai/general-from-store" {
			t.Errorf("judge model = %q, want the default model", gotModel)
		}
		if out, _ := run(ev, ""); !out.Errored {
			t.Errorf("the env var must not stand in for a missing default: %+v", out)
		}
	})

	t.Run("no source configured stays errored", func(t *testing.T) {
		ev := &store.EvalEvaluator{Name: "qa", Type: "llm_judge", PromptTemplate: &tmpl, ChoiceScores: &cs}
		if out, _ := run(ev, ""); !out.Errored {
			t.Errorf("no model anywhere must stay Errored: %+v", out)
		}
	})
}

func TestBuildLetteredChoicesPreservesOrder(t *testing.T) {
	t.Parallel()
	js := `{"Excellent": 1.0, "Good": 0.75, "Acceptable": 0.5, "Poor": 0.25, "Fail": 0.0}`
	formatted, lookup, err := buildLetteredChoices(&js)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := "(A) Excellent\n(B) Good\n(C) Acceptable\n(D) Poor\n(E) Fail"
	if formatted != want {
		t.Fatalf("formatted = %q, want %q", formatted, want)
	}
	if lookup["A"] != "Excellent" || lookup["E"] != "Fail" {
		t.Fatalf("lookup = %v", lookup)
	}
}

func TestLLMJudgeChoiceVariations(t *testing.T) {
	t.Parallel()
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs}
	cases := []struct {
		name      string
		llmReturn string
		wantScore float64
	}{
		{"plain letter A", `{"choice":"A"}`, 1.0},
		{"parenthesised letter", `{"choice":"(A)"}`, 1.0},
		{"letter with period", `{"choice":"A."}`, 1.0},
		{"lowercase letter spaces", `{"choice":" a "}`, 1.0},
		{"plain letter B", `{"choice":"B"}`, 0.0},
		{"label match", `{"choice":"yes"}`, 1.0},
		{"label match exact", `{"choice":"No"}`, 0.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			llm := &stubLLM{content: c.llmReturn}
			out := scoreFor(ev, "Q", "answer", nil, llm, nil)
			if out.Score != c.wantScore {
				t.Fatalf("%s: score = %v, want %v (reasoning=%v)", c.name, out.Score, c.wantScore, out.Reasoning)
			}
		})
	}
}

func TestUnknownHeuristicTypeFallsBack(t *testing.T) {
	t.Parallel()
	htype := "totally_unknown"
	ev := &store.EvalEvaluator{Type: "heuristic", HeuristicType: &htype}
	out := scoreFor(ev, "p", "the cat sat on the mat", ptr("the cat sat on the mat"), nil, nil)
	if out.Score < 0.99 {
		t.Fatalf("identical fallback score = %v", out.Score)
	}
}

type blockingJudgeClient struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	release  chan struct{}
}

func (b *blockingJudgeClient) JudgeScore(ctx context.Context, _, _, _ string, _ *llm.SpendAttribution) (string, error) {
	cur := b.inFlight.Add(1)
	for {
		peak := b.peak.Load()
		if cur <= peak || b.peak.CompareAndSwap(peak, cur) {
			break
		}
	}
	defer b.inFlight.Add(-1)
	select {
	case <-b.release:
		return "{}", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestBoundedJudgeClient_CapsConcurrency(t *testing.T) {
	t.Parallel()
	const maxConcurrent = 3
	const callers = 10
	inner := &blockingJudgeClient{release: make(chan struct{})}
	wrapped := newBoundedJudgeClient(inner, maxConcurrent)

	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			_, _ = wrapped.JudgeScore(context.Background(), "m", "s", "u", nil)
		})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && inner.inFlight.Load() < int32(maxConcurrent) {
		time.Sleep(2 * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond)
	if peak := inner.peak.Load(); peak > int32(maxConcurrent) {
		t.Fatalf("peak in-flight = %d, want <= %d (semaphore not enforced)", peak, maxConcurrent)
	}

	close(inner.release)
	wg.Wait()
}

func TestBoundedJudgeClient_RespectsContextCancel(t *testing.T) {
	t.Parallel()
	inner := &blockingJudgeClient{release: make(chan struct{})}
	wrapped := newBoundedJudgeClient(inner, 1)

	go func() { _, _ = wrapped.JudgeScore(context.Background(), "m", "s", "u", nil) }()
	for inner.inFlight.Load() == 0 {
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := wrapped.JudgeScore(ctx, "m", "s", "u", nil)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("JudgeScore did not return on ctx cancel")
	}
	close(inner.release)
}

func TestCanonicalizeTemplatePlaceholders(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase prompt", "Grade {{prompt}} now", "Grade {{Prompt}} now"},
		{"uppercase response", "Judge {{ RESPONSE }}", "Judge {{Response}}"},
		{"lowercase expected response", "vs {{expected response}}", "vs {{Expected Response}}"},
		{"mixed case + spacing in one template", "{{PROMPT}} / {{response}} / {{Expected   Response}}", "{{Prompt}} / {{Response}} / {{Expected Response}}"},
		{"already canonical is idempotent", "{{Prompt}} {{Response}} {{Expected Response}}", "{{Prompt}} {{Response}} {{Expected Response}}"},
		{"non-placeholder text untouched", "no placeholders here", "no placeholders here"},
		{"node placeholders", "{{node output:classify}} / {{ NODE  INPUT :  triage/fetch }}", "{{Node Output: classify}} / {{Node Input: triage/fetch}}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanonicalizeTemplatePlaceholders(c.in); got != c.want {
				t.Errorf("CanonicalizeTemplatePlaceholders(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestPlaceholderRegexesAreCaseInsensitive(t *testing.T) {
	for _, variant := range []string{"{{Prompt}}", "{{prompt}}", "{{ PROMPT }}"} {
		if !promptPlaceholderRE.MatchString(variant) {
			t.Errorf("promptPlaceholderRE should match %q", variant)
		}
	}
	for _, variant := range []string{"{{Response}}", "{{response}}", "{{ RESPONSE }}"} {
		if !responsePlaceholderScorerRE.MatchString(variant) {
			t.Errorf("responsePlaceholderScorerRE should match %q", variant)
		}
	}
}

func TestBoundedJudgeClient_SharedSemAcrossClients(t *testing.T) {
	t.Parallel()
	const budget = 3
	const clients = 4
	const callersPer = 5

	inner := &blockingJudgeClient{release: make(chan struct{})}
	sem := make(chan struct{}, budget)
	wrapped := make([]LLMJudgeClient, clients)
	for i := range wrapped {
		wrapped[i] = newBoundedJudgeClientWithSem(inner, sem)
	}

	var wg sync.WaitGroup
	for _, w := range wrapped {
		for range callersPer {
			wg.Go(func() { _, _ = w.JudgeScore(context.Background(), "m", "s", "u", nil) })
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && inner.inFlight.Load() < int32(budget) {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if peak := inner.peak.Load(); peak > int32(budget) {
		t.Fatalf("peak in-flight = %d across %d shared-sem clients, want <= %d", peak, clients, budget)
	}

	close(inner.release)
	wg.Wait()
}
