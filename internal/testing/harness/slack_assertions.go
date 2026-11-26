// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"strings"
	"testing"
)

type ExpectedSlackCall struct {
	Method          string       `yaml:"method"`
	TextContains    StringOrList `yaml:"text_contains"`
	TextNotContains StringOrList `yaml:"text_not_contains"`
	Channel         string       `yaml:"channel"`
	ThreadTS        string       `yaml:"thread_ts"`
	HasBlocks       *bool        `yaml:"has_blocks"`
	BlockContains   StringOrList `yaml:"block_contains"`
	IsFileUpload    *bool        `yaml:"is_file_upload"`
}

func AssertSlackCalls(t *testing.T, actual []CapturedSlackCall, expected []ExpectedSlackCall) {
	t.Helper()

	if len(expected) == 0 {
		return
	}

	t.Logf("slack API calls (%d total):", len(actual))
	for i, c := range actual {
		t.Logf("  [%d] %s channel=%s text=%q", i, c.Method, c.Channel, truncateStr(c.Text, 100))
	}

	pos := 0
	for i, exp := range expected {
		found := false
		for j := pos; j < len(actual); j++ {
			if matchSlackCall(actual[j], exp) {
				pos = j + 1
				found = true
				break
			}
		}
		if !found {
			var remaining []string
			for j := pos; j < len(actual); j++ {
				remaining = append(remaining, fmt.Sprintf("  [%d] %s: %q", j, actual[j].Method, truncateStr(actual[j].Text, 80)))
			}
			remainingStr := "  (none)"
			if len(remaining) > 0 {
				remainingStr = strings.Join(remaining, "\n")
			}
			t.Errorf("expected_slack_calls[%d]: no matching call found\n  expected: method=%q text_contains=%v\n  remaining actual calls (from pos %d):\n%s",
				i, exp.Method, []string(exp.TextContains), pos, remainingStr)
		}
	}
}

func matchSlackCall(actual CapturedSlackCall, exp ExpectedSlackCall) bool {
	if exp.Method != "" && actual.Method != exp.Method {
		return false
	}

	if exp.Channel != "" && actual.Channel != exp.Channel {
		return false
	}

	if exp.ThreadTS != "" && actual.ThreadTS != exp.ThreadTS {
		return false
	}

	for _, tc := range exp.TextContains {
		if !strings.Contains(actual.Text, tc) {
			if !strings.Contains(actual.Blocks, tc) {
				return false
			}
		}
	}

	for _, tnc := range exp.TextNotContains {
		if strings.Contains(actual.Text, tnc) || strings.Contains(actual.Blocks, tnc) {
			return false
		}
	}

	if exp.HasBlocks != nil {
		hasBlocks := actual.Blocks != "" && actual.Blocks != "null" && actual.Blocks != "[]"
		if *exp.HasBlocks != hasBlocks {
			return false
		}
	}

	for _, bc := range exp.BlockContains {
		if !strings.Contains(actual.Blocks, bc) {
			return false
		}
	}

	if exp.IsFileUpload != nil {
		isUpload := actual.Method == "files.getUploadURLExternal" ||
			actual.Method == "file_upload" ||
			actual.Method == "files.completeUploadExternal"
		if *exp.IsFileUpload != isUpload {
			return false
		}
	}

	return true
}

func (h *TestHarness) AssertSlackCalls(t *testing.T) {
	t.Helper()
	if len(h.scenario.ExpectedSlackCalls) == 0 {
		return
	}
	slackGW, ok := h.gateway.(*SlackTestGateway)
	if !ok {
		t.Log("expected_slack_calls defined but gateway is not SlackTestGateway; skipping")
		return
	}
	AssertSlackCalls(t, slackGW.SlackCalls(), h.scenario.ExpectedSlackCalls)
}

func (h *TestHarness) AssertSlackToolCalls(t *testing.T) {
	t.Helper()
	if h.slackToolMockServer == nil || len(h.scenario.ExpectedSlackCalls) == 0 {
		return
	}
	if _, isSlackGW := h.gateway.(*SlackTestGateway); isSlackGW {
		return
	}
	AssertSlackCalls(t, h.slackToolMockServer.Calls(), h.scenario.ExpectedSlackCalls)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
