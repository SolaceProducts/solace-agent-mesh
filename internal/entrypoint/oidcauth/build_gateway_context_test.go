// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"reflect"
	"testing"
)

func TestBuildGatewayContext_IDTokenOnlyProvidesGroups(t *testing.T) {
	p := newTestProvider(t, nil)
	userInfo := &UserInfo{
		Sub:   "user-1",
		Email: "u@example.com",
		Name:  "User One",
		RawResponse: map[string]any{
			"sub":   "user-1",
			"email": "u@example.com",
			"name":  "User One",
		},
	}
	idTokenClaims := map[string]any{
		"groups": []any{"gid-admin"},
	}

	ctx := p.buildGatewayContext(userInfo, idTokenClaims)
	claims, ok := ctx["idp_claims"].(map[string]any)
	if !ok {
		t.Fatalf("idp_claims missing or wrong type: %T", ctx["idp_claims"])
	}
	got, ok := claims["groups"].([]any)
	if !ok {
		t.Fatalf("groups missing from merged idp_claims: %v", claims)
	}
	if !reflect.DeepEqual(got, []any{"gid-admin"}) {
		t.Errorf("groups = %v, want [gid-admin]", got)
	}
	if claims["email"] != "u@example.com" {
		t.Errorf("userinfo email lost in merge: %v", claims["email"])
	}
}

func TestBuildGatewayContext_UserInfoOnly(t *testing.T) {
	p := newTestProvider(t, nil)
	userInfo := &UserInfo{
		RawResponse: map[string]any{
			"sub":   "user-2",
			"email": "u2@example.com",
		},
	}

	ctx := p.buildGatewayContext(userInfo, nil)
	claims, ok := ctx["idp_claims"].(map[string]any)
	if !ok {
		t.Fatalf("idp_claims missing or wrong type: %T", ctx["idp_claims"])
	}
	if claims["sub"] != "user-2" {
		t.Errorf("sub = %v, want user-2", claims["sub"])
	}
}

func TestBuildGatewayContext_IDTokenOverridesOnConflict(t *testing.T) {
	p := newTestProvider(t, nil)
	userInfo := &UserInfo{
		RawResponse: map[string]any{
			"sub":  "from-userinfo",
			"name": "From UserInfo",
		},
	}
	idTokenClaims := map[string]any{
		"sub":    "from-id-token",
		"groups": []any{"gid-admin"},
	}

	ctx := p.buildGatewayContext(userInfo, idTokenClaims)
	claims, ok := ctx["idp_claims"].(map[string]any)
	if !ok {
		t.Fatalf("idp_claims missing or wrong type: %T", ctx["idp_claims"])
	}
	if claims["sub"] != "from-id-token" {
		t.Errorf("id_token should override on conflict: sub = %v, want from-id-token", claims["sub"])
	}
	if claims["name"] != "From UserInfo" {
		t.Errorf("non-overlapping userinfo key lost: name = %v, want From UserInfo", claims["name"])
	}
}

func TestBuildGatewayContext_BothNilOmitsIdpClaims(t *testing.T) {
	p := newTestProvider(t, nil)
	ctx := p.buildGatewayContext(&UserInfo{}, nil)
	if _, present := ctx["idp_claims"]; present {
		t.Errorf("idp_claims should be absent when both sources are nil, got %v", ctx["idp_claims"])
	}
	if ctx["gateway_id"] != "gw-1" {
		t.Errorf("gateway_id = %v, want gw-1", ctx["gateway_id"])
	}
	if ctx["oidc_provider"] != "azure" {
		t.Errorf("oidc_provider = %v, want azure", ctx["oidc_provider"])
	}
}

func TestBuildGatewayContext_IDTokenOnlyPopulatesIdpClaims(t *testing.T) {
	p := newTestProvider(t, nil)
	userInfo := &UserInfo{}
	idTokenClaims := map[string]any{
		"sub":    "user-3",
		"groups": []any{"gid-admin"},
	}

	ctx := p.buildGatewayContext(userInfo, idTokenClaims)
	claims, ok := ctx["idp_claims"].(map[string]any)
	if !ok {
		t.Fatalf("idp_claims missing even though id_token claims were provided: %T", ctx["idp_claims"])
	}
	if claims["sub"] != "user-3" {
		t.Errorf("sub = %v, want user-3", claims["sub"])
	}
}

func TestBuildGatewayContext_SynthesizesFromUserInfoFields(t *testing.T) {
	p := newTestProvider(t, nil)
	userInfo := &UserInfo{
		Sub:   "sub-5",
		Email: "u5@example.com",
		Name:  "User Five",
	}
	ctx := p.buildGatewayContext(userInfo, nil)
	claims, ok := ctx["idp_claims"].(map[string]any)
	if !ok {
		t.Fatalf("idp_claims missing: %T", ctx["idp_claims"])
	}
	if claims["sub"] != "sub-5" || claims["email"] != "u5@example.com" || claims["name"] != "User Five" {
		t.Errorf("synthesized claims = %v, want sub/email/name from UserInfo", claims)
	}
}

func TestVerifyIDTokenClaims_NilOnEmpty(t *testing.T) {
	p := newTestProvider(t, nil)
	got, err := p.verifyIDTokenClaims(t.Context(), "")
	if err != nil {
		t.Fatalf("verifyIDTokenClaims(\"\") err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("verifyIDTokenClaims(\"\") = %v, want nil", got)
	}
}

func TestVerifyIDTokenClaims_FailsClosedWhenVerifierMissing(t *testing.T) {
	p := newTestProvider(t, nil)
	if _, err := p.verifyIDTokenClaims(t.Context(), "header.payload.sig"); err == nil {
		t.Fatal("expected error when verifier is unconfigured and id_token is non-empty")
	}
}

func TestBuildGatewayContext_FallsBackToUserInfoClaims(t *testing.T) {
	p := newTestProvider(t, nil)
	userInfo := &UserInfo{
		Claims: map[string]any{
			"sub": "user-4",
		},
	}
	ctx := p.buildGatewayContext(userInfo, nil)
	claims, ok := ctx["idp_claims"].(map[string]any)
	if !ok {
		t.Fatalf("idp_claims missing: %T", ctx["idp_claims"])
	}
	if claims["sub"] != "user-4" {
		t.Errorf("sub = %v, want user-4", claims["sub"])
	}
}
