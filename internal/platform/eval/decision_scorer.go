// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/providers"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const (
	decidedByDecision = "decision"
	decidedByLLMJudge = "llm_judge"
)

const (
	escalationLowConfidence = taxonomy.EscalationReasonLowConfidence
	escalationNoConfidence  = taxonomy.EscalationReasonNoConfidence
	escalationProviderError = taxonomy.EscalationReasonProviderError
	escalationModelLookup   = taxonomy.EscalationReasonModelLookup
)

const stateBytesPerToken = 3

const defaultDecisionEvalTimeout = 30 * time.Second

const decisionLookupTimeout = 30 * time.Second

const maxDecisionTimeoutSeconds = 600

var ErrDecisionModelUnavailable = errors.New("decision model unavailable")

type DecisionModelSnapshot struct {
	ConfigID       string `json:"config_id,omitempty"`
	Ref            string `json:"ref"`
	Provider       string `json:"provider"`
	Model          string `json:"model,omitempty"`
	APIBase        string `json:"api_base,omitempty"`
	MaxStateBytes  int    `json:"max_state_bytes,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func freezeDecisionModel(ref string, raw, snap map[string]any, logger *slog.Logger) *DecisionModelSnapshot {
	provider := samapp.DecisionProvider(raw)
	m := &DecisionModelSnapshot{Ref: ref, Provider: provider}
	m.ConfigID, _ = snap["id"].(string)
	m.Model, _ = raw["model"].(string)
	m.APIBase, _ = raw["api_base"].(string)
	m.MaxStateBytes = decisionStateLimit(provider, raw)
	m.TimeoutSeconds = cmp.Or(positiveInt(raw["timeout_seconds"]), positiveInt(raw["timeoutSeconds"]))
	if m.TimeoutSeconds > maxDecisionTimeoutSeconds {
		m.TimeoutSeconds = maxDecisionTimeoutSeconds
		if logger != nil {
			logger.Warn("eval: decision model timeout above the cap; clamping it",
				"model", ref, "param", timeoutParamName(raw), "cap", maxDecisionTimeoutSeconds)
		}
	}
	if logger != nil {
		for _, key := range []string{"max_state_bytes", "maxStateBytes", "timeout_seconds", "timeoutSeconds"} {
			if v, set := raw[key]; set && v != nil && v != "" && positiveInt(v) == 0 && !isZero(v) {
				logger.Warn("eval: decision model param is not a positive whole number; ignoring it",
					"model", ref, "param", key, "type", fmt.Sprintf("%T", v))
			}
		}
	}
	return m
}

func timeoutParamName(raw map[string]any) string {
	if positiveInt(raw["timeout_seconds"]) > 0 {
		return "timeout_seconds"
	}
	return "timeoutSeconds"
}

func decisionStateLimit(provider string, raw map[string]any) int {
	ceiling := providers.ContextTokens(provider) * stateBytesPerToken
	limit := cmp.Or(positiveInt(raw["max_state_bytes"]), positiveInt(raw["maxStateBytes"]))
	if limit == 0 {
		limit = positiveInt(raw["max_input_tokens"]) * stateBytesPerToken
	}
	if limit == 0 || (ceiling > 0 && limit > ceiling) {
		return ceiling
	}
	return limit
}

func positiveInt(v any) int {
	var f float64
	switch n := v.(type) {
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case float64:
		f = n
	case json.Number:
		var err error
		if f, err = n.Float64(); err != nil {
			return 0
		}
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(n), 64); err != nil {
			return 0
		}
	default:
		return 0
	}
	if f <= 0 || f != math.Trunc(f) || f > math.MaxInt32 {
		return 0
	}
	return int(f)
}

func isZero(v any) bool {
	f, ok := asFloat(v)
	if s, isText := v.(string); isText {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		f, ok = parsed, err == nil
	}
	return ok && f == 0
}

type ScoreDetails struct {
	DecidedBy        string                 `json:"decidedBy,omitempty"`
	Model            string                 `json:"model,omitempty"`
	Answer           *DecisionAnswerDetails `json:"answer,omitempty"`
	Escalated        bool                   `json:"escalated,omitempty"`
	EscalationReason string                 `json:"escalationReason,omitempty"`
	ProviderError    string                 `json:"providerError,omitempty"`
	Usage            *JudgeUsageDetails     `json:"usage,omitempty"`
	DurationMs       int64                  `json:"durationMs,omitempty"`
}

type DecisionAnswerDetails struct {
	Choice         string             `json:"choice,omitempty"`
	Probability    *float64           `json:"probability,omitempty"`
	Score          *float64           `json:"score,omitempty"`
	Confidence     *float64           `json:"confidence,omitempty"`
	Probabilities  map[string]float64 `json:"probabilities,omitempty"`
	ScoringApplied string             `json:"scoringApplied,omitempty"`
}

type JudgeUsageDetails struct {
	Decision *JudgeTokenUsage `json:"decision,omitempty"`
	LLMJudge *JudgeTokenUsage `json:"llmJudge,omitempty"`
}

type JudgeTokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

type DecisionModelResolver interface {
	ResolveDecision(ctx context.Context, aliasOrID string) (raw map[string]any, err error)
}

type decisionClientSource interface {
	DecisionClient(ctx context.Context, m DecisionModelSnapshot) (decision.Evaluator, error)
}

type unusableMarker interface {
	markUnusable(m DecisionModelSnapshot, err error) bool
}

type onceWarner interface {
	warnOnce(key string) bool
}

type spendRecorder interface {
	recorded(client decision.Evaluator, attribution *llm.SpendAttribution) decision.Evaluator
}

func warnLevel(src decisionClientSource, key string) slog.Level {
	if w, ok := src.(onceWarner); ok && !w.warnOnce(key) {
		return slog.LevelDebug
	}
	return slog.LevelWarn
}

type runDecisionClients struct {
	resolver DecisionModelResolver
	sem      chan struct{}
	logger   *slog.Logger
	spend    *spend.Publisher
	gate     *budget.Gate

	mu      sync.Mutex
	clients map[DecisionModelSnapshot]decision.Evaluator
	failed  map[DecisionModelSnapshot]error
	retried map[DecisionModelSnapshot]bool
	warned  map[string]bool
}

type decisionClientError struct {
	err      error
	shown    string
	rejected decision.FailureKind
}

var errModelStoreUnavailable = errors.New("model configuration store unavailable")

func (e *decisionClientError) storeUnavailable() bool {
	return errors.Is(e.err, errModelStoreUnavailable)
}

func (e *decisionClientError) Error() string { return e.err.Error() }
func (e *decisionClientError) Unwrap() error { return e.err }

const maxShownCause = 200

func newRunDecisionClients(resolver DecisionModelResolver, sem chan struct{}, logger *slog.Logger) *runDecisionClients {
	return &runDecisionClients{
		resolver: resolver,
		sem:      sem,
		logger:   logger,
		clients:  make(map[DecisionModelSnapshot]decision.Evaluator),
		failed:   make(map[DecisionModelSnapshot]error),
		retried:  make(map[DecisionModelSnapshot]bool),
		warned:   make(map[string]bool),
	}
}

func (r *runDecisionClients) meterOn(pub *spend.Publisher, gate *budget.Gate) {
	r.spend, r.gate = pub, gate
}

func (r *runDecisionClients) recorded(client decision.Evaluator, attribution *llm.SpendAttribution) decision.Evaluator {
	return spend.NewMeteredEvaluator(client, r.spend, attribution, r.gate)
}

func (r *runDecisionClients) warnOnce(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.warned[key] {
		return false
	}
	r.warned[key] = true
	return true
}

func (r *runDecisionClients) markUnusable(m DecisionModelSnapshot, err error) bool {
	kind := decision.Classify(err)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, failed := r.failed[m]; failed {
		return false
	}
	r.failed[m] = &decisionClientError{
		err:      err,
		shown:    fmt.Sprintf("decision model %q rejected the run's requests (%s)", m.Ref, kind),
		rejected: kind,
	}
	r.logger.Warn("eval: decision model unusable for this run",
		"model", m.Ref, "provider", m.Provider, "category", kind)
	return true
}

func (r *runDecisionClients) DecisionClient(ctx context.Context, m DecisionModelSnapshot) (decision.Evaluator, error) {
	r.mu.Lock()
	if failure, ok := r.failed[m]; ok {
		r.mu.Unlock()
		return nil, failure
	}
	if c, ok := r.clients[m]; ok {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()

	client, shown, err := r.build(ctx, m)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		failure := &decisionClientError{err: err, shown: shown}
		if failure.storeUnavailable() {
			level := slog.LevelWarn
			if r.retried[m] {
				level = slog.LevelDebug
			}
			r.retried[m] = true
			r.logger.Log(ctx, level, "eval: decision model lookup failed; retrying on the next case",
				"model", m.Ref, "provider", m.Provider, "category", decision.Classify(err))
			return nil, failure
		}
		if _, logged := r.failed[m]; !logged {
			r.logger.Warn("eval: decision model unavailable for this run",
				"model", m.Ref, "provider", m.Provider, "category", decision.Classify(err))
		}
		r.failed[m] = failure
		return nil, failure
	}
	if failure, ok := r.failed[m]; ok {
		return nil, failure
	}
	if c, ok := r.clients[m]; ok {
		return c, nil
	}
	r.clients[m] = client
	return client, nil
}

func (r *runDecisionClients) build(ctx context.Context, m DecisionModelSnapshot) (_ decision.Evaluator, shown string, _ error) {
	if r.resolver == nil {
		return nil, "no decision model resolver is configured",
			&decision.Error{Kind: decision.FailureConfig, Err: errors.New("no decision model resolver configured")}
	}
	ref := m.ConfigID
	if ref == "" {
		ref = m.Ref
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), decisionLookupTimeout)
	defer cancel()
	raw, err := r.resolver.ResolveDecision(lookupCtx, ref)
	if errors.Is(err, ErrDecisionModelUnavailable) {
		return nil, fmt.Sprintf("decision model %q could not be loaded; it may have been deleted or changed to a chat model", m.Ref),
			&decision.Error{Kind: decision.FailureConfig, Err: err}
	}
	if err != nil {
		return nil, fmt.Sprintf("decision model %q could not be looked up; the model configuration store did not answer", m.Ref),
			&decision.Error{Kind: decision.FailureUnreachable, Err: fmt.Errorf("%w: %w", errModelStoreUnavailable, err)}
	}
	if provider := samapp.DecisionProvider(raw); provider != m.Provider {
		shown := fmt.Sprintf("decision model %q changed provider from %s to %s after the run was triggered", m.Ref, m.Provider, provider)
		return nil, shown, &decision.Error{Kind: decision.FailureConfig, Err: errors.New(shown)}
	}
	apiBase, _ := raw["api_base"].(string)
	if apiBase != m.APIBase {
		shown := fmt.Sprintf("decision model %q changed its API base after the run was triggered", m.Ref)
		return nil, shown, &decision.Error{Kind: decision.FailureConfig, Err: errors.New(shown)}
	}
	apiKey, _ := raw["api_key"].(string)
	timeout := defaultDecisionEvalTimeout
	if m.TimeoutSeconds > 0 {
		timeout = time.Duration(m.TimeoutSeconds) * time.Second
	}
	client, err := providers.NewEvaluator(providers.Config{
		Provider: m.Provider,
		ClientConfig: decision.ClientConfig{
			BaseURL:       apiBase,
			APIKey:        apiKey,
			Timeout:       timeout,
			MaxStateBytes: m.MaxStateBytes,
			Logger:        r.logger,
		},
	})
	if err != nil {
		if decision.Classify(err) == decision.FailureUnreachable {
			err = &decision.Error{Kind: decision.FailureConfig, Err: err}
		}
		return nil, fmt.Sprintf("decision model %q has an API base or key that its provider client rejects", m.Ref), err
	}
	instrumented := observability.NewInstrumentedEvaluator(client, m.Model, "platform-eval-decision-judge")
	if r.sem == nil {
		return instrumented, "", nil
	}
	return boundedDecisionClient{inner: instrumented, sem: r.sem}, "", nil
}

type boundedDecisionClient struct {
	inner decision.Evaluator
	sem   chan struct{}
}

func (b boundedDecisionClient) Evaluate(ctx context.Context, req decision.Request) (decision.Result, error) {
	select {
	case b.sem <- struct{}{}:
	case <-ctx.Done():
		return decision.Result{}, ctx.Err()
	}
	defer func() { <-b.sem }()
	return b.inner.Evaluate(ctx, req)
}

type decisionVerdict struct {
	score      float64
	label      string
	confidence *float64
	answer     DecisionAnswerDetails
}

func mapDecisionAnswer(q decision.Question, scoring string, choiceScores map[string]any, a decision.Answer) (decisionVerdict, error) {
	v := decisionVerdict{answer: DecisionAnswerDetails{Probabilities: a.Probabilities}}
	var err error
	switch q.Type {
	case decision.TypeBoolean:
		mapBooleanAnswer(&v, a)
	case decision.TypeChoice:
		err = mapChoiceAnswer(&v, scoring, choiceScores, a)
	case decision.TypeScore:
		err = mapScoreAnswer(&v, q, scoring, a)
	default:
		err = fmt.Errorf("unknown question type %q", q.Type)
	}
	if err != nil {
		return v, err
	}
	v.score = clamp01(v.score)
	v.answer.Choice = v.label
	v.answer.Confidence = v.confidence
	return v, nil
}

func mapBooleanAnswer(v *decisionVerdict, a decision.Answer) {
	p := a.Probability
	v.score = p
	v.label = decision.BooleanCriterionFalse
	if p >= 0.5 {
		v.label = decision.BooleanCriterionTrue
	}
	confidence := math.Max(p, 1-p)
	v.confidence = &confidence
	v.answer.Probability = &p
}

func mapChoiceAnswer(v *decisionVerdict, scoring string, choiceScores map[string]any, a decision.Answer) error {
	chosen, ok := asFloat(choiceScores[a.Choice])
	if !ok {
		return fmt.Errorf("the decision model chose %q, which has no score", decision.BoundedString(a.Choice))
	}
	v.label = a.Choice
	v.score = chosen
	v.answer.ScoringApplied = scoringChosen
	if scoring == scoringExpected {
		value := func(key string) (float64, bool) { return asFloat(choiceScores[key]) }
		if expected, ok := weightedMean(a.Probabilities, len(choiceScores), value); ok {
			v.score = expected
			v.answer.ScoringApplied = scoringExpected
		}
	}
	v.confidence = answerConfidence(a, a.Choice)
	return nil
}

func mapScoreAnswer(v *decisionVerdict, q decision.Question, scoring string, a decision.Answer) error {
	levels := q.CriterionKeys()
	if len(levels) < 2 {
		return errors.New("a score question needs at least two levels")
	}
	top := float64(len(levels) - 1)
	position := math.Min(math.Max(a.Score, 0), top)
	v.label = levels[int(math.Round(position))]
	v.score = math.Round(position) / top
	v.answer.ScoringApplied = scoringChosen
	if scoring == scoringExpected {
		index := func(key string) (float64, bool) {
			i := slices.Index(levels, key)
			return float64(i) / top, i >= 0
		}
		if expected, ok := weightedMean(a.Probabilities, len(levels), index); ok {
			v.score = expected
			v.answer.ScoringApplied = scoringExpected
		}
	}
	v.confidence = answerConfidence(a, v.label)
	v.answer.Score = &position
	return nil
}

func answerConfidence(a decision.Answer, chosen string) *float64 {
	if a.Confidence != nil {
		return a.Confidence
	}
	if p, ok := a.Probabilities[chosen]; ok {
		return &p
	}
	return nil
}

const probabilityRounding = 0.005

const minExpectedMassFloor = 0.97

func minExpectedMass(options int) float64 {
	return math.Max(1-probabilityRounding*float64(options), minExpectedMassFloor) - 1e-9
}

func weightedMean(probabilities map[string]float64, options int, value func(string) (float64, bool)) (float64, bool) {
	var sum, mass float64
	for key, p := range probabilities {
		v, ok := value(key)
		if !ok || p <= 0 {
			continue
		}
		sum += p * v
		mass += p
	}
	if mass < minExpectedMass(options) {
		return 0, false
	}
	return sum / mass, true
}

func decisionState(in ScoreInputs, reads []string) map[string]any {
	state := make(map[string]any, len(reads))
	for _, r := range reads {
		switch r {
		case readPrompt:
			state["prompt"] = in.Prompt
		case readResponse:
			response := in.Response
			if isFileOnlyReply(in) {
				response = fileOnlyReplyNote(in.WorkflowTrajectory)
			}
			state["response"] = response
		case readExpectedResponse:
			if in.Expected != nil {
				state["expected_response"] = *in.Expected
			}
		case readTranscript:
			if in.Transcript != "" {
				state[transcriptStateKey(in.WorkflowTrajectory)] = in.Transcript
			}
		case readArtifacts:
			if a := stateArtifacts(in); len(a) > 0 {
				state["artifacts"] = a
			}
		}
	}
	return state
}

func transcriptStateKey(workflowTrajectory bool) string {
	if workflowTrajectory {
		return "workflow_trajectory"
	}
	return "transcript"
}

func fileOnlyReplyNote(workflowTrajectory bool) string {
	if workflowTrajectory {
		return "[No text in the workflow's output; it answered with files.]"
	}
	return "[No text in the agent's reply; it answered with files.]"
}

func stateArtifacts(in ScoreInputs) map[string]any {
	out := map[string]any{}
	for key, blocks := range map[string][]ArtifactBlock{
		"given_to_agent":    in.InputArtifacts,
		"reference":         in.ExpectedArtifacts,
		"produced_by_agent": in.Artifacts,
	} {
		if len(blocks) == 0 {
			continue
		}
		list := make([]map[string]any, 0, len(blocks))
		for _, b := range blocks {
			switch v := b.(type) {
			case ArtifactJudgeText:
				list = append(list, map[string]any{"name": v.Filename, "mime_type": v.MIMEType, "content": v.Text, "truncated": v.Truncated})
			case ArtifactPlaceholder:
				list = append(list, map[string]any{"name": v.Filename, "mime_type": v.MIMEType, "content_included": false})
			}
		}
		out[key] = list
	}
	return out
}

func decisionScore(ctx context.Context, logger *slog.Logger, in ScoreInputs, llmClient LLMJudgeClient) ScoreOutput {
	cfg, unusable := decisionScoringConfig(ctx, logger, in)
	if unusable != nil {
		return *unusable
	}
	choiceScores, err := choiceScoresOf(in.Evaluator)
	if err != nil {
		logger.Log(ctx, warnLevel(in.Decision, "choice_scores:"+in.Evaluator.ID),
			"eval: decision evaluator has unreadable choice scores", "evaluatorID", in.Evaluator.ID, "error", err)
		return erroredScore("the evaluator's choice scores could not be read")
	}
	c := &decisionCase{
		ctx: ctx, logger: logger, in: in, llmClient: llmClient, cfg: cfg, choiceScores: choiceScores,
		details: ScoreDetails{DecidedBy: decidedByDecision}, start: time.Now(),
	}
	return c.score()
}

func decisionScoringConfig(ctx context.Context, logger *slog.Logger, in ScoreInputs) (*decisionRunConfig, *ScoreOutput) {
	errored := func(detail string) (*decisionRunConfig, *ScoreOutput) {
		out := erroredScore(detail)
		return nil, &out
	}
	ev := in.Evaluator
	if !in.DecisionEnabled {
		return errored(decisionDisabledMessage())
	}
	cfg, err := parseDecisionRunConfig(ev.DecisionConfig)
	if err != nil || cfg == nil {
		logger.Log(ctx, warnLevel(in.Decision, "settings:"+ev.ID),
			"eval: decision evaluator has no readable settings", "evaluatorID", ev.ID, "error", err)
		return errored("the decision evaluator's settings could not be read")
	}
	if cfg.ResolvedModel == nil {
		return errored(fmt.Sprintf("decision model %q was not configured when the run was triggered", derefString(ev.Model)))
	}
	if in.Decision == nil {
		return errored("no decision client configured")
	}
	return cfg, nil
}

type decisionCase struct {
	ctx          context.Context
	logger       *slog.Logger
	in           ScoreInputs
	llmClient    LLMJudgeClient
	cfg          *decisionRunConfig
	choiceScores map[string]any
	details      ScoreDetails
	start        time.Time
}

func (c *decisionCase) score() ScoreOutput {
	q := c.cfg.Question.toDecision()
	client, err := c.in.Decision.DecisionClient(c.ctx, *c.cfg.ResolvedModel)
	if err != nil {
		return c.fail(err)
	}
	if recorder, ok := c.in.Decision.(spendRecorder); ok {
		model := c.cfg.ResolvedModel
		client = recorder.recorded(client, withModel(c.in.Spend, model.Ref, model.ConfigID))
	}
	res, err := client.Evaluate(c.ctx, decision.Request{
		Model:     c.cfg.ResolvedModel.Model,
		State:     decisionState(c.in, c.cfg.Reads),
		Questions: []decision.Question{q},
		Logger:    c.logger,
	})
	if err != nil {
		return c.fail(err)
	}
	c.details.Model = decision.BoundedString(res.Model)
	c.details.Usage = &JudgeUsageDetails{Decision: &JudgeTokenUsage{InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens}}
	if err = res.Validate([]decision.Question{q}); err != nil {
		return c.fail(err)
	}
	verdict, err := mapDecisionAnswer(q, c.cfg.Scoring, c.choiceScores, res.Answers[decisionQuestionKey])
	if err != nil {
		return c.fail(&decision.Error{Kind: decision.FailureInvalidResponse, Err: err})
	}
	c.details.Answer = &verdict.answer
	c.logger.Debug("eval: decision evaluator answered", "evaluator", c.in.Evaluator.Name,
		"confidence", optionalFloat(verdict.confidence), "duration_ms", time.Since(c.start).Milliseconds())

	if c.cfg.MinConfidence != nil && c.escalates() {
		if verdict.confidence == nil {
			return c.escalate(escalationNoConfidence)
		}
		if *verdict.confidence < *c.cfg.MinConfidence {
			return c.escalate(escalationLowConfidence)
		}
	}
	label := verdict.label
	reasoning := fmt.Sprintf("%s · %s (%s)", label, confidenceText(verdict.confidence), cmp.Or(c.details.Model, c.cfg.ResolvedModel.Model))
	if c.cfg.Scoring == scoringExpected && verdict.answer.ScoringApplied == scoringChosen {
		reasoning += " · scored as chosen: the answer reported too little of the probabilities to weight"
	}
	return c.finish(ScoreOutput{
		Score:     verdict.score,
		Choice:    &label,
		Passed:    verdict.score >= scoringPassThreshold,
		Reasoning: &reasoning,
	})
}

func (c *decisionCase) escalates() bool {
	return c.cfg.EscalateToEvaluatorID != nil
}

func (c *decisionCase) finish(out ScoreOutput) ScoreOutput {
	c.details.DurationMs = time.Since(c.start).Milliseconds()
	if out.Errored {
		c.details.DecidedBy = ""
	}
	out.Details = &c.details
	return out
}

func confidenceText(confidence *float64) string {
	if confidence == nil {
		return "confidence not reported"
	}
	return fmt.Sprintf("%.0f%% confidence", *confidence*100)
}

func optionalFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func (c *decisionCase) fail(err error) ScoreOutput {
	kind := decision.Classify(err)
	if c.ctx.Err() != nil {
		c.logger.Debug("eval: decision evaluator call abandoned", "evaluator", c.in.Evaluator.Name)
		return c.finish(erroredScore("scoring was cancelled"))
	}
	if refused, ok := budgetRefusedScore(c.logger, c.in.Evaluator.Name, err); ok {
		return c.finish(refused)
	}
	ce, unbuilt := errors.AsType[*decisionClientError](err)
	if unbuilt {
		c.details.ProviderError = string(ce.rejected)
		if ce.storeUnavailable() && c.escalates() {
			return c.escalate(escalationModelLookup)
		}
		reason := "decision model unavailable"
		if ce.shown != "" {
			reason += " (" + truncateRunes(ce.shown, maxShownCause) + ")"
		}
		return c.finish(erroredScore(reason))
	}
	c.details.ProviderError = string(kind)
	if markedEarlier := decision.KeyRejected(err) && c.markedUnusableEarlier(err); !markedEarlier {
		c.logger.Warn("eval: decision evaluator call failed",
			"evaluator", c.in.Evaluator.Name, "category", kind, "duration_ms", time.Since(c.start).Milliseconds())
	}
	if !kind.Permanent() && c.escalates() {
		return c.escalate(escalationProviderError)
	}
	return c.finish(erroredScore("decision model failed: " + string(kind)))
}

func (c *decisionCase) markedUnusableEarlier(err error) bool {
	marker, ok := c.in.Decision.(unusableMarker)
	return ok && !marker.markUnusable(*c.cfg.ResolvedModel, err)
}

func (c *decisionCase) escalate(reason string) ScoreOutput {
	c.details.Escalated = true
	c.details.EscalationReason = reason
	c.details.DecidedBy = decidedByLLMJudge
	var out ScoreOutput
	if target := evaluatorFromSnapshot(c.cfg.EscalateTo); target == nil {
		out = erroredScore("the escalation evaluator is missing from the run snapshot")
	} else {
		judgeIn := c.in
		judgeIn.Evaluator = target
		out = llmJudgeScore(c.ctx, c.logger, judgeIn, c.llmClient)
	}
	if out.Details != nil && out.Details.Usage != nil && out.Details.Usage.LLMJudge != nil {
		if c.details.Usage == nil {
			c.details.Usage = &JudgeUsageDetails{}
		}
		c.details.Usage.LLMJudge = out.Details.Usage.LLMJudge
	}
	note := "Escalated (" + strings.ReplaceAll(reason, "_", " ") + ")"
	if out.Reasoning != nil && *out.Reasoning != "" {
		note += ": " + *out.Reasoning
	}
	out.Reasoning = &note
	return c.finish(out)
}

func choiceScoresOf(ev *store.EvalEvaluator) (map[string]any, error) {
	if ev.ChoiceScores == nil || *ev.ChoiceScores == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(*ev.ChoiceScores), &m); err != nil {
		return nil, fmt.Errorf("evaluator %s: decode choice_scores: %w", ev.ID, err)
	}
	return m, nil
}

func (s *RunService) escalationTargets(ctx context.Context, evals []*store.EvalEvaluator) (map[string]*store.EvalEvaluator, error) {
	var ids []string
	for _, e := range evals {
		if e.Type != evaluatorTypeDecision {
			continue
		}
		cfg, err := parseDecisionConfig(e.DecisionConfig)
		if err != nil || cfg == nil || cfg.EscalateToEvaluatorID == nil {
			continue
		}
		if !slices.Contains(ids, *cfg.EscalateToEvaluatorID) {
			ids = append(ids, *cfg.EscalateToEvaluatorID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.evaluators.GetByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("get escalation evaluators: %w", err)
	}
	out := make(map[string]*store.EvalEvaluator, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}
