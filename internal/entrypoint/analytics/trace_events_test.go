// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func statusPayload(data string) string {
	return `{"jsonrpc":"2.0","id":"x","result":{"kind":"status-update","status":{"state":"working","message":{"parts":[{"kind":"data","data":` + data + `}]}}}}`
}

func TestSummarizeEvent_ExtractsSignalFromTypedPayload(t *testing.T) {
	cases := []struct {
		name, dir, payload, wantKind, wantSummarySub string
	}{
		{"error", "error", `{"jsonrpc":"2.0","id":"x","error":{"code":-32603,"message":"AI provider 502"}}`, "error", "502"},
		{"request", "request", `{"jsonrpc":"2.0","id":"x","method":"message/send","params":{"message":{"parts":[{"kind":"text","text":"do the thing"}]}}}`, "request", "do the thing"},
		{"final response", "response", `{"jsonrpc":"2.0","id":"x","result":{"kind":"task","status":{"state":"completed","message":{"parts":[{"kind":"text","text":"the answer"}]}}}}`, "response", "completed: the answer"},
		{"tool_start", "status", statusPayload(`{"type":"tool_invocation_start","tool_name":"load_skill"}`), "tool_start", "load_skill"},
		{"tool_result", "status", statusPayload(`{"type":"tool_result","tool_name":"load_skill","result_data":"Loaded skill X"}`), "tool_result", "load_skill → Loaded skill X"},
		{"tool_result structured payload", "status", statusPayload(`{"type":"tool_result","tool_name":"sql_query","result_data":{"status":"success","message":"Query returned 28 rows","content":"[{\"a\":1}]"}}`), "tool_result", "sql_query → Query returned 28 rows"},
		{"progress", "status", statusPayload(`{"type":"agent_progress_update","status_text":"Searching cases"}`), "progress", "Searching cases"},
		{"llm_response function call", "status", statusPayload(`{"type":"llm_response","data":{"content":{"parts":[{"function_call":{"name":"peer_x"}}]}}}`), "llm_response", "→ peer_x"},
		{"request_echo marker", "status", statusPayload(`{"request":{"config":{"system_instruction":"be good"}}}`), "request_echo", ""},
		{"unknown signal keeps its type", "status", statusPayload(`{"type":"some_future_signal"}`), "some_future_signal", ""},
		{"unknown signal auto-summarizes from common field", "status", statusPayload(`{"type":"brand_new_signal_v9","message":"forward compatible"}`), "brand_new_signal_v9", "forward compatible"},
		{"unparseable falls back to direction", "status", `not json`, "status", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, s := summarizeEvent(tc.dir, tc.payload)
			if k != tc.wantKind {
				t.Errorf("kind = %q, want %q", k, tc.wantKind)
			}
			if tc.wantSummarySub != "" && !strings.Contains(s, tc.wantSummarySub) {
				t.Errorf("summary = %q, want to contain %q", s, tc.wantSummarySub)
			}
			if tc.wantSummarySub == "" && s != "" {
				t.Errorf("summary = %q, want empty", s)
			}
		})
	}
}

func TestHeadTailSample_PreservesTerminalEvent_SingleSlotKeepsTailNotHead(t *testing.T) {
	mk := func(n int) []*store.TaskEvent {
		out := make([]*store.TaskEvent, n)
		for i := range out {
			out[i] = &store.TaskEvent{CreatedTime: int64(i)}
		}
		return out
	}
	if got := headTailSample(mk(5), 30); len(got) != 5 {
		t.Errorf("total<=cap: len=%d, want 5 (unchanged)", len(got))
	}
	if got := headTailSample(mk(10), 1); len(got) != 1 || got[0].CreatedTime != 9 {
		t.Errorf("cap=1: got %v, want single terminal event (CreatedTime 9)", got)
	}
	if got := headTailSample(mk(10), 2); len(got) != 2 || got[0].CreatedTime != 0 || got[1].CreatedTime != 9 {
		t.Errorf("cap=2: got %v, want first(0)+last(9)", got)
	}
	got := headTailSample(mk(10), 4)
	if len(got) != 4 || got[0].CreatedTime != 0 || got[3].CreatedTime != 9 {
		t.Errorf("cap=4: got %v, want head 0,1 + tail 8,9", got)
	}
}

func TestCondenseSummary_RuneSafeTruncation(t *testing.T) {
	long := strings.Repeat("a", maxEventSummaryChars-1) + "世" + "tail"
	got := condenseSummary(long)
	if !utf8.ValidString(got) {
		t.Errorf("not valid UTF-8 — rune was split: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("want ellipsis suffix, got %q", got)
	}
	if len(got) > maxEventSummaryChars+len("…") {
		t.Errorf("len = %d, want <= %d", len(got), maxEventSummaryChars+len("…"))
	}
}
