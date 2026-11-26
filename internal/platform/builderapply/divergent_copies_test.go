// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"strings"
	"testing"
)

const divergentCopiesWarning = "is stored in both spellings"

func TestApply_WarnsWhenABlockStoredInBothSpellingsStillDiffers(t *testing.T) {
	t.Parallel()
	const stateSummaryTokens = "auto_summarization: {summary_max_tokens: 900}"
	for _, tc := range []struct {
		name     string
		stored   string
		line     string
		wantWarn bool
	}{
		{
			name:     "a block whose copies differ in a field the save did not state",
			stored:   `{"autoSummarization":{"enabled":true},"auto_summarization":{"enabled":false,"summary_max_tokens":5}}`,
			line:     stateSummaryTokens,
			wantWarn: true,
		},
		{
			name:   "a block whose copies agree once the save has written both",
			stored: `{"autoSummarization":{"enabled":true,"summaryMaxTokens":5},"auto_summarization":{"enabled":true,"summary_max_tokens":7}}`,
			line:   stateSummaryTokens,
		},
		{
			name:   "a block whose copies agree up to number spelling",
			stored: `{"autoSummarization":{"enabled":true,"summaryMaxTokens":900},"auto_summarization":{"enabled":true,"summary_max_tokens":900.0}}`,
			line:   stateSummaryTokens,
		},
		{
			name:   "a scalar set in both spellings",
			stored: `{"maxLlmCallsPerTask":30,"max_llm_calls_per_task":99}`,
			line:   maxLLMCallsLine + "80",
		},
		{
			name:   "a differing pair the save does not write",
			stored: `{"autoSummarization":{"enabled":true},"auto_summarization":{"enabled":false},"maxLlmCallsPerTask":30}`,
			line:   maxLLMCallsLine + "80",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			seedStoredRuntimeConfig(t, svc, "reporter", json.RawMessage(tc.stored))
			component := agentComponentWithKey(t, "reporter", tc.line)

			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{component}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			var found []string
			for _, w := range opFor(t, resp, kindAgent, "reporter").Warnings {
				if strings.Contains(w, divergentCopiesWarning) {
					found = append(found, w)
				}
			}
			switch {
			case tc.wantWarn && len(found) != 1:
				t.Fatalf("warnings = %q, want exactly one %q warning", found, divergentCopiesWarning)
			case tc.wantWarn:
				for _, want := range []string{"auto_summarization", "autoSummarization", "differ", "deploy may use either", "state the whole auto_summarization block", "--target <target> --only agent --name <agent>", "keep one spelling of the block"} {
					if !strings.Contains(found[0], want) {
						t.Errorf("warning = %q, want it to contain %q", found[0], want)
					}
				}
			case len(found) != 0:
				t.Errorf("warnings = %q, want none", found)
			}

			again, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{component}}, testActor)
			if err != nil {
				t.Fatalf("second Apply: %v", err)
			}
			if op := opFor(t, again, kindAgent, "reporter"); op.Action != actionUnchanged {
				t.Errorf("a re-save plans %s %v, want %s", op.Action, op.ChangedFields, actionUnchanged)
			}
		})
	}
}
