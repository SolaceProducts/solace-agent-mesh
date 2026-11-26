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
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type DesiredExample struct {
	SequenceNumber   int
	Prompt           string
	ExpectedResponse *string
	InputData        json.RawMessage
	ExpectedPath     *ExpectedPath
	ExpectedFacts    *ExpectedFacts
}

type ExampleReconcileResult struct {
	Inserts   int
	Updates   int
	Deletes   int
	Unchanged int
}

func (s *DatasetService) ReconcileExamples(
	ctx context.Context,
	datasetID string,
	desired []DesiredExample,
	userID string,
) (*ExampleReconcileResult, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	desired, err := s.prepareDesiredExamples(desired)
	if err != nil {
		return nil, err
	}
	maxCap := s.maxExamplesCap()
	if len(desired) > maxCap {
		return nil, &BusinessRuleViolationError{
			Code: "DATASET_EXAMPLE_CAP_REACHED",
			Message: fmt.Sprintf("desired example count %d exceeds per-dataset cap of %d",
				len(desired), maxCap),
		}
	}

	actual, err := s.examples.ListByDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("list examples: %w", err)
	}
	diff := DiffExamples(actual, desired)

	res := &ExampleReconcileResult{Unchanged: len(actual) - diff.Updates() - diff.Deletes()}
	if res.Deletes, err = s.deleteExamples(ctx, diff.DeleteIDs); err != nil {
		return res, err
	}
	if res.Updates, err = s.updateExamples(ctx, diff.UpdateOps); err != nil {
		return res, err
	}
	if res.Inserts, err = s.insertExamples(ctx, datasetID, diff.InsertOps); err != nil {
		return res, err
	}

	s.logger.Info("eval: reconciled examples",
		"datasetID", datasetID, "user", userID,
		"inserts", res.Inserts, "updates", res.Updates,
		"deletes", res.Deletes, "unchanged", res.Unchanged)
	return res, nil
}

func (s *DatasetService) deleteExamples(ctx context.Context, ids []string) (int, error) {
	deleted := 0
	for _, id := range ids {
		if err := s.examples.Delete(ctx, id, s.fileDeletes.event()); err != nil {
			if errors.Is(err, store.ErrEvalNotFound) {
				continue
			}
			return deleted, fmt.Errorf("delete example %s: %w", id, err)
		}
		deleted++
	}
	return deleted, nil
}

func (s *DatasetService) updateExamples(ctx context.Context, ops []ExampleUpdateOp) (int, error) {
	for i, u := range ops {
		stored, err := storedExampleFields(u.InputData, u.ExpectedPath, u.ExpectedFacts)
		if err != nil {
			return i, err
		}
		if _, err := s.examples.UpdateWith(ctx, u.ID, store.EvalExampleUpdate{
			Prompt:           &u.Prompt,
			ExpectedResponse: u.ExpectedResponse,
			ClearExpected:    u.ExpectedResponse == nil,
			InputData:        stored.inputData,
			ClearInputData:   stored.inputData == nil,
			ExpectedPath:     stored.path,
			ClearPath:        stored.path == nil,
			ExpectedFacts:    stored.facts,
			ClearFacts:       stored.facts == nil,
		}); err != nil {
			return i, fmt.Errorf("update example %s: %w", u.ID, err)
		}
	}
	return len(ops), nil
}

func (s *DatasetService) insertExamples(ctx context.Context, datasetID string, ops []DesiredExample) (int, error) {
	if len(ops) == 0 {
		return 0, nil
	}
	toCreate := make([]*store.EvalExample, 0, len(ops))
	for _, in := range ops {
		stored, err := storedExampleFields(in.InputData, in.ExpectedPath, in.ExpectedFacts)
		if err != nil {
			return 0, err
		}
		toCreate = append(toCreate, &store.EvalExample{
			DatasetID:        datasetID,
			SequenceNumber:   in.SequenceNumber,
			Prompt:           in.Prompt,
			ExpectedResponse: in.ExpectedResponse,
			InputData:        stored.inputData,
			ExpectedPath:     stored.path,
			ExpectedFacts:    stored.facts,
		})
	}
	if err := s.examples.BulkCreate(ctx, toCreate); err != nil {
		return 0, fmt.Errorf("bulk create examples: %w", err)
	}
	return len(toCreate), nil
}

type storedFields struct {
	inputData, path, facts *string
}

func storedExampleFields(input json.RawMessage, path *ExpectedPath, facts *ExpectedFacts) (storedFields, error) {
	var stored storedFields
	var err error
	if stored.inputData, err = storedInputData(input); err != nil {
		return storedFields{}, err
	}
	if stored.path, err = encodeExpectedPath(path); err != nil {
		return storedFields{}, err
	}
	if stored.facts, err = encodeExpectedFacts(facts); err != nil {
		return storedFields{}, err
	}
	return stored, nil
}

type ExampleDiff struct {
	InsertOps []DesiredExample
	UpdateOps []ExampleUpdateOp
	DeleteIDs []string
}

func (d ExampleDiff) Updates() int { return len(d.UpdateOps) }

