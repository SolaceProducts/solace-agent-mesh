// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func init() {
	generic.Register(generic.Kind{
		Name:          "dataset",
		Plural:        "datasets",
		Order:         60,
		CreateProto:   wire.CreateDatasetRequest{},
		UpdateProto:   wire.UpdateDatasetRequest{},
		ResponseProto: wire.DatasetResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateDatasetRequest)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.DatasetResponse)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.DatasetResponse)
			return r.ID
		},
	})
}

type datasetKindPlan struct {
	Kind      string
	Creates   []datasetOp
	Updates   []datasetOp
	Unchanged []datasetOp
	Deletes   []datasetOp
}

type datasetOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateDatasetRequest
	Update     wire.UpdateDatasetRequest

	Examples        []translate.ExampleSpec
	ExamplesPresent bool
	ExampleChanges  exampleCounts
}

type exampleCounts struct {
	Inserts, Updates, Deletes int
}

func (c exampleCounts) any() bool {
	return c.Inserts+c.Updates+c.Deletes > 0
}

func (c exampleCounts) String() string {
	return fmt.Sprintf("examples: +%d ~%d -%d", c.Inserts, c.Updates, c.Deletes)
}

type datasetOpResult struct {
	Op   datasetOp
	Verb string
	Err  error
}

type datasetOutcome struct {
	Kind     string
	Created  []datasetOpResult
	Updated  []datasetOpResult
	Deleted  []datasetOpResult
	Skipped  []datasetOpResult
	Failures []datasetOpResult

	Examples []datasetExamplesOutcome
}

type datasetExamplesOutcome struct {
	Name      string
	Source    string
	Inserts   int
	Updates   int
	Deletes   int
	Unchanged int
	Err       error
}

func planDatasets(desired []resolve.ResolvedResource, actual []wire.DatasetResponse) (*datasetKindPlan, error) {
	k, ok := generic.Lookup("dataset")
	if !ok {
		return nil, fmt.Errorf("dataset kind not registered")
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateDatasetRequest, len(desired))
	examples := make(map[string]datasetExamples, len(desired))
	for _, r := range desired {
		create, ex, err := translateDataset(r)
		if err != nil {
			return nil, err
		}
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate dataset name %q in resolved resources", r.Name)
		}
		live[r.Name] = create
		examples[r.Name] = ex
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     create,
		})
	}

	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		actualVals = append(actualVals, reflect.ValueOf(a))
	}

	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	return convertDatasetPlan(gp, live, examples), nil
}

type datasetExamples struct {
	Examples []translate.ExampleSpec
	Present  bool
}

func translateDataset(r resolve.ResolvedResource) (wire.CreateDatasetRequest, datasetExamples, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	rows, present, err := translate.ResolveExamplesFile(in, r.SourceFile)
	if err != nil {
		return wire.CreateDatasetRequest{}, datasetExamples{},
			fmt.Errorf("translate dataset %q (%s): %w", r.Name, r.SourceFile, err)
	}
	create, err := translate.ForwardDatasetV2(in, translate.DatasetV2Hooks{})
	if err != nil {
		return create, datasetExamples{}, fmt.Errorf("translate dataset %q (%s): %w", r.Name, r.SourceFile, err)
	}
	return create, datasetExamples{Examples: rows, Present: present}, nil
}

func planDatasetExamples(ctx context.Context, plan *datasetKindPlan, listExamples func(ctx context.Context, datasetID string) ([]wire.ExampleResponse, error)) error {
	for _, ops := range [][]datasetOp{plan.Updates, plan.Unchanged} {
		for i := range ops {
			op := &ops[i]
			if !op.ExamplesPresent || op.ID == "" {
				continue
			}
			actual, err := listExamples(ctx, op.ID)
			if err != nil {
				return fmt.Errorf("list examples for dataset %q: %w", op.Name, err)
			}
			diff := computeWireExampleDiff(actual, translateExampleSpecs(op.Examples))
			op.ExampleChanges = exampleCounts{Inserts: len(diff.Inserts), Updates: len(diff.Updates), Deletes: len(diff.Deletes)}
		}
	}
	return nil
}

