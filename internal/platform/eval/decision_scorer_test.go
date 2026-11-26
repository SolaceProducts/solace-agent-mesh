// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/gemini"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/typesafe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestExpectedScoringMassThreshold(t *testing.T) {
	question := func(typ decision.QuestionType, n int) (decision.Question, map[string]any) {
		q := decision.Question{Key: "answer", Type: typ, Instructions: "x"}
		scores := make(map[string]any, n)
		for i := range n {
			key := fmt.Sprintf("o%d", i)
			q.Criteria = append(q.Criteria, decision.Criterion{Key: key})
			scores[key] = float64(i) / float64(n-1)
		}
		return q, scores
	}
	spread := func(n int, mass float64) map[string]float64 {
		p := make(map[string]float64, n)
		for i := range n {
			p[fmt.Sprintf("o%d", i)] = mass / float64(n)
		}
		return p
	}
	cases := map[string]struct {
		typ   decision.QuestionType
		n     int
		probs map[string]float64
		want  string
	}{
		"4 options at 0.985 is within rounding":             {decision.TypeChoice, 4, map[string]float64{"o0": 0.25, "o1": 0.25, "o2": 0.25, "o3": 0.235}, scoringExpected},
		"4 options at exactly 0.98 is within rounding":      {decision.TypeChoice, 4, spread(4, 0.98), scoringExpected},
		"4 options at 0.975 is short":                       {decision.TypeChoice, 4, spread(4, 0.975), scoringChosen},
		"7 options at 0.14 each is within rounding":         {decision.TypeChoice, 7, spread(7, 0.98), scoringExpected},
		"26 options stop at the 0.97 floor":                 {decision.TypeChoice, 26, spread(26, 0.965), scoringChosen},
		"26 options above the floor are weighted":           {decision.TypeChoice, 26, spread(26, 0.975), scoringExpected},
		"an omitted zero option is not missing mass":        {decision.TypeChoice, 4, map[string]float64{"o0": 0.5, "o1": 0.3, "o2": 0.2}, scoringExpected},
		"5 levels at 0.981 is within rounding":              {decision.TypeScore, 5, spread(5, 0.981), scoringExpected},
		"a float sum a hair under the threshold counts":     {decision.TypeChoice, 3, spread(3, 0.985), scoringExpected},
		"4 options reporting 3 at 0.982 is within rounding": {decision.TypeChoice, 4, map[string]float64{"o0": 0.5, "o1": 0.3, "o2": 0.182}, scoringExpected},
		"10 options at exactly the 0.97 floor":              {decision.TypeChoice, 10, spread(10, 0.97), scoringExpected},
		"10 options at 0.969 is short":                      {decision.TypeChoice, 10, spread(10, 0.969), scoringChosen},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q, scores := question(tc.typ, tc.n)
			conf := 0.9
			v, err := mapDecisionAnswer(q, scoringExpected, scores, decision.Answer{Choice: "o0", Confidence: &conf, Probabilities: tc.probs})
			if err != nil {
				t.Fatalf("mapDecisionAnswer: %v", err)
			}
			if v.answer.ScoringApplied != tc.want {
				t.Fatalf("scoringApplied = %q, want %q", v.answer.ScoringApplied, tc.want)
			}
		})
	}
}

