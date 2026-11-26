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
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const scoringPassThreshold = 0.5

const errNoJudgeModel = "No model is set on this evaluator and no general model is configured"

const (
	reasonNoExpectedResponse        = "No expected response provided for heuristic evaluation"
	reasonHeuristicFileOnlyResponse = "Heuristic evaluators grade the agent's text response; this trial produced only file artifacts"
	evaluationFailedPrefix          = "Evaluation failed"
)

func failedScore(detail string) ScoreOutput {
	reason := evaluationFailedPrefix + ": " + detail
	return ScoreOutput{Reasoning: &reason}
}

func erroredScore(detail string) ScoreOutput {
	out := failedScore(detail)
	out.Errored = true
	return out
}

func budgetRefusedScore(logger *slog.Logger, evaluator string, err error) (ScoreOutput, bool) {
	exhausted, ok := errors.AsType[*budget.ExhaustedError](err)
	if !ok {
		return ScoreOutput{}, false
	}
	logger.Debug("eval: scoring call refused: a budget is spent", "evaluator", evaluator, "budgetTier", exhausted.Tier)
	return erroredScore("Not scored: " + spentBudgetText(exhausted, evalJudgeBudgetText)), true
}

const llmJudgeSystemPrompt = llmJudgeSystemPromptCore + llmJudgeExampleFileClauses + llmJudgeTranscriptClauses

const llmJudgeWorkflowSystemPrompt = llmJudgeSystemPromptCore + llmJudgeExampleFileClauses + llmJudgeWorkflowTrajectoryClauses

const llmJudgeSystemPromptCore = "You are an automated evaluator. " +
	"Apply the evaluation criterion provided in the user message. " +
	"Think step-by-step, then return ONLY a JSON object " +
	`with two fields: "choice" (the letter of the option you selected) and "reasoning" (a single sentence). ` +
	"Do not include any other text. " +
	"When the agent produces files, text artifacts (markdown, JSON, CSV, source code, …) are " +
	"inlined after the response between «--- Artifact: …» delimiters; evaluate their content " +
	"directly. Binary artifacts (images, PDFs, Office docs) appear as filenames only — treat " +
	"those as references you cannot read, do not guess at their contents, and let any missing " +
	"information be reflected naturally in your assessment. "

const llmJudgeExampleFileClauses = "Files the agent was GIVEN are listed under \"" + inputArtifactsHeader + "\"; files the agent " +
	"PRODUCED are listed under \"" + producedArtifactsHeader + "\". All sections use the same " +
	"delimiters, so attribute every block to the heading above it: never credit the agent for " +
	"content that arrived with the request, and never fault it for content the input file " +
	"already contained. " +
	"A reference file may be listed under \"" + expectedArtifactsHeader + "\" — it shows what a " +
	"correct answer looks like and was never shown to the agent, so an agent whose output " +
	"resembles it closely has done well, not copied. Compare the agent-produced artifacts " +
	"against it by CONTENT, not by filename: agents choose their own filenames, so a matching " +
	"file under a different name is not an error. When a reference file is present but the " +
	"agent produced no artifact answering it, that is a failure to produce the expected output, " +
	"not missing information. "

const llmJudgeTranscriptClauses = "An execution transcript may also be included between «--- Agent transcript: …» delimiters. " +
	"It interleaves, in execution order, the agent's assistant text ([assistant] lines), each " +
	"tool it called with the parameter values it passed ([tool call] name(args) lines), and that " +
	"tool's result ([tool result] name → value lines, capped/truncated for very large outputs). " +
	"Use it to verify which tools the agent actually invoked and whether it honored their " +
	"results — do NOT treat tools merely mentioned in prose as having been called, and judge a " +
	"required tool as used only when a matching [tool call] line is present. " +
	"When the agent delegates work to another agent, each transcript line is attributed to the " +
	"agent that produced it, written as [<agent> → tool call] / [<agent> → tool result]. A " +
	"[<agent> → delegate] peer \"<peer>\" — task: … line means <agent> handed that task to the " +
	"peer <peer>; every [<peer> → tool call] / [<peer> → tool result] line after it was made by " +
	"the peer, NOT by the delegating agent. Attribute each tool call strictly to the agent named " +
	"on its line: that the parent delegated to a peer and the peer then used tool X must NOT be " +
	"read as the parent using tool X itself."

