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

func (h *TestHarness) matchStatusEvent(collected CollectedEvent, expected ExpectedEvent) bool {
	if expected.SignalType != "" {
		signalType := extractSignalType(collected.Payload)
		return signalType == expected.SignalType
	}
	if len(expected.ContentParts) > 0 {
		parts := extractStatusParts(collected.Payload)
		hasTextPart := false
		for _, p := range parts {
			kind, _ := p["kind"].(string)
			if kind != "text" {
				continue
			}
			hasTextPart = true
			text, _ := p["text"].(string)
			if len(expected.ContentParts) > 0 && len(expected.ContentParts[0].TextContains) > 0 {
				firstExpected := expected.ContentParts[0].TextContains[0]
				if strings.Contains(text, firstExpected) {
					return true
				}
			} else {
				return true
			}
		}
		_ = hasTextPart
		return false
	}
	return true
}

func (h *TestHarness) assertStatusEvent(t *testing.T, idx int, collected CollectedEvent, expected ExpectedEvent) {
	t.Helper()
	prefix := fmt.Sprintf("expected_event[%d](status_update)", idx)

	if expected.SignalType != "" || len(expected.SignalDataContains) > 0 {
		signalData := extractSignalData(collected.Payload)
		if signalData == nil {
			t.Errorf("%s: could not extract signal data from status event payload", prefix)
			return
		}

		if expected.SignalType != "" {
			if st, ok := signalData["type"].(string); !ok || st != expected.SignalType {
				t.Errorf("%s: signal_type = %v, want %q", prefix, signalData["type"], expected.SignalType)
			}
		}

		for k, wantVal := range expected.SignalDataContains {
			gotVal, ok := signalData[k]
			if !ok {
				t.Errorf("%s: signal_data_contains key %q not found in signal data", prefix, k)
				continue
			}
			wantJSON, _ := json.Marshal(wantVal)
			gotJSON, _ := json.Marshal(gotVal)
			if string(wantJSON) != string(gotJSON) {
				t.Errorf("%s: signal_data_contains[%q] = %s, want %s", prefix, k, gotJSON, wantJSON)
			}
		}
	}

	if len(expected.ContentParts) > 0 {
		rawParts := extractStatusPartsRaw(collected.Payload)
		if rawParts == nil {
			t.Errorf("%s: could not extract parts from status event payload for content_parts assertion", prefix)
			return
		}
		h.assertMessageParts(t, prefix, rawParts, expected.ContentParts)
	}
}

