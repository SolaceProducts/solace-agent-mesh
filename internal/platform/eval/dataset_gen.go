// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const DefaultDatasetGenAgent = "Orchestrator"

const DefaultDatasetGenTimeout = 180 * time.Second

const nameWithSuffixFmt = "%s (%s)"

const errExamplesNotArray = "The AI returned JSON but 'examples' was not an array."

const CodeExampleCapReached = "DATASET_EXAMPLE_CAP_REACHED"

const (
	CodeDatasetGenInvalidResponse = "DATASET_GEN_INVALID_RESPONSE"
	CodeExampleGenInvalidResponse = "EXAMPLE_GEN_INVALID_RESPONSE"
)

type gatewayClient interface {
	SubmitTask(ctx context.Context, gatewayURL, agentName, prompt, authHeader string) (string, error)
	PollTask(ctx context.Context, gatewayURL, taskID, authHeader string, waitTime time.Duration) (map[string]any, error)
}

type genAgentRepoSource interface {
	GetByName(ctx context.Context, name string) (*store.Agent, []string, []string, []string, []string, error)
}

type AIGenConfig struct {
	Client gatewayClient

	AgentRepo genAgentRepoSource

	AgentRegistry agentRegistrySource

	Orchestrator string

	Timeout time.Duration
}

func (s *DatasetService) WithAIGen(cfg AIGenConfig) *DatasetService {
	if cfg.Client != nil {
		if cfg.Orchestrator == "" {
			cfg.Orchestrator = DefaultDatasetGenAgent
		}
		if cfg.Timeout <= 0 {
			cfg.Timeout = DefaultDatasetGenTimeout
		}
		s.aiGen = &cfg
	}
	return s
}

func (s *DatasetService) IsAIGenEnabled() bool {
	return s.aiGen != nil
}

type FeatureDisabledError struct{ Message string }

func (e *FeatureDisabledError) Error() string { return e.Message }

func (s *DatasetService) GenerateDataset(
	ctx context.Context,
	req GenerateDatasetRequest,
	userID, gatewayURL, authHeader string,
) (*DatasetResponse, error) {
	datasetID, err := s.generateDataset(ctx, "", req, userID, gatewayURL, authHeader)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, datasetID)
}

func (s *DatasetService) generateDataset(
	ctx context.Context,
	operationID string,
	req GenerateDatasetRequest,
	userID, gatewayURL, authHeader string,
) (string, error) {
	if s.aiGen == nil {
		return "", &FeatureDisabledError{Message: "AI dataset generation is not configured. Configure a broker to enable it."}
	}
	if err := validateGenerateDatasetRequest(req); err != nil {
		return "", err
	}

	agentCtx := s.buildAgentContext(ctx, req.TargetAgent)
	prompt := buildCreatePrompt(req.TargetAgent, agentCtx, req.NumExamples, ptrToStr(req.Focus))

	focusForLog := ""
	if req.Focus != nil {
		focusForLog = *req.Focus
	}
	s.logger.Info("eval: generating dataset",
		"numExamples", req.NumExamples,
		"targetAgent", req.TargetAgent,
		"focus", focusForLog,
		"orchestrator", s.aiGen.Orchestrator,
	)

	started := time.Now()
	responseText, err := s.runOrchestratorRoundTrip(ctx, "DATASET_GEN_FAILED", "dataset", gatewayURL, prompt, authHeader)
	if err != nil {
		return "", err
	}

	parsed, err := parseGeneratedDataset(responseText)
	if err != nil {
		return "", err
	}

	uniqueName, err := s.ensureUniqueName(ctx, parsed.Name)
	if err != nil {
		return "", err
	}

	dataset := &store.EvalDataset{
		Name:      uniqueName,
		CreatedBy: userID,
		UpdatedBy: userID,
	}
	if parsed.Description != "" {
		desc := parsed.Description
		dataset.Description = &desc
	}

	maxCap := s.maxExamplesCap()
	maxInsertable := min(req.NumExamples, maxCap)
	if len(parsed.Examples) > maxInsertable {
		s.logger.Warn("eval: orchestrator overshot example count; truncating",
			"returned", len(parsed.Examples), "requested", req.NumExamples, "cap", maxCap)
		parsed.Examples = parsed.Examples[:maxInsertable]
	}

	rows := generatedExampleRows(parsed.Examples)
	if err = s.examples.SaveGeneratedDataset(ctx, operationID, dataset, rows); err != nil {
		if errors.Is(err, store.ErrEvalNameTaken) {
			return "", &ConflictError{Message: fmt.Sprintf("dataset with name %q already exists", uniqueName)}
		}
		if errors.Is(err, store.ErrEvalGenerationNotInProgress) {
			return "", err
		}
		return "", fmt.Errorf("save generated dataset: %w", err)
	}

	s.logger.Info("eval: generated dataset",
		"datasetID", dataset.ID,
		"name", dataset.Name,
		"examples", len(rows),
		"elapsed_ms", time.Since(started).Milliseconds(),
	)
	return dataset.ID, nil
}

