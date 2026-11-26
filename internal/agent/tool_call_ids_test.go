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

func TestUniquifyToolCallIDs(t *testing.T) {
	prior := []llm.ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "web_search", Name: "web_search"}}},
		{Role: "tool", ToolCallID: "web_search", Content: "r"},
	}
	cases := []struct {
		name  string
		prior []llm.ChatMessage
		in    []string
		want  []string
	}{
		{"unique ids untouched", prior, []string{"call-1", "call-2"}, []string{"call-1", "call-2"}},
		{"reused across turns", prior, []string{"web_search"}, []string{"web_search_2"}},
		{"repeated in one batch", nil, []string{"web_search", "web_search", "web_search"}, []string{"web_search", "web_search_2", "web_search_3"}},
		{"minted id skips one the batch already holds", nil, []string{"web_search", "web_search", "web_search_2"}, []string{"web_search", "web_search_3", "web_search_2"}},
		{"empty id untouched", nil, []string{"", ""}, []string{"", ""}},
		{"litellm thought signature kept after the counter", []llm.ChatMessage{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "web_search__thought__AY89a1/Pb+0"}}}}, []string{"web_search__thought__AY89a1/Pb+0"}, []string{"web_search_2__thought__AY89a1/Pb+0"}},
		{"bifrost thought signature kept after the counter", nil, []string{"web_search_ts_Q2lw", "web_search_ts_Q2lw"}, []string{"web_search_ts_Q2lw", "web_search_2_ts_Q2lw"}},
		{"distinct signatures still share a base", nil, []string{"web_search_ts_AAAA", "web_search_ts_BBBB"}, []string{"web_search_ts_AAAA", "web_search_2_ts_BBBB"}},
		{"minted id skips a base held with a signature", nil, []string{"web_search", "web_search", "web_search_2_ts_AAAA"}, []string{"web_search", "web_search_3", "web_search_2_ts_AAAA"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := make([]llm.ToolCall, len(tc.in))
			for i, id := range tc.in {
				calls[i] = llm.ToolCall{ID: id, Name: "web_search"}
			}
			got := uniquifyToolCallIDs(calls, tc.prior)
			ids := make([]string, len(got))
			for i, c := range got {
				ids[i] = c.ID
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("ids = %q, want %q", ids, tc.want)
			}
			for i, id := range tc.in {
				if calls[i].ID != id {
					t.Fatalf("input call %d mutated to %q", i, calls[i].ID)
				}
			}
		})
	}
}

func TestUniquifyToolCallIDs_SignatureSurvivesForProvider(t *testing.T) {
	const id = "web_search_ts_Q2lwaGVy"
	got := uniquifyToolCallIDs([]llm.ToolCall{{ID: id}, {ID: id}}, nil)
	renamed := got[1].ID
	if renamed[llm.ThoughtSignatureStart(renamed):] != id[llm.ThoughtSignatureStart(id):] {
		t.Fatalf("signature changed: %q -> %q", id, renamed)
	}
}

var longThoughtSignature = "_ts_" + strings.Repeat("Q2lwaGVy", 10)

func TestUniquifyToolCallIDs_SignedAndUnsignedShareABase(t *testing.T) {
	signed := "web_search" + longThoughtSignature
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"signed first", []string{signed, "web_search"}, []string{signed, "web_search_2"}},
		{"unsigned first", []string{"web_search", signed}, []string{"web_search", "web_search_2" + longThoughtSignature}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uniquifyToolCallIDs([]llm.ToolCall{{ID: tc.in[0], Name: "web_search"}, {ID: tc.in[1], Name: "web_search"}}, nil)
			if got[0].ID != tc.want[0] || got[1].ID != tc.want[1] {
				t.Fatalf("ids = %q, %q, want %q, %q", got[0].ID, got[1].ID, tc.want[0], tc.want[1])
			}
			first, second := got[0].ID, got[1].ID
			if first[:llm.ThoughtSignatureStart(first)] == second[:llm.ThoughtSignatureStart(second)] {
				t.Fatalf("ids %q and %q still collide once the signature is stripped", first, second)
			}
		})
	}
}

func TestRenameRedeclaredToolCallIDs_SignedAndUnsignedInOneTurn(t *testing.T) {
	signed := "web_search" + longThoughtSignature
	messages := []llm.ChatMessage{
		{Role: "user", Content: "search a and b"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: signed, Name: "web_search", Arguments: `{"q":"a"}`},
			{ID: "web_search", Name: "web_search", Arguments: `{"q":"b"}`},
		}},
		{Role: "tool", Content: "result a", ToolCallID: signed, ToolName: "web_search"},
		{Role: "tool", Content: "result b", ToolCallID: "web_search", ToolName: "web_search"},
	}

	got, renamed := renameRedeclaredToolCallIDs(messages, len(messages), nil)

	if renamed != 1 {
		t.Fatalf("renamed = %d, want 1", renamed)
	}
	if got[1].ToolCalls[0].ID != signed || got[2].ToolCallID != signed {
		t.Errorf("signed call/result = %q/%q, want both unchanged", got[1].ToolCalls[0].ID, got[2].ToolCallID)
	}
	if got[1].ToolCalls[1].ID != "web_search_2" || got[3].ToolCallID != "web_search_2" {
		t.Errorf("unsigned call/result = %q/%q, want web_search_2 for both", got[1].ToolCalls[1].ID, got[3].ToolCallID)
	}
	if messages[1].ToolCalls[1].ID != "web_search" || messages[3].ToolCallID != "web_search" {
		t.Error("rename mutated the caller's messages")
	}
}

func TestUniquifyToolCallIDs_RenamedBaseFitsProviderLimit(t *testing.T) {
	name := strings.Repeat("a", 64)
	for _, id := range []string{name, name + "_ts_Q2lw"} {
		got := uniquifyToolCallIDs([]llm.ToolCall{{ID: id}, {ID: id}}, nil)
		renamed := got[1].ID
		base := renamed[:llm.ThoughtSignatureStart(renamed)]
		if len(base) > 64 {
			t.Errorf("renamed base for %q is %d chars, want <= 64", id, len(base))
		}
		if base == name {
			t.Errorf("renamed id %q still carries the original base", renamed)
		}
		if renamed[len(base):] != id[llm.ThoughtSignatureStart(id):] {
			t.Errorf("signature changed: %q -> %q", id, renamed)
		}
	}
}

func TestRenameRedeclaredToolCallIDs_RenamedBaseFitsProviderLimit(t *testing.T) {
	name := strings.Repeat("a", 64)
	messages := []llm.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: name, Name: "t", Arguments: `{}`}}},
		{Role: "tool", Content: "r1", ToolCallID: name, ToolName: "t"},
		{Role: "user", Content: "second"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: name, Name: "t", Arguments: `{}`}}},
		{Role: "tool", Content: "r2", ToolCallID: name, ToolName: "t"},
	}

	got, renamed := renameRedeclaredToolCallIDs(messages, len(messages), nil)

	if renamed != 1 {
		t.Fatalf("renamed = %d, want 1", renamed)
	}
	id := got[4].ToolCalls[0].ID
	if len(id) > 64 || id == name {
		t.Errorf("renamed id %q: want a distinct id of <= 64 chars", id)
	}
	if got[5].ToolCallID != id {
		t.Errorf("result id = %q, want %q", got[5].ToolCallID, id)
	}
}
