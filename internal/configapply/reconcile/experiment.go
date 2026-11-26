// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"maps"
	"reflect"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func init() {
	generic.Register(generic.Kind{
		Name:          "experiment",
		Plural:        "experiments",
		Order:         80,
		CreateProto:   experimentHashView{},
		UpdateProto:   experimentHashView{},
		ResponseProto: experimentHashView{},
		NameFromCreate: func(v any) string {
			r, _ := v.(experimentHashView)
			return r.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(experimentHashView)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(experimentHashView)
			return r.ID
		},
	})
}

type experimentHashView struct {
	ID                 string           `json:"id" cfgapply:"read-only"`
	Name               string           `json:"name"`
	Description        string           `json:"description"`
	DatasetID          string           `json:"datasetId" cfgapply:"xref=dataset"`
	TargetAgent        string           `json:"targetAgent" cfgapply:"xref=agent"`
	TargetWorkflow     string           `json:"targetWorkflow" cfgapply:"xref=workflow"`
	Models             []map[string]any `json:"models"`
	RunsPerExample     int              `json:"runsPerExample"`
	MaxWorkers         int              `json:"maxWorkers"`
	EvaluatorIDs       []string         `json:"evaluatorIds" cfgapply:"xref=evaluator"`
	PrimaryEvaluatorID string           `json:"primaryEvaluatorId" cfgapply:"xref=evaluator"`
	RunAs              string           `json:"runAs"`
}

type experimentKindPlan struct {
	Kind      string
	Creates   []experimentOp
	Updates   []experimentOp
	Unchanged []experimentOp
	Deletes   []experimentOp
}

type experimentOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateExperimentRequest
	Patch      wire.UpdateExperimentRequest
}

type experimentOpResult struct {
	Op   experimentOp
	Verb string
	Err  error
}

type experimentOutcome struct {
	Kind     string
	Created  []experimentOpResult
	Updated  []experimentOpResult
	Deleted  []experimentOpResult
	Skipped  []experimentOpResult
	Failures []experimentOpResult
}

const (
	experimentDefaultRunsPerExample = 1
	experimentDefaultMaxWorkers     = 4
)

type experimentXRefAdapter struct {
	hooks translate.ExperimentHooks
}

func (a experimentXRefAdapter) LookupID(kind, name string) (string, error) {
	switch kind {
	case "dataset":
		if a.hooks.LookupDatasetID == nil {
			return name, nil
		}
		return a.hooks.LookupDatasetID(name)
	case "evaluator":
		if a.hooks.LookupEvaluatorID == nil {
			return name, nil
		}
		return a.hooks.LookupEvaluatorID(name)
	}
	return name, nil
}

func (a experimentXRefAdapter) LookupName(kind, id string) (string, error) {
	switch kind {
	case "dataset":
		if a.hooks.LookupDatasetName == nil {
			return id, nil
		}
		return a.hooks.LookupDatasetName(id)
	case "evaluator":
		if a.hooks.LookupEvaluatorName == nil {
			return id, nil
		}
		return a.hooks.LookupEvaluatorName(id)
	}
	return id, nil
}

func planExperiments(desired []resolve.ResolvedResource, actual []wire.ExperimentResponse, hooks translate.ExperimentHooks) (*experimentKindPlan, error) {
	k, ok := generic.Lookup("experiment")
	if !ok {
		return nil, fmt.Errorf("experiment kind not registered")
	}
	xref := experimentXRefAdapter{hooks: hooks}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateExperimentRequest, len(desired))
	for _, r := range desired {
		create, err := translateExperiment(r)
		if err != nil {
			return nil, err
		}
		if err := generic.ResolveForward(&create, xref); err != nil {
			return nil, fmt.Errorf("experiment %q: %w", r.Name, err)
		}
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate experiment name %q in resolved resources", r.Name)
		}
		if _, err := evalplatform.NormalizeRunAs(ptrString(create.RunAs)); err != nil {
			return nil, fmt.Errorf("experiment %q (%s): %w", r.Name, r.SourceFile, err)
		}
		live[r.Name] = create
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     experimentHashViewFromCreate(create),
		})
	}

	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		actualVals = append(actualVals, reflect.ValueOf(experimentHashViewFromResponse(a)))
	}
	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	actualByName := make(map[string]wire.ExperimentResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}
	return convertExperimentPlan(gp, live, actualByName)
}

