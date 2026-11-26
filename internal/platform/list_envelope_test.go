// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"
)

func assertDataIsEmptyArray(t *testing.T, body []byte) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if got := string(envelope.Data); got != "[]" {
		t.Errorf("data = %s, want []", got)
	}
}

func TestUnpagedResponse_NilListSerializesAsArray(t *testing.T) {
	body, err := json.Marshal(unpagedResponse[ModelConfigResponse](nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertDataIsEmptyArray(t, body)
}