func TestMapDecisionAnswer(t *testing.T) {
	choice := decision.Question{Key: "answer", Type: decision.TypeChoice, Instructions: "x",
		Criteria: []decision.Criterion{{Key: "fully"}, {Key: "partially"}, {Key: "not_at_all"}}}
	score := decision.Question{Key: "answer", Type: decision.TypeScore, Instructions: "x",
		Criteria: []decision.Criterion{{Key: "poor"}, {Key: "fair"}, {Key: "good"}}}
	boolean := decision.Question{Key: "answer", Type: decision.TypeBoolean, Instructions: "x"}
	choiceScores := map[string]any{"fully": 1.0, "partially": 0.5, "not_at_all": 0.0}
	conf := 0.9

	cases := map[string]struct {
		q              decision.Question
		scoring        string
		answer         decision.Answer
		wantScore      float64
		wantLabel      string
		wantConfidence float64
		wantApplied    string
	}{
		"boolean scores P(true) and derives its confidence": {
			q: boolean, scoring: scoringChosen, answer: decision.Answer{Probability: 0.3},
			wantScore: 0.3, wantLabel: "false", wantConfidence: 0.7,
		},
		"choice chosen scores the chosen option": {
			q: choice, scoring: scoringChosen,
			answer:    decision.Answer{Choice: "partially", Confidence: &conf, Probabilities: map[string]float64{"fully": 0.1, "partially": 0.9}},
			wantScore: 0.5, wantLabel: "partially", wantConfidence: 0.9, wantApplied: scoringChosen,
		},
		"choice confidence falls back to the chosen probability": {
			q: choice, scoring: scoringChosen,
			answer:    decision.Answer{Choice: "fully", Probabilities: map[string]float64{"fully": 0.65}},
			wantScore: 1, wantLabel: "fully", wantConfidence: 0.65, wantApplied: scoringChosen,
		},
		"choice confidence falls back to the chosen option's probability, not the likeliest": {
			q: choice, scoring: scoringChosen,
			answer:    decision.Answer{Choice: "partially", Probabilities: map[string]float64{"fully": 0.7, "partially": 0.3}},
			wantScore: 0.5, wantLabel: "partially", wantConfidence: 0.3, wantApplied: scoringChosen,
		},
		"choice expected keeps the chosen score when the probabilities are partial": {
			q: choice, scoring: scoringExpected,
			answer:    decision.Answer{Choice: "fully", Confidence: &conf, Probabilities: map[string]float64{"fully": 0.2, "not_at_all": 0.2}},
			wantScore: 1, wantLabel: "fully", wantConfidence: 0.9, wantApplied: scoringChosen,
		},
		"choice expected weights every option when the probabilities are complete": {
			q: choice, scoring: scoringExpected,
			answer:    decision.Answer{Choice: "fully", Confidence: &conf, Probabilities: map[string]float64{"fully": 0.6, "partially": 0.4}},
			wantScore: 0.8, wantLabel: "fully", wantConfidence: 0.9, wantApplied: scoringExpected,
		},
		"choice expected tolerates what rounding loses": {
			q: choice, scoring: scoringExpected,
			answer:    decision.Answer{Choice: "fully", Confidence: &conf, Probabilities: map[string]float64{"fully": 0.33, "partially": 0.33, "not_at_all": 0.33}},
			wantScore: 0.5, wantLabel: "fully", wantConfidence: 0.9, wantApplied: scoringExpected,
		},
		"choice expected tolerates float summation error": {
			q: choice, scoring: scoringExpected,
			answer:    decision.Answer{Choice: "fully", Confidence: &conf, Probabilities: map[string]float64{"fully": 0.7, "partially": 0.2, "not_at_all": 0.1}},
			wantScore: 0.8, wantLabel: "fully", wantConfidence: 0.9, wantApplied: scoringExpected,
		},
		"choice expected falls back to chosen past what rounding loses": {
			q: choice, scoring: scoringExpected,
			answer:    decision.Answer{Choice: "fully", Confidence: &conf, Probabilities: map[string]float64{"fully": 0.49, "partially": 0, "not_at_all": 0.49}},
			wantScore: 1, wantLabel: "fully", wantConfidence: 0.9, wantApplied: scoringChosen,
		},
		"score chosen is the level position": {
			q: score, scoring: scoringChosen,
			answer:    decision.Answer{Score: 1, Confidence: &conf},
			wantScore: 0.5, wantLabel: "fair", wantConfidence: 0.9, wantApplied: scoringChosen,
		},
		"score chosen rounds a between-levels score to the chosen level": {
			q: score, scoring: scoringChosen,
			answer:    decision.Answer{Score: 1.4, Confidence: &conf},
			wantScore: 0.5, wantLabel: "fair", wantConfidence: 0.9, wantApplied: scoringChosen,
		},
		"score expected weights every level": {
			q: score, scoring: scoringExpected,
			answer:    decision.Answer{Score: 2, Confidence: &conf, Probabilities: map[string]float64{"good": 0.5, "fair": 0.5}},
			wantScore: 0.75, wantLabel: "good", wantConfidence: 0.9, wantApplied: scoringExpected,
		},
		"score expected falls back to chosen when a level's mass is missing": {
			q: score, scoring: scoringExpected,
			answer:    decision.Answer{Score: 2, Confidence: &conf, Probabilities: map[string]float64{"good": 0.9}},
			wantScore: 1, wantLabel: "good", wantConfidence: 0.9, wantApplied: scoringChosen,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v, err := mapDecisionAnswer(tc.q, tc.scoring, choiceScores, tc.answer)
			if err != nil {
				t.Fatalf("mapDecisionAnswer: %v", err)
			}
			if math.Abs(v.score-tc.wantScore) > 1e-9 || v.label != tc.wantLabel || v.confidence == nil || math.Abs(*v.confidence-tc.wantConfidence) > 1e-9 {
				t.Fatalf("got score=%v label=%q confidence=%v, want %v %q %v", v.score, v.label, optionalFloat(v.confidence), tc.wantScore, tc.wantLabel, tc.wantConfidence)
			}
			if v.answer.ScoringApplied != tc.wantApplied {
				t.Fatalf("scoringApplied = %q, want %q", v.answer.ScoringApplied, tc.wantApplied)
			}
		})
	}

	t.Run("a choice with neither confidence nor probabilities reports no confidence", func(t *testing.T) {
		v, err := mapDecisionAnswer(choice, scoringChosen, choiceScores, decision.Answer{Choice: "partially"})
		if err != nil {
			t.Fatalf("mapDecisionAnswer: %v", err)
		}
		if v.confidence != nil || v.answer.Confidence != nil {
			t.Fatalf("confidence = %v, want none rather than a made-up value", optionalFloat(v.confidence))
		}
	})

	t.Run("a choice with no score is an error", func(t *testing.T) {
		if _, err := mapDecisionAnswer(choice, scoringChosen, choiceScores, decision.Answer{Choice: "other"}); err == nil {
			t.Fatal("want an error for an unscored choice")
		}
	})
}

