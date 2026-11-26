// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "testing"

func TestToolResultStubBatchBytesIs24KiBAsTheDocsSay(t *testing.T) {
	if got := ToolResultStubBatchBytes("gemini", "gemini-4-neon"); got != 24576 {
		t.Errorf("Gemini batch = %d bytes, want 24576: the smart context management page promises 24 KiB (24,576 bytes)", got)
	}
}

func TestToolResultStubBatchBytesBatchesOnlyForGemini(t *testing.T) {
	tests := []struct {
		provider, model string
		want            int
	}{
		{"gemini", "gemini-4-neon", geminiToolResultStubBatchBytes},
		{"gemini", "tunedModels/acme-support", geminiToolResultStubBatchBytes},
		{"vertex", "Gemini-3.6-Flash", geminiToolResultStubBatchBytes},
		{"vertex", "vertex_ai/gemini-3.6-flash", geminiToolResultStubBatchBytes},
		{"openai", "Gemini-4-Neon", geminiToolResultStubBatchBytes},
		{"openrouter", "google/gemini-3", geminiToolResultStubBatchBytes},
		{"vertex", "claude-sonnet-5-5", 0},
		{"anthropic", "claude-sonnet-5-5", 0},
		{"bedrock", "anthropic.claude-haiku-5", 0},
		{"openai", "gpt-6-sol", 0},
		{"openai", "acme-router", 0},
		{"openai", "acme-geminiish-router", 0},
		{"", "", 0},
	}
	for _, tc := range tests {
		if got := ToolResultStubBatchBytes(tc.provider, tc.model); got != tc.want {
			t.Errorf("ToolResultStubBatchBytes(%q, %q) = %d, want %d", tc.provider, tc.model, got, tc.want)
		}
	}
}