func translateExperiment(r resolve.ResolvedResource) (wire.CreateExperimentRequest, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardExperimentV2(in, translate.ExperimentV2Hooks{})
	if err != nil {
		return create, fmt.Errorf("translate experiment %q (%s): %w", r.Name, r.SourceFile, err)
	}
	return create, nil
}

func experimentHashViewFromCreate(c wire.CreateExperimentRequest) experimentHashView {
	rpe := experimentDefaultRunsPerExample
	if c.RunsPerExample != nil {
		rpe = *c.RunsPerExample
	}
	mw := experimentDefaultMaxWorkers
	if c.MaxWorkers != nil {
		mw = *c.MaxWorkers
	}
	return experimentHashView{
		Name:               c.Name,
		Description:        c.Description,
		DatasetID:          c.DatasetID,
		TargetAgent:        c.TargetAgent,
		TargetWorkflow:     c.TargetWorkflow,
		Models:             normalizeExperimentModels(experimentModelMaps(c.Models)),
		RunsPerExample:     rpe,
		MaxWorkers:         mw,
		EvaluatorIDs:       sortedCopy(c.EvaluatorIDs),
		PrimaryEvaluatorID: ptrString(c.PrimaryEvaluatorID),
		RunAs:              normalizeRunAsForHash(c.RunAs),
	}
}

func experimentHashViewFromResponse(r wire.ExperimentResponse) experimentHashView {
	return experimentHashView{
		ID:                 r.ID,
		Name:               r.Name,
		Description:        ptrString(r.Description),
		DatasetID:          ptrString(r.DatasetID),
		TargetAgent:        r.TargetAgent,
		TargetWorkflow:     ptrString(r.TargetWorkflow),
		Models:             normalizeExperimentModels(experimentModelMaps(r.Models)),
		RunsPerExample:     r.RunsPerExample,
		MaxWorkers:         r.MaxWorkers,
		EvaluatorIDs:       sortedCopy(r.EvaluatorIDs),
		PrimaryEvaluatorID: ptrString(r.PrimaryEvaluatorID),
		RunAs:              normalizeRunAsForHash(r.RunAs),
	}
}

func normalizeRunAsForHash(v *string) string {
	principal, err := evalplatform.NormalizeRunAs(ptrString(v))
	if err != nil {
		return ptrString(v)
	}
	return principal
}

func normalizeExperimentModels(models []map[string]any) []map[string]any {
	if len(models) == 0 {
		return nil
	}
	return models
}

func convertExperimentPlan(gp *generic.KindPlan, live map[string]wire.CreateExperimentRequest, actual map[string]wire.ExperimentResponse) (*experimentKindPlan, error) {
	out := &experimentKindPlan{Kind: "experiment"}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, experimentOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, Create: live[op.Name]})
	}
	for _, op := range gp.Updates {
		update := experimentOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Create:     live[op.Name],
		}
		patch, err := experimentPatch(live[op.Name], actual[op.Name])
		if err != nil {
			return nil, fmt.Errorf("experiment %q (%s): %w", op.Name, op.Source, err)
		}
		update.Patch = patch
		out.Updates = append(out.Updates, update)
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, experimentOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, ID: op.ID})
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, experimentOp{Name: op.Name, ID: op.ID})
	}
	return out, nil
}

func experimentPatch(create wire.CreateExperimentRequest, actual wire.ExperimentResponse) (wire.UpdateExperimentRequest, error) {
	if create.RunsPerExample == nil {
		create.RunsPerExample = new(int)
		*create.RunsPerExample = experimentDefaultRunsPerExample
	}
	if create.MaxWorkers == nil {
		create.MaxWorkers = new(int)
		*create.MaxWorkers = experimentDefaultMaxWorkers
	}
	if create.TargetWorkflow != "" {
		actual.TargetAgent = ""
	} else {
		actual.TargetWorkflow = nil
	}
	patch, err := generic.ToPatch(create, actual, wire.UpdateExperimentRequest{})
	if err != nil {
		return wire.UpdateExperimentRequest{}, err
	}
	update, _ := patch.(wire.UpdateExperimentRequest)
	return update, nil
}

