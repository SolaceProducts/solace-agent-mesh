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
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/google/uuid"
)

const (
	minChoiceScores = 2
	maxChoiceScores = 26

	maxEvaluatorNameLen        = 255
	minEvaluatorDescriptionLen = 1
	maxEvaluatorDescriptionLen = 1000
)

const EvaluatorIsSystemDefaultCode = "EVALUATOR_IS_SYSTEM_DEFAULT"

var responsePlaceholderRE = regexp.MustCompile(`(?i)\{\{\s*Response\s*\}\}`)

type ModelTypeLookup func(ctx context.Context, aliasOrID string) (modelType string, found bool, err error)

type EvaluatorService struct {
	repo      *store.EvalEvaluatorRepo
	logger    *slog.Logger
	registry  *features.Registry
	modelType ModelTypeLookup
}

func NewEvaluatorService(repo *store.EvalEvaluatorRepo, logger *slog.Logger) *EvaluatorService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &EvaluatorService{repo: repo, logger: logger}
}

func (s *EvaluatorService) SetFeatureRegistry(registry *features.Registry) {
	s.registry = registry
}

func (s *EvaluatorService) SetModelTypeLookup(lookup ModelTypeLookup) {
	s.modelType = lookup
}

const listEvaluatorsFailed = "list evaluators: %w"

func (s *EvaluatorService) List(ctx context.Context) ([]EvaluatorResponse, error) {
	rows, err := s.repo.ListWithCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf(listEvaluatorsFailed, err)
	}
	out := make([]EvaluatorResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, evaluatorResponseFromCount(r, s.logger))
	}
	return out, nil
}

func (s *EvaluatorService) ListPaginated(
	ctx context.Context,
	pageNumber, pageSize int,
	sortBy store.EvalEvaluatorSortField,
	desc bool,
) ([]EvaluatorResponse, int, error) {
	rows, total, err := s.repo.ListWithCountsPaginated(ctx, pageNumber, pageSize, sortBy, desc)
	if err != nil {
		return nil, 0, fmt.Errorf(listEvaluatorsFailed, err)
	}
	out := make([]EvaluatorResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, evaluatorResponseFromCount(r, s.logger))
	}
	return out, total, nil
}

func (s *EvaluatorService) ExperimentCount(ctx context.Context, id string) (int, error) {
	n, err := s.repo.CountExperiments(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count evaluator experiments: %w", err)
	}
	return n, nil
}

func (s *EvaluatorService) Get(ctx context.Context, id string) (*EvaluatorResponse, error) {
	row, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapEvaluatorErr(err, id)
	}
	resp := evaluatorResponseFromRow(row, 0, s.logger)
	return &resp, nil
}

func mapEvaluatorErr(err error, id string) error {
	if errors.Is(err, store.ErrEvalNotFound) {
		return &NotFoundError{Resource: "EvalEvaluator", ID: id}
	}
	return fmt.Errorf("evaluator lookup: %w", err)
}