const llmJudgeWorkflowTrajectoryClauses = "The response under evaluation is a WORKFLOW's final output, and an execution " +
	"trajectory may be included between «--- Agent transcript: …» delimiters. It lists, in execution order, " +
	"each node the workflow ran: a [node <id> · <type> <component>] started line, the tool calls made inside " +
	"the node ([node <id> → tool call] name(args) and [node <id> → tool result] name → value, or " +
	"[node <id> · <agent> → tool call] when the node's agent made the call, or [node <id> · <agent>'s peer → tool call] " +
	"when a peer that agent delegated to made it), and a " +
	"[node <id>] line with its outcome — success, error with the error, skipped with the reason (a branch not " +
	"taken is skipped, not failed), or paused for human input — followed by the branch it selected, its answers " +
	"or why a loop stopped, and the input it received and the output it returned (capped). [workflow] lines mark " +
	"the start and the outcome. A node that runs a sub-workflow shows that workflow's start and outcome as " +
	"[node <id>] sub-workflow lines, and its nodes as [node <id> ▸ <inner id> …]. A tool call that cannot be " +
	"tied to a node is labelled [unattributed → tool call], and a forwarded event whose node is unknown is shown " +
	"under (unattributed). " +
	"Use it to check which nodes ran, which branches were taken, what each node received and returned, and " +
	"which tools were called: treat a node or tool as having run only when its line is present. The work a map " +
	"or loop node does for each item is not listed: judge it from that node's output, not from missing lines. The " +
	"trajectory can be incomplete: a «[transcript truncated …]» notice at its top, a [trajectory incomplete …] " +
	"line, or no closing [workflow] outcome line means events are missing, and then a missing line is not " +
	"evidence that a node did not run. Grade the final output against the request; use the trajectory to judge " +
	"how it was reached when the criteria ask about the process."

const defaultLLMJudgeTemplate = "You are evaluating an AI assistant's response for quality and correctness.\n\n" +
	"User input: {{Prompt}}\n" +
	"Agent response: {{Response}}\n" +
	"Reference answer (if provided): {{Expected Response}}\n\n" +
	"Assess the response considering:\n" +
	"- Accuracy: Is the information factually correct?\n" +
	"- Completeness: Does it fully address the user's question?\n" +
	"- Adherence to reference: If a reference answer was provided, how well does the response align with it?\n\n" +
	"An excellent response is accurate, complete, and closely aligned with any reference answer provided. " +
	"A good response addresses most criteria with only minor gaps or inaccuracies. " +
	"An acceptable response partially addresses the question but has noticeable gaps or inaccuracies. " +
	"A poor response has significant inaccuracies or omissions, or only tangentially addresses the question. " +
	"A failing response is incorrect, irrelevant, or unhelpful."

var (
	promptPlaceholderRE         = regexp.MustCompile(`(?i)\{\{\s*Prompt\s*\}\}`)
	responsePlaceholderScorerRE = regexp.MustCompile(`(?i)\{\{\s*Response\s*\}\}`)
	expectedPlaceholderRE       = regexp.MustCompile(`(?i)\{\{\s*Expected\s+Response\s*\}\}`)
	nodePlaceholderRE           = regexp.MustCompile(`(?i)\{\{\s*Node\s+(Output|Input)\s*:\s*([^{}\r\n]*?)\s*\}\}`)
	judgePlaceholderRE          = regexp.MustCompile(strings.Join([]string{
		promptPlaceholderRE.String(), responsePlaceholderScorerRE.String(), expectedPlaceholderRE.String(), nodePlaceholderRE.String(),
	}, "|"))
	nonAlphaRE = regexp.MustCompile(`[^A-Za-z]`)
)

func CanonicalizeTemplatePlaceholders(template string) string {
	return judgePlaceholderRE.ReplaceAllStringFunc(template, func(m string) string {
		switch {
		case promptPlaceholderRE.MatchString(m):
			return "{{Prompt}}"
		case responsePlaceholderScorerRE.MatchString(m):
			return "{{Response}}"
		case expectedPlaceholderRE.MatchString(m):
			return "{{Expected Response}}"
		}
		side, node := nodePlaceholderParts(m)
		return "{{Node " + side + ": " + node + "}}"
	})
}

func nodePlaceholderParts(placeholder string) (side, node string) {
	parts := nodePlaceholderRE.FindStringSubmatch(placeholder)
	side = "Output"
	if strings.EqualFold(parts[1], "input") {
		side = "Input"
	}
	return side, parts[2]
}

func namesNoNode(template string) bool {
	for _, m := range nodePlaceholderRE.FindAllStringSubmatch(template, -1) {
		if m[2] == "" {
			return true
		}
	}
	return false
}

var nodePlaceholderAttemptRE = regexp.MustCompile(`(?i)\{\{\s*Nodes?[\s_]*(?::|ou\w*|inp\w*|in\w*put)`)

func malformedNodePlaceholderAt(template string) int {
	wellFormed := map[int]bool{}
	for _, loc := range nodePlaceholderRE.FindAllStringIndex(template, -1) {
		wellFormed[loc[0]] = true
	}
	for _, loc := range nodePlaceholderAttemptRE.FindAllStringIndex(template, -1) {
		if !wellFormed[loc[0]] {
			return utf8.RuneCountInString(template[:loc[0]]) + 1
		}
	}
	return 0
}

func readsNodeEvents(ev *store.EvalEvaluator) bool {
	switch {
	case isWorkflowPathEvaluator(ev):
		return true
	case ev.Type == evaluatorTypeDecision:
		target := escalationTarget(ev)
		return target != nil && readsNodeEvents(target)
	}
	return ev.Type == evaluatorTypeLLMJudge && ev.PromptTemplate != nil && nodePlaceholderRE.MatchString(*ev.PromptTemplate)
}

