// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestDiffExamples_InsertsOnly(t *testing.T) {
	desired := []DesiredExample{
		{SequenceNumber: 1, Prompt: "a"},
		{SequenceNumber: 2, Prompt: "b", ExpectedResponse: ptr("B")},
	}
	diff := DiffExamples(nil, desired)
	if len(diff.InsertOps) != 2 || len(diff.UpdateOps) != 0 || len(diff.DeleteIDs) != 0 {
		t.Fatalf("diff = %+v", diff)
	}
	if diff.InsertOps[0].SequenceNumber != 1 || diff.InsertOps[1].SequenceNumber != 2 {
		t.Errorf("inserts not sorted by sequence: %+v", diff.InsertOps)
	}
}

func TestDiffExamples_DeletesOnly(t *testing.T) {
	actual := []*store.EvalExample{
		{ID: "x1", SequenceNumber: 1, Prompt: "a"},
		{ID: "x2", SequenceNumber: 2, Prompt: "b"},
	}
	diff := DiffExamples(actual, nil)
	if len(diff.DeleteIDs) != 2 {
		t.Fatalf("expected 2 deletes, got %+v", diff.DeleteIDs)
	}
	if diff.DeleteIDs[0] != "x1" || diff.DeleteIDs[1] != "x2" {
		t.Errorf("deletes not in sequence order: %+v", diff.DeleteIDs)
	}
}

func TestDiffExamples_UpdatesOnly(t *testing.T) {
	actual := []*store.EvalExample{
		{ID: "x1", SequenceNumber: 1, Prompt: "a", ExpectedResponse: ptr("OLD")},
		{ID: "x2", SequenceNumber: 2, Prompt: "b"},
	}
	desired := []DesiredExample{
		{SequenceNumber: 1, Prompt: "a", ExpectedResponse: ptr("NEW")},
		{SequenceNumber: 2, Prompt: "b"},
	}
	diff := DiffExamples(actual, desired)
	if len(diff.UpdateOps) != 1 || diff.UpdateOps[0].ID != "x1" {
		t.Fatalf("expected 1 update on x1, got %+v", diff.UpdateOps)
	}
	if len(diff.InsertOps) != 0 || len(diff.DeleteIDs) != 0 {
		t.Errorf("unexpected inserts/deletes: %+v", diff)
	}
}

func TestDiffExamples_Mixed(t *testing.T) {
	actual := []*store.EvalExample{
		{ID: "x1", SequenceNumber: 1, Prompt: "a"},
		{ID: "x3", SequenceNumber: 3, Prompt: "old"},
		{ID: "x5", SequenceNumber: 5, Prompt: "to-delete"},
		{ID: "x7", SequenceNumber: 7, Prompt: "another-to-delete"},
	}
	desired := []DesiredExample{
		{SequenceNumber: 1, Prompt: "a"},
		{SequenceNumber: 3, Prompt: "new"},
		{SequenceNumber: 4, Prompt: "fresh"},
		{SequenceNumber: 8, Prompt: "another-fresh"},
	}
	diff := DiffExamples(actual, desired)
	if len(diff.InsertOps) != 2 {
		t.Errorf("expected 2 inserts, got %+v", diff.InsertOps)
	}
	if len(diff.UpdateOps) != 1 || diff.UpdateOps[0].ID != "x3" {
		t.Errorf("expected 1 update on x3, got %+v", diff.UpdateOps)
	}
	if len(diff.DeleteIDs) != 2 || diff.DeleteIDs[0] != "x5" || diff.DeleteIDs[1] != "x7" {
		t.Errorf("expected sorted deletes [x5,x7], got %+v", diff.DeleteIDs)
	}
	if diff.Updates() != 1 || diff.Deletes() != 2 {
		t.Errorf("Updates/Deletes counts off: %d / %d", diff.Updates(), diff.Deletes())
	}
}

func TestDiffExamples_NilExpectedRoundTrip(t *testing.T) {
	actual := []*store.EvalExample{
		{ID: "x1", SequenceNumber: 1, Prompt: "a", ExpectedResponse: ptr("hi")},
	}
	desired := []DesiredExample{{SequenceNumber: 1, Prompt: "a"}}
	diff := DiffExamples(actual, desired)
	if len(diff.UpdateOps) != 1 {
		t.Errorf("nil-out expected_response should produce update: %+v", diff)
	}
}

