// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

const DefaultMaxResponseSize int64 = 10 << 20

const MaxResponseSizeCeiling int64 = 50 << 20

type ToolsetConfig struct {
	SpecURL    string
	SpecFile   string
	SpecInline string

	SpecFormat string

	BaseURL string

	AllowList []string
	DenyList  []string

	Auth    *AuthConfig
	Headers map[string]string

	RequiredScopes []string

	MaxResponseSize int64
}

type AuthConfig struct {
	Type       AuthType
	Token      string
	HeaderName string
	APIKey     string
	Username   string
	Password   string

	In string

	AuthorizationURL        string
	TokenURL                string
	RefreshURL              string
	ClientID                string
	ClientSecret            string
	Scopes                  []string
	RedirectURI             string
	TokenEndpointAuthMethod string
	UsePKCE                 bool
	DisableConsentPrompt    bool
	Audience                string
	Resource                string
	ServiceAccountJSON      string
	CredentialKey           string
	DisplayName             string
	CACertPath              string
	InsecureSkipVerify      bool
}

func ParseToolsetConfig(raw map[string]any) (*ToolsetConfig, error) {
	cfg := config.FromMap(raw)

	tc := &ToolsetConfig{
		SpecURL:    cfg.GetString("specification_url"),
		SpecFile:   cfg.GetString("specification_file"),
		SpecInline: cfg.GetString("specification"),
		SpecFormat: cfg.GetString("specification_format"),
		BaseURL:    cfg.GetString("base_url"),
	}

	sources := 0
	for _, s := range []string{tc.SpecURL, tc.SpecFile, tc.SpecInline} {
		if s != "" {
			sources++
		}
	}
	if sources == 0 {
		return nil, fmt.Errorf("openapi: one of specification_url, specification_file, or specification is required")
	}
	if sources > 1 {
		return nil, fmt.Errorf("openapi: specification_url, specification_file, and specification are mutually exclusive")
	}

	if tc.SpecFormat != "" && tc.SpecFormat != "json" && tc.SpecFormat != "yaml" {
		return nil, fmt.Errorf("openapi: specification_format must be \"json\" or \"yaml\", got %q", tc.SpecFormat)
	}

	maxResponse, err := parseMaxResponseSize(cfg)
	if err != nil {
		return nil, err
	}
	tc.MaxResponseSize = maxResponse

	if allow := cfg.GetRaw("allow_list"); allow != nil {
		if list, ok := allow.([]any); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					tc.AllowList = append(tc.AllowList, s)
				}
			}
		}
	}

	if deny := cfg.GetRaw("deny_list"); deny != nil {
		if list, ok := deny.([]any); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					tc.DenyList = append(tc.DenyList, s)
				}
			}
		}
	}

	if authCfg := cfg.GetSection("auth"); authCfg != nil {
		userType := authCfg.GetString("type")
		var initialType AuthType
		switch userType {
		case "":
			initialType = AuthTypeUnset
		case "none":
			initialType = AuthTypeNone
		case "bearer":
			initialType = AuthTypeBearer
		case "basic":
			initialType = AuthTypeBasic
		case "apikey":
			initialType = AuthTypeAPIKey
		case "oauth2":
			initialType = AuthTypeUnset
		case "serviceaccount":
			initialType = AuthTypeServiceAccount
		default:
			return nil, fmt.Errorf(
				"openapi: unknown auth.type %q (must be bearer, apikey, basic, oauth2, serviceaccount, or none)",
				userType,
			)
		}

		ac := &AuthConfig{
			Type:     initialType,
			Token:    authCfg.GetString("token"),
			Username: authCfg.GetString("username"),
			Password: authCfg.GetString("password"),

			AuthorizationURL:        authCfg.GetString("authorization_url"),
			TokenURL:                authCfg.GetString("token_url"),
			RefreshURL:              authCfg.GetString("refresh_url"),
			ClientID:                authCfg.GetString("client_id"),
			ClientSecret:            authCfg.GetString("client_secret"),
			RedirectURI:             authCfg.GetString("redirect_uri"),
			TokenEndpointAuthMethod: authCfg.GetString("token_endpoint_auth_method"),
			UsePKCE:                 authCfg.GetBool("use_pkce"),
			DisableConsentPrompt:    authCfg.GetBool("disable_consent_prompt"),
			Audience:                authCfg.GetString("audience"),
			Resource:                authCfg.GetString("resource"),
			ServiceAccountJSON:      authCfg.GetString("service_account_json"),
			CredentialKey:           authCfg.GetString("credential_key"),
			DisplayName:             strings.TrimSpace(authCfg.GetString("display_name")),
			CACertPath:              authCfg.GetString("ca_cert_path"),
			InsecureSkipVerify:      authCfg.GetBool("insecure_skip_verify"),
		}

		if userType == "apikey" {
			ac.HeaderName = authCfg.GetString("name")
			ac.APIKey = authCfg.GetString("value")
			ac.In = authCfg.GetString("in")
			if ac.In == "" {
				ac.In = "header"
			}
		}

		if userType == "oauth2" {
			if ac.AuthorizationURL != "" {
				ac.Type = AuthTypeOAuth2AuthorizationCode
			} else {
				ac.Type = AuthTypeOAuth2ClientCredentials
			}
		}

		if scopesRaw := authCfg.GetRaw("scopes"); scopesRaw != nil {
			ac.Scopes = parseScopes(scopesRaw)
		}
		tc.Auth = ac
	}

	if hdrs := cfg.GetRaw("headers"); hdrs != nil {
		if m, ok := hdrs.(map[string]any); ok {
			tc.Headers = make(map[string]string, len(m))
			for k, v := range m {
				if s, ok := v.(string); ok {
					tc.Headers[k] = s
				}
			}
		}
	}

	if scopes := cfg.GetRaw("required_scopes"); scopes != nil {
		if list, ok := scopes.([]any); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					tc.RequiredScopes = append(tc.RequiredScopes, s)
				}
			}
		}
	}

	return tc, nil
}