func TestDecisionStateLimitFollowsTheModel(t *testing.T) {
	cases := map[string]struct {
		provider string
		raw      map[string]any
		want     int
	}{
		"an explicit max_state_bytes wins":             {decision.ProviderOpenAI, map[string]any{"max_state_bytes": 4096, "max_input_tokens": 100}, 4096},
		"the model's context window is used next":      {decision.ProviderTypeSafe, map[string]any{"max_input_tokens": float64(32000)}, 96000},
		"OpenAI's Decisions API reads up to 1M tokens": {decision.ProviderOpenAI, map[string]any{}, 3_000_000},
		"Gemini's Decisions API reads up to 262,144 tokens": {
			decision.ProviderGoogleAIStudio, map[string]any{}, 786_432},
		"an admin value never exceeds Gemini's ceiling": {
			decision.ProviderGoogleAIStudio, map[string]any{"max_state_bytes": 10_000_000}, 786_432},
		"TypeSafe keeps the client's default cap": {decision.ProviderTypeSafe, map[string]any{}, 0},
		"a malformed setting is ignored":          {decision.ProviderTypeSafe, map[string]any{"max_state_bytes": "big"}, 0},
		"an admin value never exceeds the provider's ceiling": {
			decision.ProviderOpenAI, map[string]any{"max_state_bytes": 10_000_000}, 3_000_000},
		"a value typed into a form as text is read": {decision.ProviderTypeSafe, map[string]any{"max_state_bytes": "4096"}, 4096},
		"the camelCase spelling workflows accept is read": {
			decision.ProviderTypeSafe, map[string]any{"maxStateBytes": 2048}, 2048},
		"a context window converts at three bytes a token": {
			decision.ProviderTypeSafe, map[string]any{"max_input_tokens": 1000}, 3000},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := decisionStateLimit(tc.provider, tc.raw); got != tc.want {
				t.Fatalf("decisionStateLimit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFreezeDecisionModelWarnsOnUnusableParams(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	raw := map[string]any{"max_state_bytes": "64k", "maxStateBytes": 0, "timeout_seconds": nil, "timeoutSeconds": "30"}
	m := freezeDecisionModel("jev", raw, map[string]any{"id": "m1"}, logger)
	if m.MaxStateBytes != 0 || m.TimeoutSeconds != 30 {
		t.Fatalf("snapshot = %+v, want the unusable cap ignored and the camelCase timeout read", m)
	}
	out := buf.String()
	if strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, "param=max_state_bytes") || !strings.Contains(out, "type=string") {
		t.Fatalf("log = %q, want one warning naming max_state_bytes and its type", out)
	}
	if strings.Contains(out, "64k") {
		t.Fatalf("log = %q, want the admin's value left out", out)
	}

	t.Run("a timeout above the cap is clamped and warned about once", func(t *testing.T) {
		var buf strings.Builder
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		m := freezeDecisionModel("jev", map[string]any{"timeoutSeconds": 3600}, map[string]any{"id": "m1"}, logger)
		if m.TimeoutSeconds != maxDecisionTimeoutSeconds {
			t.Fatalf("TimeoutSeconds = %d, want the cap %d", m.TimeoutSeconds, maxDecisionTimeoutSeconds)
		}
		out := buf.String()
		if strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, "param=timeoutSeconds") || !strings.Contains(out, "cap=600") {
			t.Fatalf("log = %q, want one warning naming the param and the cap", out)
		}
	})

	t.Run("a timeout at the cap is kept without a warning", func(t *testing.T) {
		var buf strings.Builder
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		m := freezeDecisionModel("jev", map[string]any{"timeout_seconds": maxDecisionTimeoutSeconds}, map[string]any{"id": "m1"}, logger)
		if m.TimeoutSeconds != maxDecisionTimeoutSeconds || buf.Len() != 0 {
			t.Fatalf("TimeoutSeconds = %d, log = %q", m.TimeoutSeconds, buf.String())
		}
	})
}

func TestDecisionStateReads(t *testing.T) {
	in := ScoreInputs{
		Prompt: "p", Response: "", Expected: new("e"), Transcript: "[tool call] search(q)",
		Artifacts: []ArtifactBlock{
			ArtifactJudgeText{Filename: "a.md", MIMEType: "text/markdown", Text: "hello"},
			ArtifactPlaceholder{Filename: "b.png", MIMEType: "image/png", Reason: "binary"},
		},
	}

	t.Run("every read field is sent", func(t *testing.T) {
		state := decisionState(in, []string{readPrompt, readResponse, readExpectedResponse, readTranscript, readArtifacts})
		if state["prompt"] != "p" || state["expected_response"] != "e" || state["transcript"] != "[tool call] search(q)" {
			t.Fatalf("state = %v", state)
		}
		if response, _ := state["response"].(string); response != "[No text in the agent's reply; it answered with files.]" {
			t.Fatalf("response = %q, want the agent's file-only placeholder", response)
		}
		artifacts, _ := state["artifacts"].(map[string]any)
		produced, _ := artifacts["produced_by_agent"].([]map[string]any)
		if len(produced) != 2 {
			t.Fatalf("artifacts = %v, want the two produced files", state["artifacts"])
		}
		byName := map[string]map[string]any{}
		for _, a := range produced {
			name, _ := a["name"].(string)
			byName[name] = a
		}
		if text := byName["a.md"]; text == nil || text["content"] != "hello" || text["mime_type"] != "text/markdown" {
			t.Fatalf("a.md = %v", text)
		}
		binary := byName["b.png"]
		if binary == nil || binary["content_included"] != false {
			t.Fatalf("b.png = %v", binary)
		}
		if _, ok := binary["content"]; ok {
			t.Fatalf("b.png carries content: %v", binary)
		}
	})

	t.Run("a workflow trial sends its trajectory as a workflow's steps", func(t *testing.T) {
		wf := in
		wf.WorkflowTrajectory = true
		wf.Transcript = "[node classify · agent OrderClassifier] started"
		state := decisionState(wf, []string{readResponse, readTranscript})
		if state["workflow_trajectory"] != wf.Transcript {
			t.Fatalf("state = %v, want the trajectory under workflow_trajectory", state)
		}
		if _, ok := state["transcript"]; ok {
			t.Errorf("the trajectory was also sent as an agent transcript: %v", state)
		}
		if response, _ := state["response"].(string); response != "[No text in the workflow's output; it answered with files.]" {
			t.Errorf("response = %q, want the workflow's file-only placeholder", response)
		}
	})

	t.Run("fields reads omits are absent", func(t *testing.T) {
		state := decisionState(in, []string{readPrompt, readResponse})
		for _, key := range []string{"expected_response", "transcript", "artifacts"} {
			if _, ok := state[key]; ok {
				t.Errorf("state has %q although reads omits it: %v", key, state)
			}
		}
	})
}

type fakeDecision struct {
	result    decision.Result
	err       error
	clientErr error
	calls     atomic.Int32
	last      decision.Request
}

func (f *fakeDecision) DecisionClient(context.Context, DecisionModelSnapshot) (decision.Evaluator, error) {
	if f.clientErr != nil {
		return nil, f.clientErr
	}
	return f, nil
}

func (f *fakeDecision) Evaluate(_ context.Context, req decision.Request) (decision.Result, error) {
	f.calls.Add(1)
	f.last = req
	return f.result, f.err
}

type failingDecisionSource struct {
	err   error
	shown string
}

func (f *failingDecisionSource) DecisionClient(context.Context, DecisionModelSnapshot) (decision.Evaluator, error) {
	return nil, &decisionClientError{err: f.err, shown: f.shown}
}

type countingJudge struct{ calls atomic.Int32 }

func (c *countingJudge) JudgeScore(context.Context, string, string, string, *llm.SpendAttribution) (string, error) {
	c.calls.Add(1)
	return `{"choice":"B","reasoning":"judge says yes"}`, nil
}

type usageJudge struct{ countingJudge }

func (u *usageJudge) JudgeScoreWithUsage(ctx context.Context, model, sys, user string, attribution *llm.SpendAttribution) (string, *JudgeTokenUsage, error) {
	content, err := u.JudgeScore(ctx, model, sys, user, attribution)
	return content, &JudgeTokenUsage{InputTokens: 50, OutputTokens: 7}, err
}

type erroringJudge struct{ calls atomic.Int32 }

func (e *erroringJudge) JudgeScore(context.Context, string, string, string, *llm.SpendAttribution) (string, error) {
	e.calls.Add(1)
	return "", errors.New("judge provider unavailable")
}

func unconfidentAnswer(choice string) decision.Result {
	return decision.Result{
		Model:   "jev-1.13.0-20260917",
		Answers: map[string]decision.Answer{decisionQuestionKey: {Type: decision.TypeChoice, Choice: choice}},
		Usage:   decision.Usage{InputTokens: 912},
	}
}

func decisionEvaluatorRow(t *testing.T, cfg decisionRunConfig) *store.EvalEvaluator {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	cs := `{"fully":1,"partially":0.5,"not_at_all":0}`
	return &store.EvalEvaluator{ID: "dec", Name: "answers", Type: "decision", Model: new("jev"), ChoiceScores: &cs, DecisionConfig: &s}
}

func baseDecisionConfig(withEscalation bool) decisionRunConfig {
	cfg := decisionRunConfig{
		DecisionConfig: DecisionConfig{
			Question: *choiceQuestion(),
			Scoring:  scoringChosen,
			Reads:    []string{readPrompt, readResponse},
		},
		ResolvedModel: &DecisionModelSnapshot{Ref: "jev", Provider: decision.ProviderTypeSafe, Model: "jev-1.13.0"},
	}
	if withEscalation {
		cfg.MinConfidence = new(0.8)
		cfg.EscalateToEvaluatorID = new("judge")
		cfg.EscalateTo = map[string]any{
			"id": "judge", "name": "quality", "type": "llm_judge", "model": "gpt",
			"prompt_template": "Grade {{Response}}", "choice_scores": map[string]any{"Yes": 1.0, "No": 0.0},
		}
	}
	return cfg
}

func answer(choice string, confidence float64) decision.Result {
	return decision.Result{
		Model: "jev-1.13.0-20260917",
		Answers: map[string]decision.Answer{decisionQuestionKey: {
			Type: decision.TypeChoice, Choice: choice, Confidence: &confidence,
			Probabilities: map[string]float64{choice: confidence},
		}},
		Usage: decision.Usage{InputTokens: 912},
	}
}

func runDecisionScore(t *testing.T, ev *store.EvalEvaluator, fake *fakeDecision, judge LLMJudgeClient, enabled bool) ScoreOutput {
	t.Helper()
	return ScoreResult(t.Context(), nil, ScoreInputs{
		Evaluator: ev, Prompt: "What is 2+2?", Response: "4", Expected: new("4"),
		Decision: fake, DecisionEnabled: enabled,
	}, judge)
}

func TestADecisionEvaluatorReadsAWorkflowTrialAsAWorkflow(t *testing.T) {
	cfg := baseDecisionConfig(false)
	cfg.Reads = []string{readPrompt, readResponse, readTranscript}
	fake := &fakeDecision{result: answer("fully", 0.95)}
	trajectory := "[node classify · tool classify_order] success — output: {\"priority\":\"urgent\"}"

	out := ScoreResult(t.Context(), nil, ScoreInputs{
		Evaluator: decisionEvaluatorRow(t, cfg), Prompt: "Route order O-1", Response: `{"priority":"urgent"}`,
		Transcript: trajectory, WorkflowTrajectory: true, Decision: fake, DecisionEnabled: true,
	}, nil)

	if out.Errored || out.Score != 1 {
		t.Fatalf("out = %+v, want the decision model's answer scored", out)
	}
	state, _ := fake.last.State.(map[string]any)
	if state["workflow_trajectory"] != trajectory {
		t.Errorf("state = %v, want the trajectory under workflow_trajectory", state)
	}
	if _, ok := state["transcript"]; ok {
		t.Errorf("state = %v, want no agent transcript for a workflow trial", state)
	}
}

func TestDecisionScore(t *testing.T) {
	t.Run("a confident answer is scored by the decision model", func(t *testing.T) {
		fake := &fakeDecision{result: answer("partially", 0.93)}
		judge := &countingJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), fake, judge, true)
		if out.Errored || out.Score != 0.5 || !out.Passed || out.Choice == nil || *out.Choice != "partially" {
			t.Fatalf("out = %+v", out)
		}
		if judge.calls.Load() != 0 {
			t.Fatal("a confident answer must not escalate")
		}
		d := out.Details
		if d == nil || d.DecidedBy != "decision" || d.Escalated || d.Answer == nil || optionalFloat(d.Answer.Confidence) != 0.93 ||
			d.Usage == nil || d.Usage.Decision == nil || d.Usage.Decision.InputTokens != 912 || d.Model != "jev-1.13.0-20260917" {
			t.Fatalf("details = %+v", d)
		}
		if out.Reasoning == nil || !strings.Contains(*out.Reasoning, "partially · 93% confidence") {
			t.Fatalf("reasoning = %v", out.Reasoning)
		}
	})

	t.Run("the state carries only what reads names", func(t *testing.T) {
		fake := &fakeDecision{result: answer("fully", 1)}
		runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(false)), fake, nil, true)
		state, _ := fake.last.State.(map[string]any)
		if state["prompt"] != "What is 2+2?" || state["response"] != "4" {
			t.Fatalf("state = %v", state)
		}
		if _, ok := state["expected_response"]; ok {
			t.Fatal("expected_response sent although reads omits it")
		}
		if fake.last.Model != "jev-1.13.0" || len(fake.last.Questions) != 1 || fake.last.Questions[0].Key != decisionQuestionKey {
			t.Fatalf("request = %+v", fake.last)
		}
	})

	t.Run("a low-confidence answer escalates to the LLM judge", func(t *testing.T) {
		fake := &fakeDecision{result: answer("partially", 0.42)}
		judge := &countingJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), fake, judge, true)
		if judge.calls.Load() != 1 || out.Score != 1.0 || out.Errored {
			t.Fatalf("judge calls=%d out=%+v", judge.calls.Load(), out)
		}
		d := out.Details
		if !d.Escalated || d.EscalationReason != "low_confidence" || d.DecidedBy != "llm_judge" || d.Answer == nil || d.Answer.Choice != "partially" {
			t.Fatalf("details = %+v", d)
		}
		if !strings.HasPrefix(*out.Reasoning, "Escalated (low confidence): judge says yes") {
			t.Fatalf("reasoning = %q", *out.Reasoning)
		}
	})

	t.Run("a transient provider failure escalates", func(t *testing.T) {
		fake := &fakeDecision{err: &decision.Error{Kind: decision.FailureRateLimited, Err: errors.New("429")}}
		judge := &countingJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), fake, judge, true)
		if judge.calls.Load() != 1 || out.Errored || out.Details.EscalationReason != "provider_error" || out.Details.ProviderError != "rate_limited" {
			t.Fatalf("judge calls=%d out=%+v details=%+v", judge.calls.Load(), out, out.Details)
		}
	})

	t.Run("an oversized state escalates as input_too_large", func(t *testing.T) {
		fake := &fakeDecision{err: &decision.Error{Kind: decision.FailureInputTooLarge, Err: &decision.StateTooLargeError{Size: 200, Limit: 100}}}
		judge := &countingJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), fake, judge, true)
		if judge.calls.Load() != 1 || out.Details.ProviderError != "input_too_large" {
			t.Fatalf("out = %+v details=%+v", out, out.Details)
		}
	})

	t.Run("a provider failure without escalateTo is errored", func(t *testing.T) {
		fake := &fakeDecision{err: &decision.Error{Kind: decision.FailureTimeout, Err: errors.New("deadline")}}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(false)), fake, nil, true)
		if !out.Errored || out.Details == nil || out.Details.ProviderError != "timeout" || !strings.Contains(*out.Reasoning, "timeout") {
			t.Fatalf("out = %+v", out)
		}
	})

	t.Run("a permanent failure never escalates", func(t *testing.T) {
		fake := &fakeDecision{err: &decision.Error{Kind: decision.FailureUnauthorized, Err: errors.New("401")}}
		judge := &countingJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), fake, judge, true)
		if !out.Errored || judge.calls.Load() != 0 {
			t.Fatalf("errored=%v judge calls=%d", out.Errored, judge.calls.Load())
		}
	})

	t.Run("an answer outside the options is an invalid response", func(t *testing.T) {
		fake := &fakeDecision{result: answer("sideways", 0.99)}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(false)), fake, nil, true)
		if !out.Errored || out.Details.ProviderError != "invalid_response" {
			t.Fatalf("out = %+v details=%+v", out, out.Details)
		}
	})

	t.Run("an invalid answer keeps the tokens it was billed", func(t *testing.T) {
		fake := &fakeDecision{result: answer("sideways", 0.99)}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(false)), fake, nil, true)
		u := out.Details.Usage
		if !out.Errored || u == nil || u.Decision == nil || u.Decision.InputTokens != 912 || out.Details.Model != "jev-1.13.0-20260917" {
			t.Fatalf("out = %+v details=%+v", out, out.Details)
		}
	})

	t.Run("an escalated invalid answer keeps both judges' usage", func(t *testing.T) {
		judge := &usageJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), &fakeDecision{result: answer("sideways", 0.99)}, judge, true)
		u := out.Details.Usage
		if judge.calls.Load() != 1 || !out.Details.Escalated || u == nil || u.Decision == nil || u.Decision.InputTokens != 912 || u.LLMJudge == nil {
			t.Fatalf("judge calls=%d details=%+v usage=%+v", judge.calls.Load(), out.Details, u)
		}
	})

	t.Run("with the flag off the score is errored and nothing is sent", func(t *testing.T) {
		fake := &fakeDecision{result: answer("fully", 1)}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(false)), fake, nil, false)
		if !out.Errored || fake.calls.Load() != 0 || !strings.Contains(*out.Reasoning, "SAM_FEATURE_EVAL_DECISION_EVALUATOR") {
			t.Fatalf("out = %+v calls=%d", out, fake.calls.Load())
		}
	})

	t.Run("a target missing from the snapshot errors a low-confidence case", func(t *testing.T) {
		cfg := baseDecisionConfig(true)
		cfg.EscalateTo = nil
		out := runDecisionScore(t, decisionEvaluatorRow(t, cfg), &fakeDecision{result: answer("partially", 0.42)}, &countingJudge{}, true)
		if !out.Errored || !strings.Contains(*out.Reasoning, "missing from the run snapshot") {
			t.Fatalf("out = %+v", out)
		}
	})

	t.Run("a cancelled run neither escalates nor reports a provider failure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		judge := &countingJudge{}
		out := ScoreResult(ctx, nil, ScoreInputs{
			Evaluator: decisionEvaluatorRow(t, baseDecisionConfig(true)), Prompt: "p", Response: "r",
			Decision: &fakeDecision{err: context.Canceled}, DecisionEnabled: true,
		}, judge)
		if !out.Errored || judge.calls.Load() != 0 || out.Details.ProviderError != "" || out.Details.Escalated {
			t.Fatalf("out = %+v details = %+v", out, out.Details)
		}
	})

	t.Run("a model that cannot be built shows a fixed cause, never the raw error", func(t *testing.T) {
		build := &failingDecisionSource{
			err:   &decision.Error{Kind: decision.FailureConfig, Err: errors.New("pq: connection refused to db.internal:5432")},
			shown: `decision model "jev" could not be loaded; it may have been deleted or changed to a chat model`,
		}
		out := ScoreResult(t.Context(), nil, ScoreInputs{
			Evaluator: decisionEvaluatorRow(t, baseDecisionConfig(true)), Prompt: "p", Response: "r",
			Decision: build, DecisionEnabled: true,
		}, &countingJudge{})
		if !out.Errored || !strings.Contains(*out.Reasoning, "could not be loaded") || strings.Contains(*out.Reasoning, "db.internal") {
			t.Fatalf("reasoning = %q", *out.Reasoning)
		}
	})

	t.Run("an answer without a confidence is kept and says so", func(t *testing.T) {
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(false)), &fakeDecision{result: unconfidentAnswer("partially")}, nil, true)
		if out.Errored || out.Score != 0.5 || out.Details == nil || out.Details.Answer == nil {
			t.Fatalf("out = %+v details = %+v", out, out.Details)
		}
		if out.Details.Answer.Confidence != nil {
			t.Fatalf("confidence = %v, want none rather than a made-up value", *out.Details.Answer.Confidence)
		}
		if out.Reasoning == nil || !strings.Contains(*out.Reasoning, "confidence not reported") {
			t.Fatalf("reasoning = %v", out.Reasoning)
		}
	})

	t.Run("an answer without a confidence escalates as no confidence", func(t *testing.T) {
		judge := &countingJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), &fakeDecision{result: unconfidentAnswer("partially")}, judge, true)
		if judge.calls.Load() != 1 || out.Errored || !out.Details.Escalated || out.Details.EscalationReason != "no_confidence" {
			t.Fatalf("judge calls=%d out=%+v details=%+v", judge.calls.Load(), out, out.Details)
		}
	})

	t.Run("expected scoring that falls back to chosen says so", func(t *testing.T) {
		cfg := baseDecisionConfig(false)
		cfg.Scoring = scoringExpected
		out := runDecisionScore(t, decisionEvaluatorRow(t, cfg), &fakeDecision{result: answer("partially", 0.9)}, nil, true)
		if out.Errored || out.Score != 0.5 || out.Details == nil || out.Details.Answer == nil || out.Details.Answer.ScoringApplied != scoringChosen {
			t.Fatalf("out = %+v details = %+v", out, out.Details)
		}
		if out.Reasoning == nil || !strings.Contains(*out.Reasoning, "scored as chosen") {
			t.Fatalf("reasoning = %v, want it to say the answer was scored as chosen", out.Reasoning)
		}
	})

	t.Run("a confidence exactly at the threshold is not escalated", func(t *testing.T) {
		judge := &countingJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), &fakeDecision{result: answer("partially", 0.8)}, judge, true)
		if judge.calls.Load() != 0 || out.Errored || out.Details.Escalated || out.Details.DecidedBy != "decision" {
			t.Fatalf("judge calls=%d out=%+v details=%+v", judge.calls.Load(), out, out.Details)
		}
	})

	t.Run("an escalated case records both judges' usage", func(t *testing.T) {
		judge := &usageJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), &fakeDecision{result: answer("partially", 0.42)}, judge, true)
		u := out.Details.Usage
		if judge.calls.Load() != 1 || u == nil || u.Decision == nil || u.LLMJudge == nil {
			t.Fatalf("judge calls=%d usage=%+v", judge.calls.Load(), u)
		}
		if u.Decision.InputTokens != 912 || u.LLMJudge.InputTokens != 50 || u.LLMJudge.OutputTokens != 7 {
			t.Fatalf("usage decision=%+v llmJudge=%+v", *u.Decision, *u.LLMJudge)
		}
	})

	t.Run("an escalation whose judge fails is errored and names no judge", func(t *testing.T) {
		judge := &erroringJudge{}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), &fakeDecision{result: answer("partially", 0.42)}, judge, true)
		if judge.calls.Load() != 1 || !out.Errored || out.Details == nil || !out.Details.Escalated || out.Details.DecidedBy != "" {
			t.Fatalf("judge calls=%d out=%+v details=%+v", judge.calls.Load(), out, out.Details)
		}
	})

	t.Run("a model lookup the store did not answer escalates without blaming the provider", func(t *testing.T) {
		judge := &countingJudge{}
		clients := newRunDecisionClients(&fakeDecisionResolver{err: errors.New("connection refused")}, nil, slog.New(slog.DiscardHandler))
		out := ScoreResult(t.Context(), nil, ScoreInputs{
			Evaluator: decisionEvaluatorRow(t, baseDecisionConfig(true)), Prompt: "What is 2+2?", Response: "4",
			Decision: clients, DecisionEnabled: true,
		}, judge)
		d := out.Details
		if judge.calls.Load() != 1 || out.Errored || d == nil || d.EscalationReason != "model_lookup_failed" || d.ProviderError != "" {
			t.Fatalf("judge calls=%d out=%+v details=%+v", judge.calls.Load(), out, d)
		}
	})

	t.Run("a model that cannot be loaded errors the case and says why", func(t *testing.T) {
		judge := &countingJudge{}
		fake := &fakeDecision{clientErr: &decisionClientError{
			err:   &decision.Error{Kind: decision.FailureConfig, Err: ErrDecisionModelUnavailable},
			shown: `decision model "jev" could not be loaded`,
		}}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(true)), fake, judge, true)
		if judge.calls.Load() != 0 || !out.Errored || out.Details.ProviderError != "" || out.Reasoning == nil ||
			!strings.Contains(*out.Reasoning, "decision model unavailable") || !strings.Contains(*out.Reasoning, "could not be loaded") {
			t.Fatalf("judge calls=%d out=%+v reasoning=%v", judge.calls.Load(), out, out.Reasoning)
		}
	})

	t.Run("a permanent failure row names no judge", func(t *testing.T) {
		fake := &fakeDecision{err: &decision.Error{Kind: decision.FailureUnauthorized, Err: errors.New("401")}}
		out := runDecisionScore(t, decisionEvaluatorRow(t, baseDecisionConfig(false)), fake, nil, true)
		if !out.Errored || out.Details == nil || out.Details.DecidedBy != "" || out.Details.ProviderError != "unauthorized" {
			t.Fatalf("out = %+v details = %+v", out, out.Details)
		}
	})

	t.Run("unreadable choice scores error the case before anything is sent", func(t *testing.T) {
		ev := decisionEvaluatorRow(t, baseDecisionConfig(true))
		ev.ChoiceScores = new("{not json")
		judge := &countingJudge{}
		fake := &fakeDecision{result: answer("fully", 0.9)}
		out := runDecisionScore(t, ev, fake, judge, true)
		if !out.Errored || judge.calls.Load() != 0 || fake.calls.Load() != 0 {
			t.Fatalf("judge calls=%d provider calls=%d out=%+v", judge.calls.Load(), fake.calls.Load(), out)
		}
		if out.Reasoning == nil || !strings.Contains(*out.Reasoning, "choice scores could not be read") {
			t.Fatalf("reasoning = %v", out.Reasoning)
		}
	})

	t.Run("a model missing at trigger time is errored", func(t *testing.T) {
		cfg := baseDecisionConfig(false)
		cfg.ResolvedModel = nil
		out := runDecisionScore(t, decisionEvaluatorRow(t, cfg), &fakeDecision{}, nil, true)
		if !out.Errored || !strings.Contains(*out.Reasoning, "not configured") {
			t.Fatalf("out = %+v", out)
		}
	})
}

