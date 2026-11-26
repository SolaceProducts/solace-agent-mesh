// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestRepairDanglingToolCalls_NoHistory(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "You are an agent."},
		{Role: "user", Content: "Hello"},
	}
	got, gotOffset := repairDanglingToolCalls(messages, 1, testLogger(), nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if gotOffset != 1 {
		t.Fatalf("expected historyEnd 1, got %d", gotOffset)
	}
}

func TestRepairDanglingToolCalls_CleanHistory(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "prev question"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
		}},
		{Role: "tool", Content: `{"status":"success"}`, ToolCallID: "call-1"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 5
	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)
	if len(got) != len(messages) {
		t.Fatalf("expected %d messages unchanged, got %d", len(messages), len(got))
	}
	if gotOffset != historyEnd {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd, gotOffset)
	}
}

func TestRepairDanglingToolCalls_SingleDangling(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "prev question"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
		}},
		{Role: "user", Content: "another question"},
		{Role: "assistant", Content: "response"},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 5

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if len(got) != len(messages)+1 {
		t.Fatalf("expected %d messages, got %d", len(messages)+1, len(got))
	}
	if gotOffset != historyEnd+1 {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd+1, gotOffset)
	}

	repair := got[3]
	if repair.Role != "tool" {
		t.Errorf("expected role 'tool', got %q", repair.Role)
	}
	if repair.ToolCallID != "call-1" {
		t.Errorf("expected ToolCallID 'call-1', got %q", repair.ToolCallID)
	}
	if repair.ToolName != "tool_a" {
		t.Errorf("expected ToolName 'tool_a', got %q", repair.ToolName)
	}

	if got[len(got)-1].Content != "new question" {
		t.Errorf("expected last message to be current turn user message")
	}
}

func TestRepairDanglingToolCalls_MultipleDangling(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
			{ID: "call-2", Name: "tool_b", Arguments: `{}`},
			{ID: "call-3", Name: "tool_c", Arguments: `{}`},
		}},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 2

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if len(got) != len(messages)+3 {
		t.Fatalf("expected %d messages, got %d", len(messages)+3, len(got))
	}
	if gotOffset != historyEnd+3 {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd+3, gotOffset)
	}

	for j, id := range []string{"call-1", "call-2", "call-3"} {
		repair := got[2+j]
		if repair.Role != "tool" || repair.ToolCallID != id {
			t.Errorf("repair[%d]: expected tool with ToolCallID %q, got role=%q id=%q", j, id, repair.Role, repair.ToolCallID)
		}
	}
}

func TestRepairDanglingToolCalls_DanglingAtEndOfHistory(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
		}},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 3

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if len(got) != len(messages)+1 {
		t.Fatalf("expected %d messages, got %d", len(messages)+1, len(got))
	}
	if gotOffset != historyEnd+1 {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd+1, gotOffset)
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "call-1" {
		t.Errorf("expected repair at index 3")
	}
}

func TestRepairDanglingToolCalls_MixedHistory(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-ok", Name: "good_tool", Arguments: `{}`},
		}},
		{Role: "tool", Content: `{"status":"success"}`, ToolCallID: "call-ok"},
		{Role: "assistant", Content: "answer1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-bad", Name: "bad_tool", Arguments: `{}`},
		}},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "answer3"},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 9

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if len(got) != len(messages)+1 {
		t.Fatalf("expected %d messages, got %d", len(messages)+1, len(got))
	}
	if gotOffset != historyEnd+1 {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd+1, gotOffset)
	}

	if got[3].Role != "tool" || got[3].ToolCallID != "call-ok" {
		t.Error("proper tool result should be unchanged")
	}

	repair := got[7]
	if repair.Role != "tool" || repair.ToolCallID != "call-bad" {
		t.Errorf("expected repair for call-bad, got role=%q id=%q", repair.Role, repair.ToolCallID)
	}
}

