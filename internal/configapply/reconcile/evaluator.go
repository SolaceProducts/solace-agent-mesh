// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func init() {
	generic.Register(generic.Kind{
		Name:          "evaluator",
		Plural:        "evaluators",
		Order:         70,
		CreateProto:   evaluatorHashView{},
		UpdateProto:   evaluatorHashView{},
		ResponseProto: evaluatorHashView{},
		NameFromCreate: func(v any) string {
			r, _ := v.(evaluatorHashView)
			return r.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(evaluatorHashView)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(evaluatorHashView)
			return r.ID
		},
	})
}

type evaluatorHashView struct {
	ID             string                          `json:"id" cfgapply:"read-only"`
	Name           string                          `json:"name"`
	Description    string                          `json:"description"`
	Type           string                          `json:"type" cfgapply:"discriminator"`
	Model          string                          `json:"model"`
	PromptTemplate string                          `json:"promptTemplate"`
	ChoiceScores   map[string]any                  `json:"choiceScores"`
	Question       *evalplatform.EvaluatorQuestion `json:"question"`
	Scoring        string                          `json:"scoring"`
	Reads          []string                        `json:"reads"`
	MinConfidence  *float64                        `json:"minConfidence"`
	EscalateTo     string                          `json:"escalateToEvaluatorId"`
}

const (
	decisionEvaluatorType = "decision"
	llmJudgeEvaluatorType = "llm_judge"
)

type evaluatorKindPlan struct {
	Kind      string
	Creates   []evaluatorOp
	Updates   []evaluatorOp
	Unchanged []evaluatorOp
	Deletes   []evaluatorOp
}

type evaluatorOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Type       string
	Create     wire.CreateEvaluatorRequest
	Patch      wire.UpdateEvaluatorRequest
}

type evaluatorOpResult struct {
	Op   evaluatorOp
	Verb string
	Err  error
}

type evaluatorOutcome struct {
	Kind     string
	Created  []evaluatorOpResult
	Updated  []evaluatorOpResult
	Deleted  []evaluatorOpResult
	Skipped  []evaluatorOpResult
	Failures []evaluatorOpResult
}

func planEvaluators(desired []resolve.ResolvedResource, actual []wire.EvaluatorResponse) (*evaluatorKindPlan, error) {
	k, ok := generic.Lookup("evaluator")
	if !ok {
		return nil, fmt.Errorf("evaluator kind not registered")
	}

	actualByName := make(map[string]wire.EvaluatorResponse, len(actual))
	nameByID := make(map[string]string, len(actual))
	for _, a := range actual {
		nameByID[a.ID] = a.Name
		if a.IsDefault {
			continue
		}
		actualByName[a.Name] = a
	}
	creates := make([]wire.CreateEvaluatorRequest, len(desired))
	for i, r := range desired {
		create, err := translateEvaluator(r)
		if err != nil {
			return nil, err
		}
		creates[i] = create
	}
	knownTypes := evaluatorTypes(creates, actual)

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateEvaluatorRequest, len(desired))
	for i, r := range desired {
		create := creates[i]
		if err := checkDesiredEvaluator(r.Name, create, actualByName, knownTypes, live); err != nil {
			return nil, err
		}
		live[r.Name] = create
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     evaluatorHashViewFromCreate(create),
		})
	}

	actualVals := make([]reflect.Value, 0, len(actualByName))
	for _, a := range actualByName {
		actualVals = append(actualVals, reflect.ValueOf(evaluatorHashViewFromResponse(a, nameByID)))
	}
	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	plan, err := convertEvaluatorPlan(gp, live, evaluatorsByNameWithNamedTargets(actualByName, nameByID))
	if err != nil {
		return nil, err
	}
	for i := range plan.Deletes {
		plan.Deletes[i].Type = actualByName[plan.Deletes[i].Name].EvaluatorType
	}
	return plan, nil
}

func checkDesiredEvaluator(name string, create wire.CreateEvaluatorRequest, actualByName map[string]wire.EvaluatorResponse,
	knownTypes map[string]string, live map[string]wire.CreateEvaluatorRequest) error {
	if target := ptrString(create.EscalateToEvaluatorID); target != "" {
		targetType, known := knownTypes[target]
		if !known {
			return fmt.Errorf("evaluator %q: escalateToEvaluatorId %q names no evaluator in the manifest or on the platform", name, target)
		}
		if targetType != llmJudgeEvaluatorType {
			return fmt.Errorf("evaluator %q: escalateToEvaluatorId %q must name an llm_judge evaluator; it is a %s evaluator", name, target, targetType)
		}
	}
	if existing, found := actualByName[name]; found && existing.EvaluatorType != create.EvaluatorType {
		return fmt.Errorf("evaluator %q: type change (%q -> %q) is not supported on update; delete the evaluator and recreate it",
			name, existing.EvaluatorType, create.EvaluatorType)
	}
	if _, dup := live[name]; dup {
		return fmt.Errorf("duplicate evaluator name %q in resolved resources", name)
	}
	return nil
}

