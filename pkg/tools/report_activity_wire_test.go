// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"testing"
)

func TestWindowControlBody_WireKeysMatchGatewayDTO(t *testing.T) {
	w := Window{WindowKey: "24h"}
	f := activityFilters{TargetUserID: "alice"}

	raw, err := windowControlBody(w, f)
	if err != nil {
		t.Fatalf("windowControlBody: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := out["userId"]; got != "alice" {
		t.Errorf(`body["userId"] = %v, want "alice" (server DTO reads json:"userId")`, got)
	}
	if _, ok := out["targetUserId"]; ok {
		t.Error(`body["targetUserId"] present — server DTO has no field with that json tag; filter would silently drop`)
	}
	if _, ok := out["handlerName"]; ok {
		t.Error(`body["handlerName"] present — agent filter was removed; wire body must not carry it`)
	}
}
