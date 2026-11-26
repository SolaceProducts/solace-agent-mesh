// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func (h *TestHarness) AssertEventMeshOutput(t *testing.T, events []CollectedEvent, expected []ExpectedEventMeshOutput) {
	t.Helper()

	if len(expected) == 0 {
		return
	}

	if len(events) == 0 {
		t.Fatalf("expected %d event mesh output(s) but got none", len(expected))
	}

	for i, exp := range expected {
		if i >= len(events) {
			t.Errorf("expected event mesh output[%d] not received (only got %d events)", i, len(events))
			continue
		}
		evt := events[i]
		assertEMOutput(t, i, evt, exp)
	}
}

func assertEMOutput(t *testing.T, idx int, evt CollectedEvent, exp ExpectedEventMeshOutput) {
	t.Helper()
	prefix := fmt.Sprintf("em_output[%d]", idx)

	if exp.TopicExact != "" && evt.Topic != exp.TopicExact {
		t.Errorf("%s: topic = %q, want exact %q", prefix, evt.Topic, exp.TopicExact)
	}
	if exp.TopicContains != "" && !strings.Contains(evt.Topic, exp.TopicContains) {
		t.Errorf("%s: topic %q does not contain %q", prefix, evt.Topic, exp.TopicContains)
	}

	payloadStr := string(evt.Payload)

	for _, substr := range exp.PayloadTextContains {
		if !strings.Contains(payloadStr, substr) {
			t.Errorf("%s: payload does not contain %q\n  payload: %s", prefix, substr, truncate(payloadStr, 500))
		}
	}

	if exp.PayloadExact != "" && payloadStr != exp.PayloadExact {
		t.Errorf("%s: payload = %q, want exact %q", prefix, truncate(payloadStr, 200), exp.PayloadExact)
	}

	if len(exp.PayloadContains) > 0 {
		var payloadMap map[string]any
		if err := json.Unmarshal(evt.Payload, &payloadMap); err != nil {
			t.Errorf("%s: payload is not valid JSON for payload_contains check: %v\n  payload: %s",
				prefix, err, truncate(payloadStr, 200))
			return
		}

		for key, expectedVal := range exp.PayloadContains {
			actualVal, exists := payloadMap[key]
			if !exists {
				t.Errorf("%s: payload missing key %q", prefix, key)
				continue
			}
			if !jsonValuesMatch(expectedVal, actualVal) {
				t.Errorf("%s: payload[%q] = %v, want %v", prefix, key, actualVal, expectedVal)
			}
		}
	}
}

func jsonValuesMatch(expected, actual any) bool {
	eb, err1 := json.Marshal(expected)
	ab, err2 := json.Marshal(actual)
	if err1 != nil || err2 != nil {
		return fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
	}
	return string(eb) == string(ab)
}