func checkPrunedEscalationTargets(plan *evaluatorKindPlan) error {
	conflicts := prunedEscalationTargets(plan)
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("apply --prune would delete escalation targets still in use (%s); add them to the manifest or change escalateToEvaluatorId",
		strings.Join(conflicts, "; "))
}

func prunedEscalationWarnings(plan *evaluatorKindPlan) []string {
	conflicts := prunedEscalationTargets(plan)
	warnings := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		warnings = append(warnings, c+", which is not in the manifest, so apply --prune fails until you add it or change escalateToEvaluatorId")
	}
	return warnings
}

func prunedEscalationTargets(plan *evaluatorKindPlan) []string {
	if plan == nil || len(plan.Deletes) == 0 {
		return nil
	}
	deleted := make(map[string]bool, len(plan.Deletes))
	for _, op := range plan.Deletes {
		deleted[op.Name] = true
	}
	var conflicts []string
	for _, ops := range [][]evaluatorOp{plan.Creates, plan.Updates, plan.Unchanged} {
		for _, op := range ops {
			if target := ptrString(op.Create.EscalateToEvaluatorID); deleted[target] {
				conflicts = append(conflicts, fmt.Sprintf("evaluator %q escalates to %q", op.Name, target))
			}
		}
	}
	return conflicts
}

func evaluatorTypes(desired []wire.CreateEvaluatorRequest, actual []wire.EvaluatorResponse) map[string]string {
	types := make(map[string]string, len(desired)+len(actual))
	for _, a := range actual {
		types[a.Name] = a.EvaluatorType
	}
	for _, c := range desired {
		types[c.Name] = c.EvaluatorType
	}
	return types
}

func translateEvaluator(r resolve.ResolvedResource) (wire.CreateEvaluatorRequest, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardEvaluatorV2(in, translate.EvaluatorV2Hooks{})
	if err != nil {
		return create, fmt.Errorf("translate evaluator %q (%s): %w", r.Name, r.SourceFile, err)
	}
	if create.EscalateToEvaluatorID != nil && strings.TrimSpace(*create.EscalateToEvaluatorID) == "" {
		create.EscalateToEvaluatorID = nil
	}
	switch create.EvaluatorType {
	case "", llmJudgeEvaluatorType:
		if hasDecisionSettings(create) {
			return create, fmt.Errorf("evaluator %q (%s): question, scoring, reads, minConfidence and escalateToEvaluatorId are only for type: decision",
				r.Name, r.SourceFile)
		}
		if create.Model == nil || strings.TrimSpace(*create.Model) == "" {
			return create, fmt.Errorf("evaluator %q (%s): model is required for type: llm_judge", r.Name, r.SourceFile)
		}
		create.EvaluatorType = llmJudgeEvaluatorType
	case decisionEvaluatorType:
		if create.Model == nil || strings.TrimSpace(*create.Model) == "" {
			return create, fmt.Errorf("evaluator %q (%s): model is required for type: decision", r.Name, r.SourceFile)
		}
		if create.MinConfidence != nil && create.EscalateToEvaluatorID == nil {
			return create, fmt.Errorf("evaluator %q (%s): minConfidence needs escalateToEvaluatorId", r.Name, r.SourceFile)
		}
	default:
		return create, fmt.Errorf("evaluator %q (%s): type %q is not %q or %q",
			r.Name, r.SourceFile, create.EvaluatorType, llmJudgeEvaluatorType, decisionEvaluatorType)
	}
	return create, nil
}

func hasDecisionSettings(c wire.CreateEvaluatorRequest) bool {
	return c.Question != nil || c.Scoring != "" || len(c.Reads) > 0 || c.MinConfidence != nil || c.EscalateToEvaluatorID != nil
}

