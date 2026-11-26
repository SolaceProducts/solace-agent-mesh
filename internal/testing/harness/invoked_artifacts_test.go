// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestInvokedArtifactsForMetadata(t *testing.T) {
	in := []ArtifactRef{
		{Filename: "sales.csv", Version: 0},
		{Filename: "notes.txt", Version: 3},
	}
	got := invokedArtifactsForMetadata(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	first, ok := got[0].(map[string]any)
	if !ok {
		t.Fatalf("entry[0] type = %T, want map[string]any", got[0])
	}
	if first["filename"] != "sales.csv" {
		t.Errorf("entry[0].filename = %v, want sales.csv", first["filename"])
	}
	if first["version"] != 0 {
		t.Errorf("entry[0].version = %v, want 0", first["version"])
	}

	second, ok := got[1].(map[string]any)
	if !ok {
		t.Fatalf("entry[1] type = %T, want map[string]any", got[1])
	}
	if second["version"] != 3 {
		t.Errorf("entry[1].version = %v, want 3", second["version"])
	}
}

func TestInvokedArtifactsForMetadataEmpty(t *testing.T) {
	if got := invokedArtifactsForMetadata(nil); len(got) != 0 {
		t.Errorf("nil input -> len = %d, want 0", len(got))
	}
	if got := invokedArtifactsForMetadata([]ArtifactRef{}); len(got) != 0 {
		t.Errorf("empty slice -> len = %d, want 0", len(got))
	}
}

func TestAssertPromptContainsArtifactSummary(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expected    []ArtifactRef
		wantFailure bool
	}{
		{
			name:        "go header present",
			content:     "please process --- Metadata for artifact 'sales.csv' (v0) ---\nmore stuff",
			expected:    []ArtifactRef{{Filename: "sales.csv", Version: 0}},
			wantFailure: false,
		},
		{
			name:        "python alt header present",
			content:     "context: Artifact: 'notes.txt' (version: 3) follows",
			expected:    []ArtifactRef{{Filename: "notes.txt", Version: 3}},
			wantFailure: false,
		},
		{
			name:        "neither header -> failure",
			content:     "no summary here at all",
			expected:    []ArtifactRef{{Filename: "sales.csv", Version: 0}},
			wantFailure: true,
		},
		{
			name:        "wrong filename -> failure",
			content:     "--- Metadata for artifact 'other.csv' (v0) ---",
			expected:    []ArtifactRef{{Filename: "sales.csv", Version: 0}},
			wantFailure: true,
		},
		{
			name:        "wrong version -> failure",
			content:     "--- Metadata for artifact 'sales.csv' (v5) ---",
			expected:    []ArtifactRef{{Filename: "sales.csv", Version: 0}},
			wantFailure: true,
		},
		{
			name:        "empty filename skipped",
			content:     "",
			expected:    []ArtifactRef{{Filename: ""}},
			wantFailure: false,
		},
		{
			name:        "version-less fallback header accepted",
			content:     "--- Metadata for artifact 'sales.csv' ---",
			expected:    []ArtifactRef{{Filename: "sales.csv", Version: 0}},
			wantFailure: false,
		},
		{
			name:        "v N/A header accepted",
			content:     "--- Metadata for artifact 'report.txt' (vN/A) ---",
			expected:    []ArtifactRef{{Filename: "report.txt", Version: 0}},
			wantFailure: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := mockllm.RecordedRequest{
				Messages: []llm.ChatMessage{
					{Role: "user", Content: tc.content},
				},
			}
			interaction := LLMInteraction{
				ExpectedRequest: &ExpectedLLMRequest{
					PromptContainsArtifactSummaryFor: tc.expected,
				},
			}
			sub := &testing.T{}
			AssertLLMRequests(sub, []mockllm.RecordedRequest{req}, []LLMInteraction{interaction})
			if got := sub.Failed(); got != tc.wantFailure {
				t.Errorf("AssertLLMRequests failure = %v, want %v", got, tc.wantFailure)
			}
		})
	}
}
