// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"maps"
	"slices"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type a2aHashView struct {
	ID                    string `json:"id" cfgapply:"read-only"`
	Name                  string `json:"name"`
	Description           string `json:"description"`
	AgentURL              string `json:"agentUrl"`
	AgentCardLocation     string `json:"agentCardLocation"`
	AgentCardURL          string `json:"agentCardUrl,omitempty"`
	AgentCardHeaders      any    `json:"agentCardHeaders,omitempty"`
	AgentCardAuthType     string `json:"agentCardAuthType,omitempty"`
	AgentAuthType         string `json:"agentAuthType,omitempty"`
	TaskHeaders           any    `json:"taskHeaders,omitempty"`
	RequestTimeoutSeconds int    `json:"requestTimeoutSeconds"`
	Skills                any    `json:"skills,omitempty"`
	InputModes            any    `json:"inputModes,omitempty"`
	OutputModes           any    `json:"outputModes,omitempty"`
	ProtocolVersion       string `json:"protocolVersion"`
	TrustedCardJWKSURLs   any    `json:"trustedCardJwksUrls,omitempty"`
	RequireSignedCard     bool   `json:"requireSignedCard"`

	AgentAuthPublic     any `json:"agentAuthPublic,omitempty"`
	AgentCardAuthPublic any `json:"agentCardAuthPublic,omitempty"`
}

func mergePublicAuthFields(authored, stored any) (any, []string) {
	storedMap, ok := stored.(map[string]any)
	if !ok || len(storedMap) == 0 {
		return authored, nil
	}
	authoredMap, ok := authored.(map[string]any)
	if !ok {
		return authored, nil
	}
	out := maps.Clone(authoredMap)
	if out == nil {
		out = map[string]any{}
	}
	var preserved []string
	for key, value := range storedMap {
		if _, stated := out[key]; !stated {
			out[key] = value
			preserved = append(preserved, key)
		}
	}
	slices.Sort(preserved)
	return normalizeJSON(out), preserved
}

func publicAuthFields(config any, authType string) any {
	m, ok := config.(map[string]any)
	if !ok {
		return nil
	}
	secret := make(map[string]struct{})
	for _, k := range entplatform.A2AAuthSecretWireFields(authType) {
		secret[k] = struct{}{}
	}
	out := map[string]any{}
	for key, v := range m {
		if key == "type" {
			continue
		}
		if _, isSecret := secret[key]; isSecret {
			continue
		}
		out[key] = v
	}
	return normalizeJSON(out)
}

const defaultRequestTimeoutSeconds = 120

func a2aViewFromCreate(c entplatform.CreateRemoteAgentRequest) a2aHashView {
	return a2aHashView{
		Name:                  c.Name,
		Description:           c.Description,
		AgentURL:              c.AgentURL,
		AgentCardLocation:     c.AgentCardLocation,
		AgentCardURL:          c.AgentCardURL,
		AgentCardHeaders:      normalizeJSON(c.AgentCardHeaders),
		AgentCardAuthType:     effectiveAuthType(c.AgentCardAuthType, c.AgentCardAuthConfig),
		AgentAuthType:         effectiveAuthType(c.AgentAuthType, c.AgentAuthConfig),
		AgentAuthPublic:       publicAuthFields(c.AgentAuthConfig, effectiveAuthType(c.AgentAuthType, c.AgentAuthConfig)),
		AgentCardAuthPublic:   publicAuthFields(c.AgentCardAuthConfig, effectiveAuthType(c.AgentCardAuthType, c.AgentCardAuthConfig)),
		TaskHeaders:           normalizeJSON(c.TaskHeaders),
		RequestTimeoutSeconds: orDefaultTimeout(c.RequestTimeoutSeconds),
		Skills:                normalizeJSON(c.Skills),
		InputModes:            normalizeJSON(c.InputModes),
		OutputModes:           normalizeJSON(c.OutputModes),
		ProtocolVersion:       orDefaultProtocolPin(c.ProtocolVersion),
		TrustedCardJWKSURLs:   normalizeTrustedURLs(c.TrustedCardJWKSURLs),
		RequireSignedCard:     c.RequireSignedCard != nil && *c.RequireSignedCard,
	}
}

func a2aViewFromResponse(r entplatform.RemoteAgentResponse) a2aHashView {
	return a2aHashView{
		ID:                    r.ID,
		Name:                  r.Name,
		Description:           r.Description,
		AgentURL:              r.AgentURL,
		AgentCardLocation:     r.AgentCardLocation,
		AgentCardURL:          r.AgentCardURL,
		AgentCardHeaders:      normalizeJSON(r.AgentCardHeaders),
		AgentCardAuthType:     r.AgentCardAuthType,
		AgentAuthType:         r.AgentAuthType,
		AgentAuthPublic:       publicAuthFields(r.AgentAuthConfig, r.AgentAuthType),
		AgentCardAuthPublic:   publicAuthFields(r.AgentCardAuthConfig, r.AgentCardAuthType),
		TaskHeaders:           normalizeJSON(r.TaskHeaders),
		RequestTimeoutSeconds: orDefaultTimeout(r.RequestTimeoutSeconds),
		Skills:                normalizeJSON(r.Skills),
		InputModes:            normalizeJSON(r.InputModes),
		OutputModes:           normalizeJSON(r.OutputModes),
		ProtocolVersion:       orDefaultProtocolPin(r.ProtocolVersion),
		TrustedCardJWKSURLs:   normalizeTrustedURLs(r.TrustedCardJWKSURLs),
		RequireSignedCard:     r.RequireSignedCard,
	}
}

func orDefaultProtocolPin(pin string) string {
	if pin == "" {
		return entplatform.ProtocolPinAuto
	}
	return pin
}

func normalizeTrustedURLs(urls []string) any {
	if len(urls) == 0 {
		return nil
	}
	return normalizeJSON(urls)
}

func orDefaultTimeout(v int) int {
	if v == 0 {
		return defaultRequestTimeoutSeconds
	}
	return v
}

func effectiveAuthType(explicit string, config any) string {
	if explicit != "" {
		return explicit
	}
	if m, ok := config.(map[string]any); ok {
		if t, ok := m["type"].(string); ok {
			return t
		}
	}
	return ""
}

func normalizeJSON(v any) any {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	switch t := out.(type) {
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
	case []any:
		if len(t) == 0 {
			return nil
		}
	}
	return out
}
