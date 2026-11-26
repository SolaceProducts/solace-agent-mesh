// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestDivergentAgentExtraConfigKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		blob string
		want []string
	}{
		{name: "one spelling", blob: `{"autoSummarization":{"enabled":true}}`},
		{name: "scalar copies agree", blob: `{"maxLlmCallsPerTask":3,"max_llm_calls_per_task":3}`},
		{name: "scalar copies differ", blob: `{"maxLlmCallsPerTask":3,"max_llm_calls_per_task":4}`, want: []string{"max_llm_calls_per_task"}},
		{name: "block copies agree", blob: `{"autoSummarization":{"enabled":true,"summaryMaxTokens":9},"auto_summarization":{"enabled":true,"summary_max_tokens":9}}`},
		{name: "block copies differ in a value", blob: `{"autoSummarization":{"enabled":true},"auto_summarization":{"enabled":false}}`, want: []string{"auto_summarization"}},
		{name: "block copies differ in a field", blob: `{"autoSummarization":{"enabled":true,"summaryMaxTokens":9},"auto_summarization":{"enabled":true}}`, want: []string{"auto_summarization"}},
		{name: "number spelling is not a difference", blob: `{"maxLlmCallsPerTask":3,"max_llm_calls_per_task":3.0}`},
		{name: "a pair inside one copy of a block", blob: `{"autoSummarization":{"summaryMaxTokens":1,"summary_max_tokens":2}}`, want: []string{"auto_summarization"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var blob map[string]any
			if err := json.Unmarshal([]byte(tc.blob), &blob); err != nil {
				t.Fatalf("blob: %v", err)
			}
			if got := DivergentAgentExtraConfigKeys(blob); !slices.Equal(got, tc.want) {
				t.Errorf("DivergentAgentExtraConfigKeys = %v, want %v", got, tc.want)
			}
		})
	}
}