func convertDatasetPlan(
	gp *generic.KindPlan,
	live map[string]wire.CreateDatasetRequest,
	examples map[string]datasetExamples,
) *datasetKindPlan {
	out := &datasetKindPlan{Kind: "dataset"}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, datasetOp{
			Name:            op.Name,
			Source:          op.Source,
			Provenance:      op.Provenance,
			Create:          live[op.Name],
			Examples:        examples[op.Name].Examples,
			ExamplesPresent: examples[op.Name].Present,
		})
	}
	for _, op := range gp.Updates {
		patch, _ := op.Patch.Interface().(wire.UpdateDatasetRequest)
		out.Updates = append(out.Updates, datasetOp{
			Name:            op.Name,
			Source:          op.Source,
			Provenance:      op.Provenance,
			ID:              op.ID,
			Create:          live[op.Name],
			Update:          patch,
			Examples:        examples[op.Name].Examples,
			ExamplesPresent: examples[op.Name].Present,
		})
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, datasetOp{
			Name:            op.Name,
			Source:          op.Source,
			Provenance:      op.Provenance,
			ID:              op.ID,
			Examples:        examples[op.Name].Examples,
			ExamplesPresent: examples[op.Name].Present,
		})
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, datasetOp{Name: op.Name, ID: op.ID})
	}
	return out
}

func applyDatasets(ctx context.Context, c *Client, plan *datasetKindPlan, opts ApplyOptions) *datasetOutcome {
	outcome := &datasetOutcome{Kind: "dataset"}
	if plan == nil {
		return outcome
	}
	for i, op := range plan.Creates {
		resp, err := c.CreateDataset(ctx, op.Create)
		res := datasetOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		if resp != nil {
			plan.Creates[i].ID = resp.ID
		}
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		_, err := c.UpdateDataset(ctx, op.ID, op.Update)
		res := datasetOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Updated = append(outcome.Updated, res)
		}
	}
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, datasetOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.DeleteDataset(ctx, op.ID)
		res := datasetOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}

	reconcileDatasetExamples(ctx, c, plan, outcome)
	return outcome
}

func reconcileDatasetExamples(ctx context.Context, c *Client, plan *datasetKindPlan, outcome *datasetOutcome) {
	walk := func(ops []datasetOp) {
		for _, op := range ops {
			if !op.ExamplesPresent || op.ID == "" {
				continue
			}
			res := applyExampleDiff(ctx, c, op)
			outcome.Examples = append(outcome.Examples, res)
			if res.Err != nil {
				outcome.Failures = append(outcome.Failures,
					datasetOpResult{Op: op, Verb: "examples", Err: res.Err})
			}
		}
	}
	walk(plan.Creates)
	walk(plan.Updates)
	walk(plan.Unchanged)
}

func applyExampleDiff(ctx context.Context, c *Client, op datasetOp) datasetExamplesOutcome {
	out := datasetExamplesOutcome{Name: op.Name, Source: op.Source}
	desired := translateExampleSpecs(op.Examples)

	actual, err := c.ListAllExamples(ctx, op.ID)
	if err != nil {
		out.Err = fmt.Errorf("list examples for dataset %q: %w", op.Name, err)
		return out
	}
	diff := computeWireExampleDiff(actual, desired)
	out.Unchanged = len(actual) - len(diff.Updates) - len(diff.Deletes)

	for _, id := range diff.Deletes {
		if err := c.DeleteExample(ctx, id); err != nil {
			out.Err = fmt.Errorf("delete example %s in dataset %q: %w", id, op.Name, err)
			return out
		}
		out.Deletes++
	}
	for _, u := range diff.Updates {
		if _, err := c.UpdateExample(ctx, u.ID, u.Req); err != nil {
			out.Err = fmt.Errorf("update example %s in dataset %q: %w", u.ID, op.Name, err)
			return out
		}
		out.Updates++
	}
	for _, in := range diff.Inserts {
		if _, err := c.CreateExample(ctx, op.ID, in); err != nil {
			out.Err = fmt.Errorf("create example seq=%d in dataset %q: %w",
				derefInt(in.SequenceNumber), op.Name, err)
			return out
		}
		out.Inserts++
	}
	return out
}

func translateExampleSpecs(rows []translate.ExampleSpec) []wire.CreateExampleRequest {
	out := make([]wire.CreateExampleRequest, 0, len(rows))
	for _, r := range rows {
		seq := r.SequenceNumber
		out = append(out, wire.CreateExampleRequest{
			Prompt:           r.Prompt,
			ExpectedResponse: r.ExpectedResponse,
			InputData:        apiv2.JSONObject(r.InputData),
			ExpectedPath:     wire.ExpectedPathFromEval(r.ExpectedPath),
			ExpectedFacts:    wire.ExpectedFactsFromEval(r.ExpectedFacts),
			SequenceNumber:   &seq,
		})
	}
	return out
}

type wireExampleDiff struct {
	Inserts []wire.CreateExampleRequest
	Updates []wireExampleUpdate
	Deletes []string
}

type wireExampleUpdate struct {
	ID  string
	Seq int
	Req wire.UpdateExampleRequest
}