func generatedExampleRows(parsed []parsedExample) []*store.EvalExample {
	rows := make([]*store.EvalExample, 0, len(parsed))
	for _, ex := range parsed {
		row := &store.EvalExample{Prompt: ex.Prompt}
		if ex.ExpectedResponse != nil && *ex.ExpectedResponse != "" {
			row.ExpectedResponse = ex.ExpectedResponse
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *DatasetService) GenerateExamples(
	ctx context.Context,
	datasetID string,
	req GenerateExamplesRequest,
	userID, gatewayURL, authHeader string,
) ([]ExampleResponse, error) {
	created, err := s.generateExamples(ctx, "", datasetID, req, userID, gatewayURL, authHeader)
	if err != nil {
		return nil, err
	}
	out := make([]ExampleResponse, 0, len(created))
	for _, ex := range created {
		out = append(out, exampleResponseFromRow(ex))
	}
	return out, nil
}

func (s *DatasetService) generateExamples(
	ctx context.Context,
	operationID, datasetID string,
	req GenerateExamplesRequest,
	userID, gatewayURL, authHeader string,
) ([]*store.EvalExample, error) {
	if s.aiGen == nil {
		return nil, &FeatureDisabledError{Message: "AI example generation is not configured. Configure a broker to enable it."}
	}
	if err := validateGenerateExamplesRequest(req); err != nil {
		return nil, err
	}

	dataset, err := s.datasets.GetByID(ctx, datasetID)
	if err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	existing, err := s.examples.ListByDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("list existing examples: %w", err)
	}

	maxCap := s.maxExamplesCap()
	if capErr := exampleCapError(len(existing), req.NumExamples, maxCap); capErr != nil {
		return nil, capErr
	}

	agentCtx := s.buildAgentContext(ctx, req.TargetAgent)
	prompt := buildAppendPrompt(req.TargetAgent, agentCtx, req.NumExamples, existing, ptrToStr(req.Focus))

	focusForLog := ""
	if req.Focus != nil {
		focusForLog = *req.Focus
	}
	s.logger.Info("eval: generating examples",
		"numExamples", req.NumExamples,
		"datasetID", dataset.ID,
		"targetAgent", req.TargetAgent,
		"focus", focusForLog,
		"orchestrator", s.aiGen.Orchestrator,
	)

	started := time.Now()
	responseText, err := s.runOrchestratorRoundTrip(ctx, "EXAMPLE_GEN_FAILED", "example", gatewayURL, prompt, authHeader)
	if err != nil {
		return nil, err
	}

	parsedExamples, err := parseGeneratedExamples(responseText)
	if err != nil {
		return nil, err
	}

	maxInsertable := min(req.NumExamples, maxCap-len(existing))
	if len(parsedExamples) > maxInsertable {
		s.logger.Warn("eval: orchestrator overshot example count; truncating",
			"datasetID", dataset.ID, "returned", len(parsedExamples), "fit", maxInsertable)
		parsedExamples = parsedExamples[:maxInsertable]
	}

	created := generatedExampleRows(parsedExamples)
	if err := s.examples.SaveGeneratedExamples(ctx, operationID, datasetID, created); err != nil {
		if errors.Is(err, store.ErrEvalGenerationNotInProgress) {
			return nil, err
		}
		return nil, fmt.Errorf("save generated examples: %w", err)
	}

	s.logger.Info("eval: appended generated examples",
		"datasetID", dataset.ID,
		"created", len(created),
		"userID", userID,
		"elapsed_ms", time.Since(started).Milliseconds(),
	)
	return created, nil
}

func (s *DatasetService) runOrchestratorRoundTrip(
	ctx context.Context,
	failureCode string,
	kind string,
	gatewayURL, prompt, authHeader string,
) (string, error) {
	taskID, err := s.aiGen.Client.SubmitTask(ctx, gatewayURL, s.aiGen.Orchestrator, prompt, authHeader)
	if err != nil {
		return "", &BusinessRuleViolationError{
			Code:    failureCode,
			Message: fmt.Sprintf("Failed to submit %s-generation task to the Solace Agent Mesh entrypoint: %s", kind, err.Error()),
		}
	}
	if taskID == "" {
		return "", &BusinessRuleViolationError{
			Code:    failureCode,
			Message: fmt.Sprintf("Failed to submit %s-generation task to the Solace Agent Mesh entrypoint.", kind),
		}
	}

	taskResult, err := s.aiGen.Client.PollTask(ctx, gatewayURL, taskID, authHeader, s.aiGen.Timeout)
	if err != nil {
		return "", &BusinessRuleViolationError{
			Code:    failureCode,
			Message: fmt.Sprintf("%s-generation task failed: %s", capitalise(kind), err.Error()),
		}
	}
	if taskResult == nil {
		return "", &BusinessRuleViolationError{
			Code:    failureCode,
			Message: fmt.Sprintf("%s-generation task did not complete before timeout.", capitalise(kind)),
		}
	}

	responseText := ExtractResponseText(taskResult)
	if responseText == "" {
		return "", &BusinessRuleViolationError{
			Code:    failureCode,
			Message: "Orchestrator returned an empty response.",
		}
	}
	return responseText, nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func validateGenerateDatasetRequest(req GenerateDatasetRequest) error {
	if strings.TrimSpace(req.TargetAgent) == "" {
		return &ValidationError{Field: "targetAgent", Message: "targetAgent is required"}
	}
	if req.NumExamples <= 0 {
		return &ValidationError{Field: "numExamples", Message: "numExamples must be positive"}
	}
	if req.NumExamples > MaxExamplesPerDataset {
		return &ValidationError{Field: "numExamples", Message: fmt.Sprintf("numExamples must be at most %d", MaxExamplesPerDataset)}
	}
	return nil
}

func validateGenerateExamplesRequest(req GenerateExamplesRequest) error {
	if strings.TrimSpace(req.TargetAgent) == "" {
		return &ValidationError{Field: "targetAgent", Message: "targetAgent is required"}
	}
	if req.NumExamples <= 0 {
		return &ValidationError{Field: "numExamples", Message: "numExamples must be positive"}
	}
	if req.NumExamples > MaxExamplesPerDataset {
		return &ValidationError{Field: "numExamples", Message: fmt.Sprintf("numExamples must be at most %d", MaxExamplesPerDataset)}
	}
	return nil
}

type agentContext struct {
	Description  string
	Instructions string
	Capabilities string
}

func (s *DatasetService) buildAgentContext(ctx context.Context, agentName string) agentContext {
	var (
		description  string
		instructions string
		skills       []string
		toolsets     []string
	)

	if s.aiGen != nil && s.aiGen.AgentRegistry != nil {
		if card := s.aiGen.AgentRegistry.Get(agentName); card != nil {
			description = card.Description
			for _, sk := range card.Skills {
				if sk.Description != "" {
					skills = append(skills, fmt.Sprintf(nameWithSuffixFmt, sk.Name, sk.Description))
				} else if sk.Name != "" {
					skills = append(skills, sk.Name)
				}
			}
		}
	}

	if s.aiGen != nil && s.aiGen.AgentRepo != nil {
		dbAgent, dbToolsets, _, _, _, err := s.aiGen.AgentRepo.GetByName(ctx, agentName)
		switch {
		case err != nil:
			s.logger.Warn("eval: agent repo lookup failed; using card-only context",
				"agent", agentName, "error", err)
		case dbAgent != nil:
			if description == "" && dbAgent.Description != nil {
				description = *dbAgent.Description
			}
			if dbAgent.SystemPrompt != nil {
				instructions = *dbAgent.SystemPrompt
			}
			if len(dbToolsets) > 0 {
				toolsets = append([]string(nil), dbToolsets...)
			}
			if len(skills) == 0 && dbAgent.Skills != nil {
				skills = append(skills, parseDBSkills(*dbAgent.Skills)...)
			}
		}
	}

	parts := make([]string, 0, 2)
	if len(toolsets) > 0 {
		parts = append(parts, "Toolsets: "+strings.Join(toolsets, ", "))
	}
	if len(skills) > 0 {
		parts = append(parts, "Skills: "+strings.Join(skills, ", "))
	}

	return agentContext{
		Description:  description,
		Instructions: instructions,
		Capabilities: strings.Join(parts, "; "),
	}
}

func parseDBSkills(raw string) []string {
	if raw == "" {
		return nil
	}
	var rows []a2a.AgentSkill
	if err := json.Unmarshal([]byte(raw), &rows); err == nil && len(rows) > 0 {
		out := make([]string, 0, len(rows))
		for _, sk := range rows {
			if sk.Description != "" {
				out = append(out, fmt.Sprintf(nameWithSuffixFmt, sk.Name, sk.Description))
			} else if sk.Name != "" {
				out = append(out, sk.Name)
			}
		}
		return out
	}

	var generic []map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil
	}
	out := make([]string, 0, len(generic))
	for _, sk := range generic {
		name, _ := sk["name"].(string)
		desc, _ := sk["description"].(string)
		switch {
		case desc != "":
			out = append(out, fmt.Sprintf(nameWithSuffixFmt, name, desc))
		case name != "":
			out = append(out, name)
		}
	}
	return out
}

func (s *DatasetService) ensureUniqueName(ctx context.Context, base string) (string, error) {
	exists, err := s.datasets.NameExists(ctx, base)
	if err != nil {
		return "", fmt.Errorf("name exists check: %w", err)
	}
	if !exists {
		return base, nil
	}
	trimmed := base
	if len(trimmed) > 230 {
		trimmed = trimmed[:230]
	}
	dated := fmt.Sprintf(nameWithSuffixFmt, trimmed, time.Now().Format("2006-01-02 15:04"))
	exists, err = s.datasets.NameExists(ctx, dated)
	if err != nil {
		return "", fmt.Errorf("dated name exists check: %w", err)
	}
	if !exists {
		return dated, nil
	}
	return fmt.Sprintf(nameWithSuffixFmt, trimmed, randHex(8)), nil
}

func randHex(n int) string {
	buf := make([]byte, (n+1)/2)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", n)
	}
	return hex.EncodeToString(buf)[:n]
}