func evaluatorHashViewFromCreate(c wire.CreateEvaluatorRequest) evaluatorHashView {
	v := evaluatorHashView{
		Name:           c.Name,
		Description:    c.Description,
		Type:           c.EvaluatorType,
		Model:          ptrString(c.Model),
		PromptTemplate: evalplatform.CanonicalizeTemplatePlaceholders(ptrString(c.PromptTemplate)),
		ChoiceScores:   anyScores(c.ChoiceScores),
		Question:       v1Question(c.Question),
		Scoring:        strings.TrimSpace(c.Scoring),
		Reads:          c.Reads,
		MinConfidence:  c.MinConfidence,
		EscalateTo:     ptrString(c.EscalateToEvaluatorID),
	}
	if v.Type == decisionEvaluatorType {
		if v.Question != nil {
			q := v.Question.Normalized()
			v.Question = &q
		}
		if v.Scoring == "" {
			v.Scoring = evalplatform.DefaultDecisionScoring
		}
		if len(v.Reads) == 0 {
			v.Reads = evalplatform.DefaultDecisionReads()
		} else {
			reads := make([]string, len(v.Reads))
			for i, r := range v.Reads {
				reads[i] = strings.TrimSpace(r)
			}
			v.Reads = reads
		}
	}
	return v
}

func evaluatorHashViewFromResponse(r wire.EvaluatorResponse, nameByID map[string]string) evaluatorHashView {
	desc := ""
	if r.Description != nil {
		desc = *r.Description
	}
	escalateTo := ptrString(r.EscalateToEvaluatorID)
	if name, ok := nameByID[escalateTo]; ok {
		escalateTo = name
	}
	return evaluatorHashView{
		ID:             r.ID,
		Name:           r.Name,
		Description:    desc,
		Type:           r.EvaluatorType,
		Model:          ptrString(r.Model),
		PromptTemplate: evalplatform.CanonicalizeTemplatePlaceholders(ptrString(r.PromptTemplate)),
		ChoiceScores:   anyScores(r.ChoiceScores),
		Question:       v1Question(r.Question),
		Scoring:        ptrString(r.Scoring),
		Reads:          r.Reads,
		MinConfidence:  r.MinConfidence,
		EscalateTo:     escalateTo,
	}
}

func ptrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func anyScores(scores map[string]float64) map[string]any {
	if scores == nil {
		return nil
	}
	out := make(map[string]any, len(scores))
	for choice, score := range scores {
		out[choice] = score
	}
	return out
}

func v1Question(q *apiv2.EvaluatorQuestion) *evalplatform.EvaluatorQuestion {
	if q == nil {
		return nil
	}
	out := &evalplatform.EvaluatorQuestion{Type: q.Type, Instructions: q.Instructions}
	for _, c := range q.Criteria {
		out.Criteria = append(out.Criteria, evalplatform.EvaluatorCriterion{Key: c.Key, Description: ptrString(c.Description)})
	}
	return out
}

func evaluatorsByNameWithNamedTargets(actualByName map[string]wire.EvaluatorResponse, nameByID map[string]string) map[string]wire.EvaluatorResponse {
	out := make(map[string]wire.EvaluatorResponse, len(actualByName))
	for name, a := range actualByName {
		if target, ok := nameByID[ptrString(a.EscalateToEvaluatorID)]; ok {
			a.EscalateToEvaluatorID = &target
		}
		out[name] = a
	}
	return out
}

func withDecisionDefaults(c wire.CreateEvaluatorRequest) wire.CreateEvaluatorRequest {
	if c.EvaluatorType != decisionEvaluatorType {
		return c
	}
	if strings.TrimSpace(c.Scoring) == "" {
		c.Scoring = evalplatform.DefaultDecisionScoring
	}
	if len(c.Reads) == 0 {
		c.Reads = evalplatform.DefaultDecisionReads()
	}
	return c
}

func convertEvaluatorPlan(gp *generic.KindPlan, live map[string]wire.CreateEvaluatorRequest, actual map[string]wire.EvaluatorResponse) (*evaluatorKindPlan, error) {
	out := &evaluatorKindPlan{Kind: "evaluator"}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, evaluatorOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, Create: live[op.Name]})
	}
	for _, op := range gp.Updates {
		update := evaluatorOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Create:     live[op.Name],
		}
		patch, err := generic.ToPatch(withDecisionDefaults(live[op.Name]), actual[op.Name], wire.UpdateEvaluatorRequest{})
		if err != nil {
			return nil, fmt.Errorf("evaluator %q (%s): %w", op.Name, op.Source, err)
		}
		update.Patch, _ = patch.(wire.UpdateEvaluatorRequest)
		out.Updates = append(out.Updates, update)
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, evaluatorOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, ID: op.ID, Create: live[op.Name]})
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, evaluatorOp{Name: op.Name, ID: op.ID})
	}
	return out, nil
}

