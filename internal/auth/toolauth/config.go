// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

type AuthConfig struct {
	Type          string
	Scheme        *OAuthScheme
	Credential    *AuthCredential
	CredentialKey string

	ResourceURL string

	DisplayName string

	CACertPath         string
	InsecureSkipVerify bool

	DisablePKCE bool

	DisableConsentPrompt bool
}

type OAuthScheme struct {
	AuthorizationURL        string
	TokenURL                string
	RefreshURL              string
	Scopes                  []string
	TokenEndpointAuthMethod string
	Audience                string

	Resource string
}

type AuthCredential struct {
	ClientID     string
	ClientSecret string
	Username     string
	Password     string
	Token        string
}

func ParseAuthConfig(raw map[string]any, defaultKey string) *AuthConfig {
	authType, _ := raw["type"].(string)
	if authType == "" {
		return nil
	}

	ac := &AuthConfig{
		Type: authType,
	}

	ac.CACertPath, _ = raw["ca_cert_path"].(string)
	ac.InsecureSkipVerify, _ = raw["insecure_skip_verify"].(bool)
	displayName, _ := raw["display_name"].(string)
	ac.DisplayName = strings.TrimSpace(displayName)

	if key, ok := raw["credential_key"].(string); ok && key != "" {
		ac.CredentialKey = key
	} else {
		ac.CredentialKey = defaultKey + "_" + authType
	}

	if credMap, ok := raw["credential"].(map[string]any); ok {
		cred := &AuthCredential{}
		cred.ClientID, _ = credMap["client_id"].(string)
		cred.ClientSecret, _ = credMap["client_secret"].(string)
		cred.Username, _ = credMap["username"].(string)
		cred.Password, _ = credMap["password"].(string)
		cred.Token, _ = credMap["token"].(string)
		ac.Credential = cred
	}

	if authType == "oauth2" {
		if schemeMap, ok := raw["scheme"].(map[string]any); ok {
			scheme := &OAuthScheme{}
			scheme.AuthorizationURL, _ = schemeMap["authorization_url"].(string)
			scheme.TokenURL, _ = schemeMap["token_url"].(string)
			scheme.RefreshURL, _ = schemeMap["refresh_url"].(string)
			scheme.Scopes = ParseStringSlice(schemeMap["scopes"])
			scheme.TokenEndpointAuthMethod, _ = schemeMap["token_endpoint_auth_method"].(string)
			scheme.Audience, _ = schemeMap["audience"].(string)
			scheme.Resource, _ = schemeMap["resource"].(string)
			ac.Scheme = scheme
		}
		if schemeMap, ok := raw["scheme"].(map[string]any); ok {
			if dp, ok := schemeMap["disable_pkce"].(bool); ok && dp {
				ac.DisablePKCE = true
			}
			if dc, ok := schemeMap["disable_consent_prompt"].(bool); ok && dc {
				ac.DisableConsentPrompt = true
			}
		}
	}

	return ac
}

func MergeAuthConfigs(toolDeclared, deployerDeclared *AuthConfig) *AuthConfig {
	if toolDeclared == nil {
		return deployerDeclared
	}
	if deployerDeclared == nil {
		return toolDeclared
	}

	merged := &AuthConfig{
		Type:          deployerDeclared.Type,
		CredentialKey: deployerDeclared.CredentialKey,
		ResourceURL:   deployerDeclared.ResourceURL,
		CACertPath:    deployerDeclared.CACertPath,
		DisplayName:   deployerDeclared.DisplayName,
	}
	if merged.Type == "" {
		merged.Type = toolDeclared.Type
	}
	if merged.CredentialKey == "" {
		merged.CredentialKey = toolDeclared.CredentialKey
	}
	if merged.ResourceURL == "" {
		merged.ResourceURL = toolDeclared.ResourceURL
	}
	if merged.DisplayName == "" {
		merged.DisplayName = toolDeclared.DisplayName
	}
	if merged.CACertPath == "" {
		merged.CACertPath = toolDeclared.CACertPath
	}
	merged.InsecureSkipVerify = toolDeclared.InsecureSkipVerify || deployerDeclared.InsecureSkipVerify

	if deployerDeclared.Credential != nil {
		merged.Credential = deployerDeclared.Credential
	} else {
		merged.Credential = toolDeclared.Credential
	}

	merged.DisablePKCE = toolDeclared.DisablePKCE || deployerDeclared.DisablePKCE
	merged.DisableConsentPrompt = toolDeclared.DisableConsentPrompt || deployerDeclared.DisableConsentPrompt

	switch {
	case deployerDeclared.Scheme != nil && toolDeclared.Scheme != nil:
		merged.Scheme = &OAuthScheme{
			AuthorizationURL:        toolDeclared.Scheme.AuthorizationURL,
			TokenURL:                toolDeclared.Scheme.TokenURL,
			RefreshURL:              toolDeclared.Scheme.RefreshURL,
			Scopes:                  toolDeclared.Scheme.Scopes,
			TokenEndpointAuthMethod: toolDeclared.Scheme.TokenEndpointAuthMethod,
			Audience:                toolDeclared.Scheme.Audience,
			Resource:                toolDeclared.Scheme.Resource,
		}
		if deployerDeclared.Scheme.AuthorizationURL != "" {
			merged.Scheme.AuthorizationURL = deployerDeclared.Scheme.AuthorizationURL
		}
		if deployerDeclared.Scheme.TokenURL != "" {
			merged.Scheme.TokenURL = deployerDeclared.Scheme.TokenURL
		}
		if deployerDeclared.Scheme.RefreshURL != "" {
			merged.Scheme.RefreshURL = deployerDeclared.Scheme.RefreshURL
		}
		if len(deployerDeclared.Scheme.Scopes) > 0 {
			merged.Scheme.Scopes = deployerDeclared.Scheme.Scopes
		}
		if deployerDeclared.Scheme.TokenEndpointAuthMethod != "" {
			merged.Scheme.TokenEndpointAuthMethod = deployerDeclared.Scheme.TokenEndpointAuthMethod
		}
		if deployerDeclared.Scheme.Audience != "" {
			merged.Scheme.Audience = deployerDeclared.Scheme.Audience
		}
		if deployerDeclared.Scheme.Resource != "" {
			merged.Scheme.Resource = deployerDeclared.Scheme.Resource
		}
	case deployerDeclared.Scheme != nil:
		merged.Scheme = deployerDeclared.Scheme
	case toolDeclared.Scheme != nil:
		merged.Scheme = toolDeclared.Scheme
	}

	return merged
}

func DefaultOAuthCredentialKey(ac *AuthConfig) string {
	if ac == nil {
		return ""
	}
	h := sha256.New()
	writeField := func(label, v string) {
		_, _ = fmt.Fprintf(h, "%s=%d:%s\n", label, len(v), v)
	}
	writeField("type", ac.Type)
	if ac.Scheme != nil {
		writeField("auth_url", ac.Scheme.AuthorizationURL)
		writeField("token_url", ac.Scheme.TokenURL)
		writeField("audience", ac.Scheme.Audience)
		writeField("resource", ac.Scheme.Resource)
		scopes := slices.Clone(ac.Scheme.Scopes)
		slices.Sort(scopes)
		for _, s := range scopes {
			writeField("scope", s)
		}
	}
	if ac.Credential != nil {
		writeField("client_id", ac.Credential.ClientID)
	}
	prefix := ac.Type
	if prefix == "" {
		prefix = "oauth2"
	}
	return prefix + "_" + hex.EncodeToString(h.Sum(nil))[:12]
}

func ParseStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