type fakeDecisionResolver struct {
	raw       map[string]any
	err       error
	failFirst bool
	calls     atomic.Int32
}

func (f *fakeDecisionResolver) ResolveDecision(context.Context, string) (map[string]any, error) {
	if n := f.calls.Add(1); f.failFirst && n > 1 {
		return f.raw, nil
	}
	return f.raw, f.err
}

func TestADecisionJudgesCallsAreCountedInOTel(t *testing.T) {
	for name, sem := range map[string]chan struct{}{
		"bounded by the scoring semaphore": make(chan struct{}, 1),
		"unbounded":                        nil,
	} {
		t.Run(name, func(t *testing.T) {
			metrics := observability.NewRecordingMetrics()
			previous := runtime.MetricProvider()
			runtime.Init(metrics)
			t.Cleanup(func() { runtime.Init(previous) })

			resolver := &fakeDecisionResolver{raw: map[string]any{
				"api_base": startDecisionMock(t), "api_key": "test-key", "decision_provider": decision.ProviderTypeSafe,
			}}
			model := DecisionModelSnapshot{ConfigID: "cfg", Ref: "jev", Provider: decision.ProviderTypeSafe, Model: "jev-1.13.0",
				TimeoutSeconds: 5, APIBase: resolver.raw["api_base"].(string)}
			client, err := newRunDecisionClients(resolver, sem, slog.New(slog.DiscardHandler)).DecisionClient(t.Context(), model)
			if err != nil {
				t.Fatalf("DecisionClient: %v", err)
			}
			q := choiceQuestion().toDecision()
			res, err := client.Evaluate(t.Context(), decision.Request{Model: model.Model, State: map[string]any{"response": "4"}, Questions: []decision.Question{q}})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			want := int64(res.Usage.InputTokens + res.Usage.OutputTokens)
			if got := metrics.CounterSum("gen_ai.tokens.used", "component.name", "platform-eval-decision-judge", "gen_ai.request.model", model.Model); want == 0 || got != want {
				t.Errorf("otel counted %d judge tokens, want the %d the provider reported", got, want)
			}
		})
	}
}

