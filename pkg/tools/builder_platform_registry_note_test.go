// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"
)

const registryNoteFragment = "not names an agent binds under `tools:`"

func TestPlatformToolPreviews_SayTheToolListIsNotAMenuOfBindableNames(t *testing.T) {
	registered := []map[string]any{{"name": "execute_sql_query", "id": "t1"}, {"name": "xlsx_read", "id": "t2"}}
	agents := []map[string]any{{"name": "Orchestrator", "id": "a1"}}
	listings := map[string]componentListing{
		"tool":  {items: registered, total: 2},
		"agent": {items: agents, total: 1},
	}

	tests := []struct {
		name     string
		preview  string
		wantNote bool
	}{
		{"single-type tool list", buildSingleTypePreview("tool", listings["tool"]), true},
		{"all-types list with tools", buildComponentListPreview(listings, nil, []string{"agent", "tool"}, 3), true},
		{"single-type agent list", buildSingleTypePreview("agent", listings["agent"]), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Contains(tt.preview, registryNoteFragment); got != tt.wantNote {
				t.Errorf("registry note present = %v, want %v:\n%s", got, tt.wantNote, tt.preview)
			}
		})
	}
}

func TestPlatformToolPreviews_TheNoteSitsUnderTheToolsHeadingOnly(t *testing.T) {
	listings := map[string]componentListing{
		"tool":  {items: []map[string]any{{"name": "execute_sql_query", "id": "t1"}}, total: 1},
		"agent": {items: []map[string]any{{"name": "Orchestrator", "id": "a1"}}, total: 1},
	}

	preview := buildComponentListPreview(listings, nil, []string{"agent", "tool"}, 2)

	if strings.Count(preview, registryNoteFragment) != 1 {
		t.Fatalf("the note should appear once:\n%s", preview)
	}
	toolsAt, noteAt, itemAt := strings.Index(preview, "Tools ("), strings.Index(preview, registryNoteFragment), strings.Index(preview, "execute_sql_query")
	if toolsAt < 0 || noteAt < toolsAt || itemAt < noteAt {
		t.Errorf("the note should follow the Tools heading and precede its items:\n%s", preview)
	}
}
