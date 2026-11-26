// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func pullDatasetMock(t *testing.T) *reconcile.Client {
	t.Helper()
	return datasetPlatformClient(t, wire.DatasetResponse{ID: "ds-1", Name: "acme-eval", Description: new("ACME corpus")},
		wire.ExampleResponse{ID: "e1", DatasetID: "ds-1", SequenceNumber: 1, Prompt: "capital of france?", ExpectedResponse: new("Paris")},
		wire.ExampleResponse{ID: "e2", DatasetID: "ds-1", SequenceNumber: 2, Prompt: "color of the sky?",
			InputArtifact:    &apiv2.ExampleArtifact{Filename: "flag.png"},
			ExpectedArtifact: &apiv2.ExampleArtifact{Filename: "reference.md"}},
	)
}

func datasetPlatformClient(t *testing.T, dataset wire.DatasetResponse, examples ...wire.ExampleResponse) *reconcile.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/evaluations/datasets", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.DatasetResponse{dataset})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/evaluations/datasets/"+dataset.ID+"/examples", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, examples)
	})
	return newV2PlatformClient(t, mux)
}

func TestPullDatasets_EmitsYAMLAndCSV(t *testing.T) {
	c := pullDatasetMock(t)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullDatasets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullDatasets: %v", err)
	}
	if len(names) != 1 || names[0] != "acme-eval" {
		t.Errorf("names = %+v", names)
	}
	if len(entries) != 2 {
		t.Fatalf("expected yaml + csv entries, got %d", len(entries))
	}

	var yamlEntry, csvEntry *fileEntry
	for i := range entries {
		switch {
		case strings.HasSuffix(entries[i].relPath, ".yaml"):
			yamlEntry = &entries[i]
		case strings.HasSuffix(entries[i].relPath, ".examples.csv"):
			csvEntry = &entries[i]
		}
	}
	if yamlEntry == nil || csvEntry == nil {
		t.Fatalf("missing entry: yaml=%v csv=%v", yamlEntry, csvEntry)
	}
	if !strings.Contains(string(yamlEntry.content), "examples_file: acme-eval.examples.csv") {
		t.Errorf("yaml missing examples_file ref:\n%s", yamlEntry.content)
	}
	if !strings.HasPrefix(string(csvEntry.content), "sequence_number,prompt,expected_response\n") {
		t.Errorf("csv missing canonical header: %q", csvEntry.content)
	}
	if !strings.Contains(string(csvEntry.content), "1,capital of france?,Paris") {
		t.Errorf("csv missing first row: %q", csvEntry.content)
	}
}

func TestPullDatasets_NameFilter(t *testing.T) {
	c := pullDatasetMock(t)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	_, names, err := pullDatasets(t.Context(), c, Options{Name: "other"}, res)
	if err != nil {
		t.Fatalf("pullDatasets: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected name filter to drop everything; got %v", names)
	}
}

func TestPullDatasets_WarnsAboutAttachments(t *testing.T) {
	c := pullDatasetMock(t)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	if _, _, err := pullDatasets(t.Context(), c, Options{}, res); err != nil {
		t.Fatalf("pullDatasets: %v", err)
	}

	var found string
	for _, w := range res.Warnings {
		if strings.Contains(w, "example attachments") {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("warnings = %v, want one naming the dropped attachments", res.Warnings)
	}
	for _, want := range []string{`"acme-eval"`, "2 example attachments", "will not restore them"} {
		if !strings.Contains(found, want) {
			t.Errorf("warning %q missing %q", found, want)
		}
	}
}
