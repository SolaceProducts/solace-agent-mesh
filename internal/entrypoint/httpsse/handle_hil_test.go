// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"testing"
)

func makeStatusUpdatePayload(t *testing.T, dataPart map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"result": map[string]any{
			"status": map[string]any{
				"message": map[string]any{
					"parts": []map[string]any{
						{"kind": "data", "data": dataPart},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return payload
}

func TestExtractDataPartFromPayload_HappyPath(t *testing.T) {
	t.Parallel()
	payload := makeStatusUpdatePayload(t, map[string]any{
		"type":       "user_input_request",
		"request_id": "req-1",
		"surface":    map[string]any{"k": "v"},
	})
	got := extractDataPartFromPayload(payload, "user_input_request")
	if got == nil {
		t.Fatal("expected non-nil data; got nil")
	}
	if got["request_id"] != "req-1" {
		t.Errorf("request_id = %v, want req-1", got["request_id"])
	}
}

func TestExtractDataPartFromPayload_TypeMismatchReturnsNil(t *testing.T) {
	t.Parallel()
	payload := makeStatusUpdatePayload(t, map[string]any{
		"type":       "something_else",
		"request_id": "req-1",
	})
	if got := extractDataPartFromPayload(payload, "user_input_request"); got != nil {
		t.Errorf("got %v, want nil for type mismatch", got)
	}
}

func TestExtractDataPartFromPayload_SkipsNonDataParts(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(map[string]any{
		"result": map[string]any{
			"status": map[string]any{
				"message": map[string]any{
					"parts": []map[string]any{
						{"kind": "text", "text": "hello"},
						{"kind": "data", "data": map[string]any{"type": "user_input_request", "request_id": "r-2"}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := extractDataPartFromPayload(payload, "user_input_request")
	if got == nil || got["request_id"] != "r-2" {
		t.Errorf("got %v, want data part with request_id r-2", got)
	}
}

func TestExtractDataPartFromPayload_MalformedReturnsNil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte("")},
		{"not JSON", []byte("definitely not json")},
		{"missing result", []byte(`{"id":"x"}`)},
		{"missing status", []byte(`{"result":{}}`)},
		{"data part not an object", makePayloadWithRawDataPart(t, `"a string"`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := extractDataPartFromPayload(c.payload, "user_input_request"); got != nil {
				t.Errorf("got %v, want nil for malformed input %s", got, c.name)
			}
		})
	}
}

func makePayloadWithRawDataPart(t *testing.T, dataRaw string) []byte {
	t.Helper()
	return []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":` + dataRaw + `}]}}}}`)
}

func TestExtractUserInputRequestFromPayload_DelegatesToHelper(t *testing.T) {
	t.Parallel()
	payload := makeStatusUpdatePayload(t, map[string]any{
		"type":       "user_input_request",
		"request_id": "delegated",
	})
	got := extractUserInputRequestFromPayload(payload)
	if got == nil || got["request_id"] != "delegated" {
		t.Errorf("got %v, want data part with request_id 'delegated'", got)
	}

	other := makeStatusUpdatePayload(t, map[string]any{
		"type":       "user_input_response",
		"request_id": "delegated",
	})
	if got := extractUserInputRequestFromPayload(other); got != nil {
		t.Errorf("got %v, want nil for non-request type", got)
	}
}
