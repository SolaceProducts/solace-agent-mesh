// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"encoding/json"
	"testing"
)

type patchBody struct {
	Name        Patch[string]         `json:"name,omitzero"`
	Description Patch[string]         `json:"description,omitzero"`
	Values      Patch[map[string]any] `json:"values,omitzero"`
}

func TestPatch_SendsLeftOutNullAndValueApart(t *testing.T) {
	body := patchBody{
		Description: Patch[string]{Set: true, Null: true},
		Values:      Patch[map[string]any]{Set: true, Value: map[string]any{"region": "eu"}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"description":null,"values":{"region":"eu"}}`; string(raw) != want {
		t.Errorf("body = %s, want %s", raw, want)
	}

	var back patchBody
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Name.Set {
		t.Error("name was left out but decoded as set")
	}
	if set, null := back.Description.PatchState(); !set || !null {
		t.Errorf("description state = (%t, %t), want sent as null", set, null)
	}
	if !back.Values.Set || back.Values.Null || back.Values.Value["region"] != "eu" {
		t.Errorf("values = %+v, want sent with its value", back.Values)
	}
}
