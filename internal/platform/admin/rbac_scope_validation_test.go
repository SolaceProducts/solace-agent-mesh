// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"testing"
)

func TestValidateScopes_FDMandatedWildcardShapesStayAccepted(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
	}{
		{"literal_three_segment", []string{"agent:hr-bot:invoke"}},
		{"single_segment_wildcard", []string{"agent:*:invoke"}},
		{"trailing_wildcard", []string{"tool:sql:*"}},
		{"full_wildcard", []string{"agent:*:*"}},
		{"bare_star", []string{"*"}},
		{"empty_resource_segment_flat_feature", []string{"rbac:_:read"}},
		{"empty_resource_segment_collection_create", []string{"agent_builder:_:create"}},
		{"underscore_token", []string{"chat:_:read"}},
		{"hyphenated_resource_id", []string{"project:proj-123:*"}},
		{"alphanum_segment", []string{"connector:s3v2:*"}},
		{"mixed_set", []string{
			"agent:*:invoke",
			"tool:sql:*",
			"agent_builder:*:*",
			"agent_builder:_:create",
			"rbac:_:read",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateScopes(tc.scopes); err != nil {
				t.Fatalf("validateScopes(%v) returned error: %v", tc.scopes, err)
			}
		})
	}
}

func TestValidateScopes_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
	}{
		{"empty_string", []string{""}},
		{"uppercase_segment_1", []string{"Agent:hr:invoke"}},
		{"whitespace_inside", []string{"agent: hr :invoke"}},
		{"leading_space", []string{" agent:hr:invoke"}},
		{"trailing_space", []string{"agent:hr:invoke "}},
		{"leading_colon", []string{":agent:invoke"}},
		{"slash_separator", []string{"agent/hr/invoke"}},
		{"period_separator", []string{"agent.hr.invoke"}},
		{"unicode", []string{"agent:héllo:invoke"}},
		{"two_segment", []string{"agent:read"}},
		{"four_segment", []string{"sam:agent:hr-bot:invoke"}},
		{"five_segment", []string{"agent:hr-bot:invoke:extra:more"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateScopes(tc.scopes)
			if err == nil {
				t.Fatalf("validateScopes(%v) accepted invalid input", tc.scopes)
			}
			var v *ValidationError
			if !errors.As(err, &v) {
				t.Fatalf("validateScopes(%v) returned %T, want *ValidationError", tc.scopes, err)
			}
		})
	}
}
