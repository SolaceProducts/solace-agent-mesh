// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	replyProse    = "Here are the products:"
	replyTemplate = "Here are the products:\n«««template_liquid: data=\"products.json\"\n{% for p in items %}- {{ p.name }}\n{% endfor %}\n»»»"
)

func TestReplyTextFinal(t *testing.T) {
	tests := []struct {
		name                     string
		adds                     [][2]string
		continuationParts        []string
		lastContent              string
		wantVisible, wantHistory string
	}{
		{
			name:        "a template turn keeps its block in history only",
			adds:        [][2]string{{replyProse, replyTemplate}},
			wantVisible: replyProse,
			wantHistory: replyTemplate,
		},
		{
			name:              "continuation parts lead both texts",
			adds:              [][2]string{{replyProse, replyTemplate}},
			continuationParts: []string{"Part one. "},
			wantVisible:       "Part one. " + replyProse,
			wantHistory:       "Part one. " + replyTemplate,
		},
		{
			name:        "a loop that added nothing ends with its last response",
			lastContent: "last words",
			wantVisible: "last words",
			wantHistory: "last words",
		},
		{
			name:        "a block-only reply is not replaced by the last response",
			adds:        [][2]string{{"", replyTemplate}},
			lastContent: "last words",
			wantVisible: "",
			wantHistory: replyTemplate,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r replyText
			for _, add := range tt.adds {
				r.add(add[0], add[1])
			}

			visible, history := r.final(tt.continuationParts, tt.lastContent)

			if visible != tt.wantVisible || history != tt.wantHistory {
				t.Errorf("final() = %q, %q; want %q, %q", visible, history, tt.wantVisible, tt.wantHistory)
			}
		})
	}
}

func discardRunContext() *taskRunContext {
	return newBlockValidationTestRunContext(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHandleCreatedArtifactsRecordsTheSaveBlockOnce(t *testing.T) {
	const block = "«««save_artifact: filename=\"r.md\" mime_type=\"text/markdown\"\n# Report\n»»»"
	for name, visible := range map[string]string{"answer and save": "Here is the report.", "save only": ""} {
		t.Run(name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			state := newTestTaskState(agent)
			var allText replyText
			streamResp := &llm.ChatResponse{Content: visible, RawContent: visible + "\n" + block}

			_, done, _ := agent.handleCreatedArtifacts(t.Context(), state, discardRunContext(), streamResp,
				[]artifactCreationRecord{{Filename: "r.md", CallID: "call-1", Status: "success"}}, rejectedToolCalls{}, &allText)

			if !done {
				t.Fatal("a turn that saved a file must be handled here")
			}
			_, history := allText.final(nil, "")
			saves := strings.Count(history, "«««save_artifact")
			for _, m := range state.Messages {
				saves += strings.Count(m.Content, "«««save_artifact")
			}
			if saves != 1 {
				t.Errorf("save block recorded %d times, want once", saves)
			}
		})
	}
}

func TestHandleCreatedArtifactsReturnsTheFilesTheAnswerSaved(t *testing.T) {
	cases := map[string]struct {
		visible string
		record  artifactCreationRecord
		want    int
	}{
		"not returned":     {"Here is the export.", artifactCreationRecord{Filename: "stock.csv"}, 1},
		"already returned": {"Here it is: «artifact_return:stock.csv»", artifactCreationRecord{Filename: "stock.csv"}, 1},
		"working file":     {"Config written.", artifactCreationRecord{Filename: "agent.yaml", Tags: []string{a2a.ArtifactTagWorking}}, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			state := newTestTaskState(agent)
			var allText replyText
			streamResp := &llm.ChatResponse{Content: tc.visible, RawContent: tc.visible}
			tc.record.CallID, tc.record.Status = "call-1", "success"

			agent.handleCreatedArtifacts(t.Context(), state, discardRunContext(), streamResp,
				[]artifactCreationRecord{tc.record}, rejectedToolCalls{}, &allText)

			visible, history := allText.final(nil, "")
			embed := "«artifact_return:" + tc.record.Filename + "»"
			if got := strings.Count(visible, embed); got != tc.want {
				t.Errorf("visible %q returns the file %d times, want %d", visible, got, tc.want)
			}
			if got := strings.Count(history, embed); got != tc.want {
				t.Errorf("history %q returns the file %d times, want %d", history, got, tc.want)
			}
		})
	}
}