func escalationTarget(ev *store.EvalEvaluator) *store.EvalEvaluator {
	cfg, err := parseDecisionRunConfig(ev.DecisionConfig)
	if err != nil || cfg == nil || cfg.EscalateTo == nil {
		return nil
	}
	return evaluatorFromSnapshot(cfg.EscalateTo)
}

type ScoreOutput struct {
	Score     float64
	Choice    *string
	Passed    bool
	Reasoning *string
	Errored   bool
	Details   *ScoreDetails
}

type usageJudgeClient interface {
	JudgeScoreWithUsage(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, *JudgeTokenUsage, error)
}

func judgeScore(ctx context.Context, c LLMJudgeClient, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, *JudgeTokenUsage, error) {
	if u, ok := c.(usageJudgeClient); ok {
		return u.JudgeScoreWithUsage(ctx, model, systemPrompt, userPrompt, attribution)
	}
	content, err := c.JudgeScore(ctx, model, systemPrompt, userPrompt, attribution)
	return content, nil, err
}

type LLMJudgeClient interface {
	JudgeScore(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, error)
}

type llmAdapter struct {
	client llm.Client
	policy *tempPolicy
	logger *slog.Logger
}

func NewLLMJudgeAdapter(client llm.Client) LLMJudgeClient {
	return &llmAdapter{client: client}
}

func (a *llmAdapter) withPolicy(policy *tempPolicy, logger *slog.Logger) *llmAdapter {
	return &llmAdapter{client: a.client, policy: policy, logger: logger}
}

func (a *llmAdapter) JudgeScore(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, error) {
	content, _, err := a.JudgeScoreWithUsage(ctx, model, systemPrompt, userPrompt, attribution)
	return content, err
}

func (a *llmAdapter) JudgeScoreWithUsage(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, *JudgeTokenUsage, error) {
	if a.client == nil {
		return "", nil, errors.New("llm client not configured")
	}
	model = bareModelForGateway(model)
	return judgeChat(ctx, a.client, a.policy, model, judgePrompt{system: systemPrompt, user: userPrompt}, attribution, a.logger)
}

func bareModelForGateway(model string) string {
	if model == "" {
		return model
	}
	_, bare := samapp.ParseModelProvider(model)
	return bare
}

type tempPolicy = llm.TemperaturePolicy

func newTempPolicy() *tempPolicy {
	return &tempPolicy{}
}

type judgePrompt struct {
	system, user string
}

func judgeChat(ctx context.Context, client llm.Client, policy *tempPolicy, model string, prompt judgePrompt, attribution *llm.SpendAttribution, logger *slog.Logger) (string, *JudgeTokenUsage, error) {
	req := &llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: prompt.system},
			{Role: "user", Content: prompt.user},
		},
		Temperature: new(float64),
		Observability: llm.ObservabilityContext{
			ComponentName: "platform-eval-llm-judge",
		},
		Spend: attribution,
	}
	resp, err := llm.ChatCompletionWithTemperatureFallback(ctx, client, req, policy, model, logger)
	if err != nil {
		return "", nil, fmt.Errorf("llm judge: chat completion: %w", err)
	}
	var usage *JudgeTokenUsage
	if resp.Usage != nil {
		usage = &JudgeTokenUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}
	}
	return resp.Content, usage, nil
}

type boundedJudgeClient struct {
	inner LLMJudgeClient
	sem   chan struct{}
}

func newBoundedJudgeClient(inner LLMJudgeClient, n int) LLMJudgeClient {
	return &boundedJudgeClient{inner: inner, sem: make(chan struct{}, n)}
}

func newBoundedJudgeClientWithSem(inner LLMJudgeClient, sem chan struct{}) LLMJudgeClient {
	return &boundedJudgeClient{inner: inner, sem: sem}
}

func (b *boundedJudgeClient) JudgeScore(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, error) {
	content, _, err := b.JudgeScoreWithUsage(ctx, model, systemPrompt, userPrompt, attribution)
	return content, err
}