func TestRunDecisionClients(t *testing.T) {
	model := DecisionModelSnapshot{ConfigID: "cfg", Ref: "jev", Provider: decision.ProviderTypeSafe, Model: "jev-1.13.0", TimeoutSeconds: 5}

	t.Run("builds a bounded client from the live config and answers through the semaphore", func(t *testing.T) {
		resolver := &fakeDecisionResolver{raw: map[string]any{
			"api_base": startDecisionMock(t), "api_key": "test-key", "decision_provider": decision.ProviderTypeSafe,
		}}
		model := model
		model.APIBase = resolver.raw["api_base"].(string)
		sem := make(chan struct{}, 1)
		clients := newRunDecisionClients(resolver, sem, slog.New(slog.DiscardHandler))
		c, err := clients.DecisionClient(t.Context(), model)
		if err != nil {
			t.Fatalf("DecisionClient: %v", err)
		}
		q := choiceQuestion().toDecision()
		res, err := c.Evaluate(t.Context(), decision.Request{Model: model.Model, State: map[string]any{"response": "4"}, Questions: []decision.Question{q}})
		if err != nil || res.Validate([]decision.Question{q}) != nil {
			t.Fatalf("Evaluate = %+v, %v", res, err)
		}
		if again, _ := clients.DecisionClient(t.Context(), model); again == nil || resolver.calls.Load() != 1 {
			t.Fatalf("the client must be cached for the run; resolver calls = %d", resolver.calls.Load())
		}
		if len(sem) != 0 {
			t.Fatal("the scoring slot was not released")
		}
	})

	t.Run("a held semaphore yields to a cancelled context", func(t *testing.T) {
		sem := make(chan struct{}, 1)
		sem <- struct{}{}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := boundedDecisionClient{inner: &fakeDecision{}, sem: sem}.Evaluate(ctx, decision.Request{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("a transient lookup failure is retried by the next case, not cached", func(t *testing.T) {
		resolver := &fakeDecisionResolver{
			raw:       map[string]any{"api_base": "https://decisions.example.com", "api_key": "k"},
			err:       errors.New("pq: connection refused to db.internal:5432"),
			failFirst: true,
		}
		model := model
		model.APIBase = "https://decisions.example.com"
		clients := newRunDecisionClients(resolver, nil, slog.New(slog.DiscardHandler))
		_, err := clients.DecisionClient(t.Context(), model)
		ce, ok := errors.AsType[*decisionClientError](err)
		if !ok || decision.Classify(err).Permanent() || strings.Contains(ce.shown, "db.internal") {
			t.Fatalf("err = %v (kind %s, shown %q), want a transient failure that hides the cause", err, decision.Classify(err), ce.shown)
		}
		if c, err := clients.DecisionClient(t.Context(), model); err != nil || c == nil || resolver.calls.Load() != 2 {
			t.Fatalf("the next case must retry the lookup and succeed: client=%v err=%v calls=%d", c, err, resolver.calls.Load())
		}
	})

	t.Run("a store that stays down is warned about once per model", func(t *testing.T) {
		var buf strings.Builder
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		clients := newRunDecisionClients(&fakeDecisionResolver{err: errors.New("connection refused")}, nil, logger)
		for range 3 {
			if _, err := clients.DecisionClient(t.Context(), model); err == nil {
				t.Fatal("want a lookup failure")
			}
		}
		if warns, debugs := strings.Count(buf.String(), "level=WARN"), strings.Count(buf.String(), "level=DEBUG"); warns != 1 || debugs != 2 {
			t.Fatalf("log has %d WARN and %d DEBUG lines, want 1 and 2:\n%s", warns, debugs, buf.String())
		}
	})

	t.Run("a provider that rejects a call for good is not called again this run", func(t *testing.T) {
		var buf strings.Builder
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		clients := newRunDecisionClients(nil, nil, logger)
		fake := &fakeDecision{err: rejectedStatus(http.StatusUnauthorized)}
		clients.clients[*baseDecisionConfig(false).ResolvedModel] = fake
		ev := decisionEvaluatorRow(t, baseDecisionConfig(false))
		var outs []ScoreOutput
		for range 3 {
			outs = append(outs, ScoreResult(t.Context(), logger, ScoreInputs{
				Evaluator: ev, Prompt: "What is 2+2?", Response: "4", Decision: clients, DecisionEnabled: true,
			}, nil))
		}
		if fake.calls.Load() != 1 {
			t.Fatalf("provider calls = %d, want 1", fake.calls.Load())
		}
		if !outs[0].Errored || outs[0].Details.ProviderError != "unauthorized" {
			t.Fatalf("first case = %+v details=%+v", outs[0], outs[0].Details)
		}
		for _, out := range outs[1:] {
			if !out.Errored || out.Reasoning == nil || !strings.Contains(*out.Reasoning, "rejected the run's requests (unauthorized)") ||
				out.Details == nil || out.Details.ProviderError != "unauthorized" {
				t.Fatalf("later case = %+v reasoning=%v details=%+v", out, out.Reasoning, out.Details)
			}
		}
		log := buf.String()
		if strings.Count(log, "unusable for this run") != 1 || strings.Count(log, "call failed") != 1 || strings.Contains(log, "example.com") {
			t.Fatalf("log = %q, want the first rejection and the model's unusability logged once each, without the provider's error text", log)
		}
	})

	t.Run("a Gemini invalid-key answer, a 400, is not called again this run", func(t *testing.T) {
		clients := newRunDecisionClients(nil, nil, slog.New(slog.DiscardHandler))
		fake := &fakeDecision{err: &decision.Error{Kind: decision.FailureUnauthorized,
			Err: fmt.Errorf("google_ai_studio: %w", &gemini.StatusError{StatusCode: http.StatusBadRequest, KeyInvalid: true})}}
		clients.clients[*baseDecisionConfig(false).ResolvedModel] = fake
		ev := decisionEvaluatorRow(t, baseDecisionConfig(false))
		for range 3 {
			ScoreResult(t.Context(), nil, ScoreInputs{
				Evaluator: ev, Prompt: "p", Response: "r", Decision: clients, DecisionEnabled: true,
			}, nil)
		}
		if fake.calls.Load() != 1 {
			t.Fatalf("provider calls = %d, want 1", fake.calls.Load())
		}
	})

	t.Run("a 403 can be the case's own and does not stop the run's other cases", func(t *testing.T) {
		clients := newRunDecisionClients(nil, nil, slog.New(slog.DiscardHandler))
		fake := &fakeDecision{err: rejectedStatus(http.StatusForbidden)}
		clients.clients[*baseDecisionConfig(false).ResolvedModel] = fake
		ev := decisionEvaluatorRow(t, baseDecisionConfig(false))
		for range 3 {
			ScoreResult(t.Context(), nil, ScoreInputs{
				Evaluator: ev, Prompt: "p", Response: "r", Decision: clients, DecisionEnabled: true,
			}, nil)
		}
		if fake.calls.Load() != 3 {
			t.Fatalf("provider calls = %d, want every case to call it", fake.calls.Load())
		}
	})

	t.Run("a config failure is the case's own and does not stop the run's other cases", func(t *testing.T) {
		clients := newRunDecisionClients(nil, nil, slog.New(slog.DiscardHandler))
		fake := &fakeDecision{err: &decision.Error{Kind: decision.FailureConfig, Err: errors.New("400")}}
		clients.clients[*baseDecisionConfig(false).ResolvedModel] = fake
		ev := decisionEvaluatorRow(t, baseDecisionConfig(false))
		for range 3 {
			ScoreResult(t.Context(), nil, ScoreInputs{
				Evaluator: ev, Prompt: "p", Response: "r", Decision: clients, DecisionEnabled: true,
			}, nil)
		}
		if fake.calls.Load() != 3 {
			t.Fatalf("provider calls = %d, want every case to call it", fake.calls.Load())
		}
	})

	t.Run("a broken evaluator is warned about once per run", func(t *testing.T) {
		var buf strings.Builder
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		clients := newRunDecisionClients(nil, nil, logger)
		ev := decisionEvaluatorRow(t, baseDecisionConfig(false))
		ev.ChoiceScores = new("{not json")
		for range 2 {
			out := ScoreResult(t.Context(), logger, ScoreInputs{
				Evaluator: ev, Prompt: "p", Response: "r", Decision: clients, DecisionEnabled: true,
			}, nil)
			if !out.Errored {
				t.Fatalf("out = %+v", out)
			}
		}
		if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
			t.Fatalf("log has %d WARN lines, want 1:\n%s", n, buf.String())
		}
	})

	failures := map[string]struct {
		resolver  DecisionModelResolver
		apiBase   string
		wantShown string
	}{
		"no resolver": {nil, "", "no decision model resolver"},
		"the model is gone": {&fakeDecisionResolver{err: fmt.Errorf("%w: no configured model is named %q", ErrDecisionModelUnavailable, "jev")},
			"", "could not be loaded"},
		"the provider changed after trigger": {&fakeDecisionResolver{raw: map[string]any{"decision_provider": decision.ProviderOpenAI}},
			"", "changed provider from typesafe to openai"},
		"the API base changed after trigger": {&fakeDecisionResolver{raw: map[string]any{"api_base": "https://elsewhere.example.com", "api_key": "k"}},
			"https://decisions.example.com", "changed its API base"},
		"the API base is unusable": {&fakeDecisionResolver{raw: map[string]any{"api_base": "not a url", "api_key": "k"}},
			"not a url", "API base or key"},
	}
	for name, tc := range failures {
		t.Run(name, func(t *testing.T) {
			clients := newRunDecisionClients(tc.resolver, nil, slog.New(slog.DiscardHandler))
			model := model
			model.APIBase = tc.apiBase
			_, err := clients.DecisionClient(t.Context(), model)
			ce, ok := errors.AsType[*decisionClientError](err)
			if !ok {
				t.Fatalf("err = %v, want a *decisionClientError", err)
			}
			if !strings.Contains(ce.shown, tc.wantShown) || decision.Classify(err) != decision.FailureConfig {
				t.Fatalf("err = %v (shown %q), want a config failure showing %q", err, ce.shown, tc.wantShown)
			}
			if strings.Contains(ce.shown, "pq:") || strings.Contains(ce.shown, "not a url") {
				t.Fatalf("shown leaks the raw cause: %q", ce.shown)
			}
			if _, again := clients.DecisionClient(t.Context(), model); again != err {
				t.Fatal("a failed build must be cached for the run")
			}
		})
	}
}

func rejectedStatus(status int) error {
	return &decision.Error{Kind: decision.FailureUnauthorized,
		Err: fmt.Errorf("typesafe: call https://decisions.example.com/v1?key=secret: %w", &typesafe.StatusError{StatusCode: status})}
}