func TestReconcileExamples_FullCycle(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "RX-1")

	desired := []DesiredExample{
		{SequenceNumber: 1, Prompt: "what is 1+1?", ExpectedResponse: ptr("2")},
		{SequenceNumber: 2, Prompt: "color of the sky?"},
		{SequenceNumber: 3, Prompt: "capital of france?", ExpectedResponse: ptr("Paris")},
	}
	res, err := svcs.dataset.ReconcileExamples(ctx, dsID, desired, "alice")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Inserts != 3 || res.Updates != 0 || res.Deletes != 0 {
		t.Fatalf("first pass = %+v", res)
	}

	desired = []DesiredExample{
		{SequenceNumber: 1, Prompt: "what is 1+1?", ExpectedResponse: ptr("two")},
		{SequenceNumber: 2, Prompt: "color of the sky?"},
		{SequenceNumber: 4, Prompt: "best color"},
	}
	res, err = svcs.dataset.ReconcileExamples(ctx, dsID, desired, "alice")
	if err != nil {
		t.Fatalf("Reconcile 2: %v", err)
	}
	if res.Inserts != 1 || res.Updates != 1 || res.Deletes != 1 {
		t.Fatalf("second pass = %+v", res)
	}

	res, err = svcs.dataset.ReconcileExamples(ctx, dsID, desired, "alice")
	if err != nil {
		t.Fatalf("Reconcile 3: %v", err)
	}
	if res.Inserts != 0 || res.Updates != 0 || res.Deletes != 0 {
		t.Errorf("third pass should be no-op, got %+v", res)
	}

	all, _ := svcs.example.List(ctx, dsID)
	if len(all) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(all))
	}
	if all[0].SequenceNumber != 1 || all[0].ExpectedResponse == nil || *all[0].ExpectedResponse != "two" {
		t.Errorf("row 0 = %+v", all[0])
	}
	if all[2].SequenceNumber != 4 || all[2].Prompt != "best color" {
		t.Errorf("row 2 = %+v", all[2])
	}
}

func TestReconcileExamples_RejectsBadDesired(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "RX-2")

	cases := []struct {
		name    string
		input   []DesiredExample
		errFrag string
	}{
		{"zero seq", []DesiredExample{{SequenceNumber: 0, Prompt: "x"}}, "positive integer"},
		{"negative seq", []DesiredExample{{SequenceNumber: -1, Prompt: "x"}}, "positive integer"},
		{"empty prompt", []DesiredExample{{SequenceNumber: 1, Prompt: " "}}, "prompt is required"},
		{"duplicate seq", []DesiredExample{
			{SequenceNumber: 1, Prompt: "a"},
			{SequenceNumber: 1, Prompt: "b"},
		}, "duplicate sequenceNumber"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svcs.dataset.ReconcileExamples(ctx, dsID, tc.input, "alice")
			if err == nil {
				t.Fatal("expected validation error")
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || !strings.Contains(err.Error(), tc.errFrag) {
				t.Errorf("expected validation containing %q, got %v", tc.errFrag, err)
			}
		})
	}
}

func TestReconcileExamples_RejectsOverCap(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	withMaxExamples(t, svcs, 5)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "RX-cap")

	desired := make([]DesiredExample, 6)
	for i := range desired {
		desired[i] = DesiredExample{SequenceNumber: i + 1, Prompt: "x"}
	}
	_, err := svcs.dataset.ReconcileExamples(ctx, dsID, desired, "alice")
	if err == nil {
		t.Fatal("expected cap violation")
	}
	br := asBusinessRuleErr(t, err)
	if br.Code != "DATASET_EXAMPLE_CAP_REACHED" {
		t.Errorf("unexpected code %q", br.Code)
	}
}