func extractStatusMessage(payload json.RawMessage) (json.RawMessage, error) {
	var envelope struct {
		Result struct {
			Status struct {
				Message struct {
					Parts json.RawMessage `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal status envelope: %w", err)
	}
	return envelope.Result.Status.Message.Parts, nil
}

func extractSignalType(payload json.RawMessage) string {
	data := extractSignalData(payload)
	if data == nil {
		return ""
	}
	if t, ok := data["type"].(string); ok {
		return t
	}
	return ""
}

func extractSignalData(payload json.RawMessage) map[string]any {
	rawParts, err := extractStatusMessage(payload)
	if err != nil || len(rawParts) == 0 {
		return nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		return nil
	}
	for _, raw := range parts {
		var part struct {
			Kind string         `json:"kind"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &part); err != nil {
			continue
		}
		if part.Kind == "data" && part.Data != nil {
			return part.Data
		}
	}
	return nil
}

func extractStatusParts(payload json.RawMessage) []map[string]any {
	rawParts, err := extractStatusMessage(payload)
	if err != nil || len(rawParts) == 0 {
		return nil
	}
	var parts []map[string]any
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		return nil
	}
	return parts
}

func extractStatusPartsRaw(payload json.RawMessage) json.RawMessage {
	rawParts, err := extractStatusMessage(payload)
	if err != nil {
		return nil
	}
	return rawParts
}

func (h *TestHarness) assertFinalResponse(t *testing.T, idx int, collected CollectedEvent, expected ExpectedEvent) {
	t.Helper()
	prefix := fmt.Sprintf("expected_event[%d]", idx)

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(collected.Payload, &envelope); err != nil {
		t.Errorf("%s: unmarshal response: %v", prefix, err)
		return
	}

	expectsError := expected.Error != nil || expected.Type == "error" || expected.ErrorCode != 0 || expected.ErrorMessageContains != ""
	if expectsError {
		if len(envelope.Error) == 0 || string(envelope.Error) == "null" {
			t.Errorf("%s: expected error but got success", prefix)
			return
		}
		var errObj struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(envelope.Error, &errObj); err != nil {
			t.Errorf("%s: unmarshal error: %v", prefix, err)
			return
		}
		expectedCode := expected.ErrorCode
		if expected.Error != nil && expected.Error.Code != 0 {
			expectedCode = expected.Error.Code
		}
		if expectedCode != 0 && errObj.Code != expectedCode {
			t.Errorf("%s: error_code: expected %d, got %d", prefix, expectedCode, errObj.Code)
		}
		expectedMsg := expected.ErrorMessageContains
		if expected.Error != nil && expected.Error.MessageContains != "" {
			expectedMsg = expected.Error.MessageContains
		}
		if expectedMsg != "" && !strings.Contains(errObj.Message, expectedMsg) {
			t.Errorf("%s: error_message: expected to contain %q, got %q", prefix, expectedMsg, errObj.Message)
		}
		return
	}

	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		t.Errorf("%s: unexpected error in response: %s", prefix, string(envelope.Error))
		return
	}

	var task struct {
		ID        string `json:"id"`
		ContextID string `json:"contextId"`
		Status    struct {
			State   string `json:"state"`
			Message struct {
				Kind      string          `json:"kind"`
				MessageID string          `json:"messageId"`
				Role      string          `json:"role"`
				Parts     json.RawMessage `json:"parts"`
			} `json:"message"`
		} `json:"status"`
	}
	if err := json.Unmarshal(envelope.Result, &task); err != nil {
		t.Errorf("%s: unmarshal task: %v", prefix, err)
		return
	}

	if expected.ID != "" && !MatchWildcard(task.ID, expected.ID) {
		t.Errorf("%s: id mismatch: expected %q, got %q", prefix, expected.ID, task.ID)
	}
	if expected.ContextID != "" && !MatchWildcard(task.ContextID, expected.ContextID) {
		t.Errorf("%s: contextId mismatch: expected %q, got %q", prefix, expected.ContextID, task.ContextID)
	}

	if expected.Status != nil {
		if expected.Status.State != "" && task.Status.State != expected.Status.State {
			t.Errorf("%s: status.state: expected %q, got %q", prefix, expected.Status.State, task.Status.State)
		}

		if expected.Status.Message != nil {
			h.assertMessageParts(t, prefix, task.Status.Message.Parts, expected.Status.Message.Parts)
		}
	}

	if expected.TaskState != "" && task.Status.State != expected.TaskState {
		t.Errorf("%s: task_state: expected %q, got %q", prefix, expected.TaskState, task.Status.State)
	}
	if len(expected.ContentParts) > 0 {
		h.assertMessageParts(t, prefix, task.Status.Message.Parts, expected.ContentParts)
	}
}