func (s *EvaluatorService) Create(ctx context.Context, req CreateEvaluatorRequest, userID string) (*EvaluatorResponse, error) {
	name := strings.TrimSpace(req.Name)
	desc := strings.TrimSpace(req.Description)
	identity := identityFieldErrors(name, desc)
	evType := cmp.Or(req.Type, evaluatorTypeLLMJudge)
	if evType != evaluatorTypeDecision {
		if err := identity.first(); err != nil {
			return nil, err
		}
	}

	var e *store.EvalEvaluator
	var err error
	switch evType {
	case evaluatorTypeLLMJudge:
		e, err = s.newLLMJudge(ctx, req)
	case evaluatorTypeDecision:
		e, err = s.newDecisionEvaluator(ctx, req, identity)
	default:
		return nil, &ValidationError{
			Field:   "type",
			Message: "Only llm_judge and decision evaluators can be created via the API; heuristic evaluators are system-managed.",
		}
	}
	if err != nil {
		return nil, err
	}
	e.Name = name
	e.Type = evType
	e.CreatedBy = userID
	e.UpdatedBy = userID
	if desc != "" {
		e.Description = &desc
	}
	if err := s.repo.Create(ctx, e); err != nil {
		if errors.Is(err, store.ErrEvalNameTaken) {
			return nil, &ConflictError{Message: fmt.Sprintf("evaluator with name %q already exists", name)}
		}
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	resp := evaluatorResponseFromRow(e, 0, s.logger)
	return &resp, nil
}

func validateJudgeTemplate(template string, checkNodes bool) error {
	if !responsePlaceholderRE.MatchString(template) {
		return &ValidationError{Field: "prompt_template", Message: "prompt_template must contain the {{Response}} placeholder"}
	}
	if !checkNodes {
		return nil
	}
	if at := malformedNodePlaceholderAt(template); at > 0 {
		return &ValidationError{Field: "prompt_template", Message: fmt.Sprintf("prompt_template has a mistyped node placeholder at character %d; write {{Node Output: <node id>}} or {{Node Input: <node id>}}", at)}
	}
	if namesNoNode(template) {
		return &ValidationError{Field: "prompt_template", Message: "a {{Node Output: <node id>}} or {{Node Input: <node id>}} placeholder must name a node"}
	}
	return nil
}

func (s *EvaluatorService) newLLMJudge(ctx context.Context, req CreateEvaluatorRequest) (*store.EvalEvaluator, error) {
	fe := fieldErrors{}
	rejectDecisionOnlyFields(evaluatorTypeLLMJudge, req.Question, req.Scoring, req.Reads, req.MinConfidence, req.EscalateToEvaluatorID, fe)
	if err := fe.err(); err != nil {
		return nil, err
	}
	if req.Model == nil || strings.TrimSpace(*req.Model) == "" {
		return nil, &ValidationError{Field: "model", Message: "model is required for llm_judge evaluators"}
	}
	if req.PromptTemplate == nil || strings.TrimSpace(*req.PromptTemplate) == "" {
		return nil, &ValidationError{Field: "prompt_template", Message: "prompt_template is required for llm_judge evaluators"}
	}
	if err := validateJudgeTemplate(*req.PromptTemplate, workflowEvalsEnabled(s.registry)); err != nil {
		return nil, err
	}
	if err := validateChoiceScores(req.ChoiceScores); err != nil {
		return nil, err
	}
	model := strings.TrimSpace(*req.Model)
	s.checkModelType(ctx, model, store.ModelTypeChat, fe)
	if err := fe.err(); err != nil {
		return nil, err
	}

	choices, err := marshalChoiceScores(req.ChoiceScores)
	if err != nil {
		return nil, err
	}
	tmpl := CanonicalizeTemplatePlaceholders(*req.PromptTemplate)
	return &store.EvalEvaluator{
		Model:          &model,
		PromptTemplate: &tmpl,
		ChoiceScores:   choices,
	}, nil
}

func (s *EvaluatorService) newDecisionEvaluator(ctx context.Context, req CreateEvaluatorRequest, fe fieldErrors) (*store.EvalEvaluator, error) {
	if !decisionEvaluatorsEnabled(s.registry) {
		return nil, &ValidationError{Field: "type", Message: decisionDisabledMessage()}
	}
	settings := decisionSettings{
		Model:          trimmedPtr(req.Model),
		PromptTemplate: req.PromptTemplate,
		ChoiceScores:   req.ChoiceScores,
		Config: DecisionConfig{
			Scoring:               cmp.Or(strings.TrimSpace(req.Scoring), DefaultDecisionScoring),
			Reads:                 readsOrDefault(req.Reads),
			MinConfidence:         req.MinConfidence,
			EscalateToEvaluatorID: trimmedPtr(req.EscalateToEvaluatorID),
		},
	}
	if req.Question != nil {
		settings.Config.Question = req.Question.Normalized()
	}
	return s.decisionRow(ctx, settings, "", fe)
}

func (s *EvaluatorService) decisionRow(ctx context.Context, settings decisionSettings, selfID string, fe fieldErrors) (*store.EvalEvaluator, error) {
	validateDecisionSettings(settings, fe)
	if err := s.checkEscalationTarget(ctx, settings.Config.EscalateToEvaluatorID, selfID, fe); err != nil {
		return nil, err
	}
	if settings.Model != nil && *settings.Model != "" {
		s.checkModelType(ctx, *settings.Model, store.ModelTypeDecision, fe)
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	cfg, err := marshalDecisionConfig(settings.Config)
	if err != nil {
		return nil, err
	}
	row := &store.EvalEvaluator{Model: settings.Model, DecisionConfig: cfg}
	if len(settings.ChoiceScores) > 0 {
		if row.ChoiceScores, err = marshalChoiceScores(settings.ChoiceScores); err != nil {
			return nil, err
		}
	}
	return row, nil
}

func (s *EvaluatorService) checkEscalationTarget(ctx context.Context, id *string, selfID string, fe fieldErrors) error {
	if id == nil {
		return nil
	}
	const field = "escalateToEvaluatorId"
	if *id == "" {
		fe.add(field, "escalateToEvaluatorId must name an llm_judge evaluator, or be left out")
		return nil
	}
	parsed, err := uuid.Parse(*id)
	if err != nil {
		fe.add(field, "escalateToEvaluatorId must be an evaluator id")
		return nil
	}
	*id = parsed.String()
	if *id == selfID {
		fe.add(field, "an evaluator cannot escalate to itself")
		return nil
	}
	target, err := s.repo.GetByID(ctx, *id)
	if errors.Is(err, store.ErrEvalNotFound) {
		fe.add(field, "no evaluator has id %s", *id)
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up escalation evaluator %s: %w", *id, err)
	}
	if target.Type != evaluatorTypeLLMJudge {
		fe.add(field, "escalateToEvaluatorId must name an llm_judge evaluator; %q is a %s evaluator", target.Name, target.Type)
	}
	return nil
}

func (s *EvaluatorService) checkModelType(ctx context.Context, model, want string, fe fieldErrors) {
	if s.modelType == nil || model == "" {
		return
	}
	got, found, err := s.modelType(ctx, model)
	if err != nil {
		s.logger.Warn("eval: evaluator model type lookup failed", "model", model, "error", err)
		return
	}
	if !found || got == want {
		return
	}
	switch want {
	case store.ModelTypeDecision:
		fe.add("model", "model %q is a %s model; a decision evaluator needs a model registered with modelType decision", model, cmp.Or(got, store.ModelTypeChat))
	default:
		fe.add("model", "model %q is a decision model; an llm_judge evaluator needs a chat model", model)
	}
}

func (s *EvaluatorService) Update(ctx context.Context, id string, req UpdateEvaluatorRequest, userID string) (*EvaluatorResponse, error) {
	in, existing, err := s.prepareUpdate(ctx, id, req, userID)
	if err != nil {
		return nil, err
	}
	if existing.IsDefault && builtinModelUnchanged(existing, in) {
		resp := evaluatorResponseFromRow(existing, 0, s.logger)
		return &resp, nil
	}
	updated, err := s.repo.Update(ctx, id, in)
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalEvaluator", ID: id}
		}
		if errors.Is(err, store.ErrEvalNameTaken) {
			return nil, &ConflictError{Message: "evaluator name already taken"}
		}
		return nil, fmt.Errorf("update evaluator: %w", err)
	}
	resp := evaluatorResponseFromRow(updated, 0, s.logger)
	return &resp, nil
}

