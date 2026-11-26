// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"slices"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func uniquifyToolCallIDs(calls []llm.ToolCall, prior []llm.ChatMessage) []llm.ToolCall {
	used := toolCallIDBasesIn(prior)
	reserved := toolCallIDBasesIn(prior)
	for _, tc := range calls {
		if tc.ID != "" {
			reserved[toolCallIDBase(tc.ID)] = struct{}{}
		}
	}
	var out []llm.ToolCall
	for i, tc := range calls {
		if tc.ID == "" {
			continue
		}
		base := toolCallIDBase(tc.ID)
		if _, repeated := used[base]; !repeated {
			used[base] = struct{}{}
			continue
		}
		if out == nil {
			out = slices.Clone(calls)
		}
		out[i].ID = freshToolCallID(tc.ID, reserved)
		used[toolCallIDBase(out[i].ID)] = struct{}{}
	}
	if out == nil {
		return calls
	}
	return out
}

func dropDuplicateToolCallTurns(messages []llm.ChatMessage, historyEnd int) ([]llm.ChatMessage, int, int) {
	isToolCallTurn := func(m llm.ChatMessage) bool {
		return m.Role == "assistant" && len(m.ToolCalls) > 0
	}
	firstCopyOf := make(map[int]int)
	for i := range historyEnd {
		if !isToolCallTurn(messages[i]) {
			continue
		}
		prev := i - 1
		for prev >= 0 && messages[prev].Role == "tool" {
			prev--
		}
		if prev < 0 || !isToolCallTurn(messages[prev]) || !slices.Equal(messages[prev].ToolCalls, messages[i].ToolCalls) {
			continue
		}
		if prev < i-1 && !sameRealResults(messages[prev+1:i], toolBlockAfter(messages, i, historyEnd)) {
			continue
		}
		first, again := firstCopyOf[prev]
		if !again {
			first = prev
		}
		firstCopyOf[i] = first
	}
	if len(firstCopyOf) == 0 {
		return messages, historyEnd, 0
	}

	resultsOf := make(map[int]map[string][]llm.ChatMessage)
	for _, first := range firstCopyOf {
		resultsOf[first] = make(map[string][]llm.ChatMessage)
	}
	claimed := make(map[int]struct{})
	for i := range historyEnd {
		first, dup := firstCopyOf[i]
		if !dup {
			first = i
		}
		results, grouped := resultsOf[first]
		if !grouped {
			continue
		}
		ids := toolCallIDsIn(messages[first : first+1])
		for j := i + 1; j < historyEnd; j++ {
			if f, again := firstCopyOf[j]; again && f == first {
				continue
			}
			if messages[j].Role != "tool" {
				break
			}
			id := messages[j].ToolCallID
			if _, ours := ids[id]; !ours {
				continue
			}
			if _, done := claimed[j]; done {
				continue
			}
			claimed[j] = struct{}{}
			results[id] = append(results[id], messages[j])
		}
	}

	out := make([]llm.ChatMessage, 0, len(messages))
	for i := range historyEnd {
		if _, dup := firstCopyOf[i]; dup {
			continue
		}
		if _, moved := claimed[i]; moved {
			continue
		}
		out = append(out, messages[i])
		if results, hasCopies := resultsOf[i]; hasCopies {
			out = appendResultsInCallOrder(out, messages[i].ToolCalls, results)
		}
	}
	newEnd := len(out)
	out = append(out, messages[historyEnd:]...)
	return out, newEnd, len(firstCopyOf)
}

func appendResultsInCallOrder(out []llm.ChatMessage, calls []llm.ToolCall, results map[string][]llm.ChatMessage) []llm.ChatMessage {
	perID := make(map[string]int, len(calls))
	for _, tc := range calls {
		perID[tc.ID]++
	}
	for _, tc := range calls {
		if n, pending := perID[tc.ID]; pending {
			out = append(out, pickResultRound(results[tc.ID], n)...)
			delete(perID, tc.ID)
		}
	}
	return out
}

