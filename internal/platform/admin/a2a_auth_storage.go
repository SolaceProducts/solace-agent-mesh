// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
)

const (
	authTypeBearer = "bearer"
	authTypeAPIKey = "apikey"
	authTypeOAuth2 = "oauth2"
	authTypeBasic  = "basic"
	authTypeNone   = "none"
)

func storedAuthConfigFromWire(authType string, wire *CardAuthConfig) map[string]any {
	if wire == nil {
		return nil
	}
	switch authType {
	case authTypeBearer:
		return map[string]any{"type": authTypeBearer, "token": wire.BearerToken}
	case authTypeAPIKey:
		header := wire.ApikeyHeaderName
		if header == "" {
			header = "X-API-Key"
		}
		return map[string]any{
			"type":        authTypeAPIKey,
			"api_key":     wire.ApikeyValue,
			"header_name": header,
		}
	case authTypeOAuth2:
		return map[string]any{
			"type":           authTypeOAuth2,
			"client_id":      wire.OAuth2ClientID,
			"client_secret":  wire.OAuth2ClientSecret,
			"token_endpoint": wire.OAuth2TokenURL,
			"scopes":         scopesFromString(wire.OAuth2Scope),
		}
	case authTypeBasic:
		return map[string]any{
			"type":     authTypeBasic,
			"username": wire.BasicUsername,
			"password": wire.BasicPassword,
		}
	default:
		return nil
	}
}

func scopesFromString(scope string) []string {
	if scope = strings.TrimSpace(scope); scope == "" {
		return []string{}
	}
	return strings.Fields(scope)
}

type secretFieldPair struct {
	wire   string
	stored string
}

func authSecretPairs(authType string) []secretFieldPair {
	switch authType {
	case authTypeBearer:
		return []secretFieldPair{{"token", "token"}}
	case authTypeAPIKey:
		return []secretFieldPair{{"apiKey", "api_key"}}
	case authTypeOAuth2:
		return []secretFieldPair{{"clientSecret", "client_secret"}}
	case authTypeBasic:
		return []secretFieldPair{{"password", "password"}}
	default:
		return nil
	}
}

func authNonSecretWireMap(authType string) map[string]string {
	switch authType {
	case authTypeAPIKey:
		return map[string]string{"headerName": "header_name"}
	case authTypeOAuth2:
		return map[string]string{
			"tokenEndpoint": "token_endpoint",
			"clientId":      "client_id",
			"scopes":        "scopes",
		}
	case authTypeBasic:
		return map[string]string{"username": "username"}
	default:
		return nil
	}
}

func redactStoredAuthConfig(raw *string) any {
	if raw == nil {
		return nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(*raw), &cfg); err != nil {
		return nil
	}
	authType, _ := cfg["type"].(string)
	secretPairs := authSecretPairs(authType)
	if secretPairs == nil {
		return redactUnknownSecrets(cfg)
	}
	wire := map[string]any{"type": authType}
	for wireName, storedName := range authNonSecretWireMap(authType) {
		if v, ok := cfg[storedName]; ok {
			wire[wireName] = v
		}
	}
	for _, pair := range secretPairs {
		if v, ok := cfg[pair.stored]; ok && v != nil {
			wire[pair.wire] = redactedSentinel
		}
	}
	return wire
}

var secretKeySubstrings = []string{"token", "secret", "password", "api_key", "apikey"}

func redactUnknownSecrets(cfg map[string]any) map[string]any {
	for k, v := range cfg {
		if v == nil {
			continue
		}
		lower := strings.ToLower(k)
		for _, sub := range secretKeySubstrings {
			if strings.Contains(lower, sub) {
				cfg[k] = redactedSentinel
				break
			}
		}
	}
	return cfg
}

func coerceWireAuthConfig(raw any) *CardAuthConfig {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	s := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	return &CardAuthConfig{
		BearerToken:        s("token"),
		ApikeyValue:        s("apiKey"),
		ApikeyHeaderName:   s("headerName"),
		OAuth2TokenURL:     s("tokenEndpoint"),
		OAuth2ClientID:     s("clientId"),
		OAuth2ClientSecret: s("clientSecret"),
		OAuth2Scope:        scopeStringFromStored(m["scopes"]),
		BasicUsername:      s("username"),
		BasicPassword:      s("password"),
	}
}

func effectiveAuthType(authType string, raw any) string {
	if authType != "" {
		return authType
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	t, _ := m["type"].(string)
	return t
}

func marshalStoredAuthConfig(authType string, raw any) *string {
	stored := storedAuthConfigFromWire(effectiveAuthType(authType, raw), coerceWireAuthConfig(raw))
	if stored == nil {
		return nil
	}
	return marshalJSONPtr(stored)
}

const secretMustBeResentMessage = "must be sent again when the URL it is sent to changes"

func keptSecretsSentElsewhere(field, authType string, incoming any, before, after *string, moved bool) []string {
	if before == nil || after == nil {
		return nil
	}
	prior, merged := unmarshalAuthConfig(*before), unmarshalAuthConfig(*after)
	if !moved && (authType != authTypeOAuth2 || prior["token_endpoint"] == merged["token_endpoint"]) {
		return nil
	}
	sent, _ := incoming.(map[string]any)
	var kept []string
	for _, f := range authSecretPairs(authType) {
		value, _ := merged[f.stored].(string)
		resent, _ := sent[f.wire].(string)
		if value != "" && value == prior[f.stored] && (resent == "" || resent == redactedSentinel) {
			kept = append(kept, field+"."+f.wire)
		}
	}
	return kept
}

func mergeAuthConfigUpdate(authType string, incoming any, existing *string, isRequired bool) *string {
	authType = effectiveAuthType(authType, incoming)
	secretPairs := authSecretPairs(authType)
	if secretPairs == nil {
		empty := "{}"
		return &empty
	}

	incomingMap, incomingOk := incoming.(map[string]any)
	if incoming == nil || !incomingOk {
		if existing != nil {
			return existing
		}
		empty := "{}"
		return &empty
	}

	var existingMap map[string]any
	if existing != nil {
		existingMap = unmarshalAuthConfig(*existing)
	}

	stored := storedAuthConfigFromWire(authType, coerceWireAuthConfig(incoming))
	if stored == nil {
		empty := "{}"
		return &empty
	}

	for wire, storedName := range authNonSecretWireMap(authType) {
		if _, present := incomingMap[wire]; present {
			continue
		}
		if existingMap == nil {
			continue
		}
		if v, ok := existingMap[storedName]; ok {
			stored[storedName] = v
		}
	}

	for _, f := range secretPairs {
		raw, present := incomingMap[f.wire]
		if !present {
			preserveField(stored, existingMap, f.stored)
			continue
		}
		s, _ := raw.(string)
		switch s {
		case redactedSentinel:
			preserveField(stored, existingMap, f.stored)
		case "":
			if isRequired {
				preserveField(stored, existingMap, f.stored)
			} else {
				stored[f.stored] = nil
			}
		default:
			stored[f.stored] = s
		}
	}

	return marshalJSONPtr(stored)
}

func preserveField(stored, existingMap map[string]any, field string) {
	if existingMap != nil {
		if v, ok := existingMap[field]; ok {
			stored[field] = v
			return
		}
	}
	delete(stored, field)
}
