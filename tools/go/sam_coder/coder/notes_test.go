// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

var answerSchema = map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []string{"a"}}

func TestTheFirstEarlySubmitIsSentBackToReviewTheNotes(t *testing.T) {
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "write", map[string]any{"path": "NOTES.md", "content": "open: agent b\n"})),
		toolTurn(call("2", submitResultTool, map[string]any{"a": "early"})),
		toolTurn(call("3", submitResultTool, map[string]any{"a": "final"})),
	}}

	out, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", NoShell: true, MaxSteps: 30, OutputSchema: answerSchema, NotesFile: "NOTES.md", Chat: m.chat})
	if err != nil {
		t.Fatal(err)
	}

	if got := toolResults(m.calls[2])["2"]; !strings.Contains(got, "Not yet accepted. Read NOTES.md") {
		t.Errorf("first submit got %q", got)
	}
	if got, _ := out.Result.(map[string]any); got["a"] != "final" {
		t.Errorf("result = %v", out.Result)
	}
}

func TestAStaleNotesFileGetsAReminder(t *testing.T) {
	var replies []sdk.ChatResult
	for i := range notesReminderSteps + 1 {
		replies = append(replies, toolTurn(call(string(rune('a'+i)), "glob", map[string]any{"pattern": "*"})))
	}
	m := &scriptedModel{replies: append(replies, sdk.ChatResult{Content: "done", FinishReason: "stop"})}

	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", NoShell: true, Mode: ModePlan, NotesFile: "NOTES.md", Chat: m.chat}); err != nil {
		t.Fatal(err)
	}

	reminded := 0
	for id, result := range toolResults(m.calls[len(m.calls)-1]) {
		if strings.Contains(result, "NOTES.md has not changed in") {
			reminded++
			if id != string(rune('a'+notesReminderSteps)) {
				t.Errorf("reminder on call %s", id)
			}
		}
	}
	if reminded != 1 {
		t.Errorf("%d reminders, want 1", reminded)
	}
}

func TestWithoutANotesFileNothingChanges(t *testing.T) {
	m := &scriptedModel{replies: []sdk.ChatResult{toolTurn(call("1", submitResultTool, map[string]any{"a": "x"}))}}
	out, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", NoShell: true, MaxSteps: 30, OutputSchema: answerSchema, Chat: m.chat})
	if err != nil {
		t.Fatal(err)
	}
	if out.Result == nil {
		t.Error("submit was not accepted")
	}
}