func buildGenerationPreamble(agentName string, ctx agentContext, numExamples int) []string {
	lines := []string{
		"You are generating an evaluation dataset for a target AI agent.",
		"",
		"IMPORTANT — DO NOT DELEGATE:",
		fmt.Sprintf("- Do NOT invoke, call, peer, or delegate to the target agent '%s'.", agentName),
		"- Do NOT use any tool, skill, or sub-agent to contact the target agent.",
		"- Generate the dataset YOURSELF using ONLY the target-agent metadata below.",
		"- If the metadata is insufficient, make reasonable assumptions based on the agent's name and description — but still do not contact the agent.",
		"",
		"TARGET AGENT METADATA:",
		fmt.Sprintf("- Name: %s", agentName),
	}
	if ctx.Description != "" {
		lines = append(lines, fmt.Sprintf("- Description: %s", ctx.Description))
	}
	if ctx.Instructions != "" {
		lines = append(lines, fmt.Sprintf("- Instructions / system prompt: %s", ctx.Instructions))
	}
	if ctx.Capabilities != "" {
		lines = append(lines, fmt.Sprintf("- Capabilities: %s", ctx.Capabilities))
	}
	lines = append(lines,
		"",
		fmt.Sprintf("TASK: Generate EXACTLY %d high-quality evaluation examples that will be graded by an LLM-as-judge. Return %d examples — not more, not fewer.", numExamples, numExamples),
		"",
		"EVAL BEST PRACTICES — follow these strictly:",
		"1. Each prompt must be a REALISTIC user message — something an actual end-user would send to this agent. Not a meta-instruction, not a request to the agent to invent content for you, not a self-referential task.",
		"2. Cover diverse behaviours, including:",
		"   - Core happy-path requests that exercise the agent's primary capabilities.",
		"   - Edge cases (ambiguous input, missing context, unusual phrasing, boundary values).",
		"   - Out-of-scope / refusal cases (requests the agent should decline or redirect) — only when relevant to the agent's purpose.",
		"   - Multi-step or reasoning-heavy requests where appropriate.",
		"3. Prompts must be specific and self-contained. Avoid vague prompts like 'give me some practice problems' — instead, write the actual user question ('What is the current through a 4Ω resistor at 12V?').",
		"4. Do NOT generate prompts that ask the agent to produce open-ended lists of content — those are hard to grade consistently. Test the underlying capability directly instead.",
		"",
		"EXPECTED_RESPONSE — include ONLY when verifiable:",
		"- Include `expected_response` ONLY if there is a single, clearly correct answer (math result, factual lookup, specific structured output, a required refusal, a required tool invocation).",
		"- OMIT `expected_response` (leave the field out entirely, or set it to null) for open-ended prompts where many reasonable answers exist — the LLM-as-judge will grade the agent's response on its own merits.",
		"- Never invent an 'ideal' essay/explanation just to fill the field. A missing expected_response is better than a fabricated one.",
	)
	return lines
}