func (h *TestHarness) assertMessageParts(t *testing.T, prefix string, rawParts json.RawMessage, expectedParts []PartMatch) {
	t.Helper()

	var parts []struct {
		Kind     string          `json:"kind"`
		Text     string          `json:"text"`
		Data     json.RawMessage `json:"data"`
		Filename string          `json:"filename"`
		MimeType string          `json:"mime_type"`
		File     *struct {
			Name     string `json:"name"`
			MIMEType string `json:"mimeType"`
			URI      string `json:"uri"`
		} `json:"file"`
	}
	if err := json.Unmarshal(rawParts, &parts); err != nil {
		t.Errorf("%s: unmarshal parts: %v", prefix, err)
		return
	}
	for i := range parts {
		if parts[i].File != nil {
			if parts[i].Filename == "" {
				parts[i].Filename = parts[i].File.Name
			}
			if parts[i].MimeType == "" {
				parts[i].MimeType = parts[i].File.MIMEType
			}
		}
	}

	hasFileParts := false
	for _, exp := range expectedParts {
		kind := exp.Kind
		if kind == "" {
			kind = exp.Type
		}
		if kind == "file" {
			hasFileParts = true
			break
		}
	}

	if hasFileParts {
		for i, exp := range expectedParts {
			partPrefix := fmt.Sprintf("%s.parts[%d]", prefix, i)
			expectedKind := exp.Kind
			if expectedKind == "" {
				expectedKind = exp.Type
			}

			if i >= len(parts) {
				t.Errorf("%s: expected part at index %d, but only %d parts", partPrefix, i, len(parts))
				continue
			}
			actual := parts[i]

			if expectedKind != "" && actual.Kind != expectedKind {
				t.Errorf("%s: kind mismatch: expected %q, got %q", partPrefix, expectedKind, actual.Kind)
				continue
			}

			switch expectedKind {
			case "text", "":
				if err := MatchPartText(actual.Text, exp); err != nil {
					t.Errorf("%s: %v", partPrefix, err)
				}
			case "file":
				if exp.Filename != "" && actual.Filename != exp.Filename {
					t.Errorf("%s: filename mismatch: expected %q, got %q", partPrefix, exp.Filename, actual.Filename)
				}
				if exp.MimeType != "" && actual.MimeType != exp.MimeType {
					t.Errorf("%s: mime_type mismatch: expected %q, got %q", partPrefix, exp.MimeType, actual.MimeType)
				}
			case "data":
				if exp.DataContains.Map != nil && len(actual.Data) > 0 {
					if err := MatchJSONContains(actual.Data, exp.DataContains.Map); err != nil {
						t.Errorf("%s: data_contains: %v", partPrefix, err)
					}
				}
			}
		}
		return
	}

	var allText []string
	for _, p := range parts {
		if p.Kind == "text" && p.Text != "" {
			allText = append(allText, p.Text)
		}
	}
	aggregatedText := strings.Join(allText, "")

	var dataParts []json.RawMessage
	for _, p := range parts {
		if p.Kind == "data" && len(p.Data) > 0 {
			dataParts = append(dataParts, p.Data)
		}
	}

	for i, exp := range expectedParts {
		partPrefix := fmt.Sprintf("%s.parts[%d]", prefix, i)

		expectedKind := exp.Kind
		if expectedKind == "" {
			expectedKind = exp.Type
		}

		switch expectedKind {
		case "text", "":
			if err := MatchPartText(aggregatedText, exp); err != nil {
				t.Errorf("%s: %v", partPrefix, err)
			}
		case "data":
			if len(dataParts) == 0 {
				t.Errorf("%s: expected a data part but none found", partPrefix)
				continue
			}
			if exp.DataContains.Map != nil {
				if err := MatchJSONContains(dataParts[0], exp.DataContains.Map); err != nil {
					t.Errorf("%s: data_contains: %v", partPrefix, err)
				}
			}
		default:
			if i < len(parts) {
				actual := parts[i]
				if expectedKind != "" && actual.Kind != expectedKind {
					t.Errorf("%s: kind mismatch: expected %q, got %q", partPrefix, expectedKind, actual.Kind)
				}
				if err := MatchPartText(actual.Text, exp); err != nil {
					t.Errorf("%s: %v", partPrefix, err)
				}
			} else {
				t.Errorf("%s: expected part at index %d, but only %d parts", partPrefix, i, len(parts))
			}
		}
	}
}
