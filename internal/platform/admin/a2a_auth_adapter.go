// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"strings"
)

func cardAuthConfigFromStored(authType string, stored map[string]any, logger *slog.Logger) *CardAuthConfig {
	if authType == "" || authType == "none" || stored == nil {
		return nil
	}
	s := func(k string) string {
		v, _ := stored[k].(string)
		return v
	}
	switch strings.ToLower(authType) {
	case "bearer":
		if s("token") == "" {
			return nil
		}
		return &CardAuthConfig{BearerToken: s("token")}
	case "apikey":
		if s("api_key") == "" {
			return nil
		}
		header := s("header_name")
		if header == "" {
			header = "X-API-Key"
		}
		return &CardAuthConfig{
			ApikeyValue:      s("api_key"),
			ApikeyHeaderName: header,
		}
	case "oauth2":
		if s("client_id") == "" || s("client_secret") == "" || s("token_endpoint") == "" {
			return nil
		}
		return &CardAuthConfig{
			OAuth2ClientID:     s("client_id"),
			OAuth2ClientSecret: s("client_secret"),
			OAuth2TokenURL:     s("token_endpoint"),
			OAuth2Scope:        scopeStringFromStored(stored["scopes"]),
		}
	case "basic":
		if s("username") == "" || s("password") == "" {
			return nil
		}
		return &CardAuthConfig{
			BasicUsername: s("username"),
			BasicPassword: s("password"),
		}
	default:
		if logger != nil {
			logger.Warn("unknown auth type for card-fetch adapter — ignoring",
				"authType", authType)
		}
		return nil
	}
}

func scopeStringFromStored(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, s := range v {
			if str, ok := s.(string); ok && str != "" {
				parts = append(parts, str)
			}
		}
		return strings.Join(parts, " ")
	case []string:
		return strings.Join(v, " ")
	}
	return ""
}
