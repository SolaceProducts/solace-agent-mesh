// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestOpenAPIAuthHandlerDescriptor(t *testing.T) {
	cases := []struct {
		name              string
		cfg               *AuthConfig
		wantSupportsUser  bool
		wantAuthType      string
		wantDisplayName   string
		wantConnectedWith bool
	}{
		{
			name: "authorization_code",
			cfg: &AuthConfig{
				Type:             AuthTypeOAuth2AuthorizationCode,
				CredentialKey:    "authcode-cred",
				AuthorizationURL: "https://idp.example.com/authorize",
				TokenURL:         "https://idp.example.com/token",
				ClientID:         "client-1",
			},
			wantSupportsUser:  true,
			wantAuthType:      "oauth2_authorization_code",
			wantConnectedWith: true,
		},
		{
			name: "client_credentials",
			cfg: &AuthConfig{
				Type:          AuthTypeOAuth2ClientCredentials,
				CredentialKey: "cc-cred",
				TokenURL:      "https://idp.example.com/token",
				ClientID:      "client-1",
			},
			wantAuthType: "oauth2_client_credentials",
		},
		{
			name: "serviceaccount",
			cfg: &AuthConfig{
				Type:          AuthTypeServiceAccount,
				CredentialKey: "sa-cred",
				TokenURL:      "https://idp.example.com/token",
			},
			wantAuthType: "serviceaccount",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewAuthHandlerForOpenAPI(tc.cfg, slog.Default())
			if err != nil {
				t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
			}
			if got := h.SupportsUserAuth(); got != tc.wantSupportsUser {
				t.Errorf("SupportsUserAuth() = %v, want %v", got, tc.wantSupportsUser)
			}
			if got := h.AuthType(); got != tc.wantAuthType {
				t.Errorf("AuthType() = %q, want %q", got, tc.wantAuthType)
			}
			if got := h.AuthDisplayName(); got != tc.wantDisplayName {
				t.Errorf("AuthDisplayName() = %q, want %q", got, tc.wantDisplayName)
			}

			credSvc := newMemCredSvc()
			toolCtx := makeToolCtx(credSvc)
			if stored := h.CheckStoredCredential(t.Context(), toolCtx); stored.Connected {
				t.Errorf("CheckStoredCredential with no stored token = %+v, want not connected", stored)
			}

			expiresAt := time.Now().Add(time.Hour).Unix()
			tokenJSON, err := json.Marshal(map[string]any{
				"access_token": "tok",
				"token_type":   "Bearer",
				"expires_at":   expiresAt,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := credSvc.Set(t.Context(), toolCtx.AgentName, toolCtx.AgentName, toolCtx.UserID,
				tc.cfg.CredentialKey, string(tokenJSON)); err != nil {
				t.Fatal(err)
			}

			stored := h.CheckStoredCredential(t.Context(), toolCtx)
			if stored.Connected != tc.wantConnectedWith {
				t.Errorf("CheckStoredCredential with stored token = %+v, want connected=%v", stored, tc.wantConnectedWith)
			}
			if tc.wantConnectedWith && !stored.ExpiresAt.Equal(time.Unix(expiresAt, 0).UTC()) {
				t.Errorf("ExpiresAt = %v, want %v", stored.ExpiresAt, time.Unix(expiresAt, 0).UTC())
			}
		})
	}
}