func TestReconcileExamples_DatasetNotFound(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	_, err := svcs.dataset.ReconcileExamples(ctx, "00000000-0000-0000-0000-000000000000",
		[]DesiredExample{{SequenceNumber: 1, Prompt: "x"}}, "alice")
	if err == nil {
		t.Fatal("expected NotFoundError")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Errorf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestExampleService_Create_WithSequenceNumber(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "Seq-1")

	resp, err := svcs.example.Create(ctx, dsID, CreateExampleRequest{
		Prompt:         "first",
		SequenceNumber: ptr(5),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.SequenceNumber != 5 {
		t.Errorf("got seq %d, want 5", resp.SequenceNumber)
	}

	resp, err = svcs.example.Create(ctx, dsID, CreateExampleRequest{Prompt: "auto"})
	if err != nil {
		t.Fatalf("Create auto: %v", err)
	}
	if resp.SequenceNumber != 6 {
		t.Errorf("auto seq = %d, want 6", resp.SequenceNumber)
	}

	_, err = svcs.example.Create(ctx, dsID, CreateExampleRequest{
		Prompt: "dup", SequenceNumber: ptr(5),
	})
	if err == nil {
		t.Fatal("expected conflict")
	}
	br := asBusinessRuleErr(t, err)
	if br.Code != "EXAMPLE_SEQUENCE_NUMBER_TAKEN" {
		t.Errorf("unexpected code %q", br.Code)
	}

	_, err = svcs.example.Create(ctx, dsID, CreateExampleRequest{
		Prompt: "neg", SequenceNumber: ptr(0),
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	_ = asValidationError(t, err)
}

func TestReconcileExamples_PreservesArtifacts(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "RX-artifacts")

	if _, err := svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{
		{SequenceNumber: 1, Prompt: "review the attached"},
		{SequenceNumber: 2, Prompt: "text only"},
	}, "alice"); err != nil {
		t.Fatalf("seed reconcile: %v", err)
	}
	rows, err := svcs.examples.ListByDataset(ctx, dsID)
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	withFile := rows[0]
	if setErr := svcs.artifacts.Upsert(ctx, &store.EvalExampleArtifact{
		ExampleID: withFile.ID, Role: store.EvalArtifactRoleInput,
		Filename: "config.yaml", MIMEType: "application/x-yaml", SizeBytes: 12,
		Version: 0, SessionID: evalExampleArtifactSessionID(withFile.ID, store.EvalArtifactRoleInput, "u"), ContentHash: "hash",
	}, nil); setErr != nil {
		t.Fatalf("set artifact: %v", setErr)
	}

	res, err := svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{
		{SequenceNumber: 1, Prompt: "review the attached file carefully"},
		{SequenceNumber: 2, Prompt: "text only"},
	}, "alice")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Updates != 1 || res.Deletes != 0 {
		t.Fatalf("res = %+v, want exactly one update and no deletes", res)
	}

	after, err := svcs.examples.GetByID(ctx, withFile.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Prompt != "review the attached file carefully" {
		t.Errorf("prompt = %q, want the reconciled value", after.Prompt)
	}
	row := after.ArtifactByRole(store.EvalArtifactRoleInput)
	if row == nil || row.Filename != "config.yaml" {
		t.Fatalf("artifact row = %+v, want it preserved across the reconcile", row)
	}
	if row.Version != 0 || row.ContentHash != "hash" {
		t.Errorf("artifact row = %+v, want version 0 and the original hash preserved", row)
	}
}

func TestReconcileExamples_DeletionTakesTheArtifact(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	dsID := mustCreateDataset(ctx, t, svcs.dataset, "RX-artifact-delete")

	if _, err := svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{
		{SequenceNumber: 1, Prompt: "keep me"},
		{SequenceNumber: 2, Prompt: "drop me"},
	}, "alice"); err != nil {
		t.Fatalf("seed reconcile: %v", err)
	}
	rows, err := svcs.examples.ListByDataset(ctx, dsID)
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	doomed := rows[1]
	if setErr := svcs.artifacts.Upsert(ctx, &store.EvalExampleArtifact{
		ExampleID: doomed.ID, Role: store.EvalArtifactRoleInput,
		Filename: "doomed.yaml", SizeBytes: 3, Version: 0,
		SessionID: evalExampleArtifactSessionID(doomed.ID, store.EvalArtifactRoleInput, "u"), ContentHash: "hash",
	}, nil); setErr != nil {
		t.Fatalf("set artifact: %v", setErr)
	}

	res, err := svcs.dataset.ReconcileExamples(ctx, dsID, []DesiredExample{
		{SequenceNumber: 1, Prompt: "keep me"},
	}, "alice")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Deletes != 1 {
		t.Fatalf("deletes = %d, want 1", res.Deletes)
	}
	if _, err := svcs.examples.GetByID(ctx, doomed.ID); !errors.Is(err, store.ErrEvalNotFound) {
		t.Errorf("get deleted example: err = %v, want ErrEvalNotFound", err)
	}
}
