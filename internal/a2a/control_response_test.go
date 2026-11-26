// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import "testing"

func TestControlResponseType(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		data     any
		wantType SignalType
		wantOK   bool
	}{
		{"user_input_response", "data", map[string]any{"type": "user_input_response"}, SignalUserInputResponse, true},
		{"auth_response", "data", map[string]any{"type": "auth_response"}, SignalAuthResponse, true},
		{"a control response with its fields", "data", map[string]any{"type": "user_input_response", "request_id": "r", "status": "answered"}, SignalUserInputResponse, true},
		{"the request half of the exchange", "data", map[string]any{"type": "user_input_request"}, "", false},
		{"another signal", "data", map[string]any{"type": "agent_progress_update"}, "", false},
		{"no type", "data", map[string]any{"request_id": "r"}, "", false},
		{"type of another case", "data", map[string]any{"type": "Auth_Response"}, "", false},
		{"type that is not a string", "data", map[string]any{"type": 7}, "", false},
		{"data that is not an object", "data", "auth_response", "", false},
		{"nil data", "data", nil, "", false},
		{"a text part", "text", map[string]any{"type": "auth_response"}, "", false},
		{"a file part", "file", map[string]any{"type": "user_input_response"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotOK := ControlResponseType(tc.kind, tc.data)
			if gotType != tc.wantType || gotOK != tc.wantOK {
				t.Errorf("ControlResponseType = (%q, %v), want (%q, %v)", gotType, gotOK, tc.wantType, tc.wantOK)
			}
		})
	}
}
