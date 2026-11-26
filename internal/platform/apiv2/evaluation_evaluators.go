// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type Evaluator struct {
	ID                    string             `json:"id" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	Type                  string             `json:"type" example:"evaluator"`
	Name                  string             `json:"name" example:"Answer relevance"`
	Description           *string            `json:"description" example:"Judges whether the answer addresses the question."`
	EvaluatorType         string             `json:"evaluatorType" example:"llm_judge"`
	HeuristicType         *string            `json:"heuristicType" example:"valid_json"`
	IsDefault             bool               `json:"isDefault" example:"false"`
	Model                 *string            `json:"model" example:"general"`
	PromptTemplate        *string            `json:"promptTemplate" example:"Question: {{ Prompt }} Answer: {{ Response }} Is the answer relevant?"`
	ChoiceScores          map[string]float64 `json:"choiceScores" nullable:"true" example:"{\"relevant\":1,\"partly\":0.5,\"irrelevant\":0}"`
	Question              *EvaluatorQuestion `json:"question" example:"{\"type\":\"choice\",\"instructions\":\"Is the answer relevant?\",\"criteria\":[{\"key\":\"relevant\"},{\"key\":\"irrelevant\"}]}"`
	Scoring               *string            `json:"scoring" example:"chosen"`
	Reads                 []string           `json:"reads" nullable:"true" example:"[\"prompt\",\"response\",\"expected_response\"]"`
	MinConfidence         *float64           `json:"minConfidence" example:"0.7"`
	EscalateToEvaluatorID *string            `json:"escalateToEvaluatorId" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	ExperimentCount       int                `json:"experimentCount" example:"3"`
	CreatedBy             string             `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy             string             `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime           Instant            `json:"createdTime"`
	UpdatedTime           Instant            `json:"updatedTime"`
}

type EvaluatorQuestion struct {
	Type         string               `json:"type" example:"choice"`
	Instructions string               `json:"instructions" example:"Is the answer relevant to the question?"`
	Criteria     []EvaluatorCriterion `json:"criteria" nullable:"true" example:"[{\"key\":\"relevant\",\"description\":\"The answer addresses what was asked.\"},{\"key\":\"irrelevant\",\"description\":null}]"`
}

type EvaluatorCriterion struct {
	Key         string  `json:"key" example:"relevant"`
	Description *string `json:"description" example:"The answer addresses what was asked."`
}

