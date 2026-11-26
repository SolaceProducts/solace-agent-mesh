// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"maps"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const EphemeralTestAgentNamePrefix = "__test__"

func IsEphemeralTestAgentName(name string) bool {
	return strings.HasPrefix(name, EphemeralTestAgentNamePrefix)
}

const WorkflowHandoffArtifactPrefix = "wf_"

func IsWorkflowHandoffArtifactName(filename string) bool {
	return strings.HasPrefix(filename, WorkflowHandoffArtifactPrefix)
}

const ExtensionURIDisplayName = "https://solace.com/a2a/extensions/display-name"

func DisplayNameFromCard(card AgentCard) string {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIDisplayName {
			continue
		}
		if v, ok := ext.Params["display_name"].(string); ok {
			return v
		}
	}
	return ""
}

const ExtensionURISchemas = "https://solace.com/a2a/extensions/sam/schemas"

const ExtensionURIWorkflowVisualization = "https://solace.com/a2a/extensions/sam/workflow-visualization"

const ExtensionURIAgentType = "https://solace.com/a2a/extensions/agent-type"

func AgentTypeFromCard(card AgentCard) string {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIAgentType {
			continue
		}
		if v, ok := ext.Params["type"].(string); ok && v != "" {
			return v
		}
	}
	return "standard"
}

const ExtensionURIPriority = "https://solace.com/a2a/extensions/sam/priority"

func PriorityFromCard(card AgentCard) int {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIPriority {
			continue
		}
		switch v := ext.Params["priority"].(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return 0
}

const EphemeralAgentExtensionURI = "https://solace.com/a2a/extensions/sam/ephemeral"

func IsEphemeralCard(card AgentCard) bool {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == EphemeralAgentExtensionURI {
			return true
		}
	}
	return false
}

const ExtensionURIOnDemand = "https://solace.com/a2a/extensions/sam/on-demand"

func IsOnDemandCard(card AgentCard) bool {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIOnDemand {
			return true
		}
	}
	return false
}

const ExtensionURIRegistration = "https://solace.com/a2a/extensions/sam/registration"

const ExtensionURIProvenance = "https://solace.com/a2a/extensions/sam/provenance"

const ExtensionURIArtifactAccess = "https://solace.com/a2a/extensions/sam/artifact-access"

const ExtensionURIInternal = "https://solace.com/a2a/extensions/sam/internal"

func IsInternalCard(card AgentCard) bool {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIInternal {
			continue
		}
		if internal, ok := ext.Params["internal"].(bool); ok && internal {
			return true
		}
	}
	return false
}

const ExtensionURIRequiredScopes = "https://solace.com/a2a/extensions/sam/required-scopes"

const ExtensionURIRemoteProtocol = "https://solace.com/a2a/extensions/sam/remote-protocol"

type CardSignature string

const (
	CardSignatureVerified  CardSignature = "verified"
	CardSignatureUnsigned  CardSignature = "unsigned"
	CardSignatureUnchecked CardSignature = "unchecked"
	CardSignatureRefused   CardSignature = "refused"
)

func (c CardSignature) Known() bool {
	switch c {
	case CardSignatureVerified, CardSignatureUnsigned, CardSignatureUnchecked, CardSignatureRefused:
		return true
	}
	return false
}

const (
	remoteProtocolVersionParam       = "protocol_version"
	remoteProtocolBindingParam       = "protocol_binding"
	remoteProtocolPinParam           = "pin"
	remoteProtocolDeprecatedParam    = "deprecated"
	remoteProtocolCardSignatureParam = "card_signature"
)

type RemoteProtocol struct {
	Version       string
	Binding       string
	Pin           string
	Deprecated    bool
	CardSignature CardSignature
}

