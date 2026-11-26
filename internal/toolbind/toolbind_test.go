// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolbind

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestAdvice(t *testing.T) {
	tests := []struct {
		tool string
		want []string
	}{
		{"xlsx_read", []string{"built-in skill sam-excel", "skills:", "- name: sam-excel"}},
		{"xlsx_write", []string{"sam-excel"}},
		{"docx_create_document", []string{"sam-docx"}},
		{"execute_sql_query", []string{"SQL connectors", "`type: sql`", "depends_on and connectors", "only when the user named the system", "leave it out"}},
		{"graph_db_query", []string{"graph database connectors", "`type: graph_db`"}},
		{"document_db_query", []string{"document database connectors", "`type: document_db`"}},
		{"search_query", []string{"search connectors", "`type: search`"}},
		{"query_bedrock_kb", []string{"knowledge base connectors", "`type: knowledge_base`"}},
		{"slack", []string{"Slack connectors", "`type: slack`"}},
		{"slack_update", []string{"Slack connectors", "`type: slack`"}},
		{"send_email", []string{"email connectors", "`type: email`"}},
		{"event_mesh_request", []string{"event mesh connectors", "`type: event_mesh`"}},
		{"no_such_tool", []string{"exact snake_case name", "builtin-group"}},
		{"sub_task", []string{"every agent already has it", "drop this entry"}},
		{"index_search", []string{"project chat whose files are indexed", "drop this entry"}},
		{"select_voice", []string{"belongs to no built-in group", "tell the user it is not available"}},
		{"transcribe_audio", []string{"belongs to no built-in group", "tell the user it is not available"}},
		{"sam-excel__xlsx_read", []string{"built-in skill sam-excel", "- name: sam-excel"}},
		{"sam-docx__docx_create_document", []string{"sam-docx"}},
		{"sam-docx__xlsx_read", []string{"exact snake_case name"}},
		{"nosuchskill__xlsx_read", []string{"exact snake_case name"}},
		{"mark_changes_applied", []string{"exact snake_case name"}},
		{"generate_test_plan", []string{"exact snake_case name"}},
		{"", []string{"builtin-group"}},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := Advice(tt.tool)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("Advice(%q) = %q, want it to contain %q", tt.tool, got, want)
				}
			}
		})
	}
}

func TestAdviceNeverTellsAnAutoRegisteredToolIsMissing(t *testing.T) {
	for tool := range schema.AutoRegisteredBuiltinToolNames {
		got := Advice(tool)
		for _, wrong := range []string{"not available", "tell the user", "builtin-group", "snake_case"} {
			if strings.Contains(got, wrong) {
				t.Errorf("Advice(%q) = %q; it contains %q, but the agent has this tool without an entry", tool, got, wrong)
			}
		}
		if !strings.Contains(got, "drop this entry") {
			t.Errorf("Advice(%q) = %q; it does not tell the author to drop the entry", tool, got)
		}
	}
}

func TestEveryRouteOwnsDistinctNames(t *testing.T) {
	for tool := range ConnectorRuntimes {
		if skill, ok := builtinskills.SkillProvidingTool(tool); ok {
			t.Errorf("%q is both a connector runtime and a tool of the built-in skill %s", tool, skill)
		}
		if schema.UngroupedBuiltinToolNames[tool] {
			t.Errorf("%q is both a connector runtime and an ungrouped built-in tool", tool)
		}
	}
	for tool := range schema.UngroupedBuiltinToolNames {
		if skill, ok := builtinskills.SkillProvidingTool(tool); ok {
			t.Errorf("%q is both an ungrouped built-in tool and a tool of the built-in skill %s", tool, skill)
		}
	}
}

func TestAdviceNeverPointsASkillToolAtAConnector(t *testing.T) {
	if got := Advice("xlsx_read"); strings.Contains(got, "connector") {
		t.Errorf("a skill's tool was explained as a connector one: %q", got)
	}
}

func TestExplainNamesEveryToolInOrder(t *testing.T) {
	got := Explain([]string{"docx_create_document", "execute_sql_query", "xlsx_read"})

	last := -1
	for _, tool := range []string{`"docx_create_document"`, `"execute_sql_query"`, `"xlsx_read"`} {
		at := strings.Index(got, tool)
		if at < 0 {
			t.Fatalf("Explain omitted %s: %s", tool, got)
		}
		if at < last {
			t.Errorf("Explain reordered the tools: %s", got)
		}
		last = at
	}
	if Explain(nil) != "" {
		t.Errorf("Explain(nil) = %q, want empty", Explain(nil))
	}
}

func TestFindingAdviceTakesAdvicesRoutesInAnOperatorsWords(t *testing.T) {
	tests := []struct {
		tool    string
		want    []string
		wantNot []string
	}{
		{"xlsx_read", []string{"built-in skill sam-excel", "attach the skill sam-excel"}, nil},
		{"sam-excel__xlsx_read", []string{"sam-excel"}, nil},
		{"execute_sql_query", []string{"SQL connectors", "`type: sql`", "attach a SQL connector"}, nil},
		{"sub_task", []string{"every agent already has it", "need not name it"}, nil},
		{"select_voice", []string{"belongs to no built-in group", "front matter tools: of a custom skill"}, []string{"agent's or a skill's"}},
		{"pptx_markdown_to_pptx", []string{"provides it", "replace it with a tool the agent has"}, nil},
		{"web_request", []string{"nothing this agent is given provides it", "grant that toolset to the agent"}, []string{"no built-in tool, group"}},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := FindingAdvice(tt.tool)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("FindingAdvice(%q) = %q, want it to contain %q", tt.tool, got, want)
				}
			}
			for _, bad := range tt.wantNot {
				if strings.Contains(got, bad) {
					t.Errorf("FindingAdvice(%q) = %q, must not contain %q", tt.tool, got, bad)
				}
			}
			for _, builderOnly := range []string{"build-manifest", "depends_on", "tell the user"} {
				if strings.Contains(got, builderOnly) {
					t.Errorf("FindingAdvice(%q) = %q speaks to the Builder (%q)", tt.tool, got, builderOnly)
				}
			}
		})
	}
}