func computeWireExampleDiff(actual []wire.ExampleResponse, desired []wire.CreateExampleRequest) wireExampleDiff {
	actualBySeq := make(map[int]wire.ExampleResponse, len(actual))
	for _, a := range actual {
		actualBySeq[a.SequenceNumber] = a
	}
	desiredBySeq := make(map[int]wire.CreateExampleRequest, len(desired))
	for _, d := range desired {
		if d.SequenceNumber != nil {
			desiredBySeq[*d.SequenceNumber] = d
		}
	}

	var diff wireExampleDiff
	for _, d := range desired {
		if d.SequenceNumber == nil {
			continue
		}
		seq := *d.SequenceNumber
		a, ok := actualBySeq[seq]
		if !ok {
			diff.Inserts = append(diff.Inserts, d)
			continue
		}
		if !exampleResponseMatches(a, d) {
			diff.Updates = append(diff.Updates, wireExampleUpdate{ID: a.ID, Seq: seq, Req: exampleUpdate(a, d)})
		}
	}
	for _, a := range actual {
		if _, ok := desiredBySeq[a.SequenceNumber]; !ok {
			diff.Deletes = append(diff.Deletes, a.ID)
		}
	}

	slices.SortFunc(diff.Inserts, func(a, b wire.CreateExampleRequest) int {
		return derefInt(a.SequenceNumber) - derefInt(b.SequenceNumber)
	})
	slices.SortFunc(diff.Updates, func(a, b wireExampleUpdate) int { return a.Seq - b.Seq })
	deleteSeq := make(map[string]int, len(diff.Deletes))
	for _, a := range actual {
		deleteSeq[a.ID] = a.SequenceNumber
	}
	slices.SortFunc(diff.Deletes, func(a, b string) int { return deleteSeq[a] - deleteSeq[b] })
	return diff
}

func exampleUpdate(actual wire.ExampleResponse, desired wire.CreateExampleRequest) wire.UpdateExampleRequest {
	update := wire.UpdateExampleRequest{
		Prompt:           wire.Patch[string]{Set: true, Value: desired.Prompt},
		ExpectedResponse: patchOrClear(desired.ExpectedResponse, actual.ExpectedResponse != nil),
		ExpectedPath:     patchOrClear(desired.ExpectedPath, actual.ExpectedPath != nil),
		ExpectedFacts:    patchOrClear(desired.ExpectedFacts, actual.ExpectedFacts != nil),
	}
	switch {
	case hasInputData(json.RawMessage(desired.InputData)):
		update.InputData = wire.Patch[apiv2.JSONObject]{Set: true, Value: desired.InputData}
	case hasInputData(json.RawMessage(actual.InputData)):
		update.InputData = wire.Patch[apiv2.JSONObject]{Set: true, Null: true}
	}
	return update
}

func patchOrClear[T any](desired *T, stored bool) wire.Patch[T] {
	switch {
	case desired != nil:
		return wire.Patch[T]{Set: true, Value: *desired}
	case stored:
		return wire.Patch[T]{Set: true, Null: true}
	}
	return wire.Patch[T]{}
}

func hasInputData(raw json.RawMessage) bool {
	canonical, err := evalplatform.CanonicalInputData(raw)
	return err != nil || canonical != nil
}

func exampleResponseMatches(a wire.ExampleResponse, d wire.CreateExampleRequest) bool {
	if a.Prompt != d.Prompt {
		return false
	}
	return ptrStringEqual(a.ExpectedResponse, d.ExpectedResponse) && sameInputData(json.RawMessage(a.InputData), json.RawMessage(d.InputData)) &&
		evalplatform.SameExpectedPath(wire.EvalExpectedPath(a.ExpectedPath), wire.EvalExpectedPath(d.ExpectedPath)) &&
		evalplatform.SameExpectedFacts(wire.EvalExpectedFacts(a.ExpectedFacts), wire.EvalExpectedFacts(d.ExpectedFacts))
}

func sameInputData(actual, desired json.RawMessage) bool {
	a, errA := evalplatform.CanonicalInputData(actual)
	d, errD := evalplatform.CanonicalInputData(desired)
	return errA == nil && errD == nil && bytes.Equal(a, d)
}

func ptrStringEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func renderDatasetPlan(w io.Writer, k *datasetKindPlan, opts FormatOptions) {
	const dirName = "datasets"
	total := len(k.Creates) + len(k.Updates) + len(k.Deletes) + len(k.Unchanged)
	if total == 0 {
		_, _ = fmt.Fprintf(w, noResourcesLine, dirName)
		_, _ = fmt.Fprintf(w, emptyTotalsLine, dirName)
		return
	}
	updates := slices.Clone(k.Updates)
	var unchanged []datasetOp
	for _, op := range k.Unchanged {
		if op.ExampleChanges.any() {
			updates = append(updates, op)
		} else {
			unchanged = append(unchanged, op)
		}
	}
	_, _ = fmt.Fprintf(w, "%s/\n", dirName)
	emitDatasetPlanRows(w, k.Creates, updates, k.Deletes, unchanged, opts)
	_, _ = fmt.Fprintf(w, totalsLine,
		total, dirName, len(k.Creates), len(updates), len(k.Deletes), len(unchanged))
}

func emitDatasetPlanRows(w io.Writer, creates, updates, deletes, unchanged []datasetOp, opts FormatOptions) {
	type group struct {
		ops    []datasetOp
		code   string
		symbol string
		label  string
	}
	for _, g := range []group{
		{creates, ansiGreen, "+ ", "create"},
		{updates, ansiBlue, "~ ", "update"},
		{deletes, ansiRed, "- ", planDeleteLabel(opts)},
		{unchanged, ansiDim, "= ", noChangeLabel},
	} {
		for _, op := range g.ops {
			suffix := provenanceSuffix(op.Provenance, opts)
			if g.code == ansiRed {
				_, _ = fmt.Fprintf(w, rowSimple,
					color(opts, g.code, g.symbol+padName(op.Name)),
					color(opts, g.code, g.label))
				continue
			}
			label := g.label
			if op.ExampleChanges.any() {
				label += " (" + op.ExampleChanges.String() + ")"
			}
			_, _ = fmt.Fprintf(w, rowWithBuild,
				color(opts, g.code, g.symbol+padName(op.Name)),
				color(opts, g.code, label),
				suffix)
		}
	}
}

func renderApplyDatasets(w io.Writer, o *datasetOutcome, opts FormatOptions) {
	if len(o.Created) == 0 && len(o.Updated) == 0 && len(o.Deleted) == 0 &&
		len(o.Failures) == 0 && len(o.Skipped) == 0 && len(o.Examples) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, "datasets")
	exByName := make(map[string]datasetExamplesOutcome, len(o.Examples))
	for _, ex := range o.Examples {
		exByName[ex.Name] = ex
	}
	emitDatasetApplyRow := func(code, sym, label string, name string) {
		_, _ = fmt.Fprintf(w, rowSimple,
			color(opts, code, sym+padName(name)),
			color(opts, code, label))
	}
	for _, r := range o.Created {
		emitDatasetApplyRow(ansiGreen, "+ ", "created", r.Op.Name)
		writeExampleSummary(w, exByName[r.Op.Name], opts)
	}
	for _, r := range o.Updated {
		emitDatasetApplyRow(ansiBlue, "~ ", "updated", r.Op.Name)
		writeExampleSummary(w, exByName[r.Op.Name], opts)
	}
	for _, r := range o.Deleted {
		emitDatasetApplyRow(ansiRed, "- ", "deleted", r.Op.Name)
	}
	for _, r := range o.Skipped {
		emitDatasetApplyRow(ansiDim, "- ", skipPruneLabel, r.Op.Name)
	}
	for _, ex := range unchangedDatasetExamples(o.Examples, o.Created, o.Updated) {
		emitDatasetApplyRow(ansiDim, "= ", noChangeLabel, ex.Name)
		writeExampleSummary(w, ex, opts)
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError,
			color(opts, ansiRed, "! "+padName(r.Op.Name)),
			color(opts, ansiRed, r.Verb+failedSuffix),
			r.Err)
	}
}

func writeExampleSummary(w io.Writer, ex datasetExamplesOutcome, opts FormatOptions) {
	if ex.Name == "" {
		return
	}
	if ex.Err != nil {
		_, _ = fmt.Fprintf(w, "    %s\n",
			color(opts, ansiRed, fmt.Sprintf("↳ examples: failed: %v", ex.Err)))
		return
	}
	_, _ = fmt.Fprintf(w, "    %s\n",
		color(opts, ansiDim,
			fmt.Sprintf("↳ examples: +%d ~%d -%d (%d unchanged)",
				ex.Inserts, ex.Updates, ex.Deletes, ex.Unchanged)))
}

func unchangedDatasetExamples(examples []datasetExamplesOutcome, created, updated []datasetOpResult) []datasetExamplesOutcome {
	seen := make(map[string]bool, len(created)+len(updated))
	for _, r := range created {
		seen[r.Op.Name] = true
	}
	for _, r := range updated {
		seen[r.Op.Name] = true
	}
	var out []datasetExamplesOutcome
	for _, ex := range examples {
		if !seen[ex.Name] {
			out = append(out, ex)
		}
	}
	return out
}