func (s *EvaluatorService) CheckUpdate(ctx context.Context, id string, req UpdateEvaluatorRequest) error {
	_, _, err := s.prepareUpdate(ctx, id, req, "")
	return err
}

func (s *EvaluatorService) prepareUpdate(ctx context.Context, id string, req UpdateEvaluatorRequest, userID string) (store.EvalEvaluatorUpdate, *store.EvalEvaluator, error) {
	in := store.EvalEvaluatorUpdate{UpdatedBy: userID}
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return in, nil, mapEvaluatorErr(err, id)
	}
	if existing.IsDefault {
		err = s.applyBuiltinUpdate(ctx, existing, req, &in)
		return in, existing, err
	}

	if req.NameSet && req.Name == nil {
		return in, existing, &ValidationError{Field: "name", Message: "name cannot be cleared"}
	}
	if existing.Type != evaluatorTypeDecision && req.PromptTemplateSet && req.PromptTemplate == nil {
		return in, existing, &ValidationError{Field: "prompt_template", Message: "prompt_template cannot be cleared"}
	}
	if req.ModelSet && req.Model == nil {
		return in, existing, &ValidationError{Field: "model", Message: "model cannot be cleared"}
	}

	fe := identityUpdate(req, &in)
	if existing.Type == evaluatorTypeDecision {
		err = s.applyDecisionUpdate(ctx, existing, req, &in, fe)
	} else if err = fe.first(); err == nil {
		err = s.applyLLMJudgeUpdate(ctx, existing, req, &in)
	}
	return in, existing, err
}