func TestRepairDanglingToolCalls_IncomingContentMatchesDangling(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-peer", Name: "delegate_to_helper", Arguments: `{}`},
		}},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 3
	incoming := []llm.ChatMessage{
		{Role: "tool", Content: `{"status":"success","result":"peer response"}`, ToolCallID: "call-peer", ToolName: "delegate_to_helper"},
	}

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), incoming)

	if len(got) != len(messages) {
		t.Fatalf("expected %d messages (no repair), got %d", len(messages), len(got))
	}
	if gotOffset != historyEnd {
		t.Fatalf("expected historyEnd %d unchanged, got %d", historyEnd, gotOffset)
	}
}

func TestRepairDanglingToolCalls_IncomingContentPartialMatch(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
			{ID: "call-2", Name: "tool_b", Arguments: `{}`},
			{ID: "call-3", Name: "tool_c", Arguments: `{}`},
		}},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 2
	incoming := []llm.ChatMessage{
		{Role: "tool", Content: `{"status":"success"}`, ToolCallID: "call-2", ToolName: "tool_b"},
	}

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), incoming)

	if len(got) != len(messages)+2 {
		t.Fatalf("expected %d messages, got %d", len(messages)+2, len(got))
	}
	if gotOffset != historyEnd+2 {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd+2, gotOffset)
	}

	repairIDs := []string{}
	for _, m := range got {
		if m.Role == "tool" && m.Content != "" && m.ToolCallID != "" {
			if len(m.Content) > 50 {
				repairIDs = append(repairIDs, m.ToolCallID)
			}
		}
	}
	if len(repairIDs) != 2 || repairIDs[0] != "call-1" || repairIDs[1] != "call-3" {
		t.Errorf("expected repairs for [call-1, call-3], got %v", repairIDs)
	}
}

func TestRepairDanglingToolCalls_IncomingContentMultipleResults(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
			{ID: "call-2", Name: "tool_b", Arguments: `{}`},
		}},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 2
	incoming := []llm.ChatMessage{
		{Role: "tool", Content: `{"status":"success"}`, ToolCallID: "call-1", ToolName: "tool_a"},
		{Role: "tool", Content: `{"status":"success"}`, ToolCallID: "call-2", ToolName: "tool_b"},
	}

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), incoming)

	if len(got) != len(messages) {
		t.Fatalf("expected %d messages (no repairs), got %d", len(messages), len(got))
	}
	if gotOffset != historyEnd {
		t.Fatalf("expected historyEnd %d unchanged, got %d", historyEnd, gotOffset)
	}
}

func TestRepairDanglingToolCalls_NilIncomingSameAsBefore(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
		}},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 3

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if len(got) != len(messages)+1 {
		t.Fatalf("expected %d messages, got %d", len(messages)+1, len(got))
	}
	if gotOffset != historyEnd+1 {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd+1, gotOffset)
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "call-1" {
		t.Errorf("expected repair at index 3")
	}
}

func TestRepairDanglingToolCalls_HealsOrphanDuplicateFromCancelRace(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "do deep research"},
		{Role: "assistant", Content: "kicking off peer", ToolCalls: []llm.ToolCall{
			{ID: "call-X", Name: "peer_agent_foo", Arguments: `{}`},
		}},
		{Role: "assistant", Content: cancellationMarkerContent},
		{Role: "tool", Content: `{"status":"error","message":"peer agent \"foo\" request timed out or was cancelled"}`, ToolCallID: "call-X", ToolName: "peer_agent_foo"},
		{Role: "user", Content: "continue"},
		{Role: "assistant", Content: cancellationMarkerContent},
		{Role: "user", Content: "ping"},
	}
	historyEnd := 7

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if len(got) != len(messages) {
		t.Fatalf("expected len unchanged (insert balances drop), got %d vs %d", len(got), len(messages))
	}
	if gotOffset != historyEnd {
		t.Fatalf("expected historyEnd %d, got %d", historyEnd, gotOffset)
	}

	if got[3].Role != "tool" || got[3].ToolCallID != "call-X" {
		t.Errorf("expected synthetic tool(id=call-X) at index 3, got role=%q id=%q", got[3].Role, got[3].ToolCallID)
	}

	var count int
	for _, m := range got[:gotOffset] {
		if m.Role == "tool" && m.ToolCallID == "call-X" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 tool row with id=call-X after repair, got %d", count)
	}
}

