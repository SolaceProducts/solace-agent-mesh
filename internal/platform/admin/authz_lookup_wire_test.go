// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"
)

func TestIdentityLookupWireContract(t *testing.T) {
	const payload = `{
		"gateway_id": "wa-gw",
		"identity_claim_key": "phone_number",
		"identity_claim_value": "15551234567",
		"continuity_token": "US.NEW",
		"continuity_predecessor": "US.OLD",
		"continuity_discard": true
	}`

	var gatewayContext map[string]any
	if err := json.Unmarshal([]byte(payload), &gatewayContext); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	req := identityLookupRequestFrom("whatsapp:15551234567", "wa-gw", gatewayContext)

	if !req.fieldAgnostic() {
		t.Fatal("the request did not read as a claim match; a drifted key admits the sender instead of matching them")
	}
	for _, tc := range []struct{ field, got, want string }{
		{"Key", req.Key, "phone_number"},
		{"Value", req.Value, "15551234567"},
		{"ContinuityToken", req.ContinuityToken, "US.NEW"},
		{"PredecessorToken", req.PredecessorToken, "US.OLD"},
		{"GatewayID", req.GatewayID, "wa-gw"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	if !req.DiscardBinding {
		t.Error("DiscardBinding = false, want true")
	}
}

func TestLookupResponseWireNames(t *testing.T) {
	encoded, err := json.Marshal(lookupResponse{
		Roles:             []string{"support"},
		Scopes:            []string{"agent:TestAgent:invoke"},
		Resolution:        "refused",
		ResolvedUserID:    "01a0b175-0000-7000-8000-000000000000",
		ResolvedPrincipal: "alice@example.com",
		RefusalReason:     refusalSenderChanged,
		RotationApplied:   true,
		RolesSource:       "defaults",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{
		"roles", "scopes", "resolution", "resolved_user_id", "resolved_principal", "refusal_reason", "rotation_applied", "roles_source",
	} {
		if _, present := decoded[key]; !present {
			t.Errorf("reply is missing %q; the gateway's decoder ignores what it does not recognize, so this degrades silently", key)
		}
	}
}
