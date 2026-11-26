// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"encoding/json"
	"log/slog"
)

type SoftSubsetAssertFields struct {
	TaskID          string
	UserID          string
	DelegatingAgent string
	Origin          string
}

func ApplySoftSubsetAssert(
	identity *Identity,
	a2aUserConfig string,
	fields SoftSubsetAssertFields,
	logger *slog.Logger,
) {
	if identity == nil || a2aUserConfig == "" || logger == nil {
		return
	}
	bodyScopes, ok := ParseEnterpriseCapabilitiesScopes(a2aUserConfig)
	if !ok || len(bodyScopes) == 0 {
		return
	}
	jwtScopes := identity.Scopes
	if IsScopeSubset(bodyScopes, jwtScopes) {
		identity.Scopes = bodyScopes
		return
	}
	logger.Warn("a2a capability widening blocked",
		"event", "capability_widening_blocked",
		"origin", fields.Origin,
		"userID", fields.UserID,
		"taskID", fields.TaskID,
		"jwtScopes", jwtScopes,
		"bodyScopes", bodyScopes,
		"delegatingAgentName", fields.DelegatingAgent,
	)
}

func ParseEnterpriseCapabilitiesScopes(a2aUserConfig string) ([]string, bool) {
	if a2aUserConfig == "" {
		return nil, false
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(a2aUserConfig), &cfg); err != nil {
		return nil, false
	}
	raw, present := cfg["_enterprise_capabilities"]
	if !present {
		return nil, false
	}
	var scopes []string
	if err := json.Unmarshal(raw, &scopes); err != nil {
		return nil, false
	}
	return scopes, true
}

func IsScopeSubset(body, jwt []string) bool {
	for _, b := range body {
		matched := false
		for _, j := range jwt {
			if MatchScope(j, b) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
