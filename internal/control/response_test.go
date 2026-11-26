// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"encoding/json"
	"testing"
)

func TestSuccessResponse(t *testing.T) {
	resp := SuccessResponse(map[string]string{"status": "ok"})
	if resp.Error != nil {
		t.Error("expected no error")
	}
	if resp.Result == nil {
		t.Error("expected result")
	}
}

func TestErrorResponse(t *testing.T) {
	resp := ErrorResponse(ErrNotFound, "app not found")
	if resp.Result != nil {
		t.Error("expected no result")
	}
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != ErrNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrNotFound)
	}
	if resp.Error.Message != "app not found" {
		t.Errorf("message = %q, want %q", resp.Error.Message, "app not found")
	}
}

func TestMarshalUnmarshalResponse(t *testing.T) {
	tests := []struct {
		name string
		resp *Response
	}{
		{
			name: "success with map",
			resp: SuccessResponse(map[string]string{"key": "value"}),
		},
		{
			name: "success with string",
			resp: SuccessResponse("hello"),
		},
		{
			name: "error",
			resp: ErrorResponse(ErrAuthDenied, "access denied"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := MarshalResponse(tt.resp)
			if err != nil {
				t.Fatal(err)
			}

			got, err := UnmarshalResponse(data)
			if err != nil {
				t.Fatal(err)
			}

			if tt.resp.Error != nil {
				if got.Error == nil {
					t.Fatal("expected error in unmarshaled response")
				}
				if got.Error.Code != tt.resp.Error.Code {
					t.Errorf("code = %d, want %d", got.Error.Code, tt.resp.Error.Code)
				}
				if got.Error.Message != tt.resp.Error.Message {
					t.Errorf("message = %q, want %q", got.Error.Message, tt.resp.Error.Message)
				}
			} else {
				if got.Error != nil {
					t.Error("expected no error in unmarshaled response")
				}
				if got.Result == nil {
					t.Error("expected result in unmarshaled response")
				}
			}
		})
	}
}

func TestUnmarshalResponse_Invalid(t *testing.T) {
	_, err := UnmarshalResponse([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestErrorCodes(t *testing.T) {
	codes := map[int]string{
		ErrInvalidRequest:   "invalid_request",
		ErrMethodNotAllowed: "method_not_allowed",
		ErrNotFound:         "not_found",
		ErrConflict:         "conflict",
		ErrAuthDenied:       "auth_denied",
		ErrOperationFailed:  "operation_failed",
	}
	if len(codes) != 6 {
		t.Error("expected 6 distinct error codes")
	}
}

func TestResponseJSON_Shape(t *testing.T) {
	resp := SuccessResponse(map[string]any{"apps": []string{"a", "b"}})
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["result"]; !ok {
		t.Error("expected 'result' key in JSON")
	}
	if _, ok := raw["error"]; ok {
		t.Error("expected no 'error' key in success response JSON")
	}

	errResp := ErrorResponse(ErrNotFound, "gone")
	data, _ = json.Marshal(errResp)
	raw = nil
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["error"]; !ok {
		t.Error("expected 'error' key in error response JSON")
	}
}