func resolveEscalationTarget(ref *string, idByName map[string]string) (*string, error) {
	if ref == nil || *ref == "" {
		return ref, nil
	}
	id, ok := idByName[*ref]
	if !ok {
		return nil, fmt.Errorf("escalateToEvaluatorId %q names no evaluator on the platform", *ref)
	}
	return &id, nil
}

func evaluatorIDsByName(ctx context.Context, c *Client, plan *evaluatorKindPlan) (map[string]string, error) {
	idByName := map[string]string{}
	needed := slices.ContainsFunc(plan.Creates, func(op evaluatorOp) bool { return op.Create.EscalateToEvaluatorID != nil }) ||
		slices.ContainsFunc(plan.Updates, func(op evaluatorOp) bool { return op.Create.EscalateToEvaluatorID != nil })
	if !needed {
		return idByName, nil
	}
	rows, err := c.ListEvaluators(ctx)
	if err != nil {
		return idByName, fmt.Errorf("list evaluators to resolve escalateToEvaluatorId: %w", err)
	}
	for _, r := range rows {
		idByName[r.Name] = r.ID
	}
	return idByName, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func applyEvaluators(ctx context.Context, c *Client, plan *evaluatorKindPlan, opts ApplyOptions) *evaluatorOutcome {
	outcome := &evaluatorOutcome{Kind: "evaluator"}
	if plan == nil {
		return outcome
	}
	ids := newEscalationIDs(ctx, c, plan)
	creates := slices.Clone(plan.Creates)
	slices.SortStableFunc(creates, func(a, b evaluatorOp) int {
		return cmp.Compare(btoi(a.Create.EscalateToEvaluatorID != nil), btoi(b.Create.EscalateToEvaluatorID != nil))
	})
	for _, op := range creates {
		res := evaluatorOpResult{Op: op, Verb: "create"}
		req := op.Create
		if req.EscalateToEvaluatorID, res.Err = ids.resolve(req.EscalateToEvaluatorID); res.Err == nil {
			var created *wire.EvaluatorResponse
			created, res.Err = c.CreateEvaluator(ctx, req)
			ids.add(created)
		}
		outcome.record(res, &outcome.Created)
	}
	for _, op := range plan.Updates {
		res := evaluatorOpResult{Op: op, Verb: "update"}
		create, patch := op.Create, op.Patch
		if create.EscalateToEvaluatorID, res.Err = ids.resolve(create.EscalateToEvaluatorID); res.Err == nil {
			if patch.EscalateToEvaluatorID.Set && !patch.EscalateToEvaluatorID.Null {
				patch.EscalateToEvaluatorID.Value = *create.EscalateToEvaluatorID
			}
			_, res.Err = c.UpdateEvaluator(ctx, op.ID, patch)
		}
		outcome.record(res, &outcome.Updated)
	}
	deletes := slices.Clone(plan.Deletes)
	slices.SortStableFunc(deletes, func(a, b evaluatorOp) int {
		return cmp.Compare(btoi(a.Type != decisionEvaluatorType), btoi(b.Type != decisionEvaluatorType))
	})
	for _, op := range deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, evaluatorOpResult{Op: op, Verb: "delete"})
			continue
		}
		outcome.record(evaluatorOpResult{Op: op, Verb: "delete", Err: c.DeleteEvaluator(ctx, op.ID)}, &outcome.Deleted)
	}
	return outcome
}

func (o *evaluatorOutcome) record(res evaluatorOpResult, done *[]evaluatorOpResult) {
	if res.Err != nil {
		o.Failures = append(o.Failures, res)
		return
	}
	*done = append(*done, res)
}

type escalationIDs struct {
	byName  map[string]string
	listErr error
}

func newEscalationIDs(ctx context.Context, c *Client, plan *evaluatorKindPlan) *escalationIDs {
	byName, err := evaluatorIDsByName(ctx, c, plan)
	return &escalationIDs{byName: byName, listErr: err}
}

func (e *escalationIDs) resolve(ref *string) (*string, error) {
	if ref == nil {
		return nil, nil
	}
	if e.listErr != nil {
		return ref, e.listErr
	}
	return resolveEscalationTarget(ref, e.byName)
}

func (e *escalationIDs) add(created *wire.EvaluatorResponse) {
	if created != nil {
		e.byName[created.Name] = created.ID
	}
}