func appendFocusHint(lines []string, focus string) []string {
	if focus == "" {
		return lines
	}
	return append(lines,
		"",
		"USER INSTRUCTIONS (MUST FOLLOW):",
		"The person requesting this dataset provided the following instructions directly. Treat them as a hard requirement, not a suggestion. Every generated example must satisfy these instructions. If they conflict with the best practices above, the user's instructions win — adapt the best practices to fit, do not ignore the user.",
		"",
		`"""`+focus+`"""`,
	)
}

func buildCreatePrompt(agentName string, ctx agentContext, numExamples int, focus string) string {
	lines := buildGenerationPreamble(agentName, ctx, numExamples)
	lines = appendFocusHint(lines, focus)
	lines = append(lines,
		"",
		"Also produce:",
		`- "name": a concise, human-readable dataset name (max 80 chars, no surrounding quotes)`,
		`- "description": a one-sentence summary of what this dataset evaluates (max 200 chars)`,
		"",
		"Return ONLY a valid JSON object with no other text, no markdown fences, no commentary.",
		"Schema (expected_response is OPTIONAL — omit or null when not verifiable):",
		`{"name": "...", "description": "...", "examples": [{"prompt": "...", "expected_response": "..."}, {"prompt": "..."}, ...]}`,
	)
	return strings.Join(lines, "\n")
}