func parseMaxResponseSize(cfg config.Config) (int64, error) {
	if !cfg.Has("max_response_size") {
		return 0, nil
	}
	raw := cfg.GetRaw("max_response_size")
	if raw == nil || raw == "" {
		return 0, nil
	}
	v, err := wholeBytes(raw)
	if err != nil {
		return 0, fmt.Errorf(
			"openapi: max_response_size %v %w (omit it for the %d byte default; no value disables the cap)",
			raw, err, DefaultMaxResponseSize)
	}
	switch {
	case v <= 0:
		return 0, fmt.Errorf(
			"openapi: max_response_size must be positive, got %v "+
				"(omit it for the %d byte default; no value disables the cap)",
			raw, DefaultMaxResponseSize)
	case v > MaxResponseSizeCeiling:
		return 0, fmt.Errorf(
			"openapi: max_response_size %v exceeds the %d byte ceiling (the body is held in "+
				"memory while it is read)",
			raw, MaxResponseSizeCeiling)
	default:
		return v, nil
	}
}

func wholeBytes(raw any) (int64, error) {
	switch t := raw.(type) {
	case int:
		return int64(t), nil
	case int64:
		return t, nil
	case float64:
		if t != math.Trunc(t) {
			return 0, fmt.Errorf("must be a whole number of bytes, not fractional")
		}
		if t >= math.MaxInt64 || t <= math.MinInt64 {
			return 0, fmt.Errorf("is out of range for a byte count")
		}
		return int64(t), nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("must be a whole number of bytes")
		}
		return n, nil
	default:
		return 0, fmt.Errorf("must be a whole number of bytes")
	}
}

func clampResponseSize(n int64) int64 {
	if n <= 0 {
		return DefaultMaxResponseSize
	}
	return min(n, MaxResponseSizeCeiling)
}

func parseScopes(v any) []string {
	switch s := v.(type) {
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []string:
		if len(s) == 0 {
			return nil
		}
		out := make([]string, 0, len(s))
		for _, str := range s {
			if str != "" {
				out = append(out, str)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		var out []string
		for part := range strings.FieldsSeq(s) {
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	return nil
}