func BuiltinEvaluatorChangeRefused() error {
	return &BusinessRuleViolationError{
		Code:    EvaluatorIsSystemDefaultCode,
		Message: "Built-in evaluators cannot be modified, apart from the model of an LLM judge.",
	}
}

func (s *EvaluatorService) applyBuiltinUpdate(ctx context.Context, existing *store.EvalEvaluator, req UpdateEvaluatorRequest, in *store.EvalEvaluatorUpdate) error {
	if existing.Type != evaluatorTypeLLMJudge || req.setsMoreThanModel() {
		return BuiltinEvaluatorChangeRefused()
	}
	if !req.ModelSet {
		return nil
	}
	if req.Model == nil {
		in.ClearModel = true
		return nil
	}
	model := strings.TrimSpace(*req.Model)
	if model == "" {
		return &ValidationError{Field: "model", Message: "model cannot be empty; send null to use the general model"}
	}
	if derefString(existing.Model) != model {
		if err := s.checkBuiltinModel(ctx, model); err != nil {
			return err
		}
	}
	in.Model = &model
	return nil
}

func (s *EvaluatorService) checkBuiltinModel(ctx context.Context, model string) error {
	if s.modelType == nil {
		return nil
	}
	got, found, err := s.modelType(ctx, model)
	if err != nil {
		return fmt.Errorf("look up model %q: %w", model, err)
	}
	if !found {
		return &ValidationError{Field: "model", Message: fmt.Sprintf("no model configuration matches %q", model)}
	}
	if got != store.ModelTypeChat {
		return &ValidationError{Field: "model", Message: fmt.Sprintf("model %q is a decision model; an llm_judge evaluator needs a chat model", model)}
	}
	return nil
}

func builtinModelUnchanged(existing *store.EvalEvaluator, in store.EvalEvaluatorUpdate) bool {
	switch {
	case in.ClearModel:
		return existing.Model == nil
	case in.Model != nil:
		return existing.Model != nil && *existing.Model == *in.Model
	default:
		return true
	}
}

func identityUpdate(req UpdateEvaluatorRequest, in *store.EvalEvaluatorUpdate) fieldErrors {
	fe := fieldErrors{}
	if req.NameSet && req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		switch {
		case name == "":
			fe.add("name", "name is required")
		case len(name) > maxEvaluatorNameLen:
			fe.add("name", "name must be at most %d characters", maxEvaluatorNameLen)
		}
		in.Name = &name
	}
	if req.DescriptionSet && req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		switch {
		case len(desc) < minEvaluatorDescriptionLen:
			fe.add("description", "description is required")
		case len(desc) > maxEvaluatorDescriptionLen:
			fe.add("description", "description must be at most %d characters", maxEvaluatorDescriptionLen)
		}
		in.Description = &desc
	}
	return fe
}

func (s *EvaluatorService) applyLLMJudgeUpdate(ctx context.Context, existing *store.EvalEvaluator, req UpdateEvaluatorRequest, in *store.EvalEvaluatorUpdate) error {
	fe := fieldErrors{}
	rejectDecisionOnlyFields(existing.Type,
		ifSet(req.QuestionSet, req.Question), stringValue(req.ScoringSet, req.Scoring), ifSet(req.ReadsSet, req.Reads),
		ifSet(req.MinConfidenceSet, req.MinConfidence), ifSet(req.EscalateToEvaluatorIDSet, req.EscalateToEvaluatorID), fe)
	if err := fe.err(); err != nil {
		return err
	}
	if req.ModelSet && req.Model != nil {
		trimmed := strings.TrimSpace(*req.Model)
		s.checkModelType(ctx, trimmed, store.ModelTypeChat, fe)
		if err := fe.err(); err != nil {
			return err
		}
		in.Model = &trimmed
	}
	if req.PromptTemplateSet && req.PromptTemplate != nil {
		v := CanonicalizeTemplatePlaceholders(*req.PromptTemplate)
		changed := strings.TrimSpace(v) != strings.TrimSpace(CanonicalizeTemplatePlaceholders(derefString(existing.PromptTemplate)))
		if err := validateJudgeTemplate(*req.PromptTemplate, changed && workflowEvalsEnabled(s.registry)); err != nil {
			return err
		}
		in.PromptTemplate = &v
	}
	if req.ChoiceScoresSet {
		if vErr := validateChoiceScores(req.ChoiceScores); vErr != nil {
			return vErr
		}
		choices, mErr := marshalChoiceScores(req.ChoiceScores)
		if mErr != nil {
			return mErr
		}
		in.ChoiceScores = choices
	}
	return nil
}