func pickResultRound(rows []llm.ChatMessage, perRound int) []llm.ChatMessage {
	var fallback []llm.ChatMessage
	for start := 0; start < len(rows); start += perRound {
		round := rows[start:min(start+perRound, len(rows))]
		if fallback == nil {
			fallback = round
		}
		if len(round) == perRound && !slices.ContainsFunc(round, isSyntheticToolResult) {
			return round
		}
	}
	return fallback
}

func sameRealResults(a, b []llm.ChatMessage) bool {
	before, after := realResultContentsByID(a), realResultContentsByID(b)
	for id, contents := range before {
		if other, both := after[id]; both && !slices.Equal(contents, other) {
			return false
		}
	}
	return true
}

func realResultContentsByID(rows []llm.ChatMessage) map[string][]string {
	contents := make(map[string][]string)
	for _, m := range rows {
		if m.Role == "tool" && !isSyntheticToolResult(m) {
			contents[m.ToolCallID] = append(contents[m.ToolCallID], m.Content)
		}
	}
	return contents
}

func toolBlockAfter(messages []llm.ChatMessage, i, end int) []llm.ChatMessage {
	j := i + 1
	for j < end && messages[j].Role == "tool" {
		j++
	}
	return messages[i+1 : j]
}

func isSyntheticToolResult(m llm.ChatMessage) bool {
	return m.Content == cancelledToolResultContent || m.Content == danglingToolResultContent
}

func renameRedeclaredToolCallIDs(messages []llm.ChatMessage, historyEnd int, inFlight map[string]struct{}) ([]llm.ChatMessage, int) {
	reserved := toolCallIDBasesIn(messages[:historyEnd])
	declared := make(map[string]struct{})
	var out []llm.ChatMessage
	renamed := 0
	for i := range historyEnd {
		if messages[i].Role != "assistant" || len(messages[i].ToolCalls) == 0 {
			continue
		}
		assigned := make(map[string][]string, len(messages[i].ToolCalls))
		var calls []llm.ToolCall
		for k, tc := range messages[i].ToolCalls {
			if tc.ID == "" {
				continue
			}
			id := tc.ID
			_, redeclared := declared[toolCallIDBase(id)]
			_, awaited := inFlight[id]
			if redeclared && !awaited {
				if calls == nil {
					calls = slices.Clone(messages[i].ToolCalls)
				}
				id = freshToolCallID(tc.ID, reserved)
				calls[k].ID = id
				renamed++
			}
			declared[toolCallIDBase(id)] = struct{}{}
			assigned[tc.ID] = append(assigned[tc.ID], id)
		}
		if calls == nil {
			continue
		}
		if out == nil {
			out = slices.Clone(messages)
		}
		out[i].ToolCalls = calls
		for j := i + 1; j < historyEnd && messages[j].Role == "tool"; j++ {
			queue := assigned[messages[j].ToolCallID]
			if len(queue) == 0 {
				continue
			}
			out[j].ToolCallID = queue[0]
			assigned[messages[j].ToolCallID] = queue[1:]
		}
	}
	if out == nil {
		return messages, 0
	}
	return out, renamed
}

const maxProviderToolCallIDLen = 64

func freshToolCallID(id string, reserved map[string]struct{}) string {
	cut := llm.ThoughtSignatureStart(id)
	base, signature := id[:cut], id[cut:]
	for n := 2; ; n++ {
		suffix := "_" + strconv.Itoa(n)
		numbered := base[:min(len(base), maxProviderToolCallIDLen-len(suffix))] + suffix
		if _, taken := reserved[numbered]; !taken {
			reserved[numbered] = struct{}{}
			return numbered + signature
		}
	}
}

func toolCallIDBase(id string) string {
	return id[:llm.ThoughtSignatureStart(id)]
}

func toolCallIDBasesIn(messages []llm.ChatMessage) map[string]struct{} {
	bases := make(map[string]struct{})
	for id := range toolCallIDsIn(messages) {
		bases[toolCallIDBase(id)] = struct{}{}
	}
	return bases
}

func toolCallIDsIn(messages []llm.ChatMessage) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, m := range messages {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				ids[tc.ID] = struct{}{}
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			ids[m.ToolCallID] = struct{}{}
		}
	}
	return ids
}

func declaredToolCallIDs(messages []llm.ChatMessage) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			ids[tc.ID] = struct{}{}
		}
	}
	return ids
}