func datasetCreateNameSet(p *datasetKindPlan) map[string]struct{} {
	if p == nil {
		return nil
	}
	set := make(map[string]struct{}, len(p.Creates))
	for _, op := range p.Creates {
		set[op.Name] = struct{}{}
	}
	return set
}

func evaluatorCreateNameSet(p *evaluatorKindPlan) map[string]struct{} {
	if p == nil {
		return nil
	}
	set := make(map[string]struct{}, len(p.Creates))
	for _, op := range p.Creates {
		set[op.Name] = struct{}{}
	}
	return set
}

func resolvePlaceholderExperimentRefs(ctx context.Context, c *Client, plan *experimentKindPlan) error {
	if plan == nil || (len(plan.Creates) == 0 && len(plan.Updates) == 0) {
		return nil
	}
	datasets, err := c.ListDatasets(ctx)
	if err != nil {
		return fmt.Errorf("list datasets for experiment placeholder resolution: %w", err)
	}
	dsByName := make(map[string]string, len(datasets))
	for _, d := range datasets {
		dsByName[d.Name] = d.ID
	}
	evaluators, err := c.ListEvaluators(ctx)
	if err != nil {
		return fmt.Errorf("list evaluators for experiment placeholder resolution: %w", err)
	}
	evByName := make(map[string]string, len(evaluators))
	for _, e := range evaluators {
		evByName[e.Name] = e.ID
	}
	for i := range plan.Creates {
		if err := rewriteExperimentCreateRefs(&plan.Creates[i], dsByName, evByName); err != nil {
			return err
		}
	}
	for i := range plan.Updates {
		if err := rewriteExperimentUpdateRefs(&plan.Updates[i], dsByName, evByName); err != nil {
			return err
		}
	}
	return nil
}

func rewriteExperimentCreateRefs(op *experimentOp, dsByName, evByName map[string]string) error {
	ds, err := rewritePlaceholderID(op.Create.DatasetID, dsByName, "experiment", op.Name, "dataset")
	if err != nil {
		return err
	}
	op.Create.DatasetID = ds
	evIDs, err := rewritePlaceholderIDs(op.Create.EvaluatorIDs, evByName, "experiment", op.Name, "evaluator")
	if err != nil {
		return err
	}
	op.Create.EvaluatorIDs = evIDs
	if op.Create.PrimaryEvaluatorID != nil {
		pe, err := rewritePlaceholderID(*op.Create.PrimaryEvaluatorID, evByName, "experiment", op.Name, "evaluator")
		if err != nil {
			return err
		}
		op.Create.PrimaryEvaluatorID = &pe
	}
	return nil
}

func rewriteExperimentUpdateRefs(op *experimentOp, dsByName, evByName map[string]string) error {
	if err := rewriteExperimentCreateRefs(op, dsByName, evByName); err != nil {
		return err
	}
	if op.Patch.DatasetID.Set {
		op.Patch.DatasetID.Value = op.Create.DatasetID
	}
	if op.Patch.EvaluatorIDs.Set {
		op.Patch.EvaluatorIDs.Value = op.Create.EvaluatorIDs
	}
	if op.Patch.PrimaryEvaluatorID.Set && !op.Patch.PrimaryEvaluatorID.Null {
		op.Patch.PrimaryEvaluatorID.Value = ptrString(op.Create.PrimaryEvaluatorID)
	}
	return nil
}

func applyExperiments(ctx context.Context, c *Client, plan *experimentKindPlan, opts ApplyOptions) *experimentOutcome {
	outcome := &experimentOutcome{Kind: "experiment"}
	if plan == nil {
		return outcome
	}
	for _, op := range plan.Creates {
		_, err := c.CreateExperiment(ctx, op.Create)
		res := experimentOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Created = append(outcome.Created, res)
		}
	}
	for _, op := range plan.Updates {
		_, err := c.UpdateExperiment(ctx, op.ID, op.Patch)
		res := experimentOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Updated = append(outcome.Updated, res)
		}
	}
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, experimentOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.DeleteExperiment(ctx, op.ID)
		res := experimentOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
	return outcome
}