func (b *boundedJudgeClient) JudgeScoreWithUsage(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, *JudgeTokenUsage, error) {
	select {
	case b.sem <- struct{}{}:
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
	defer func() { <-b.sem }()
	return judgeScore(ctx, b.inner, model, systemPrompt, userPrompt, attribution)
}

type JudgeModelResolver interface {
	Resolve(ctx context.Context, modelAliasOrID string) (raw map[string]any, configID string, dedicated bool, err error)
}

type snapshotJudgeResolver struct {
	inner    JudgeModelResolver
	fallback map[string]string

	mu    sync.Mutex
	cache map[string]judgeResolution
}

type judgeResolution struct {
	raw       map[string]any
	configID  string
	dedicated bool
}

func newSnapshotJudgeResolver(inner JudgeModelResolver, fallback map[string]string) *snapshotJudgeResolver {
	return &snapshotJudgeResolver{
		inner:    inner,
		fallback: fallback,
		cache:    make(map[string]judgeResolution),
	}
}

func (s *snapshotJudgeResolver) Resolve(ctx context.Context, ref string) (map[string]any, string, bool, error) {
	s.mu.Lock()
	if r, ok := s.cache[ref]; ok {
		s.mu.Unlock()
		return r.raw, r.configID, r.dedicated, nil
	}
	s.mu.Unlock()

	var res judgeResolution
	if s.inner != nil {
		raw, configID, dedicated, err := s.inner.Resolve(ctx, ref)
		if err != nil {
			return nil, "", false, err
		}
		res = judgeResolution{raw: raw, configID: configID, dedicated: dedicated}
	}
	if res.raw == nil {
		if name := s.fallback[ref]; name != "" && name != ref {
			res = judgeResolution{raw: map[string]any{"model": name}}
		}
	}

	s.mu.Lock()
	s.cache[ref] = res
	s.mu.Unlock()
	return res.raw, res.configID, res.dedicated, nil
}

type runJudgeClient struct {
	bound       LLMJudgeClient
	resolver    JudgeModelResolver
	logger      *slog.Logger
	concurrency int
	grants      llm.GrantTokens

	policy *tempPolicy

	spend *spend.Publisher
	gate  *budget.Gate

	mu     sync.Mutex
	owned  map[string]ownedJudge
	failed map[string]error
}

type ownedJudge struct {
	client llm.Client
	model  string
}

var errJudgeProvider = errors.New("llm judge provider unavailable")

func newRunJudgeClient(bound LLMJudgeClient, resolver JudgeModelResolver, logger *slog.Logger, concurrency int) *runJudgeClient {
	if logger == nil {
		logger = slog.Default()
	}
	policy := newTempPolicy()
	if a, ok := bound.(*llmAdapter); ok {
		bound = a.withPolicy(policy, logger)
	}
	return &runJudgeClient{
		bound:       bound,
		resolver:    resolver,
		logger:      logger,
		concurrency: concurrency,
		policy:      policy,
		owned:       make(map[string]ownedJudge),
		failed:      make(map[string]error),
	}
}

func (r *runJudgeClient) meterOn(pub *spend.Publisher, gate *budget.Gate) {
	r.spend, r.gate = pub, gate
	if (pub == nil && gate == nil) || r.bound == nil {
		return
	}
	if a, ok := r.bound.(*llmAdapter); ok && a.client != nil {
		a.client = spend.NewMeteredClient(a.client, "", pub, gate)
		return
	}
	r.logger.Warn("eval: judge spend not recorded on the platform's default model; its client cannot be wrapped",
		"client", fmt.Sprintf("%T", r.bound))
}

func (r *runJudgeClient) JudgeScore(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, error) {
	content, _, err := r.JudgeScoreWithUsage(ctx, model, systemPrompt, userPrompt, attribution)
	return content, err
}

func (r *runJudgeClient) JudgeScoreWithUsage(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, *JudgeTokenUsage, error) {
	if r.resolver == nil || model == "" {
		return r.boundJudge(ctx, model, systemPrompt, userPrompt, r.unresolvedSpend(attribution, model))
	}
	raw, configID, dedicated, err := r.resolver.Resolve(ctx, model)
	if err != nil {
		return "", nil, fmt.Errorf("%w: resolve model config %q: %w", errJudgeProvider, model, err)
	}
	if raw == nil {
		return r.boundJudge(ctx, model, systemPrompt, userPrompt, r.unresolvedSpend(attribution, model))
	}
	if !dedicated {
		judgeModel := model
		if m, ok := raw["model"].(string); ok && m != "" {
			judgeModel = m
		}
		return r.boundJudge(ctx, judgeModel, systemPrompt, userPrompt, withModel(attribution, model, configID))
	}
	client, ownedModel, err := r.ownedClient(ctx, configID, raw)
	if err != nil {
		return "", nil, fmt.Errorf("%w: model config %q: %w", errJudgeProvider, model, err)
	}
	client = spend.NewMeteredClient(client, ownedModel, r.spend, r.gate)
	return judgeChat(ctx, client, r.policy, ownedModel, judgePrompt{system: systemPrompt, user: userPrompt}, withModel(attribution, model, configID), r.logger)
}

func (r *runJudgeClient) unresolvedSpend(attribution *llm.SpendAttribution, model string) *llm.SpendAttribution {
	if attribution != nil && attribution.ModelConfigID == "" && attribution.ModelKey == "" && r.spend != nil {
		r.logger.Debug("eval: judge call recorded unpriced; its model matches no model configuration", "model", model)
	}
	return attribution
}

func withModel(attribution *llm.SpendAttribution, modelKey, modelConfigID string) *llm.SpendAttribution {
	if attribution == nil {
		return nil
	}
	keyed := *attribution
	keyed.ModelKey, keyed.ModelConfigID = modelKey, modelConfigID
	return &keyed
}

func (r *runJudgeClient) boundJudge(ctx context.Context, model, systemPrompt, userPrompt string, attribution *llm.SpendAttribution) (string, *JudgeTokenUsage, error) {
	if r.bound == nil {
		return "", nil, errors.New("llm client not configured")
	}
	return judgeScore(ctx, r.bound, model, systemPrompt, userPrompt, attribution)
}

const judgeClientBuildTimeout = 30 * time.Second

func (r *runJudgeClient) ownedClient(ctx context.Context, configID string, raw map[string]any) (llm.Client, string, error) {
	r.mu.Lock()
	if oc, ok := r.owned[configID]; ok {
		r.mu.Unlock()
		return oc.client, oc.model, nil
	}
	if err, ok := r.failed[configID]; ok {
		r.mu.Unlock()
		return nil, "", err
	}
	r.mu.Unlock()

	cfg, err := samapp.ParseModelConfig(raw, r.logger)
	if err != nil {
		err = fmt.Errorf("parse model config: %w", err)
		r.cacheBuildFailure(configID, err)
		return nil, "", err
	}
	cfg.Logger = r.logger
	cfg.Grants = r.grants
	if r.concurrency > 0 {
		cfg.Concurrency = r.concurrency
	}

	buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), judgeClientBuildTimeout)
	defer cancel()
	client, err := llm.NewBifrostClient(buildCtx, *cfg)
	if err != nil {
		releaseAPIKeyEnv(cfg.APIKeyEnvVar)
		r.cacheBuildFailure(configID, err)
		return nil, "", err
	}
	instrumented := observability.NewInstrumentedLLMClient(client, cfg.Model)

	r.mu.Lock()
	defer r.mu.Unlock()
	if oc, ok := r.owned[configID]; ok {
		instrumented.Shutdown()
		return oc.client, oc.model, nil
	}
	r.owned[configID] = ownedJudge{client: instrumented, model: cfg.Model}
	return instrumented, cfg.Model, nil
}

