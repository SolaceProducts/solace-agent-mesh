// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestPullDatasets_WritesTheExpectedFacts(t *testing.T) {
	c := datasetPlatformClient(t, wire.DatasetResponse{ID: "ds-1", Name: "orders"},
		wire.ExampleResponse{ID: "e1", DatasetID: "ds-1", SequenceNumber: 1, Prompt: "refund O-1",
			ExpectedFacts: &apiv2.ExpectedFacts{Required: []string{"O-1"}, Forbidden: []string{"escalated"}}},
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
	want := "sequence_number,prompt,expected_response,expected_facts\n" +
		`1,refund O-1,,"{""required"":[""O-1""],""forbidden"":[""escalated""]}"` + "\n"
	if string(csvBody) != want {
		t.Errorf("pulled CSV =\n%s\nwant\n%s", csvBody, want)
	}
}
