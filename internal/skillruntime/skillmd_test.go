// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"strings"
	"testing"
)

func TestParseSkillMD(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		wantName       string
		wantDesc       string
		wantTags       []string
		wantBodyPrefix string
		wantErr        bool
	}{
		{
			name: "with frontmatter",
			content: `---
name: test-skill
description: A test skill
tags:
  - alpha
  - beta
---

# Body

Hello there.`,
			wantName:       "test-skill",
			wantDesc:       "A test skill",
			wantTags:       []string{"alpha", "beta"},
			wantBodyPrefix: "# Body",
		},
		{
			name:           "no frontmatter",
			content:        "# Just a body\n\nNo frontmatter here.",
			wantBodyPrefix: "# Just a body",
		},
		{
			name:    "malformed frontmatter",
			content: "---\nname: [unclosed\n---\n\nbody",
			wantErr: true,
		},
		{
			name: "frontmatter without trailing body",
			content: `---
name: only-fm
description: nothing else
---`,
			wantName: "only-fm",
			wantDesc: "nothing else",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := ParseSkillMD(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fm.Name != tt.wantName {
				t.Errorf("Name: got %q, want %q", fm.Name, tt.wantName)
			}
			if fm.Description != tt.wantDesc {
				t.Errorf("Description: got %q, want %q", fm.Description, tt.wantDesc)
			}
			if len(tt.wantTags) > 0 && !equalStrings(fm.Tags, tt.wantTags) {
				t.Errorf("Tags: got %v, want %v", fm.Tags, tt.wantTags)
			}
			if tt.wantBodyPrefix != "" && !strings.HasPrefix(body, tt.wantBodyPrefix) {
				t.Errorf("body prefix: got %q, want prefix %q", body, tt.wantBodyPrefix)
			}
		})
	}
}

func TestParseBundledToolsManifest(t *testing.T) {
	yaml := []byte(`
version: 1
tools:
  search_docs:
    description: Search the docs
    parameters:
      type: object
      properties:
        q:
          type: string
    instructions: Use this for documentation lookups.
  ping:
    description: Ping
`)
	tools, err := ParseBundledToolsManifest(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}

	byName := make(map[string]map[string]any, len(tools))
	for _, e := range tools {
		byName[e["tool_name"].(string)] = e
	}
	if byName["search_docs"]["description"] != "Search the docs" {
		t.Errorf("search_docs description wrong: %v", byName["search_docs"]["description"])
	}
	if byName["search_docs"]["instructions"] != "Use this for documentation lookups." {
		t.Errorf("search_docs instructions wrong: %v", byName["search_docs"]["instructions"])
	}
	if byName["search_docs"]["parameters"] == nil {
		t.Errorf("search_docs parameters missing")
	}
	if _, hasInstr := byName["ping"]["instructions"]; hasInstr {
		t.Errorf("ping should not have instructions key (none in YAML)")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
