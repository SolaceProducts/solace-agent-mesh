// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const unfilledDeploySuffix = "cannot be deployed until all required fields are provided"

const (
	unfilledAgentMessage       = "agent " + unfilledDeploySuffix
	unfilledRemoteAgentMessage = "remote agent " + unfilledDeploySuffix
	unfilledWorkflowMessage    = "workflow " + unfilledDeploySuffix
)

func UnfilledDeployRefusal(err error) (*ValidationError, bool) {
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok || len(ve.Details) == 0 || !strings.HasSuffix(ve.Message, unfilledDeploySuffix) {
		return nil, false
	}
	return ve, true
}

func requireConnectorsFilled(ctx context.Context, repo *store.ConnectorRepo, connectorIDs []string, message string) error {
	if repo == nil || len(connectorIDs) == 0 {
		return nil
	}
	details := map[string][]string{}
	for _, id := range connectorIDs {
		conn, err := repo.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("load connector %s: %w", id, err)
		}
		if conn == nil {
			continue
		}
		for _, field := range placeholderFields(conn.Values) {
			key := fmt.Sprintf("connectors.%s.%s", conn.Name, field)
			details[key] = []string{fmt.Sprintf("connector %q has no value for %s; set it on the Connectors page", conn.Name, field)}
		}
	}
	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Message: message, Details: details}
}

func requireRemoteAgentAuthFilled(a2a *store.A2AAgent) error {
	agentAuth, cardAuth := proxyAuthConfigs(a2a)
	details := map[string][]string{}
	collectEmittedAuthPlaceholders(a2a.Name, "agentAuthConfig", derefStr(a2a.AgentAuthType), agentAuth, details)
	collectEmittedAuthPlaceholders(a2a.Name, "agentCardAuthConfig", derefStr(a2a.AgentCardAuthType), cardAuth, details)
	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Message: unfilledRemoteAgentMessage, Details: details}
}

func collectEmittedAuthPlaceholders(agentName, prefix, authType string, emitted map[string]any, details map[string][]string) {
	wireNames := emittedWireNames(authType)
	found := map[string][]string{}
	collectPlaceholderFields("", emitted, found)
	for path := range found {
		head, tail := path, ""
		if i := strings.IndexAny(path, ".["); i >= 0 {
			head, tail = path[:i], path[i:]
		}
		field := prefix + "." + cmp.Or(wireNames[head], head) + tail
		details[field] = []string{fmt.Sprintf("remote agent %q has no value for %s; set it on the Agents page", agentName, field)}
	}
}

func emittedWireNames(authType string) map[string]string {
	names := map[string]string{}
	add := func(stored, wire string) {
		probe := map[string]any{stored: true}
		normalizeAuthConfig(probe, authType)
		delete(probe, "type")
		for emitted := range probe {
			names[emitted] = wire
		}
	}
	for _, pair := range authSecretPairs(authType) {
		add(pair.stored, pair.wire)
	}
	for wire, stored := range authNonSecretWireMap(authType) {
		add(stored, wire)
	}
	return names
}

func placeholderFields(values *string) []string {
	if values == nil {
		return nil
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(*values), &parsed) != nil {
		return nil
	}
	found := map[string][]string{}
	collectPlaceholderFields("", parsed, found)
	return slices.Sorted(maps.Keys(found))
}