func (s *EvaluatorService) applyDecisionUpdate(ctx context.Context, existing *store.EvalEvaluator, req UpdateEvaluatorRequest, in *store.EvalEvaluatorUpdate, fe fieldErrors) error {
	if !decisionEvaluatorsEnabled(s.registry) {
		return &ValidationError{Field: "type", Message: decisionDisabledMessage()}
	}
	cfg, err := parseDecisionConfig(existing.DecisionConfig)
	if err != nil {
		return fmt.Errorf("evaluator %s: %w", existing.ID, err)
	}
	if cfg == nil {
		cfg = &DecisionConfig{Scoring: DefaultDecisionScoring, Reads: DefaultDecisionReads()}
	}
	settings := decisionSettings{Model: existing.Model, Config: *cfg}
	if existing.ChoiceScores != nil && *existing.ChoiceScores != "" {
		if uerr := json.Unmarshal([]byte(*existing.ChoiceScores), &settings.ChoiceScores); uerr != nil {
			return fmt.Errorf("evaluator %s: decode choice_scores: %w", existing.ID, uerr)
		}
	}

	if req.QuestionSet && req.Question == nil {
		fe.add("question", "question cannot be cleared")
	}
	mergeDecisionUpdate(&settings, req)

	row, err := s.decisionRow(ctx, settings, existing.ID, fe)
	if err != nil {
		return err
	}
	in.Model = row.Model
	in.DecisionConfig = row.DecisionConfig
	if row.ChoiceScores != nil {
		in.ChoiceScores = row.ChoiceScores
	} else if existing.ChoiceScores != nil {
		in.ClearChoiceScores = true
	}
	return nil
}

func mergeDecisionUpdate(settings *decisionSettings, req UpdateEvaluatorRequest) {
	if req.ModelSet && req.Model != nil {
		settings.Model = trimmedPtr(req.Model)
	}
	if req.PromptTemplateSet {
		settings.PromptTemplate = req.PromptTemplate
	}
	if req.ChoiceScoresSet {
		settings.ChoiceScores = req.ChoiceScores
	}
	if req.QuestionSet && req.Question != nil {
		settings.Config.Question = req.Question.Normalized()
	}
	if req.ScoringSet {
		settings.Config.Scoring = cmp.Or(strings.TrimSpace(stringValue(true, req.Scoring)), DefaultDecisionScoring)
	}
	if req.ReadsSet {
		settings.Config.Reads = readsOrDefault(req.Reads)
	}
	if req.MinConfidenceSet {
		settings.Config.MinConfidence = req.MinConfidence
	}
	if req.EscalateToEvaluatorIDSet {
		settings.Config.EscalateToEvaluatorID = trimmedPtr(req.EscalateToEvaluatorID)
	}
}

func (s *EvaluatorService) Delete(ctx context.Context, id string) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return mapEvaluatorErr(err, id)
	}
	if existing.IsDefault {
		return &BusinessRuleViolationError{
			Code:    EvaluatorIsSystemDefaultCode,
			Message: "Built-in evaluators cannot be deleted.",
		}
	}
	if existing.Type == evaluatorTypeLLMJudge {
		referrers, err := s.escalationReferrers(ctx, existing.ID)
		if err != nil {
			return err
		}
		if len(referrers) > 0 {
			return &BusinessRuleViolationError{
				Code: "EVALUATOR_IS_ESCALATION_TARGET",
				Message: fmt.Sprintf("Evaluator %q is the escalation target of %s; change or remove their escalateToEvaluatorId first.",
					existing.Name, strings.Join(referrers, ", ")),
			}
		}
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return &NotFoundError{Resource: "EvalEvaluator", ID: id}
		}
		return fmt.Errorf("delete evaluator: %w", err)
	}
	return nil
}

func (s *EvaluatorService) escalationReferrers(ctx context.Context, id string) ([]string, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf(listEvaluatorsFailed, err)
	}
	var names []string
	for _, r := range rows {
		if r.Type != evaluatorTypeDecision {
			continue
		}
		cfg, err := parseDecisionConfig(r.DecisionConfig)
		if err != nil {
			s.logger.Warn("eval: decision evaluator settings unreadable", "evaluatorID", r.ID, "error", err)
			names = append(names, fmt.Sprintf("%q (unreadable settings)", r.Name))
			continue
		}
		if cfg == nil || cfg.EscalateToEvaluatorID == nil {
			continue
		}
		if *cfg.EscalateToEvaluatorID == id {
			names = append(names, fmt.Sprintf("%q", r.Name))
		}
	}
	return names, nil
}