func (r *runJudgeClient) cacheBuildFailure(configID string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.owned[configID]; ok {
		return
	}
	r.failed[configID] = err
}

func (r *runJudgeClient) Close() {
	r.mu.Lock()
	owned := r.owned
	r.owned = make(map[string]ownedJudge)
	r.failed = make(map[string]error)
	r.mu.Unlock()
	for _, oc := range owned {
		if sd, ok := oc.client.(interface{ Shutdown() }); ok {
			sd.Shutdown()
		}
	}
}

func releaseAPIKeyEnv(name string) {
	if strings.HasPrefix(name, "SAM_AWE_LLM_API_KEY_") {
		_ = os.Unsetenv(name)
	}
}

type ScoreInputs struct {
	Evaluator               *store.EvalEvaluator
	Prompt                  string
	Response                string
	Expected                *string
	Artifacts               []ArtifactBlock
	InputArtifacts          []ArtifactBlock
	ExpectedArtifacts       []ArtifactBlock
	Transcript              string
	ExpectedPath            *ExpectedPath
	ExpectedPathUnreadable  bool
	ExpectedFacts           *ExpectedFacts
	ExpectedFactsUnreadable bool
	ObservedPath            observedPath
	WorkflowTrajectory      bool
	DefaultModel            string
	Decision                decisionClientSource
	DecisionEnabled         bool
	Spend                   *llm.SpendAttribution
	DefaultModelKey         string
	DefaultModelID          string
}

func ScoreResult(ctx context.Context, logger *slog.Logger, in ScoreInputs, llmClient LLMJudgeClient) ScoreOutput {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if isWorkflowPathEvaluator(in.Evaluator) {
		return workflowPathScore(in)
	}
	if !hasAgentReply(in.Response, in.Artifacts) {
		reason := "No agent response provided"
		return ScoreOutput{Reasoning: &reason}
	}

	switch in.Evaluator.Type {
	case evaluatorTypeHeuristic:
		if isFileOnlyReply(in) {
			reason := reasonHeuristicFileOnlyResponse
			return ScoreOutput{Reasoning: &reason}
		}
		if isOutputFactsEvaluator(in.Evaluator) {
			return outputFactsScore(in)
		}
		return heuristicScore(logger, in.Evaluator, in.Response, in.Expected)
	case evaluatorTypeDecision:
		return decisionScore(ctx, logger, in, llmClient)
	}
	return llmJudgeScore(ctx, logger, in, llmClient)
}

func hasAgentReply(response string, produced []ArtifactBlock) bool {
	return response != "" || len(produced) > 0
}

func isFileOnlyReply(in ScoreInputs) bool {
	return in.Response == "" && len(in.Artifacts) > 0
}

func heuristicScore(logger *slog.Logger, ev *store.EvalEvaluator, response string, expected *string) ScoreOutput {
	htype := "rouge"
	if ev.HeuristicType != nil {
		htype = *ev.HeuristicType
	}
	switch htype {
	case "valid_json":
		raw := validJSONScore(response)
		return ScoreOutput{Score: raw, Passed: raw >= scoringPassThreshold}
	case "json_diff":
		if expected == nil {
			reason := reasonNoExpectedResponse
			return ScoreOutput{Reasoning: &reason}
		}
		raw := clamp01(jsonDiffScore(*expected, response))
		return ScoreOutput{Score: raw, Passed: raw >= scoringPassThreshold}
	case "levenshtein":
		if expected == nil {
			reason := reasonNoExpectedResponse
			return ScoreOutput{Reasoning: &reason}
		}
		raw := clamp01(levenshteinScore(*expected, response))
		return ScoreOutput{Score: raw, Passed: raw >= scoringPassThreshold}
	case "rouge":
		if expected == nil {
			reason := reasonNoExpectedResponse
			return ScoreOutput{Reasoning: &reason}
		}
		raw := clamp01(wordOverlapF1(*expected, response))
		return ScoreOutput{Score: raw, Passed: raw >= scoringPassThreshold}
	default:
		logger.Warn("eval: unknown heuristic_type, falling back to word overlap", "heuristicType", htype, "evaluator", ev.Name)
		if expected == nil {
			reason := reasonNoExpectedResponse
			return ScoreOutput{Reasoning: &reason}
		}
		raw := clamp01(wordOverlapF1(*expected, response))
		return ScoreOutput{Score: raw, Passed: raw >= scoringPassThreshold}
	}
}

