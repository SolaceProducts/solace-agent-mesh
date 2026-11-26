// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func (h *TestHarness) AssertLogsOmit(t *testing.T) {
	t.Helper()

	if len(h.scenario.AssertLogsOmit) == 0 {
		return
	}
	for _, failure := range evaluateLogOmissions(h.CapturedLogs(), h.scenario.AssertLogsOmit) {
		t.Error(failure)
	}
}

func evaluateLogOmissions(logs string, assertions []LogOmissionAssert) []string {
	var failures []string

	if logs == "" {
		return []string{"assert_logs_omit: no log output was captured, so the assertion cannot distinguish a fixed leak from a run that logged nothing"}
	}

	for i, a := range assertions {
		if a.RequirePresent != "" && !strings.Contains(logs, a.RequirePresent) {
			failures = append(failures, fmt.Sprintf(
				"assert_logs_omit[%d] (%s): require_present %q is absent from the logs — the run did not reach the code path under test, so the omission of %q proves nothing",
				i, a.Description, a.RequirePresent, strings.Join(a.Markers, ", ")))
			continue
		}
		for _, marker := range a.Markers {
			if strings.Contains(logs, marker) {
				failures = append(failures, fmt.Sprintf(
					"assert_logs_omit[%d] (%s): marker %q reached the logs%s",
					i, a.Description, marker, firstContextLine(logs, marker)))
			}
		}
	}
	return failures
}

func firstContextLine(logs, marker string) string {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, marker) {
			trimmed := strings.TrimSpace(line)
			const maxLen = 300
			if len(trimmed) > maxLen {
				cut := maxLen
				for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
					cut--
				}
				trimmed = trimmed[:cut] + "…"
			}
			return "\n  first offending line: " + trimmed
		}
	}
	return ""
}
