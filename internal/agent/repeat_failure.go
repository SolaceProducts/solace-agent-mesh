// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const repeatFailureLimit = 3

const repeatFailureReminder = "%s failed %d times with the same arguments and the same error. It is left out of this reply only and is offered again on your next one, " +
	"so fix the cause now instead of switching to another tool. If it needs a file that does not exist, save that file in this reply, then call it again."

type repeatedFailures struct {
	counts   map[string]int
	pending  string
	withheld string
}

func (r *repeatedFailures) record(calls []llm.ToolCall, results []*tool.Result) {
	for i, tc := range calls {
		if i >= len(results) || !isFailedResult(results[i]) {
			continue
		}
		key := tc.Name + "\x00" + canonicalArgs(tc.Arguments) + "\x00" + results[i].Message
		if r.counts == nil {
			r.counts = make(map[string]int)
		}
		r.counts[key]++
		if r.counts[key] >= repeatFailureLimit {
			r.pending = tc.Name
			delete(r.counts, key)
		}
	}
}

func (r *repeatedFailures) startTurn(tools []llm.ToolDefinition) []llm.ToolDefinition {
	r.withheld, r.pending = r.pending, ""
	if r.withheld == "" {
		return tools
	}
	return withoutTool(tools, r.withheld)
}

func (r *repeatedFailures) remind(messages []llm.ChatMessage) []llm.ChatMessage {
	if r.withheld == "" {
		return messages
	}
	return withRepeatFailureReminder(messages, r.withheld)
}

func isFailedResult(res *tool.Result) bool {
	return res != nil && (res.Status == tool.StatusError || res.Status == tool.StatusFailed)
}

func canonicalArgs(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(b)
}

func withoutTool(tools []llm.ToolDefinition, name string) []llm.ToolDefinition {
	return slices.DeleteFunc(slices.Clone(tools), func(t llm.ToolDefinition) bool { return t.Name == name })
}

func withRepeatFailureReminder(messages []llm.ChatMessage, name string) []llm.ChatMessage {
	out := make([]llm.ChatMessage, len(messages), len(messages)+1)
	copy(out, messages)
	return append(out, trailingReminder(fmt.Sprintf(repeatFailureReminder, name, repeatFailureLimit)))
}