func validateChoiceScores(choices map[string]any) error {
	if len(choices) < minChoiceScores {
		return &ValidationError{
			Field:   "choice_scores",
			Message: fmt.Sprintf("choice_scores must define at least %d labeled outcomes for llm_judge evaluators.", minChoiceScores),
		}
	}
	if len(choices) > maxChoiceScores {
		return &ValidationError{
			Field:   "choice_scores",
			Message: fmt.Sprintf("choice_scores supports at most %d entries (received %d).", maxChoiceScores, len(choices)),
		}
	}
	return nil
}

func marshalChoiceScores(scores map[string]any) (*string, error) {
	b, err := json.Marshal(scores)
	if err != nil {
		return nil, fmt.Errorf("marshal choice_scores: %w", err)
	}
	s := string(b)
	return &s, nil
}

func identityFieldErrors(name, desc string) fieldErrors {
	fe := fieldErrors{}
	switch {
	case name == "":
		fe.add("name", "name is required")
	case len(name) > maxEvaluatorNameLen:
		fe.add("name", "name must be at most %d characters", maxEvaluatorNameLen)
	}
	switch {
	case len(desc) < minEvaluatorDescriptionLen:
		fe.add("description", "description is required")
	case len(desc) > maxEvaluatorDescriptionLen:
		fe.add("description", "description must be at most %d characters", maxEvaluatorDescriptionLen)
	}
	return fe
}

func (fe fieldErrors) first() error {
	for _, field := range []string{"name", "description"} {
		if msgs := fe[field]; len(msgs) > 0 {
			return &ValidationError{Field: field, Message: msgs[0]}
		}
	}
	return nil
}

func readsOrDefault(reads []string) []string {
	if len(reads) == 0 {
		return DefaultDecisionReads()
	}
	out := make([]string, len(reads))
	for i, r := range reads {
		out[i] = strings.TrimSpace(r)
	}
	return out
}

func trimmedPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

func ifSet[T any](set bool, v T) T {
	if !set {
		var zero T
		return zero
	}
	return v
}

func stringValue(set bool, v *string) string {
	if !set || v == nil {
		return ""
	}
	return *v
}

func evaluatorResponseFromRow(e *store.EvalEvaluator, experimentCount int, logger *slog.Logger) EvaluatorResponse {
	resp := EvaluatorResponse{
		ID:              e.ID,
		Name:            e.Name,
		Description:     e.Description,
		Type:            e.Type,
		HeuristicType:   e.HeuristicType,
		IsDefault:       e.IsDefault,
		Model:           e.Model,
		PromptTemplate:  e.PromptTemplate,
		ExperimentCount: experimentCount,
		CreatedBy:       e.CreatedBy,
		UpdatedBy:       e.UpdatedBy,
		CreatedTime:     Timestamp(e.CreatedTime),
		UpdatedTime:     Timestamp(e.UpdatedTime),
	}
	if e.ChoiceScores != nil && *e.ChoiceScores != "" {
		var scores map[string]any
		if err := json.Unmarshal([]byte(*e.ChoiceScores), &scores); err != nil {
			orDiscard(logger).Warn("eval: unreadable choice scores; returning the evaluator without them", "evaluatorID", e.ID, "error", err)
		} else {
			resp.ChoiceScores = scores
		}
	}
	cfg, err := parseDecisionConfig(e.DecisionConfig)
	if err != nil {
		orDiscard(logger).Warn("eval: unreadable decision settings; returning the evaluator without them", "evaluatorID", e.ID, "error", err)
	}
	if cfg != nil {
		q := cfg.Question
		resp.Question = &q
		resp.Scoring = cfg.Scoring
		resp.Reads = cfg.Reads
		resp.MinConfidence = cfg.MinConfidence
		resp.EscalateToEvaluatorID = cfg.EscalateToEvaluatorID
	}
	return resp
}

func evaluatorResponseFromCount(e *store.EvalEvaluatorWithCount, logger *slog.Logger) EvaluatorResponse {
	return evaluatorResponseFromRow(e.EvalEvaluator, e.ExperimentCount, logger)
}