func TestRepairDanglingToolCalls_PartialParallelPairInsertsMissingSynthetics(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "run two in parallel"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-A", Name: "tool_a", Arguments: `{}`},
			{ID: "call-B", Name: "tool_b", Arguments: `{}`},
		}},
		{Role: "tool", Content: `{"status":"success"}`, ToolCallID: "call-A", ToolName: "tool_a"},
		{Role: "assistant", Content: "moving on"},
		{Role: "user", Content: "next"},
	}
	historyEnd := 5

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if gotOffset != historyEnd+1 {
		t.Fatalf("expected one insert (gotOffset=%d want=%d)", gotOffset, historyEnd+1)
	}
	pairedIDs := map[string]bool{}
	var syntheticForB llm.ChatMessage
	for _, m := range got[:gotOffset] {
		if m.Role == "tool" && m.ToolCallID != "" {
			pairedIDs[m.ToolCallID] = true
			if m.ToolCallID == "call-B" {
				syntheticForB = m
			}
		}
	}
	if !pairedIDs["call-A"] || !pairedIDs["call-B"] {
		t.Errorf("expected both call-A and call-B paired, got %v", pairedIDs)
	}
	if !strings.Contains(syntheticForB.Content, "automatically repaired") {
		t.Errorf("expected dangling-repair content for call-B synthetic, got %q", syntheticForB.Content)
	}
}

func TestRepairDanglingToolCalls_DropsOrphanWhenImmediatePairAlreadyExists(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "tool_a", Arguments: `{}`},
		}},
		{Role: "tool", Content: `{"status":"success"}`, ToolCallID: "call-1", ToolName: "tool_a"},
		{Role: "assistant", Content: cancellationMarkerContent},
		{Role: "tool", Content: `{"status":"cancelled"}`, ToolCallID: "call-1", ToolName: "tool_a"},
		{Role: "user", Content: "follow-up"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 7

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if gotOffset != historyEnd-1 {
		t.Fatalf("expected one drop, gotOffset=%d want=%d", gotOffset, historyEnd-1)
	}
	var found []llm.ChatMessage
	for _, m := range got[:gotOffset] {
		if m.Role == "tool" && m.ToolCallID == "call-1" {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly 1 tool row with id=call-1, got %d", len(found))
	}
	if found[0].Content != `{"status":"success"}` {
		t.Errorf("expected the first (real) result kept, got %q", found[0].Content)
	}
}

func TestRepairDanglingToolCalls_NoAssistantToolCalls(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "just text"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "more text"},
		{Role: "user", Content: "new question"},
	}
	historyEnd := 5

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if &got[0] != &messages[0] {
		t.Error("expected same slice returned when no tool calls in history")
	}
	if gotOffset != historyEnd {
		t.Fatalf("expected historyEnd unchanged")
	}
}

func TestRepairDanglingToolCalls_CrossTurnReusedIDKeepsBothResults(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "search a"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "web_search", Name: "web_search", Arguments: `{"q":"a"}`}}},
		{Role: "tool", Content: "result a", ToolCallID: "web_search", ToolName: "web_search"},
		{Role: "assistant", Content: "found a"},
		{Role: "user", Content: "search b"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "web_search", Name: "web_search", Arguments: `{"q":"b"}`}}},
		{Role: "tool", Content: "result b", ToolCallID: "web_search", ToolName: "web_search"},
		{Role: "assistant", Content: "found b"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 9

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if gotOffset != historyEnd {
		t.Fatalf("expected no insert or drop, gotOffset=%d want=%d", gotOffset, historyEnd)
	}
	assertToolHistoryPaired(t, got[:gotOffset])
	if got[6].ToolCalls[0].ID != "web_search_2" || got[7].ToolCallID != "web_search_2" {
		t.Errorf("second turn call/result ids = %q/%q, want web_search_2 for both", got[6].ToolCalls[0].ID, got[7].ToolCallID)
	}
	if got[7].Content != "result b" {
		t.Errorf("second turn result content = %q, want the real result", got[7].Content)
	}
	if messages[6].ToolCalls[0].ID != "web_search" || messages[7].ToolCallID != "web_search" {
		t.Error("repair mutated the caller's messages")
	}
}

