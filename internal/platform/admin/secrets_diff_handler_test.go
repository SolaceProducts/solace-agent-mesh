// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "testing"

func required(name string) secretField { return secretField{Name: name, Required: true} }
func optional(name string) secretField { return secretField{Name: name, Required: false} }

func TestCompareSecrets_MissingCandidatePreservesExisting(t *testing.T) {
	stored := map[string]any{"api_key": "stored-value"}
	got := compareSecrets([]secretField{required("api_key")}, map[string]any{}, stored)
	if !got["api_key"].Matches {
		t.Errorf("omit-from-candidate must report matches=true (preserve existing); got %+v", got)
	}
}

func TestCompareSecrets_JSONNullCandidatePreservesExisting(t *testing.T) {
	stored := map[string]any{"api_key": "stored-value"}
	candidate := map[string]any{"api_key": nil}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if !got["api_key"].Matches {
		t.Errorf("JSON null candidate must report matches=true (preserve); got %+v", got)
	}
}

func TestCompareSecrets_SentinelTreatedAsMatch(t *testing.T) {
	stored := map[string]any{"api_key": "real-stored"}
	candidate := map[string]any{"api_key": "<REDACTED>"}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if !got["api_key"].Matches {
		t.Errorf("<REDACTED> round-trip must report matches=true; got %+v", got)
	}
}

func TestCompareSecrets_EmptyStringOnRequiredPreservesExisting(t *testing.T) {
	stored := map[string]any{"api_key": "stored-value"}
	candidate := map[string]any{"api_key": ""}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if !got["api_key"].Matches {
		t.Errorf("empty-string on required must report matches=true (preserve); got %+v", got)
	}
}

func TestCompareSecrets_EmptyStringOnOptionalIsRotation(t *testing.T) {
	stored := map[string]any{"token": "stored-value"}
	candidate := map[string]any{"token": ""}
	got := compareSecrets([]secretField{optional("token")}, candidate, stored)
	if got["token"].Matches {
		t.Errorf("empty-string on optional must report matches=false (delete); got %+v", got)
	}
}

func TestCompareSecrets_MatchingValueReportsTrue(t *testing.T) {
	stored := map[string]any{"api_key": "xoxb-abc-123"}
	candidate := map[string]any{"api_key": "xoxb-abc-123"}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if !got["api_key"].Matches {
		t.Errorf("equal byte strings must report matches=true; got %+v", got)
	}
}

func TestCompareSecrets_DifferingValueReportsFalse(t *testing.T) {
	stored := map[string]any{"api_key": "xoxb-old"}
	candidate := map[string]any{"api_key": "xoxb-rotated"}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if got["api_key"].Matches {
		t.Errorf("rotated secret must report matches=false; got %+v", got)
	}
}

func TestCompareSecrets_NonStringCandidateReportsFalse(t *testing.T) {
	stored := map[string]any{"api_key": "stored"}
	candidate := map[string]any{"api_key": map[string]any{"oops": "object"}}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if got["api_key"].Matches {
		t.Errorf("non-string candidate must report matches=false; got %+v", got)
	}
}

func TestCompareSecrets_NonStringStoredReportsFalse(t *testing.T) {
	stored := map[string]any{"api_key": 42}
	candidate := map[string]any{"api_key": "real-value"}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if got["api_key"].Matches {
		t.Errorf("non-string stored value must report matches=false; got %+v", got)
	}
}

func TestCompareSecrets_MissingStoredWithCandidatePresentReportsFalse(t *testing.T) {
	stored := map[string]any{}
	candidate := map[string]any{"api_key": "fresh-value"}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if got["api_key"].Matches {
		t.Errorf("new secret with no stored value must report matches=false; got %+v", got)
	}
}

func TestCompareSecrets_NonSecretFieldsIgnored(t *testing.T) {
	stored := map[string]any{"api_key": "stored", "region": "us-east-1"}
	candidate := map[string]any{"api_key": "stored", "region": "us-west-2"}
	got := compareSecrets([]secretField{required("api_key")}, candidate, stored)
	if len(got) != 1 {
		t.Errorf("non-secret region key must not appear in result; got %+v", got)
	}
}

func TestModelAuthSecretKeys_KnownAuthTypes(t *testing.T) {
	cases := []struct {
		authType string
		want     []string
	}{
		{"apikey", []string{"api_key"}},
		{"oauth2", []string{"client_secret"}},
		{"bearer", []string{"token"}},
		{"basic", []string{"password"}},
		{"aws_iam", []string{"aws_secret_access_key", "aws_session_token"}},
		{"gcp_service_account", []string{"service_account_json", "vertex_credentials"}},
		{"unknown_kind", nil},
		{"", nil},
	}
	for _, tc := range cases {
		t.Run(tc.authType, func(t *testing.T) {
			got := modelAuthSecretKeys[tc.authType]
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