const (
	jsonFenceOpen  = "```json"
	jsonFenceClose = "```"
)

func unwrapJSONFence(text string) string {
	stripped := strings.TrimSpace(text)
	if strings.HasPrefix(stripped, jsonFenceOpen) && strings.HasSuffix(stripped, jsonFenceClose) &&
		len(stripped) >= len(jsonFenceOpen)+len(jsonFenceClose) {
		inner := stripped[len(jsonFenceOpen) : len(stripped)-len(jsonFenceClose)]
		return strings.TrimSpace(inner)
	}
	return text
}

func validJSONScore(response string) float64 {
	var v any
	if err := json.Unmarshal([]byte(unwrapJSONFence(response)), &v); err != nil {
		return 0.0
	}
	return 1.0
}

func jsonDiffScore(expected, actual string) float64 {
	var ev, av any
	if err := json.Unmarshal([]byte(unwrapJSONFence(expected)), &ev); err != nil {
		return 0.0
	}
	if err := json.Unmarshal([]byte(unwrapJSONFence(actual)), &av); err != nil {
		return 0.0
	}
	matched, total := compareJSON(ev, av)
	if total == 0 {
		return 1.0
	}
	return matched / float64(total)
}

func compareJSON(a, b any) (float64, int) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := make(map[string]bool, len(am)+len(bm))
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		if len(keys) == 0 {
			return 1.0, 1
		}
		var matched float64
		var total int
		for k := range keys {
			av, aOK := am[k]
			bv, bOK := bm[k]
			if aOK && bOK {
				m, t := compareJSON(av, bv)
				matched += m
				total += t
			} else {
				total++
			}
		}
		return matched, total
	}
	al, aok := a.([]any)
	bl, bok := b.([]any)
	if aok && bok {
		length := max(len(al), len(bl))
		if length == 0 {
			return 1.0, 1
		}
		var matched float64
		var total int
		for i := range length {
			if i < len(al) && i < len(bl) {
				m, t := compareJSON(al[i], bl[i])
				matched += m
				total += t
			} else {
				total++
			}
		}
		return matched, total
	}
	if reflect.DeepEqual(a, b) {
		return 1.0, 1
	}
	return 0.0, 1
}

func levenshteinScore(expected, actual string) float64 {
	if expected == "" && actual == "" {
		return 1.0
	}
	if expected == "" || actual == "" {
		return 0.0
	}
	a := []rune(expected)
	b := []rune(actual)
	dist := levenshteinDistance(a, b)
	maxLen := max(len(a), len(b))
	return 1.0 - float64(dist)/float64(maxLen)
}