func TestRepairDanglingToolCalls_ParallelReusedIDInOneBatch(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "search a and b"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "web_search", Name: "web_search", Arguments: `{"q":"a"}`},
			{ID: "web_search", Name: "web_search", Arguments: `{"q":"b"}`},
		}},
		{Role: "tool", Content: "result a", ToolCallID: "web_search", ToolName: "web_search"},
		{Role: "tool", Content: "result b", ToolCallID: "web_search", ToolName: "web_search"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 6

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if gotOffset != historyEnd {
		t.Fatalf("expected no insert or drop, gotOffset=%d want=%d", gotOffset, historyEnd)
	}
	assertToolHistoryPaired(t, got[:gotOffset])
	if got[3].ToolCallID != "web_search" || got[3].Content != "result a" {
		t.Errorf("first result = %q/%q, want web_search/result a", got[3].ToolCallID, got[3].Content)
	}
	if got[4].ToolCallID != "web_search_2" || got[4].Content != "result b" {
		t.Errorf("second result = %q/%q, want web_search_2/result b", got[4].ToolCallID, got[4].Content)
	}
}

func TestRepairDanglingToolCalls_DropsToolRowNeverDeclared(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "tool_a", Arguments: `{}`}}},
		{Role: "tool", Content: "ok", ToolCallID: "call-1", ToolName: "tool_a"},
		{Role: "tool", Content: "stray", ToolCallID: "call-ghost", ToolName: "tool_b"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 6

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if gotOffset != historyEnd-1 {
		t.Fatalf("expected one drop, gotOffset=%d want=%d", gotOffset, historyEnd-1)
	}
	for _, m := range got[:gotOffset] {
		if m.ToolCallID == "call-ghost" {
			t.Fatal("tool row for an undeclared id survived repair")
		}
	}
	assertToolHistoryPaired(t, got[:gotOffset])
}

func TestRepairDanglingToolCalls_DropsToolRowWhenNoCallDeclared(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q"},
		{Role: "tool", Content: "stray", ToolCallID: "call-ghost", ToolName: "tool_b"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 4

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	if gotOffset != historyEnd-1 || len(got) != len(messages)-1 {
		t.Fatalf("expected one drop, gotOffset=%d len=%d", gotOffset, len(got))
	}
}

func TestRepairDanglingToolCalls_DuplicateTurnInterleavedWithResults(t *testing.T) {
	call := llm.ToolCall{ID: "toolu_bdrk_01A", Name: "list_files", Arguments: `{"path":"/"}`}
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "list files"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "a.txt", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "a.txt", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "assistant", Content: "one file"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 7

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	assertHistoryShape(t, got, gotOffset, []string{"system", "user", "assistant:toolu_bdrk_01A", "tool:toolu_bdrk_01A", "assistant", "user"})
	if got[3].Content != "a.txt" {
		t.Errorf("result content = %q, want the real result", got[3].Content)
	}
	if len(messages) != 8 || messages[4].ToolCalls[0].ID != call.ID {
		t.Error("repair mutated the caller's messages")
	}
}

func TestRepairDanglingToolCalls_DuplicateTurnBeforeDuplicateResults(t *testing.T) {
	call := llm.ToolCall{ID: "toolu_bdrk_01P", Name: "peer_agent_research", Arguments: `{"task":"dig"}`}
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "research this"},
		{Role: "assistant", Content: "asking the researcher", ToolCalls: []llm.ToolCall{call}},
		{Role: "assistant", Content: "asking the researcher", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "findings", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "tool", Content: "findings", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "assistant", Content: "here is what I found"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 7

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	assertHistoryShape(t, got, gotOffset, []string{"system", "user", "assistant:toolu_bdrk_01P", "tool:toolu_bdrk_01P", "assistant", "user"})
}

func TestRepairDanglingToolCalls_DuplicateTurnKeepsFirstRealResult(t *testing.T) {
	call := llm.ToolCall{ID: "toolu_bdrk_01P", Name: "peer_agent_research", Arguments: `{}`}
	cases := []struct {
		name    string
		results []string
		want    string
	}{
		{"stub then real", []string{cancelledToolResultContent, "findings"}, "findings"},
		{"repair stub then real", []string{danglingToolResultContent, "findings"}, "findings"},
		{"identical real results", []string{"findings", "findings"}, "findings"},
		{"only stubs", []string{cancelledToolResultContent, danglingToolResultContent}, cancelledToolResultContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := []llm.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "research this"},
				{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
				{Role: "tool", Content: tc.results[0], ToolCallID: call.ID, ToolName: call.Name},
				{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
				{Role: "tool", Content: tc.results[1], ToolCallID: call.ID, ToolName: call.Name},
				{Role: "user", Content: "new"},
			}
			historyEnd := 6

			got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

			assertHistoryShape(t, got, gotOffset, []string{"system", "user", "assistant:toolu_bdrk_01P", "tool:toolu_bdrk_01P", "user"})
			if got[3].Content != tc.want {
				t.Errorf("kept result = %q, want %q", got[3].Content, tc.want)
			}
		})
	}
}

