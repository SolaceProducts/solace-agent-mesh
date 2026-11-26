// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import "testing"

func TestExtractAnswersAnAnswerAbsentFromTheFormReadsAsNilToTheModel(t *testing.T) {
	questions := []any{
		map[string]any{"question": "Which policy?"},
		map[string]any{"question": "Anything else?"},
	}
	answers := extractAnswers(questions, map[string]any{"answers": map[string]any{"q1": "No"}})
	if answers["Which policy?"] != "<nil>" || answers["Anything else?"] != "No" {
		t.Errorf("answers %q, want {Which policy?: <nil>, Anything else?: No}", answers)
	}
}
