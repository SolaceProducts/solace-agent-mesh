// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"testing"
)

func TestValidateTaskMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		wantErr bool
	}{
		{"plain string", `"run report"`, false},
		{"blank string", `"   "`, true},
		{"text part by kind", `[{"kind":"text","text":"run report"}]`, false},
		{"text part by type", `[{"type":"text","text":"run report"}]`, false},
		{"text part without kind or type", `[{"text":"run report"}]`, false},
		{"blank text part without kind or type", `[{"text":"   "}]`, true},
		{"data part", `[{"kind":"data","data":{"k":"v"}}]`, false},
		{"data part with null data", `[{"kind":"data","data":null}]`, true},
		{"file part only", `[{"kind":"file","text":"x"}]`, true},
		{"empty list", `[]`, true},
		{"object", `{}`, true},
		{"number", `7`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTaskMessage(json.RawMessage(tt.message))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTaskMessage(%s) = %v, wantErr %v", tt.message, err, tt.wantErr)
			}
		})
	}
}
