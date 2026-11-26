// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"testing"
)

func TestMatchJSONContains(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected map[string]any
		wantErr  bool
	}{
		{
			name:     "exact string match",
			json:     `{"name": "alice", "age": 30}`,
			expected: map[string]any{"name": "alice"},
			wantErr:  false,
		},
		{
			name:     "missing key",
			json:     `{"name": "alice"}`,
			expected: map[string]any{"age": 30},
			wantErr:  true,
		},
		{
			name:     "wrong string value",
			json:     `{"name": "alice"}`,
			expected: map[string]any{"name": "bob"},
			wantErr:  true,
		},
		{
			name:     "number match",
			json:     `{"count": 42}`,
			expected: map[string]any{"count": 42.0},
			wantErr:  false,
		},
		{
			name:     "int match via float",
			json:     `{"count": 42}`,
			expected: map[string]any{"count": 42},
			wantErr:  false,
		},
		{
			name:     "boolean match",
			json:     `{"active": true}`,
			expected: map[string]any{"active": true},
			wantErr:  false,
		},
		{
			name:     "boolean mismatch",
			json:     `{"active": true}`,
			expected: map[string]any{"active": false},
			wantErr:  true,
		},
		{
			name:     "nested object match",
			json:     `{"user": {"name": "alice", "role": "admin"}}`,
			expected: map[string]any{"user": map[string]any{"name": "alice"}},
			wantErr:  false,
		},
		{
			name:     "nested object mismatch",
			json:     `{"user": {"name": "alice"}}`,
			expected: map[string]any{"user": map[string]any{"name": "bob"}},
			wantErr:  true,
		},
		{
			name:     "array match",
			json:     `{"items": ["a", "b", "c"]}`,
			expected: map[string]any{"items": []any{"a", "b"}},
			wantErr:  false,
		},
		{
			name:     "array too few elements",
			json:     `{"items": ["a"]}`,
			expected: map[string]any{"items": []any{"a", "b"}},
			wantErr:  true,
		},
		{
			name:     "regex directive match",
			json:     `{"id": "abc-123-def"}`,
			expected: map[string]any{"id": map[string]any{"_regex": `^abc-\d+-def$`}},
			wantErr:  false,
		},
		{
			name:     "regex directive mismatch",
			json:     `{"id": "xyz"}`,
			expected: map[string]any{"id": map[string]any{"_regex": `^abc`}},
			wantErr:  true,
		},
		{
			name:     "matches_regex suffix",
			json:     `{"filename": "report_2024.pdf"}`,
			expected: map[string]any{"filename_matches_regex": `report_\d+\.pdf`},
			wantErr:  false,
		},
		{
			name:     "contains suffix",
			json:     `{"description": "the quick brown fox"}`,
			expected: map[string]any{"description_contains": []any{"quick", "fox"}},
			wantErr:  false,
		},
		{
			name:     "contains suffix mismatch",
			json:     `{"description": "the quick brown fox"}`,
			expected: map[string]any{"description_contains": []any{"lazy"}},
			wantErr:  true,
		},
		{
			name:     "invalid JSON input",
			json:     `not json`,
			expected: map[string]any{"key": "val"},
			wantErr:  true,
		},
		{
			name:     "empty expected matches anything",
			json:     `{"a": 1, "b": 2}`,
			expected: map[string]any{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := MatchJSONContains([]byte(tt.json), tt.expected)
			if (err != nil) != tt.wantErr {
				t.Errorf("MatchJSONContains() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCleanBase64(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already valid with double padding",
			input: "SGVsbG8=",
			want:  "SGVsbG8=",
		},
		{
			name:  "valid with double equals",
			input: "YQ==",
			want:  "YQ==",
		},
		{
			name:  "trailing garbage after padding",
			input: "SGVsbG8=\nsome garbage",
			want:  "SGVsbG8=",
		},
		{
			name:  "no padding needs re-padding mod 2",
			input: "YQ",
			want:  "YQ==",
		},
		{
			name:  "no padding needs re-padding mod 3",
			input: "YWJ",
			want:  "YWJ=",
		},
		{
			name:  "clean base64 no padding mod 0",
			input: "YWJj",
			want:  "YWJj",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanBase64(tt.input)
			if got != tt.want {
				t.Errorf("cleanBase64(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMatchPartText(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		pm      PartMatch
		wantErr bool
	}{
		{
			name:    "text_exact match",
			text:    "hello world",
			pm:      PartMatch{TextExact: "hello world"},
			wantErr: false,
		},
		{
			name:    "text_exact mismatch",
			text:    "hello world",
			pm:      PartMatch{TextExact: "goodbye"},
			wantErr: true,
		},
		{
			name:    "text_contains match",
			text:    "the quick brown fox",
			pm:      PartMatch{TextContains: []string{"quick", "fox"}},
			wantErr: false,
		},
		{
			name:    "text_contains mismatch",
			text:    "the quick brown fox",
			pm:      PartMatch{TextContains: []string{"lazy"}},
			wantErr: true,
		},
		{
			name:    "text_not_contains pass",
			text:    "hello world",
			pm:      PartMatch{TextNotContains: []string{"goodbye"}},
			wantErr: false,
		},
		{
			name:    "text_not_contains fail",
			text:    "hello world",
			pm:      PartMatch{TextNotContains: []string{"hello"}},
			wantErr: true,
		},
		{
			name:    "text_regex match",
			text:    "version 1.2.3",
			pm:      PartMatch{TextRegex: []string{`\d+\.\d+\.\d+`}},
			wantErr: false,
		},
		{
			name:    "text_regex mismatch",
			text:    "no numbers",
			pm:      PartMatch{TextRegex: []string{`\d+`}},
			wantErr: true,
		},
		{
			name:    "empty part match passes",
			text:    "anything",
			pm:      PartMatch{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := MatchPartText(tt.text, tt.pm)
			if (err != nil) != tt.wantErr {
				t.Errorf("MatchPartText() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMatchWildcard(t *testing.T) {
	if !MatchWildcard("anything", "*") {
		t.Error("wildcard '*' should match anything")
	}
	if !MatchWildcard("exact", "exact") {
		t.Error("exact match should return true")
	}
	if MatchWildcard("hello", "world") {
		t.Error("non-matching should return false")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short string should not be truncated: %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("long string should be truncated: %q", got)
	}
}
