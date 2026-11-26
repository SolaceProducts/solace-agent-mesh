// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import "testing"

func TestAuthType_IsOAuth2(t *testing.T) {
	cases := []struct {
		in   AuthType
		want bool
	}{
		{AuthTypeOAuth2AuthorizationCode, true},
		{AuthTypeOAuth2ClientCredentials, true},
		{AuthTypeServiceAccount, false},
		{AuthTypeBearer, false},
		{AuthTypeAPIKey, false},
		{AuthTypeBasic, false},
		{AuthTypeNone, false},
		{AuthTypeUnset, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			if got := tc.in.IsOAuth2(); got != tc.want {
				t.Errorf("(%q).IsOAuth2() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestAuthType_RequiresToolsetExchange(t *testing.T) {
	cases := []struct {
		in   AuthType
		want bool
	}{
		{AuthTypeOAuth2ClientCredentials, true},
		{AuthTypeServiceAccount, true},
		{AuthTypeOAuth2AuthorizationCode, false},
		{AuthTypeBearer, false},
		{AuthTypeAPIKey, false},
		{AuthTypeBasic, false},
		{AuthTypeNone, false},
		{AuthTypeUnset, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			if got := tc.in.RequiresToolsetExchange(); got != tc.want {
				t.Errorf("(%q).RequiresToolsetExchange() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