func buildAppendPrompt(
	agentName string,
	ctx agentContext,
	numExamples int,
	existing []*store.EvalExample,
	focus string,
) string {
	lines := buildGenerationPreamble(agentName, ctx, numExamples)
	lines = append(lines,
		"",
		"EXISTING EXAMPLES (do not duplicate these — generate NEW examples that complement them):",
		"- Match the existing examples' style and difficulty level, but cover different scenarios.",
		"- If the existing set is heavy in one area, balance coverage with the missing categories from the best-practices list above.",
	)
	for _, ex := range existing {
		lines = append(lines, fmt.Sprintf("- PROMPT: %s", ex.Prompt))
		if ex.ExpectedResponse != nil && *ex.ExpectedResponse != "" {
			lines = append(lines, fmt.Sprintf("  EXPECTED: %s", *ex.ExpectedResponse))
		}
	}
	lines = appendFocusHint(lines, focus)
	lines = append(lines,
		"",
		"Return ONLY a valid JSON object with no other text, no markdown fences, no commentary.",
		"Schema (expected_response is OPTIONAL — omit or null when not verifiable):",
		`{"examples": [{"prompt": "...", "expected_response": "..."}, {"prompt": "..."}, ...]}`,
	)
	return strings.Join(lines, "\n")
}

type parsedExample struct {
	Prompt           string
	ExpectedResponse *string
}

type parsedDataset struct {
	Name        string
	Description string
	Examples    []parsedExample
}

func parseGeneratedDataset(text string) (*parsedDataset, error) {
	obj, err := extractJSONBlock(text, false)
	if err != nil || obj == nil {
		return nil, &BusinessRuleViolationError{
			Code:    CodeDatasetGenInvalidResponse,
			Message: "The AI did not return valid JSON. Try again, pick a different model, or lower the number of examples.",
		}
	}
	dict, ok := obj.(map[string]any)
	if !ok {
		return nil, &BusinessRuleViolationError{
			Code:    CodeDatasetGenInvalidResponse,
			Message: "The AI returned JSON but it was not an object.",
		}
	}

	name := strutil.Truncate(strings.TrimSpace(stringField(dict, "name")), 255, "")
	if name == "" {
		name = fmt.Sprintf("Untitled Generated Dataset (%s)", randHex(8))
	}

	desc := strutil.Truncate(strings.TrimSpace(stringField(dict, "description")), 1000, "")

	rows, ok := dict["examples"].([]any)
	if !ok {
		return nil, &BusinessRuleViolationError{
			Code:    CodeDatasetGenInvalidResponse,
			Message: errExamplesNotArray,
		}
	}
	valid := collectValidExamples(rows)
	if len(valid) == 0 {
		return nil, &BusinessRuleViolationError{
			Code:    CodeDatasetGenInvalidResponse,
			Message: "The AI returned an empty or invalid example list.",
		}
	}
	return &parsedDataset{Name: name, Description: desc, Examples: valid}, nil
}