func TestRepairDanglingToolCalls_DuplicateTurnAwaitingIncomingResult(t *testing.T) {
	call := llm.ToolCall{ID: "toolu_bdrk_01P", Name: "peer_agent_research", Arguments: `{}`}
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "research this"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "findings", ToolCallID: call.ID, ToolName: call.Name},
	}
	historyEnd := 4

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), messages[historyEnd:])

	if gotOffset != 3 || len(got) != 4 {
		t.Fatalf("gotOffset=%d len=%d, want the copy dropped and no synthetic result", gotOffset, len(got))
	}
	if got[3].Content != "findings" {
		t.Errorf("incoming result = %q, want it kept after history", got[3].Content)
	}
}

func TestRepairDanglingToolCalls_DuplicateParallelTurn(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "toolu_bdrk_01A", Name: "tool_a", Arguments: `{}`},
		{ID: "toolu_bdrk_01B", Name: "tool_b", Arguments: `{}`},
	}
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "run both"},
		{Role: "assistant", ToolCalls: calls},
		{Role: "tool", Content: "b", ToolCallID: "toolu_bdrk_01B", ToolName: "tool_b"},
		{Role: "tool", Content: "a", ToolCallID: "toolu_bdrk_01A", ToolName: "tool_a"},
		{Role: "assistant", ToolCalls: calls},
		{Role: "tool", Content: "b", ToolCallID: "toolu_bdrk_01B", ToolName: "tool_b"},
		{Role: "tool", Content: "a", ToolCallID: "toolu_bdrk_01A", ToolName: "tool_a"},
		{Role: "user", Content: "new"},
	}
	historyEnd := 8

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	assertHistoryShape(t, got, gotOffset, []string{"system", "user", "assistant:toolu_bdrk_01A", "tool:toolu_bdrk_01A", "tool:toolu_bdrk_01B", "user"})
}

func TestRepairDanglingToolCalls_RepeatedPollingCallWithNewResultIsKept(t *testing.T) {
	call := llm.ToolCall{ID: "check_status", Name: "check_status", Arguments: `{}`}
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "wait for the job"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "pending", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "done", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "user", Content: "new"},
	}
	historyEnd := 6

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	assertHistoryShape(t, got, gotOffset, []string{"system", "user", "assistant:check_status", "tool:check_status", "assistant:check_status_2", "tool:check_status_2", "user"})
	if got[3].Content != "pending" || got[5].Content != "done" {
		t.Errorf("results = %q, %q, want pending then done", got[3].Content, got[5].Content)
	}
}