func levenshteinDistance(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				curr[j-1]+1,
				prev[j]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func wordOverlapF1(expected, actual string) float64 {
	exp := tokenizeWords(expected)
	act := tokenizeWords(actual)
	if len(exp) == 0 && len(act) == 0 {
		return 1.0
	}
	if len(exp) == 0 || len(act) == 0 {
		return 0.0
	}
	expCounts := wordCount(exp)
	actCounts := wordCount(act)
	overlap := 0
	for w, n := range actCounts {
		if e := expCounts[w]; e > 0 {
			if n < e {
				overlap += n
			} else {
				overlap += e
			}
		}
	}
	if overlap == 0 {
		return 0.0
	}
	precision := float64(overlap) / float64(len(act))
	recall := float64(overlap) / float64(len(exp))
	return 2 * precision * recall / (precision + recall)
}

func tokenizeWords(s string) []string {
	s = strings.ToLower(s)
	out := []string{}
	cur := strings.Builder{}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func wordCount(words []string) map[string]int {
	out := make(map[string]int, len(words))
	for _, w := range words {
		out[w]++
	}
	return out
}

func llmJudgeScore(ctx context.Context, logger *slog.Logger, in ScoreInputs, llmClient LLMJudgeClient) ScoreOutput {
	ev := in.Evaluator
	if llmClient == nil {
		logger.Warn("eval: llm judge skipped — no llm client configured", "evaluator", ev.Name, "evaluatorType", ev.Type)
		return erroredScore("no LLM client configured")
	}

	rendered := renderJudgeTemplate(in)

	inputBlocks, expectedBlocks, producedBlocks := fitArtifactsToJudgeBudget(in.InputArtifacts, in.ExpectedArtifacts, in.Artifacts)
	if n := countBudgetDemotedBlocks(inputBlocks, expectedBlocks, producedBlocks); n > 0 {
		logger.Debug("eval: judge artifact budget demoted blocks to placeholders",
			"evaluator", ev.Name, "demotedBlocks", n, "producedBlocks", len(producedBlocks))
	}

	var sb strings.Builder
	sb.WriteString(rendered)
	appendArtifactSection(&sb, inputArtifactsHeader, inputBlocks)
	appendArtifactSection(&sb, expectedArtifactsHeader, expectedBlocks)
	if block := renderTranscriptBlock(in.Transcript); block != "" {
		sb.WriteString(transcriptHeading(in.WorkflowTrajectory))
		sb.WriteString(block)
	}
	appendArtifactSection(&sb, producedArtifactsHeader, producedBlocks)

	choices, letterToLabel, err := buildLetteredChoices(ev.ChoiceScores)
	if err != nil {
		logger.Warn("eval: llm judge skipped — invalid choice_scores", "evaluator", ev.Name, "error", err)
		return erroredScore(err.Error())
	}
	if len(letterToLabel) > 0 {
		sb.WriteString("\n\nChoose the option that best applies and set the 'choice' field to the corresponding letter:\n")
		sb.WriteString(choices)
	}
	user := sb.String()

	model, attribution := judgeModel(in)
	if model == "" {
		return erroredScore(errNoJudgeModel)
	}

	content, usage, err := judgeScore(ctx, llmClient, model, judgeSystemPrompt(in.WorkflowTrajectory), user, attribution)
	if err != nil {
		return judgeCallFailed(logger, ev.Name, user, err)
	}
	out := parseJudgeReply(logger, ev, content, letterToLabel)
	out.Details = &ScoreDetails{DecidedBy: decidedByLLMJudge}
	if usage != nil {
		out.Details.Usage = &JudgeUsageDetails{LLMJudge: usage}
	}
	return out
}

func parseJudgeReply(logger *slog.Logger, ev *store.EvalEvaluator, content string, letterToLabel map[string]string) ScoreOutput {
	cleaned := stripCodeFence(strings.TrimSpace(content))
	if cleaned == "" {
		return failedScore("LLM returned an empty response")
	}
	cleaned = extractJSONObject(cleaned)

	var parsed struct {
		Score     *float64 `json:"score"`
		Choice    *string  `json:"choice"`
		Reasoning *string  `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		logger.Warn("eval: llm judge response not JSON", "evaluator", ev.Name, "error", err,
			"responseLen", len(content))
		return failedScore(err.Error())
	}

	rawScore := 0.0
	if parsed.Score != nil {
		rawScore = *parsed.Score
	}
	choice := parsed.Choice
	if choice != nil && *choice == "" {
		choice = nil
	}
	reasoning := parsed.Reasoning
	if reasoning != nil && *reasoning == "" {
		reasoning = nil
	}

	if len(letterToLabel) > 0 {
		if choice == nil {
			return failedScore("LLM judge did not return a 'choice' field")
		}
		label, score, err := mapChoiceToScore(*choice, letterToLabel, ev.ChoiceScores)
		if err != nil {
			logger.Warn("eval: llm judge unmappable choice", "evaluator", ev.Name, "choiceLen", len(*choice))
			return failedScore(err.Error())
		}
		rawScore = score
		choice = &label
	}

	score := clamp01(rawScore)
	return ScoreOutput{
		Score:     score,
		Choice:    choice,
		Passed:    score >= scoringPassThreshold,
		Reasoning: reasoning,
	}
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return s
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s
}

func buildLetteredChoices(choiceScoresJSON *string) (string, map[string]string, error) {
	if choiceScoresJSON == nil || strings.TrimSpace(*choiceScoresJSON) == "" {
		return "", nil, nil
	}
	labels, err := orderedJSONKeys(*choiceScoresJSON)
	if err != nil {
		return "", nil, fmt.Errorf("parse choice_scores: %w", err)
	}
	if len(labels) == 0 {
		return "", nil, nil
	}
	if len(labels) > 26 {
		labels = labels[:26]
	}
	letters := make(map[string]string, len(labels))
	lines := make([]string, 0, len(labels))
	for i, label := range labels {
		letter := string(rune('A' + i))
		letters[letter] = label
		lines = append(lines, fmt.Sprintf("(%s) %s", letter, label))
	}
	return strings.Join(lines, "\n"), letters, nil
}

func mapChoiceToScore(choice string, letterToLabel map[string]string, choiceScoresJSON *string) (string, float64, error) {
	if choiceScoresJSON == nil {
		return "", 0, errors.New("evaluator missing choice_scores")
	}
	scores := map[string]float64{}
	if err := json.Unmarshal([]byte(*choiceScoresJSON), &scores); err != nil {
		return "", 0, fmt.Errorf("parse choice_scores: %w", err)
	}

	letters := nonAlphaRE.ReplaceAllString(choice, "")
	if len(letters) == 1 {
		letter := strings.ToUpper(letters)
		if label, ok := letterToLabel[letter]; ok {
			score, sok := scores[label]
			if !sok {
				return "", 0, fmt.Errorf("choice_scores missing label %q", label)
			}
			return label, score, nil
		}
	}

	normalized := strings.ToLower(strings.TrimSpace(choice))
	for label, score := range scores {
		if strings.ToLower(label) == normalized {
			return label, score, nil
		}
	}

	letterList := make([]string, 0, len(letterToLabel))
	for k := range letterToLabel {
		letterList = append(letterList, k)
	}
	return "", 0, fmt.Errorf("LLM judge returned unmappable choice %q (expected one of letters %v or labels)", choice, letterList)
}

func orderedJSONKeys(s string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("choice_scores is not a JSON object")
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("choice_scores: non-string key")
		}
		keys = append(keys, key)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func renderJudgeTemplate(in ScoreInputs) string {
	template := defaultLLMJudgeTemplate
	if ev := in.Evaluator; ev.PromptTemplate != nil && *ev.PromptTemplate != "" {
		template = *ev.PromptTemplate
	}
	expectedText := "[None]"
	if in.Expected != nil {
		expectedText = *in.Expected
	}
	effectiveResponse := in.Response
	if isFileOnlyReply(in) {
		effectiveResponse = "[No text in the agent's reply — see the artifact blocks below.]"
	}
	return judgePlaceholderRE.ReplaceAllStringFunc(template, func(m string) string {
		switch {
		case promptPlaceholderRE.MatchString(m):
			return in.Prompt
		case responsePlaceholderScorerRE.MatchString(m):
			return effectiveResponse
		case expectedPlaceholderRE.MatchString(m):
			return expectedText
		}
		return nodeValue(m, in)
	})
}

func nodeValue(placeholder string, in ScoreInputs) string {
	if !in.WorkflowTrajectory {
		return placeholder
	}
	side, node := nodePlaceholderParts(placeholder)
	values := in.ObservedPath.outputs
	if side == "Input" {
		values = in.ObservedPath.inputs
	}
	v, ok := values[node]
	switch {
	case !ok:
		return missingNodeValueNote(&in.ObservedPath, node, strings.ToLower(side))
	case in.ObservedPath.lastOutcome(node) == pathNodeFailed:
		return fmt.Sprintf("[Node %q failed in this trial. Its recorded %s:] %s", node, strings.ToLower(side), v)
	}
	return v
}

func missingNodeValueNote(path *observedPath, node, side string) string {
	outcome := path.lastOutcome(node)
	switch {
	case outcome == pathNodeSkipped:
		return fmt.Sprintf("[Node %q was skipped in this trial, so it has no %s]", node, side)
	case outcome == pathNodeFailed:
		return fmt.Sprintf("[Node %q failed in this trial and reported no %s]", node, side)
	case outcome == pathNodeSucceeded:
		return ranWithoutPreviewNote(node, side)
	case path.truncated:
		return fmt.Sprintf("[Node %q: the trial's events stopped being captured before it reported its %s]", node, side)
	case outcome == pathNodeRan:
		return fmt.Sprintf("[Node %q started but reported no result in this trial]", node)
	case !path.sawOutcome:
		return fmt.Sprintf("[Node %q: the workflow's result was not captured, so whether this node ran is not known]", node)
	}
	return fmt.Sprintf("[No node %q reported in this trial: it did not run, is not a node of this workflow, or runs inside a map or loop, which reports only as a whole; a nested sub-workflow's node is named as outer ▸ inner]", node)
}

func ranWithoutPreviewNote(node, side string) string {
	if side == "input" {
		return fmt.Sprintf("[Node %q ran, but recorded no input preview; switch, map and loop nodes never record one]", node)
	}
	return fmt.Sprintf("[Node %q ran, but its result carries no output preview]", node)
}

func judgeModel(in ScoreInputs) (string, *llm.SpendAttribution) {
	if model := derefString(in.Evaluator.Model); model != "" {
		return model, in.Spend
	}
	return in.DefaultModel, withModel(in.Spend, in.DefaultModelKey, in.DefaultModelID)
}

func judgeSystemPrompt(workflowTrajectory bool) string {
	if workflowTrajectory {
		return llmJudgeWorkflowSystemPrompt
	}
	return llmJudgeSystemPrompt
}

func judgeCallFailed(logger *slog.Logger, evaluator, user string, err error) ScoreOutput {
	if refused, ok := budgetRefusedScore(logger, evaluator, err); ok {
		return refused
	}
	promptRunes := utf8.RuneCountInString(user)
	if errors.Is(err, errJudgeProvider) {
		logger.Error("eval: llm judge provider unavailable", "evaluator", evaluator, "promptRunes", promptRunes, "error", err)
	} else {
		logger.Warn("eval: llm judge call failed", "evaluator", evaluator, "promptRunes", promptRunes, "error", err)
	}
	return erroredScore(err.Error())
}

func transcriptHeading(workflow bool) string {
	if workflow {
		return "\n\nWorkflow execution trajectory (each node in execution order with its outcome, " +
			"input and output, and the tool calls made inside it):\n\n"
	}
	return "\n\nAgent execution transcript (assistant text, tool calls with " +
		"parameter values, and capped tool results, in execution order):\n\n"
}