func (d ExampleDiff) Deletes() int { return len(d.DeleteIDs) }

type ExampleUpdateOp struct {
	ID               string
	SequenceNumber   int
	Prompt           string
	ExpectedResponse *string
	InputData        json.RawMessage
	ExpectedPath     *ExpectedPath
	ExpectedFacts    *ExpectedFacts
}

func DiffExamples(actual []*store.EvalExample, desired []DesiredExample) ExampleDiff {
	actualBySeq := make(map[int]*store.EvalExample, len(actual))
	for _, a := range actual {
		actualBySeq[a.SequenceNumber] = a
	}
	desiredBySeq := make(map[int]DesiredExample, len(desired))
	for _, d := range desired {
		desiredBySeq[d.SequenceNumber] = d
	}

	var diff ExampleDiff
	for _, d := range desired {
		a, ok := actualBySeq[d.SequenceNumber]
		if !ok {
			diff.InsertOps = append(diff.InsertOps, d)
			continue
		}
		if !exampleRowMatches(a, d) {
			diff.UpdateOps = append(diff.UpdateOps, ExampleUpdateOp{
				ID:               a.ID,
				SequenceNumber:   d.SequenceNumber,
				Prompt:           d.Prompt,
				ExpectedResponse: d.ExpectedResponse,
				InputData:        d.InputData,
				ExpectedPath:     d.ExpectedPath,
				ExpectedFacts:    d.ExpectedFacts,
			})
		}
	}
	for _, a := range actual {
		if _, ok := desiredBySeq[a.SequenceNumber]; !ok {
			diff.DeleteIDs = append(diff.DeleteIDs, a.ID)
		}
	}

	slices.SortFunc(diff.InsertOps, func(a, b DesiredExample) int { return a.SequenceNumber - b.SequenceNumber })
	slices.SortFunc(diff.UpdateOps, func(a, b ExampleUpdateOp) int { return a.SequenceNumber - b.SequenceNumber })
	deleteSeq := make(map[string]int, len(diff.DeleteIDs))
	for _, a := range actual {
		deleteSeq[a.ID] = a.SequenceNumber
	}
	slices.SortFunc(diff.DeleteIDs, func(a, b string) int { return deleteSeq[a] - deleteSeq[b] })
	return diff
}

func exampleRowMatches(a *store.EvalExample, d DesiredExample) bool {
	if a.Prompt != d.Prompt {
		return false
	}
	if !ptrStringEq(a.ExpectedResponse, d.ExpectedResponse) {
		return false
	}
	if !sameExpectedPath(a.ExpectedPath, d.ExpectedPath) || !sameExpectedFacts(a.ExpectedFacts, d.ExpectedFacts) {
		return false
	}
	desired, err := storedInputData(d.InputData)
	return err == nil && sameInputData(a.InputData, desired)
}

func ptrStringEq(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (s *DatasetService) prepareDesiredExamples(desired []DesiredExample) ([]DesiredExample, error) {
	prepared, withInput, err := validateDesiredExamples(desired)
	if err != nil {
		return nil, err
	}
	for _, d := range prepared {
		if _, err := storedExampleFields(nil, d.ExpectedPath, d.ExpectedFacts); err != nil {
			return nil, err
		}
	}
	withPath := slices.ContainsFunc(prepared, func(d DesiredExample) bool { return d.ExpectedPath != nil })
	withFacts := slices.ContainsFunc(prepared, func(d DesiredExample) bool { return d.ExpectedFacts != nil })
	if err := gatedFieldsRefusal(s.registry, withInput, withPath, withFacts); err != nil {
		return nil, err
	}
	return prepared, nil
}

func validateDesiredExamples(desired []DesiredExample) (prepared []DesiredExample, withInput bool, err error) {
	prepared = make([]DesiredExample, len(desired))
	seen := make(map[int]bool, len(desired))
	for i, d := range desired {
		if d.SequenceNumber <= 0 {
			return nil, false, &ValidationError{
				Field:   fmt.Sprintf("examples[%d].sequenceNumber", i),
				Message: "sequenceNumber must be a positive integer",
			}
		}
		if seen[d.SequenceNumber] {
			return nil, false, &ValidationError{
				Field:   fmt.Sprintf("examples[%d].sequenceNumber", i),
				Message: fmt.Sprintf("duplicate sequenceNumber %d", d.SequenceNumber),
			}
		}
		seen[d.SequenceNumber] = true
		canonical, inputErr := CanonicalInputData(d.InputData)
		if inputErr != nil {
			return nil, false, &ValidationError{
				Field:   fmt.Sprintf("examples[%d].inputData", i),
				Message: "inputData " + inputErr.Error(),
			}
		}
		if !hasExampleInput(d.Prompt, canonical != nil) {
			return nil, false, &ValidationError{
				Field:   fmt.Sprintf("examples[%d].prompt", i),
				Message: "prompt is required unless the example has inputData",
			}
		}
		d.InputData = canonical
		prepared[i] = d
		withInput = withInput || canonical != nil
	}
	return prepared, withInput, nil
}