func TestRepairDanglingToolCalls_RepeatedCallInLaterTurnIsKept(t *testing.T) {
	call := llm.ToolCall{ID: "get_time", Name: "get_time", Arguments: `{}`}
	cases := []struct {
		name    string
		between []llm.ChatMessage
		want    []string
	}{
		{
			"separated by a user message",
			[]llm.ChatMessage{{Role: "user", Content: "again"}},
			[]string{"system", "user", "assistant:get_time", "tool:get_time", "user", "assistant:get_time_2", "tool:get_time_2", "user"},
		},
		{
			"separated by an assistant message",
			[]llm.ChatMessage{{Role: "assistant", Content: "checking once more"}},
			[]string{"system", "user", "assistant:get_time", "tool:get_time", "assistant", "assistant:get_time_2", "tool:get_time_2", "user"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := []llm.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "what time is it"},
				{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
				{Role: "tool", Content: "10:00", ToolCallID: call.ID, ToolName: call.Name},
			}
			messages = append(messages, tc.between...)
			messages = append(messages,
				llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
				llm.ChatMessage{Role: "tool", Content: "10:05", ToolCallID: call.ID, ToolName: call.Name},
				llm.ChatMessage{Role: "user", Content: "new"},
			)
			historyEnd := len(messages) - 1

			got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

			assertHistoryShape(t, got, gotOffset, tc.want)
			if got[3].Content != "10:00" || got[6].Content != "10:05" {
				t.Errorf("results = %q, %q, want each turn's own result", got[3].Content, got[6].Content)
			}
		})
	}
}

func TestRepairDanglingToolCalls_TripleStoredTurnCollapses(t *testing.T) {
	call := llm.ToolCall{ID: "toolu_bdrk_01A", Name: "list_files", Arguments: `{}`}
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "list files"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "a.txt", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "a.txt", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "assistant", ToolCalls: []llm.ToolCall{call}},
		{Role: "tool", Content: "a.txt", ToolCallID: call.ID, ToolName: call.Name},
		{Role: "user", Content: "new"},
	}
	historyEnd := 8

	got, gotOffset := repairDanglingToolCalls(messages, historyEnd, testLogger(), nil)

	assertHistoryShape(t, got, gotOffset, []string{"system", "user", "assistant:toolu_bdrk_01A", "tool:toolu_bdrk_01A", "user"})
}

func assertHistoryShape(t *testing.T, got []llm.ChatMessage, historyEnd int, want []string) {
	t.Helper()
	shape := make([]string, len(got))
	for i, m := range got {
		shape[i] = m.Role
		switch {
		case len(m.ToolCalls) > 0:
			shape[i] += ":" + m.ToolCalls[0].ID
		case m.ToolCallID != "":
			shape[i] += ":" + m.ToolCallID
		}
	}
	if !slices.Equal(shape, want) {
		t.Fatalf("history shape = %q, want %q", shape, want)
	}
	if historyEnd != len(want)-1 {
		t.Fatalf("historyEnd = %d, want %d", historyEnd, len(want)-1)
	}
	assertToolHistoryPaired(t, got[:historyEnd])
}

func assertToolHistoryPaired(t *testing.T, history []llm.ChatMessage) {
	t.Helper()
	declared := map[string]int{}
	results := map[string]int{}
	for _, m := range history {
		for _, tc := range m.ToolCalls {
			declared[tc.ID]++
		}
		if m.Role == "tool" {
			results[m.ToolCallID]++
		}
	}
	for id, n := range declared {
		if n != 1 {
			t.Errorf("call id %q declared %d times", id, n)
		}
		if results[id] != 1 {
			t.Errorf("call id %q has %d results, want 1", id, results[id])
		}
	}
	for id := range results {
		if declared[id] == 0 {
			t.Errorf("tool result %q has no declaring call", id)
		}
	}
}
