// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestReportedFiles_OnlyAdmitNamesASavedArtifactCouldHave(t *testing.T) {
	tests := map[string]struct {
		name string
		want bool
	}{
		"a plain file name":                        {"deck.pptx", true},
		"a name with spaces and brackets":          {"Q3 report (final).pdf", true},
		"a name outside ASCII":                     {"données.csv", true},
		"an empty name":                            {"", false},
		"a blank name":                             {"   ", false},
		"the current directory":                    {".", false},
		"the parent directory":                     {"..", false},
		"a path out of the folder":                 {"../secret.csv", false},
		"a path with a user and session in it":     {"bob/sess-b/secret.csv", false},
		"a path with backslashes":                  {`bob\secret.csv`, false},
		"a name with a hash":                       {"plan #2.md", true},
		"a name with a question mark":              {"what?.txt", true},
		"a name with a query string":               {"deck.pptx?version=9", true},
		"a name with a fragment":                   {"deck.pptx#page=2", true},
		"a version separator":                      {"deck.pptx:2", false},
		"a user-scope prefix":                      {"user:deck.pptx", false},
		"a newline":                                {"deck.pptx\nsecret.csv", false},
		"a control character":                      {"deck\x07.pptx", false},
		"an opening embed delimiter":               {"deck«.pptx", false},
		"a closing embed delimiter":                {"deck».pptx", false},
		"a metadata companion file":                {"deck.pptx" + artifact.MetadataSuffix, false},
		"a converted-text companion file":          {"deck.pptx" + artifact.ConvertedTextSuffix, false},
		"a file the search index keeps internally": {artifact.BM25IndexFilename, false},
		"a workflow hand-off file":                 {workflowHandoffName, false},
	}
	for label, tc := range tests {
		t.Run(label, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			state := newTestTaskState(agent)
			content, err := json.Marshal(map[string]any{"produced_artifacts": []any{map[string]any{"filename": tc.name}}})
			if err != nil {
				t.Fatalf("marshal the delegated result naming %q: %v", tc.name, err)
			}

			state.recordReportedFiles(llm.ChatMessage{Role: "tool", ToolName: peerToolPrefix + "analyst", Content: string(content)})

			var want []string
			if tc.want {
				want = []string{tc.name}
			}
			if !slices.Equal(state.ReportedFiles, want) {
				t.Fatalf("ReportedFiles = %q, want %q", state.ReportedFiles, want)
			}
		})
	}
}

func TestReportedFiles_LegacyListRefusesNamesASavedArtifactCouldNotHave(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	content := `{"artifacts":["deck.pptx.metadata.json:1","` + workflowHandoffName + `:0","../x.csv:1","deck.pptx:2"]}`

	state.recordReportedFiles(llm.ChatMessage{Role: "tool", ToolName: peerToolPrefix + "analyst", Content: content})

	if want := []string{"deck.pptx"}; !slices.Equal(state.ReportedFiles, want) {
		t.Fatalf("ReportedFiles = %q, want %q", state.ReportedFiles, want)
	}
}