func parseGeneratedExamples(text string) ([]parsedExample, error) {
	parsed, err := extractJSONBlock(text, true)
	if err != nil || parsed == nil {
		return nil, &BusinessRuleViolationError{
			Code:    CodeExampleGenInvalidResponse,
			Message: "The AI did not return valid JSON. Try again or lower the number of examples.",
		}
	}

	var rows []any
	switch v := parsed.(type) {
	case map[string]any:
		exs, ok := v["examples"].([]any)
		if !ok {
			return nil, &BusinessRuleViolationError{
				Code:    CodeExampleGenInvalidResponse,
				Message: errExamplesNotArray,
			}
		}
		rows = exs
	case []any:
		rows = v
	default:
		return nil, &BusinessRuleViolationError{
			Code:    CodeExampleGenInvalidResponse,
			Message: errExamplesNotArray,
		}
	}
	valid := collectValidExamples(rows)
	if len(valid) == 0 {
		return nil, &BusinessRuleViolationError{
			Code:    CodeExampleGenInvalidResponse,
			Message: "The AI returned an empty or invalid example list.",
		}
	}
	return valid, nil
}

func collectValidExamples(rows []any) []parsedExample {
	out := make([]parsedExample, 0, len(rows))
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		prompt, _ := row["prompt"].(string)
		if prompt == "" {
			continue
		}
		ex := parsedExample{Prompt: prompt}
		if v, ok := row["expected_response"].(string); ok && v != "" {
			val := v
			ex.ExpectedResponse = &val
		}
		out = append(out, ex)
	}
	return out
}

func stringField(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

var fenceStripRE = regexp.MustCompile("```(?:json)?\\s*")

var (
	objectRE = regexp.MustCompile(`(?s)\{.*\}`)
	arrayRE  = regexp.MustCompile(`(?s)\[.*\]`)
)

var (
	commentLineRE   = regexp.MustCompile(`(?m)(^|\s)//[^\n]*`)
	commentBlockRE  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	trailingCommaRE = regexp.MustCompile(`,(\s*[}\]])`)
	pythonNoneRE    = regexp.MustCompile(`\bNone\b`)
	pythonTrueRE    = regexp.MustCompile(`\bTrue\b`)
	pythonFalseRE   = regexp.MustCompile(`\bFalse\b`)
)

func extractJSONBlock(text string, allowArray bool) (any, error) {
	stripped := strings.TrimSpace(fenceStripRE.ReplaceAllString(text, ""))

	objLoc := objectRE.FindStringIndex(stripped)
	var raw string
	if allowArray {
		arrLoc := arrayRE.FindStringIndex(stripped)
		switch {
		case len(objLoc) > 0 && (len(arrLoc) == 0 || objLoc[0] < arrLoc[0]):
			raw = stripped[objLoc[0]:objLoc[1]]
		case len(arrLoc) > 0:
			raw = stripped[arrLoc[0]:arrLoc[1]]
		default:
			return nil, nil //nolint:nilnil // matches Python "no match → return None"
		}
	} else {
		if len(objLoc) == 0 {
			return nil, nil //nolint:nilnil
		}
		raw = stripped[objLoc[0]:objLoc[1]]
	}

	var out any
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out, nil
	}

	cleaned := commentLineRE.ReplaceAllString(raw, "$1")
	cleaned = commentBlockRE.ReplaceAllString(cleaned, "")
	cleaned = trailingCommaRE.ReplaceAllString(cleaned, "$1")
	cleaned = pythonNoneRE.ReplaceAllString(cleaned, "null")
	cleaned = pythonTrueRE.ReplaceAllString(cleaned, "true")
	cleaned = pythonFalseRE.ReplaceAllString(cleaned, "false")
	if err := json.Unmarshal([]byte(cleaned), &out); err == nil {
		return out, nil
	}
	return nil, nil //nolint:nilnil
}

func ptrToStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
