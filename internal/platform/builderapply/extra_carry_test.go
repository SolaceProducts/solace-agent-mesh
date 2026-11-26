// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"
)

func TestLayExtraConfig(t *testing.T) {
	tests := []struct {
		name        string
		stored      string
		stated      string
		want        string
		wantProblem string
	}{
		{
			name:   "set",
			stored: `{"maxTokens":512}`,
			stated: `{"maxTokens":1024}`,
			want:   `{"maxTokens":1024}`,
		},
		{
			name:   "absent key kept",
			stored: `{"maxTokens":512,"headless":true}`,
			stated: `{"headless":false}`,
			want:   `{"maxTokens":512,"headless":false}`,
		},
		{
			name:   "null removes",
			stored: `{"outputSchema":{"type":"object"},"maxTokens":512}`,
			stated: `{"outputSchema":null}`,
			want:   `{"maxTokens":512}`,
		},
		{
			name:   "nested merge",
			stored: `{"autoSummarization":{"enabled":true,"summaryMaxTokens":800}}`,
			stated: `{"autoSummarization":{"compactionPercentage":0.5}}`,
			want:   `{"autoSummarization":{"enabled":true,"summaryMaxTokens":800,"compactionPercentage":0.5}}`,
		},
		{
			name:   "nested null",
			stored: `{"autoSummarization":{"enabled":true,"summaryMaxTokens":800}}`,
			stated: `{"autoSummarization":{"enabled":null}}`,
			want:   `{"autoSummarization":{"summaryMaxTokens":800}}`,
		},
		{
			name:   "two levels deep",
			stored: `{"agentCard":{"version":"1","welcome":{"message":"a","suggestions":[{"label":"x"}]}}}`,
			stated: `{"agentCard":{"welcome":{"message":"b"}}}`,
			want:   `{"agentCard":{"version":"1","welcome":{"message":"b","suggestions":[{"label":"x"}]}}}`,
		},
		{
			name:   "opaque value replaced whole",
			stored: `{"outputSchema":{"properties":{"a":{},"b":{}}}}`,
			stated: `{"outputSchema":{"properties":{"a":{}}}}`,
			want:   `{"outputSchema":{"properties":{"a":{}}}}`,
		},
		{
			name:   "typed list replaced whole",
			stored: `{"agentCard":{"welcome":{"suggestions":[{"label":"x"},{"label":"y"}]}}}`,
			stated: `{"agentCard":{"welcome":{"suggestions":[{"label":"z"}]}}}`,
			want:   `{"agentCard":{"welcome":{"suggestions":[{"label":"z"}]}}}`,
		},
		{
			name:   "empty block creates nothing",
			stored: `{}`,
			stated: `{"autoSummarization":{}}`,
			want:   `{}`,
		},
		{
			name:   "null inside an absent block creates nothing",
			stored: `{"maxTokens":512}`,
			stated: `{"autoSummarization":{"enabled":null}}`,
			want:   `{"maxTokens":512}`,
		},
		{
			name:   "runtime spelling kept",
			stored: `{"supports_streaming":false}`,
			stated: `{"supportsStreaming":true}`,
			want:   `{"supports_streaming":true}`,
		},
		{
			name:   "runtime block keeps runtime children",
			stored: `{"auto_summarization":{"enabled":true}}`,
			stated: `{"autoSummarization":{"summaryMaxTokens":900}}`,
			want:   `{"auto_summarization":{"enabled":true,"summary_max_tokens":900}}`,
		},
		{
			name:   "both spellings are both set",
			stored: `{"supportsStreaming":true,"supports_streaming":false}`,
			stated: `{"supportsStreaming":false}`,
			want:   `{"supportsStreaming":false,"supports_streaming":false}`,
		},
		{
			name:   "both spellings, null removes both",
			stored: `{"supportsStreaming":true,"supports_streaming":false,"maxTokens":1}`,
			stated: `{"supportsStreaming":null}`,
			want:   `{"maxTokens":1}`,
		},
		{
			name:   "both spellings of a block, each merged",
			stored: `{"autoSummarization":{"enabled":true},"auto_summarization":{"enabled":false,"summary_max_tokens":5}}`,
			stated: `{"autoSummarization":{"summaryMaxTokens":900}}`,
			want:   `{"autoSummarization":{"enabled":true,"summaryMaxTokens":900},"auto_summarization":{"enabled":false,"summary_max_tokens":900}}`,
		},
		{
			name:   "both spellings, a key not stated is left alone",
			stored: `{"supportsStreaming":true,"supports_streaming":false}`,
			stated: `{"maxTokens":2}`,
			want:   `{"supportsStreaming":true,"supports_streaming":false,"maxTokens":2}`,
		},
		{
			name:        "camel inside a runtime block",
			stored:      `{"auto_summarization":{"summaryMaxTokens":1}}`,
			stated:      `{"autoSummarization":{"summaryMaxTokens":2}}`,
			want:        `{"auto_summarization":{"summaryMaxTokens":1}}`,
			wantProblem: "stored as summaryMaxTokens inside a block",
		},
		{
			name:        "stored block not an object",
			stored:      `{"autoSummarization":"on"}`,
			stated:      `{"autoSummarization":{"enabled":true}}`,
			want:        `{"autoSummarization":"on"}`,
			wantProblem: "cannot be merged into what is stored",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stored, err := decodeLikeDeploy([]byte(tc.stored))
			if err != nil {
				t.Fatalf("stored: %v", err)
			}
			stated, err := decodeLikeDeploy([]byte(tc.stated))
			if err != nil {
				t.Fatalf("stated: %v", err)
			}

			problems := layExtraConfig(stored, stated)

			got, err := marshalWithoutHTMLEscaping(stored)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !jsonEqual(t, got, []byte(tc.want)) {
				t.Errorf("stored after lay = %s, want %s", got, tc.want)
			}
			switch {
			case tc.wantProblem == "" && len(problems) != 0:
				t.Errorf("problems = %q, want none", problems)
			case tc.wantProblem != "" && (len(problems) != 1 || !strings.Contains(problems[0], tc.wantProblem)):
				t.Errorf("problems = %q, want one containing %q", problems, tc.wantProblem)
			}
		})
	}
}
