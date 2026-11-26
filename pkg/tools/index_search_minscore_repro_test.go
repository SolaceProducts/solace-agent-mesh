// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func buildIndexZip(t *testing.T, docs map[string]string) []byte {
	t.Helper()
	builder := indexing.NewIndexBuilder(2000, 500)
	for name, text := range docs {
		builder.AddDocument(indexing.Document{
			Filename: name,
			Version:  1,
			Conversion: &convert.Result{
				Text:         text,
				CitationType: "line_range",
				Citations:    []convert.CitationRange{{Location: "lines_1_1", CharStart: 0, CharEnd: len(text)}},
			},
		})
	}
	built, err := builder.Build("test")
	if err != nil {
		t.Fatal(err)
	}
	if built.Manifest.ChunkCount == 0 {
		t.Fatalf("builder produced 0 chunks for valid text corpus")
	}
	zipData, err := indexing.SerializeToZIP(built)
	if err != nil {
		t.Fatal(err)
	}
	return zipData
}

func runIndexSearch(t *testing.T, zipData []byte, params map[string]any) *tool.Result {
	t.Helper()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ProjectID = "proj-x"
	toolCtx.ParentTaskID = "task-x"
	ctx := t.Context()
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "project_bm25_index.zip")
	if _, err := svc.Save(ctx, loc, zipData, "application/zip"); err != nil {
		t.Fatalf("save index: %v", err)
	}
	res, err := (&IndexSearchTool{}).Execute(ctx, params, toolCtx)
	if err != nil {
		t.Fatalf("Execute(%v): %v", params, err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	return res
}

func numResults(res *tool.Result) int {
	n, _ := res.Data["num_results"].(int)
	return n
}

func TestIndexSearch_MinScoreIsRelativeScale(t *testing.T) {
	zipData := buildIndexZip(t, map[string]string{
		"sagemaker.txt": "The SageMaker artifact relates to fine-tuning and specifying where Amazon SageMaker Unified Studio uploads the trained model artifact to an Amazon S3 output artifact location.",
		"billing.txt":   "Quarterly billing reconciliation covers invoices, credits, and outstanding balances across customer accounts.",
		"hr.txt":        "The employee handbook describes vacation policy, parental leave, and the annual performance review cycle.",
		"cooking.txt":   "A classic risotto requires arborio rice, warm stock added gradually, butter, and parmesan cheese.",
	})

	if got := numResults(runIndexSearch(t, zipData, map[string]any{"query": "SageMaker artifact"})); got == 0 {
		t.Fatalf("min_score=0 returned 0 results — index/search is broken independent of the filter")
	}

	if got := numResults(runIndexSearch(t, zipData, map[string]any{"query": "SageMaker artifact", "min_score": float64(10)})); got == 0 {
		t.Fatalf("min_score=10 dropped the relevant hit — raw-BM25 scale regression has returned")
	}
}

func TestIndexSearch_MinScoreStillFilters(t *testing.T) {
	zipData := buildIndexZip(t, map[string]string{
		"strong.txt": "revenue revenue revenue revenue revenue growth and quarterly revenue projections for revenue",
		"weak.txt":   "the appendix briefly mentions revenue once near unrelated logistics content here",
	})

	if got := numResults(runIndexSearch(t, zipData, map[string]any{"query": "revenue", "min_score": float64(0)})); got < 2 {
		t.Fatalf("min_score=0 should return both hits, got %d", got)
	}

	res := runIndexSearch(t, zipData, map[string]any{"query": "revenue", "min_score": float64(90)})
	if got := numResults(res); got != 1 {
		t.Fatalf("min_score=90 should keep only the top hit, got %d", got)
	}
	results, _ := res.Data["results"].([]idxSearchResult)
	if len(results) != 1 || results[0].Filename != "strong.txt" {
		t.Fatalf("expected only strong.txt to survive, got %+v", results)
	}
}

func TestIndexSearch_MinScoreTopHitAlwaysSurvives(t *testing.T) {
	single := buildIndexZip(t, map[string]string{
		"only.txt":  "the migration runbook covers cutover, rollback, and verification steps",
		"noise.txt": "unrelated culinary notes about braising and searing techniques",
	})
	res := runIndexSearch(t, single, map[string]any{"query": "migration runbook", "min_score": float64(50)})
	if got := numResults(res); got != 1 {
		t.Fatalf("single-hit corpus at min_score=50 should keep the hit, got %d", got)
	}
	results, _ := res.Data["results"].([]idxSearchResult)
	if len(results) != 1 || results[0].RelevanceScore != 1.0 {
		t.Fatalf("top hit RelevanceScore should be 1.0, got %+v", results)
	}

	two := buildIndexZip(t, map[string]string{
		"strong.txt": "revenue revenue revenue revenue revenue growth and quarterly revenue projections for revenue",
		"weak.txt":   "the appendix briefly mentions revenue once near unrelated logistics content here",
	})
	if got := numResults(runIndexSearch(t, two, map[string]any{"query": "revenue", "min_score": float64(100)})); got != 1 {
		t.Fatalf("min_score=100 should keep exactly the top hit, got %d", got)
	}

	if got := numResults(runIndexSearch(t, two, map[string]any{"query": "revenue", "min_score": float64(150)})); got != 1 {
		t.Fatalf("min_score=150 should clamp to 100 and keep the top hit, got %d", got)
	}
}
