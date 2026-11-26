// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestMarkJudgeUnreadable(t *testing.T) {
	record := func() *string {
		s := `{"filename":"a.pdf","version":1}`
		return &s
	}
	unreadable := []ArtifactBlock{ArtifactPlaceholder{Filename: "a.pdf", Reason: artifactReasonExampleUnavailable}}
	binary := []ArtifactBlock{ArtifactPlaceholder{Filename: "a.bin", Reason: "binary"}}

	marked := func(field *string) bool {
		if field == nil {
			return false
		}
		var rec exampleArtifactRecord
		if err := json.Unmarshal([]byte(*field), &rec); err != nil {
			t.Fatal(err)
		}
		return rec.JudgeUnreadable
	}

	result := &store.EvalRunResult{InputArtifactMetadata: record(), ExpectedArtifactMetadata: record()}
	markJudgeUnreadable(result, store.EvalArtifactRoleInput, unreadable)
	if !marked(result.InputArtifactMetadata) || marked(result.ExpectedArtifactMetadata) {
		t.Errorf("input role: input=%v expected=%v, want only the input marked", *result.InputArtifactMetadata, *result.ExpectedArtifactMetadata)
	}

	result = &store.EvalRunResult{InputArtifactMetadata: record(), ExpectedArtifactMetadata: record()}
	markJudgeUnreadable(result, store.EvalArtifactRoleExpected, unreadable)
	if marked(result.InputArtifactMetadata) || !marked(result.ExpectedArtifactMetadata) {
		t.Errorf("expected role: input=%v expected=%v, want only the expected marked", *result.InputArtifactMetadata, *result.ExpectedArtifactMetadata)
	}

	result = &store.EvalRunResult{InputArtifactMetadata: record()}
	markJudgeUnreadable(result, store.EvalArtifactRoleInput, binary)
	if marked(result.InputArtifactMetadata) {
		t.Error("a binary file's placeholder marked the record")
	}

	result = &store.EvalRunResult{}
	markJudgeUnreadable(result, store.EvalArtifactRoleInput, unreadable)
	if result.InputArtifactMetadata != nil {
		t.Error("a result without a file record got one")
	}

	broken := "{"
	result = &store.EvalRunResult{InputArtifactMetadata: &broken}
	markJudgeUnreadable(result, store.EvalArtifactRoleInput, unreadable)
	if *result.InputArtifactMetadata != "{" {
		t.Errorf("an unreadable record changed to %q", *result.InputArtifactMetadata)
	}
}
