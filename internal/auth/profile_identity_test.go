// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"encoding/json"
	"testing"
)

func parseProfile(t *testing.T, blob string) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		t.Fatalf("unmarshal blob: %v", err)
	}
	prof, ok := cfg["user_profile"].(map[string]any)
	if !ok {
		t.Fatalf("user_profile missing or wrong type in %q", blob)
	}
	return prof
}

func TestReconcileProfileIdentity_PinsIDAndEmailFromJWT(t *testing.T) {
	identity := &Identity{
		UserID: "alice@corp.com",
		Claims: map[string]any{"email": "alice@corp.com"},
	}
	blob := `{"_enterprise_capabilities":["tool:*:invoke"],"user_profile":{"id":"victim","work_email":"victim@corp.com","display_name":"Alice A.","job_title":"Engineer","department":"R&D","location":"Ottawa"}}`

	out := ReconcileProfileIdentity(identity, blob)
	prof := parseProfile(t, out)

	if prof["id"] != "alice@corp.com" {
		t.Errorf("id = %v, want alice@corp.com", prof["id"])
	}
	if prof["work_email"] != "alice@corp.com" {
		t.Errorf("work_email = %v, want alice@corp.com", prof["work_email"])
	}
	for k, want := range map[string]string{
		"display_name": "Alice A.",
		"job_title":    "Engineer",
		"department":   "R&D",
		"location":     "Ottawa",
	} {
		if prof[k] != want {
			t.Errorf("enrichment field %s = %v, want %q (must be preserved)", k, prof[k], want)
		}
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["_enterprise_capabilities"]; !ok {
		t.Error("_enterprise_capabilities must be preserved")
	}
}

func TestReconcileProfileIdentity_DropsWorkEmailWhenJWTHasNone(t *testing.T) {
	identity := &Identity{UserID: "u-1", Claims: map[string]any{}}
	blob := `{"user_profile":{"id":"victim","work_email":"forged@corp.com","display_name":"X"}}`

	out := ReconcileProfileIdentity(identity, blob)
	prof := parseProfile(t, out)

	if prof["id"] != "u-1" {
		t.Errorf("id = %v, want u-1", prof["id"])
	}
	if _, present := prof["work_email"]; present {
		t.Errorf("work_email must be dropped when the JWT carries no email, got %v", prof["work_email"])
	}
	if prof["display_name"] != "X" {
		t.Errorf("display_name must be preserved, got %v", prof["display_name"])
	}
}

func TestReconcileProfileIdentity_NilIdentityPassesThrough(t *testing.T) {
	blob := `{"user_profile":{"id":"whoever","work_email":"who@corp.com"}}`
	if out := ReconcileProfileIdentity(nil, blob); out != blob {
		t.Errorf("nil identity should pass the blob through unchanged; got %q", out)
	}
}

func TestReconcileProfileIdentity_EdgeCasesPassThrough(t *testing.T) {
	identity := &Identity{UserID: "u-1", Claims: map[string]any{"email": "u-1@corp.com"}}
	cases := map[string]string{
		"empty blob":              "",
		"malformed json":          "{not json",
		"no user_profile key":     `{"_enterprise_capabilities":["*"]}`,
		"user_profile not object": `{"user_profile":"oops"}`,
	}
	for name, blob := range cases {
		t.Run(name, func(t *testing.T) {
			if out := ReconcileProfileIdentity(identity, blob); out != blob {
				t.Errorf("%s: expected pass-through, got %q", name, out)
			}
		})
	}
}
