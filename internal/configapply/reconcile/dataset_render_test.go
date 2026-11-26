// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRenderDatasetPlan_Empty(t *testing.T) {
	var buf bytes.Buffer
	renderDatasetPlan(&buf, &datasetKindPlan{Kind: "dataset"}, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "datasets/") || !strings.Contains(out, "(no resources)") {
		t.Errorf("missing empty-state lines: %s", out)
	}
}

func TestRenderDatasetPlan_AllStatuses(t *testing.T) {
	plan := &datasetKindPlan{
		Kind:      "dataset",
		Creates:   []datasetOp{{Name: "fresh"}},
		Updates:   []datasetOp{{Name: "tweaked"}},
		Deletes:   []datasetOp{{Name: "obsolete"}},
		Unchanged: []datasetOp{{Name: "stable"}},
	}
	var buf bytes.Buffer
	renderDatasetPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{
		"datasets/", "+ fresh", "create",
		"~ tweaked", "update",
		"- obsolete", "delete",
		"= stable", "no change",
		"4 datasets: 1 create, 1 update, 1 delete, 1 unchanged",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\n%s", want, out)
		}
	}
}

func TestRenderApplyDatasets_NoEditsIsNoOp(t *testing.T) {
	var buf bytes.Buffer
	renderApplyDatasets(&buf, &datasetOutcome{Kind: "dataset"}, FormatOptions{NoColor: true})
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty outcome, got: %s", buf.String())
	}
}

func TestRenderApplyDatasets_Created_WithExampleSummary(t *testing.T) {
	out := &datasetOutcome{
		Kind:    "dataset",
		Created: []datasetOpResult{{Op: datasetOp{Name: "acme"}, Verb: "create"}},
		Examples: []datasetExamplesOutcome{
			{Name: "acme", Inserts: 3, Updates: 0, Deletes: 0, Unchanged: 0},
		},
	}
	var buf bytes.Buffer
	renderApplyDatasets(&buf, out, FormatOptions{NoColor: true})
	body := buf.String()
	if !strings.Contains(body, "Applied datasets:") {
		t.Errorf("missing heading: %s", body)
	}
	if !strings.Contains(body, "+ acme") || !strings.Contains(body, "created") {
		t.Errorf("missing created row: %s", body)
	}
	if !strings.Contains(body, "↳ examples: +3 ~0 -0 (0 unchanged)") {
		t.Errorf("missing example summary: %s", body)
	}
}

func TestRenderApplyDatasets_Failure(t *testing.T) {
	out := &datasetOutcome{
		Kind:     "dataset",
		Failures: []datasetOpResult{{Op: datasetOp{Name: "broken"}, Verb: "examples", Err: errors.New("boom")}},
	}
	var buf bytes.Buffer
	renderApplyDatasets(&buf, out, FormatOptions{NoColor: true})
	body := buf.String()
	if !strings.Contains(body, "! broken") || !strings.Contains(body, "examples failed") || !strings.Contains(body, "boom") {
		t.Errorf("missing failure row: %s", body)
	}
}

func TestRenderApplyDatasets_UnchangedWithCsvEdit(t *testing.T) {
	out := &datasetOutcome{
		Kind: "dataset",
		Examples: []datasetExamplesOutcome{
			{Name: "stable", Inserts: 1, Updates: 0, Deletes: 0, Unchanged: 4},
		},
	}
	var buf bytes.Buffer
	renderApplyDatasets(&buf, out, FormatOptions{NoColor: true})
	body := buf.String()
	if !strings.Contains(body, "= stable") || !strings.Contains(body, "↳ examples: +1 ~0 -0 (4 unchanged)") {
		t.Errorf("missing csv-only edit summary: %s", body)
	}
}

func TestWriteExampleSummary_NoOpWithoutName(t *testing.T) {
	var buf bytes.Buffer
	writeExampleSummary(&buf, datasetExamplesOutcome{}, FormatOptions{NoColor: true})
	if buf.Len() != 0 {
		t.Errorf("expected no output when ex.Name empty, got %s", buf.String())
	}
}

func TestWriteExampleSummary_RendersError(t *testing.T) {
	var buf bytes.Buffer
	writeExampleSummary(&buf, datasetExamplesOutcome{
		Name: "boom-set", Err: errors.New("network"),
	}, FormatOptions{NoColor: true})
	body := buf.String()
	if !strings.Contains(body, "↳ examples: failed: network") {
		t.Errorf("missing error summary: %s", body)
	}
}