type CreateEvaluatorRequest struct {
	Name                  string             `json:"name" validate:"required,max=255" example:"Answer relevance"`
	Description           string             `json:"description" validate:"required,max=1000" example:"Judges whether the answer addresses the question."`
	EvaluatorType         string             `json:"evaluatorType" example:"llm_judge"`
	Model                 *string            `json:"model" required:"true" example:"general"`
	PromptTemplate        *string            `json:"promptTemplate" example:"Question: {{ Prompt }} Answer: {{ Response }} Is the answer relevant?"`
	ChoiceScores          map[string]float64 `json:"choiceScores" example:"{\"relevant\":1,\"partly\":0.5,\"irrelevant\":0}"`
	Question              *EvaluatorQuestion `json:"question" example:"{\"type\":\"choice\",\"instructions\":\"Is the answer relevant?\",\"criteria\":[{\"key\":\"relevant\"},{\"key\":\"irrelevant\"}]}"`
	Scoring               string             `json:"scoring" example:"chosen"`
	Reads                 []string           `json:"reads" example:"[\"prompt\",\"response\",\"expected_response\"]"`
	MinConfidence         *float64           `json:"minConfidence" example:"0.7"`
	EscalateToEvaluatorID *string            `json:"escalateToEvaluatorId" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	Surface               string             `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

type UpdateEvaluatorRequest struct {
	ID                    string                    `path:"id" json:"-"`
	Name                  Patch[string]             `json:"name" example:"Answer relevance, strict"`
	Description           Patch[string]             `json:"description" example:"Judges whether the answer fully addresses the question."`
	EvaluatorType         Patch[string]             `json:"evaluatorType" example:"llm_judge"`
	Model                 Patch[string]             `json:"model" clearable:"true" example:"planning"`
	PromptTemplate        Patch[string]             `json:"promptTemplate" clearable:"true" example:"Question: {{ Prompt }} Answer: {{ Response }} Is the answer fully relevant?"`
	ChoiceScores          Patch[map[string]float64] `json:"choiceScores" clearable:"true" example:"{\"relevant\":1,\"irrelevant\":0}"`
	Question              Patch[EvaluatorQuestion]  `json:"question" example:"{\"type\":\"choice\",\"instructions\":\"Is the answer relevant?\",\"criteria\":[{\"key\":\"relevant\",\"description\":null},{\"key\":\"irrelevant\",\"description\":null}]}"`
	Scoring               Patch[string]             `json:"scoring" clearable:"true" example:"expected"`
	Reads                 Patch[[]string]           `json:"reads" clearable:"true" example:"[\"prompt\",\"response\"]"`
	MinConfidence         Patch[float64]            `json:"minConfidence" clearable:"true" example:"0.6"`
	EscalateToEvaluatorID Patch[string]             `json:"escalateToEvaluatorId" clearable:"true" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
}

type listEvaluatorsRequest struct {
	Page Page      `json:"-"`
	Sort SortOrder `sort:"name,createdTime" json:"-"`
}

const (
	evaluatorsPath    = evaluationsPath + "/evaluators"
	evaluatorPath     = evaluatorsPath + "/{id}"
	evaluatorResource = "evaluator"
)

var evaluatorSortFields = map[string]store.EvalEvaluatorSortField{
	"name":        store.EvalEvaluatorSortName,
	"createdTime": store.EvalEvaluatorSortCreatedTime,
}

func evaluatorRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: evaluatorsPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listEvaluators", Summary: "Get a list of evaluation evaluators",
				Text: "Returns one page of evaluators with the number of experiments each is attached to: the built-in evaluators first, then the others oldest first, unless sorted.", Tag: evaluatorsTag},
			Handler: Handle(h.listEvaluators, "Unable to list evaluators")},
		{Method: http.MethodPost, Path: evaluatorsPath, Scope: rbac.ScopeEvaluationCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createEvaluator", Summary: "Create an evaluation evaluator",
				Text: "Creates an llm_judge evaluator, or a decision evaluator while decision evaluators are enabled on the deployment. If the name is taken, the request fails with 409.", Tag: evaluatorsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.createEvaluator, "Unable to create evaluator")},
		{Method: http.MethodGet, Path: evaluatorPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getEvaluator", Summary: "Get an evaluation evaluator",
				Text: "Returns one evaluator with the number of experiments it is attached to.", Tag: evaluatorsTag},
			Handler: Handle(h.getEvaluator, "Unable to get evaluator")},
		{Method: http.MethodPatch, Path: evaluatorPath, Scope: rbac.ScopeEvaluationUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateEvaluator", Summary: "Update an evaluation evaluator",
				Text: "Changes the fields the body includes and keeps the rest. The evaluatorType cannot change. A decision evaluator cannot be edited while decision evaluators are disabled. A built-in evaluator accepts only a model change, and only when it is an llm_judge; any other change to it fails with 409, as does a name another evaluator has.", Tag: evaluatorsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.updateEvaluator, "Unable to update evaluator")},
		{Method: http.MethodDelete, Path: evaluatorPath, Scope: rbac.ScopeEvaluationDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteEvaluator", Summary: "Delete an evaluation evaluator",
				Text: "Deletes the evaluator and detaches it from its experiments. The scores it gave are kept. If the evaluator is built in, the request fails with 409. If a decision evaluator escalates to it, the request fails with 422.", Tag: evaluatorsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.deleteEvaluator, "Unable to delete evaluator")},
	}
}

func (h *evaluationHandler) listEvaluators(ctx context.Context, req listEvaluatorsRequest) (List[Evaluator], error) {
	rows, total, err := h.evaluators.ListPaginated(ctx, req.Page.Number, req.Page.Size, evaluatorSortFields[req.Sort.Field], req.Sort.Desc)
	if err != nil {
		return List[Evaluator]{}, err
	}
	items := make([]Evaluator, len(rows))
	for i := range rows {
		items[i] = toEvaluator(&rows[i])
	}
	return List[Evaluator]{Items: items, Total: total}, nil
}

func (h *evaluationHandler) createEvaluator(ctx context.Context, req CreateEvaluatorRequest) (Evaluator, error) {
	resp, err := h.evaluators.Create(ctx, eval.CreateEvaluatorRequest{
		Name:                  req.Name,
		Description:           req.Description,
		Type:                  req.EvaluatorType,
		Model:                 req.Model,
		PromptTemplate:        req.PromptTemplate,
		ChoiceScores:          fromChoiceScores(req.ChoiceScores),
		Question:              fromEvaluatorQuestion(req.Question),
		Scoring:               req.Scoring,
		Reads:                 req.Reads,
		MinConfidence:         req.MinConfidence,
		EscalateToEvaluatorID: req.EscalateToEvaluatorID,
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Evaluator{}, evaluatorError(err)
	}
	h.reportEvaluatorCreated(ctx, resp, req.Surface)
	return toEvaluator(resp), nil
}

func (h *evaluationHandler) reportEvaluatorCreated(ctx context.Context, resp *eval.EvaluatorResponse, surface string) {
	var questionType, escalation string
	if resp.Type == taxonomy.EvaluatorTypeDecision {
		if resp.Question != nil {
			questionType = resp.Question.Type
		}
		escalation = evaluatorEscalation(resp)
	}
	h.analytics.EvaluatorCreated(ctx, resp.Type, questionType, escalation, httputil.ParseClientSurface(surface))
}

func evaluatorEscalation(resp *eval.EvaluatorResponse) string {
	switch {
	case resp.EscalateToEvaluatorID == nil || *resp.EscalateToEvaluatorID == "":
		return taxonomy.EscalationNone
	case resp.MinConfidence == nil:
		return taxonomy.EscalationOnError
	default:
		return taxonomy.EscalationOnLowConfidence
	}
}

func (h *evaluationHandler) getEvaluator(ctx context.Context, req IDRequest) (Evaluator, error) {
	resp, err := h.evaluators.Get(ctx, req.ID)
	if err != nil {
		return Evaluator{}, err
	}
	return h.withExperimentCount(ctx, resp)
}

func (h *evaluationHandler) updateEvaluator(ctx context.Context, req UpdateEvaluatorRequest) (Evaluator, error) {
	update, details := req.toServiceRequest()
	if len(details) > 0 {
		return Evaluator{}, h.refuseEvaluatorUpdate(ctx, req.ID, update, details)
	}
	resp, err := h.evaluators.Update(ctx, req.ID, update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Evaluator{}, evaluatorError(err)
	}
	return h.withExperimentCount(ctx, resp)
}

func (h *evaluationHandler) deleteEvaluator(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, evaluatorError(h.evaluators.Delete(ctx, req.ID))
}

func (h *evaluationHandler) withExperimentCount(ctx context.Context, resp *eval.EvaluatorResponse) (Evaluator, error) {
	count, err := h.evaluators.ExperimentCount(ctx, resp.ID)
	if err != nil {
		return Evaluator{}, err
	}
	resp.ExperimentCount = count
	return toEvaluator(resp), nil
}

func (h *evaluationHandler) refuseEvaluatorUpdate(ctx context.Context, id string, update eval.UpdateEvaluatorRequest, details map[string][]string) error {
	if refusesMoreThanModel(details) {
		existing, err := h.evaluators.Get(ctx, id)
		if err != nil {
			return err
		}
		if existing.IsDefault {
			return evaluatorError(eval.BuiltinEvaluatorChangeRefused())
		}
	}
	err := evaluatorError(h.evaluators.CheckUpdate(ctx, id, update))
	if ve, ok := errors.AsType[*apierr.ValidationError](err); ok {
		for field, problems := range ve.Details {
			details[field] = append(details[field], problems...)
		}
	} else if err != nil {
		return err
	}
	return &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
}

func refusesMoreThanModel(details map[string][]string) bool {
	_, refusesModel := details["model"]
	return !refusesModel || len(details) > 1
}

func (req UpdateEvaluatorRequest) toServiceRequest() (eval.UpdateEvaluatorRequest, map[string][]string) {
	var update eval.UpdateEvaluatorRequest
	details := map[string][]string{}
	if req.EvaluatorType.Set {
		details["evaluatorType"] = []string{"cannot be changed"}
	}
	update.Name, update.NameSet = RequiredPatch(details, "name", req.Name)
	update.Description, update.DescriptionSet = RequiredPatch(details, "description", req.Description)
	if req.Model.Null {
		update.ModelSet = true
	} else {
		update.Model, update.ModelSet = RequiredPatch(details, "model", req.Model)
	}

	update.PromptTemplate, update.PromptTemplateSet = patchPointer(req.PromptTemplate)
	update.ChoiceScores, update.ChoiceScoresSet = fromChoiceScores(req.ChoiceScores.Value), req.ChoiceScores.Set
	update.Scoring, update.ScoringSet = patchPointer(req.Scoring)
	update.Reads, update.ReadsSet = req.Reads.Value, req.Reads.Set
	update.MinConfidence, update.MinConfidenceSet = sentValue(req.MinConfidence), req.MinConfidence.Set
	update.EscalateToEvaluatorID, update.EscalateToEvaluatorIDSet = patchPointer(req.EscalateToEvaluatorID)
	if req.Question.Set {
		update.QuestionSet = true
		if !req.Question.Null {
			update.Question = fromEvaluatorQuestion(&req.Question.Value)
		}
	}
	return update, details
}

func sentValue[T any](p Patch[T]) *T {
	if !p.Set || p.Null {
		return nil
	}
	return &p.Value
}

func evaluatorError(err error) error {
	if rule, ok := errors.AsType[*apierr.BusinessRuleViolationError](err); ok && rule.Code == eval.EvaluatorIsSystemDefaultCode {
		return &CallerError{Status: http.StatusConflict, Message: rule.Message, Cause: err}
	}
	err = camelFieldError(err)
	if ve, ok := errors.AsType[*apierr.ValidationError](err); ok {
		if problems, renamed := ve.Details["type"]; renamed {
			ve.Details["evaluatorType"] = problems
			delete(ve.Details, "type")
		}
	}
	return err
}

func toEvaluator(resp *eval.EvaluatorResponse) Evaluator {
	return Evaluator{
		ID:                    resp.ID,
		Type:                  evaluatorResource,
		Name:                  resp.Name,
		Description:           NullIfEmpty(resp.Description),
		EvaluatorType:         resp.Type,
		HeuristicType:         resp.HeuristicType,
		IsDefault:             resp.IsDefault,
		Model:                 resp.Model,
		PromptTemplate:        resp.PromptTemplate,
		ChoiceScores:          toChoiceScores(resp.ChoiceScores),
		Question:              toEvaluatorQuestion(resp.Question),
		Scoring:               NullIfZero(resp.Scoring),
		Reads:                 resp.Reads,
		MinConfidence:         resp.MinConfidence,
		EscalateToEvaluatorID: resp.EscalateToEvaluatorID,
		ExperimentCount:       resp.ExperimentCount,
		CreatedBy:             resp.CreatedBy,
		UpdatedBy:             resp.UpdatedBy,
		CreatedTime:           Instant(resp.CreatedTime),
		UpdatedTime:           Instant(resp.UpdatedTime),
	}
}

func toChoiceScores(scores map[string]any) map[string]float64 {
	if scores == nil {
		return nil
	}
	out := make(map[string]float64, len(scores))
	for choice, score := range scores {
		out[choice], _ = score.(float64)
	}
	return out
}

func fromChoiceScores(scores map[string]float64) map[string]any {
	if scores == nil {
		return nil
	}
	out := make(map[string]any, len(scores))
	for choice, score := range scores {
		out[choice] = score
	}
	return out
}

func toEvaluatorQuestion(q *eval.EvaluatorQuestion) *EvaluatorQuestion {
	if q == nil {
		return nil
	}
	var criteria []EvaluatorCriterion
	for _, c := range q.Criteria {
		criteria = append(criteria, EvaluatorCriterion{Key: c.Key, Description: NullIfZero(c.Description)})
	}
	return &EvaluatorQuestion{Type: q.Type, Instructions: q.Instructions, Criteria: criteria}
}

func fromEvaluatorQuestion(q *EvaluatorQuestion) *eval.EvaluatorQuestion {
	if q == nil {
		return nil
	}
	var criteria []eval.EvaluatorCriterion
	for _, c := range q.Criteria {
		criteria = append(criteria, eval.EvaluatorCriterion{Key: c.Key, Description: orEmpty(c.Description)})
	}
	return &eval.EvaluatorQuestion{Type: q.Type, Instructions: q.Instructions, Criteria: criteria}
}
