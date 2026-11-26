// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestExampleResponseCarriesArtifacts(t *testing.T) {
	row := &store.EvalExample{
		ID: "e1", DatasetID: "d1", Prompt: "p",
		Artifacts: []*store.EvalExampleArtifact{
			{Role: store.EvalArtifactRoleInput, Filename: "config.yaml", ContentHash: "h"},
			{Role: store.EvalArtifactRoleExpected, Filename: "reference.md", ContentHash: "r"},
		},
	}

	resp := exampleResponseFromRow(row)
	if resp.InputArtifact == nil || resp.InputArtifact.Filename != "config.yaml" {
		t.Errorf("inputArtifact = %+v, want config.yaml", resp.InputArtifact)
	}
	if resp.ExpectedArtifact == nil || resp.ExpectedArtifact.Filename != "reference.md" {
		t.Errorf("expectedArtifact = %+v, want reference.md", resp.ExpectedArtifact)
	}
}

func TestExampleResponseOmitsAbsentArtifacts(t *testing.T) {
	resp := exampleResponseFromRow(&store.EvalExample{ID: "e2", DatasetID: "d1", Prompt: "p"})
	if resp.InputArtifact != nil || resp.ExpectedArtifact != nil {
		t.Errorf("inputArtifact = %+v, expectedArtifact = %+v; want both nil",
			resp.InputArtifact, resp.ExpectedArtifact)
	}
}