func RemoteProtocolExtension(rp RemoteProtocol) AgentExtension {
	return AgentExtension{URI: ExtensionURIRemoteProtocol, Params: map[string]any{
		remoteProtocolVersionParam:       rp.Version,
		remoteProtocolBindingParam:       rp.Binding,
		remoteProtocolPinParam:           rp.Pin,
		remoteProtocolDeprecatedParam:    rp.Deprecated,
		remoteProtocolCardSignatureParam: string(rp.CardSignature),
	}}
}

func RemoteProtocolFromCard(card AgentCard) (RemoteProtocol, bool) {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIRemoteProtocol {
			continue
		}
		version, _ := ext.Params[remoteProtocolVersionParam].(string)
		binding, _ := ext.Params[remoteProtocolBindingParam].(string)
		pin, _ := ext.Params[remoteProtocolPinParam].(string)
		deprecated, _ := ext.Params[remoteProtocolDeprecatedParam].(bool)
		cardSignature, _ := ext.Params[remoteProtocolCardSignatureParam].(string)
		return RemoteProtocol{Version: version, Binding: binding, Pin: pin, Deprecated: deprecated, CardSignature: CardSignature(cardSignature)}, true
	}
	return RemoteProtocol{}, false
}

func SetRemoteProtocolCardSignature(card *AgentCard, cs CardSignature) {
	for i, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIRemoteProtocol {
			continue
		}
		params := make(map[string]any, len(ext.Params)+1)
		maps.Copy(params, ext.Params)
		params[remoteProtocolCardSignatureParam] = string(cs)
		card.Capabilities.Extensions[i].Params = params
	}
}

const ExtensionURIHealth = "https://solace.com/a2a/extensions/sam/health"

func HealthProblemsExtension(problems []health.Problem) AgentExtension {
	encoded := make([]map[string]any, 0, len(problems))
	for _, p := range problems {
		encoded = append(encoded, map[string]any{
			"id":       p.ID,
			"severity": p.Severity.String(),
			"detail":   p.Detail,
			"since":    p.Since.UTC().Format(time.RFC3339Nano),
		})
	}
	return AgentExtension{URI: ExtensionURIHealth, Params: map[string]any{"problems": encoded}}
}

func HealthProblemsFromCard(card AgentCard) []health.Problem {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIHealth {
			continue
		}
		return decodeHealthProblems(ext.Params["problems"])
	}
	return nil
}

const (
	maxHealthProblemsPerCard   = 50
	maxHealthProblemFieldBytes = 200
)

func decodeHealthProblems(raw any) []health.Problem {
	var maps []map[string]any
	switch v := raw.(type) {
	case []map[string]any:
		maps = v
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				maps = append(maps, m)
			}
		}
	}
	if len(maps) == 0 {
		return nil
	}
	if len(maps) > maxHealthProblemsPerCard {
		maps = maps[:maxHealthProblemsPerCard]
	}
	problems := make([]health.Problem, 0, len(maps))
	for _, m := range maps {
		p := health.Problem{
			ID:     strutil.Truncate(stringExtensionField(m, "id"), maxHealthProblemFieldBytes, "..."),
			Detail: strutil.Truncate(stringExtensionField(m, "detail"), maxHealthProblemFieldBytes, "..."),
		}
		if stringExtensionField(m, "severity") == health.Fatal.String() {
			p.Severity = health.Fatal
		}
		if since, ok := m["since"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, since); err == nil {
				p.Since = t
			}
		}
		problems = append(problems, p)
	}
	return problems
}

func stringExtensionField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func RequiredScopesFromCard(card AgentCard) []string {
	for _, extension := range card.Capabilities.Extensions {
		if extension.URI != ExtensionURIRequiredScopes {
			continue
		}
		switch scopes := extension.Params["scopes"].(type) {
		case []string:
			return scopes
		case []any:
			requiredScopes := make([]string, 0, len(scopes))
			for _, rawScope := range scopes {
				if scope, ok := rawScope.(string); ok {
					requiredScopes = append(requiredScopes, scope)
				}
			}
			return requiredScopes
		}
		return nil
	}
	return nil
}
