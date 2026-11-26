// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestPullDatasets_WritesInputDataExactly(t *testing.T) {
	c := datasetPlatformClient(t, wire.DatasetResponse{ID: "ds-1", Name: "orders"},
		wire.ExampleResponse{ID: "e1", DatasetID: "ds-1", SequenceNumber: 1, Prompt: "plain"},
		wire.ExampleResponse{ID: "e2", DatasetID: "ds-1", SequenceNumber: 2, InputData: apiv2.JSONObject(`{"note":"<a&b>","orderId":9007199254740993}`)},
	)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullDatasets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullDatasets: %v", err)
	}
	var csvBody []byte
	for _, entry := range entries {
		if strings.HasSuffix(entry.relPath, ".examples.csv") {
			csvBody = entry.content
		}
	}
	want := "sequence_number,prompt,expected_response,input_data\n" +
		"1,plain,,\n" +
		`2,,,"{""note"":""<a&b>"",""orderId"":9007199254740993}"` + "\n"
	if string(csvBody) != want {
		t.Fatalf("pulled CSV =\n%s\nwant\n%s", csvBody, want)
	}
	specs, err := translate.ParseExamplesCSV(csvBody)
	if err != nil || len(specs) != 2 || string(specs[1].InputData) != `{"note":"<a&b>","orderId":9007199254740993}` {
		t.Errorf("re-reading the pull = %+v, %v", specs, err)
	}
}

func TestPullDatasets_WritesTheExpectedPath(t *testing.T) {
	c := datasetPlatformClient(t, wire.DatasetResponse{ID: "ds-1", Name: "orders"},
		wire.ExampleResponse{ID: "e1", DatasetID: "ds-1", SequenceNumber: 1, Prompt: "refund O-1",
			ExpectedPath: &apiv2.ExpectedPath{Nodes: []apiv2.ExpectedNode{{NodeID: "approve", Expectation: "ran"}}}},
	)

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullDatasets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullDatasets: %v", err)
	}
	var csvBody []byte
	for _, entry := range entries {
		if strings.HasSuffix(entry.relPath, ".examples.csv") {
			csvBody = entry.content
		}
	}
	want := "sequence_number,prompt,expected_response,expected_path\n" +
		`1,refund O-1,,"{""nodes"":{""approve"":""ran""}}"` + "\n"
	if string(csvBody) != want {
		t.Errorf("pulled CSV =\n%s\nwant\n%s", csvBody, want)
	}
}
