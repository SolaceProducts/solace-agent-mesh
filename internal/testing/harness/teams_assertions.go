// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type ExpectedTeamsCall struct {
	ActivityType    string       `yaml:"activity_type"`
	TextContains    StringOrList `yaml:"text_contains"`
	TextNotContains StringOrList `yaml:"text_not_contains"`
	HasAttachments  *bool        `yaml:"has_attachments"`
	Method          string       `yaml:"method"`
}

func AssertTeamsCalls(t *testing.T, actual []CapturedTeamsCall, expected []ExpectedTeamsCall) {
	t.Helper()

	if len(expected) == 0 {
		return
	}

	t.Logf("Teams API calls (%d total):", len(actual))
	for i, c := range actual {
		t.Logf("  [%d] %s %s type=%s text=%q attachments=%d", i, c.Method, c.Path, c.ActivityType, truncateStr(c.Text, 100), c.Attachments)
	}

	pos := 0
	for i, exp := range expected {
		found := false
		for j := pos; j < len(actual); j++ {
			if matchTeamsCall(actual[j], exp) {
				pos = j + 1
				found = true
				break
			}
		}
		if !found {
			var remaining []string
			for j := pos; j < len(actual); j++ {
				remaining = append(remaining, fmt.Sprintf("  [%d] %s type=%s: %q", j, actual[j].Method, actual[j].ActivityType, truncateStr(actual[j].Text, 80)))
			}
			remainingStr := "  (none)"
			if len(remaining) > 0 {
				remainingStr = strings.Join(remaining, "\n")
			}
			t.Errorf("expected_teams_calls[%d]: no matching call found\n  expected: activity_type=%q text_contains=%v\n  remaining actual calls (from pos %d):\n%s",
				i, exp.ActivityType, []string(exp.TextContains), pos, remainingStr)
		}
	}
}

func matchTeamsCall(actual CapturedTeamsCall, exp ExpectedTeamsCall) bool {
	if exp.ActivityType != "" && actual.ActivityType != exp.ActivityType {
		return false
	}

	if exp.Method != "" && actual.Method != exp.Method {
		return false
	}

	for _, tc := range exp.TextContains {
		if !strings.Contains(actual.Text, tc) {
			return false
		}
	}

	for _, tnc := range exp.TextNotContains {
		if strings.Contains(actual.Text, tnc) {
			return false
		}
	}

	if exp.HasAttachments != nil {
		hasAttachments := actual.Attachments > 0
		if *exp.HasAttachments != hasAttachments {
			return false
		}
	}

	return true
}

func (h *TestHarness) AssertTeamsCalls(t *testing.T) {
	t.Helper()
	if len(h.scenario.ExpectedTeamsCalls) == 0 {
		return
	}
	teamsGW, ok := h.gateway.(*TeamsTestGateway)
	if !ok {
		t.Log("expected_teams_calls defined but gateway is not TeamsTestGateway; skipping")
		return
	}
	if teamsGW.mockServer != nil {
		teamsGW.mockServer.WaitForCallCount(len(h.scenario.ExpectedTeamsCalls), 5*time.Second)
	}
	AssertTeamsCalls(t, teamsGW.TeamsCalls(), h.scenario.ExpectedTeamsCalls)
}