func TestNamedFileReturns(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.recordToolSave(tool.ArtifactSavedInfo{Filename: "stock.csv", Version: 1})
	state.recordToolSave(tool.ArtifactSavedInfo{Filename: "agent.yaml", Version: 1, Tags: []string{a2a.ArtifactTagWorking}})
	state.recordToolSave(tool.ArtifactSavedInfo{Filename: "chart.png", Version: 1})
	peerResult := llm.ChatMessage{Role: "tool", ToolName: peerToolPrefix + "analyst", Content: `{"status":"success","artifacts":["deck.pptx:2"]}`}
	state.recordReportedFiles(peerResult)
	state.Messages = append(state.Messages, peerResult)
	state.HistoryOffset = len(state.Messages)

	answer := "Saved stock.csv (artifact_return:stock.csv), deck.pptx and agent.yaml. «artifact_return:chart.png» is the chart."
	got := namedFileReturns(answer, producedFiles(state))

	for _, name := range []string{"stock.csv", "deck.pptx"} {
		if !strings.Contains(got, "«artifact_return:"+name+"»") {
			t.Errorf("%s is named but not returned: %q", name, got)
		}
	}
	for _, name := range []string{"agent.yaml", "chart.png"} {
		if strings.Contains(got, name) {
			t.Errorf("%s must not be returned again: %q", name, got)
		}
	}
}

func TestNamedFileReturnsLeavesOutASaveBlockFile(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.CreatedArtifacts = []artifactCreationRecord{
		{Filename: "summary.md", Status: "success"},
		{Filename: "summary.html", Status: "success"},
	}
	state.recordToolSave(tool.ArtifactSavedInfo{Filename: "summary.pdf", Version: 1})

	got := namedFileReturns("summary.md is the summary and summary.pdf the PDF rendered from summary.html.", producedFiles(state))

	for _, name := range []string{"summary.md", "summary.html"} {
		if strings.Contains(got, name) {
			t.Errorf("%s already has its save card and must not be returned: %q", name, got)
		}
	}
	if !strings.Contains(got, "«artifact_return:summary.pdf»") {
		t.Errorf("the PDF a tool saved is named but not returned: %q", got)
	}
}

func TestHandleCreatedArtifactsGivesAFailedSaveANotifyTurn(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	var allText replyText
	streamResp := &llm.ChatResponse{
		Content:    "I saved the report.",
		RawContent: "I saved the report.\n«««save_artifact: filename=\"../r.md\"\n# Report\n»»»",
	}

	_, done, _ := agent.handleCreatedArtifacts(t.Context(), state, discardRunContext(), streamResp,
		[]artifactCreationRecord{{Filename: "../r.md", CallID: "call-1", Status: "error", ErrorMsg: "invalid filename"}}, rejectedToolCalls{}, &allText)

	if !done || state.finishAfterArtifacts {
		t.Fatalf("done=%v finishAfterArtifacts=%v, want the loop to continue to a notify turn", done, state.finishAfterArtifacts)
	}
	var notified bool
	for _, m := range state.Messages {
		notified = notified || (m.Role == "tool" && strings.Contains(m.Content, "invalid filename"))
	}
	if !notified {
		t.Error("the failed save must reach the model as a tool result")
	}
}

func TestBlockValidationRetryKeepsTheFailedBlockOutOfTheFinalTurn(t *testing.T) {
	agent, _, _ := newTestAgent()
	var allText replyText
	bve := &blockValidationError{
		VisibleText: replyProse,
		RawText:     replyProse + "\n«««template_liquid: data=\"absent.json\"\n{{",
		ErrorMsg:    "fix it",
		BlockType:   FencedBlockTemplate,
	}

	if _, outcome, _ := agent.handleStreamCallError(t.Context(), newTestTaskState(agent), discardRunContext(), nil, bve, &allText); outcome != loopContinue {
		t.Fatalf("outcome = %v, want loopContinue", outcome)
	}

	if _, history := allText.final(nil, ""); history != replyProse {
		t.Errorf("final-turn history = %q, want the visible text only", history)
	}
}

func TestFinalizeResponseRecordsTheHistoryTextAndShowsTheVisibleText(t *testing.T) {
	for _, compacted := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "after compaction"}[compacted], func(t *testing.T) {
			agent, _, _ := newTestAgent()
			state := newTestTaskState(agent)
			wantHistory, wantVisible := replyTemplate, replyProse
			if compacted {
				state.CompactionSummary = "summary"
				wantHistory += "\n\n---\n\n" + compactionNotice
				wantVisible += "\n\n---\n\n" + compactionNotice
			}

			resp, err := agent.finalizeResponse(t.Context(), state, discardRunContext(), replyProse, replyTemplate)
			if err != nil {
				t.Fatalf("finalizeResponse: %v", err)
			}

			last := state.Messages[len(state.Messages)-1]
			if last.Role != "assistant" || last.Content != wantHistory {
				t.Errorf("final assistant turn = %q, want %q", last.Content, wantHistory)
			}
			body, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if strings.Contains(string(body), "template_liquid") || !strings.Contains(string(body), jsonText(t, wantVisible)) {
				t.Errorf("response = %s; want the visible text %q and no template block", body, wantVisible)
			}
		})
	}
}

func jsonText(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}
	return strings.Trim(string(b), `"`)
}
