// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "testing"

func TestUsageBuckets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		usage Usage
		want  TokenBuckets
	}{
		{
			name:  "no cache",
			usage: Usage{PromptTokens: 100, CompletionTokens: 10},
			want:  TokenBuckets{Input: 100, Output: 10},
		},
		{
			name:  "cache reads and writes",
			usage: Usage{PromptTokens: 1000, CachedTokens: 800, CacheWriteTokens: 300, CompletionTokens: 50},
			want:  TokenBuckets{Input: 200, CacheRead: 500, CacheWrite: 300, Output: 50},
		},
		{
			name:  "cache write exceeds cached",
			usage: Usage{PromptTokens: 100, CachedTokens: 40, CacheWriteTokens: 60, CompletionTokens: 10},
			want:  TokenBuckets{Input: 60, CacheWrite: 40, Output: 10},
		},
		{
			name:  "cached exceeds prompt",
			usage: Usage{PromptTokens: 100, CachedTokens: 120, CacheWriteTokens: 150, CompletionTokens: 10},
			want:  TokenBuckets{CacheWrite: 100, Output: 10},
		},
		{
			name:  "negative counts",
			usage: Usage{PromptTokens: -5, CachedTokens: -1, CacheWriteTokens: -1, CompletionTokens: -3},
			want:  TokenBuckets{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.usage.Buckets(); got != tt.want {
				t.Errorf("Buckets() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
