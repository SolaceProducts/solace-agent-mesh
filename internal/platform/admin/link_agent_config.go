// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const defaultLinkWakeTimeoutSeconds = 900

func linkAgentMeshName(agentID string) string {
	return a2a.InstanceName(a2a.KindAgent, agentID)
}

func linkAgentRuntimeConfig(a *store.LinkAgent) map[string]any {
	cfg := map[string]any{
		"name":                    linkAgentMeshName(a.ID),
		linkRuntimeTokenDigestKey: a.TokenSHA256,
		"enabled":                 a.Enabled,
		"display_name":            a.Name,
		"share_user_profile":      a.ShareUserProfile,
	}
	if a.Owner != "" {
		cfg["owner"] = a.Owner
	}
	if a.Team != "" {
		cfg["team"] = a.Team
	}
	if len(a.Peers) > 0 {
		peers := make([]any, len(a.Peers))
		for i, peer := range a.Peers {
			peers[i] = peer
		}
		cfg["peers"] = peers
	}
	if a.Wake != nil {
		cfg["wake"] = linkWakeRuntimeConfig(a.Wake)
	}
	return cfg
}

func linkWakeRuntimeConfig(w *store.LinkAgentWake) map[string]any {
	wake := map[string]any{"url": w.URL, "on_start": w.OnStart}
	for key, value := range map[string]string{
		"token_url": w.TokenURL, "client_id": w.ClientID, "client_secret": w.ClientSecret, "scope": w.Scope,
	} {
		if value != "" {
			wake[key] = value
		}
	}
	if len(w.Headers) > 0 {
		headers := make(map[string]any, len(w.Headers))
		for name, value := range w.Headers {
			headers[name] = value
		}
		wake["headers"] = headers
	}
	if w.TimeoutSeconds > 0 {
		wake["timeout_seconds"] = w.TimeoutSeconds
	}
	return wake
}

func computeLinkAgentConfigHash(a *store.LinkAgent) string {
	encoded, _ := json.Marshal(map[string]any{
		"entrypointId": a.EntrypointID,
		"description":  a.Description,
		"runtime":      linkAgentRuntimeConfig(a),
	})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

const linkRuntimeTokenDigestKey = "token_sha256"

type linkAgentDeploymentSnapshot struct {
	EntrypointID string         `json:"entrypointId"`
	Agent        map[string]any `json:"agent"`
}

func linkAgentSnapshotOf(a *store.LinkAgent) linkAgentDeploymentSnapshot {
	return linkAgentDeploymentSnapshot{EntrypointID: a.EntrypointID, Agent: linkAgentRuntimeConfig(a)}
}

func readLinkAgentSnapshot(raw *string) (linkAgentDeploymentSnapshot, bool) {
	var snapshot linkAgentDeploymentSnapshot
	if raw == nil || json.Unmarshal([]byte(*raw), &snapshot) != nil {
		return linkAgentDeploymentSnapshot{}, false
	}
	return snapshot, snapshot.EntrypointID != "" && snapshot.Agent != nil
}

type deployedLinkAgent struct {
	agentID      string
	deploymentID string
	entrypointID string
	config       map[string]any
}

const linkAgentsRevisionLength = 32

func linkAgentsRevision(deployed []deployedLinkAgent) string {
	if len(deployed) == 0 {
		return ""
	}
	pairs := make([]string, len(deployed))
	for i, d := range deployed {
		pairs[i] = d.agentID + "=" + d.deploymentID
	}
	slices.Sort(pairs)
	sum := sha256.Sum256([]byte(strings.Join(pairs, "\n")))
	return hex.EncodeToString(sum[:])[:linkAgentsRevisionLength]
}

func deployedLinkAgentInfo(a *store.LinkAgent) DeployedLinkAgentInfo {
	info := DeployedLinkAgentInfo{
		Name:             a.Name,
		Description:      a.Description,
		EntrypointID:     a.EntrypointID,
		TokenPrefix:      a.TokenPrefix,
		Enabled:          a.Enabled,
		Owner:            a.Owner,
		Team:             a.Team,
		Peers:            append([]string{}, a.Peers...),
		ShareUserProfile: a.ShareUserProfile,
	}
	if w := a.Wake; w != nil {
		info.Wake = &DeployedLinkAgentWake{
			URL:            w.URL,
			TokenURL:       w.TokenURL,
			ClientID:       w.ClientID,
			Scope:          w.Scope,
			HeaderNames:    slices.Sorted(maps.Keys(w.Headers)),
			TimeoutSeconds: EffectiveLinkWakeTimeout(w.TimeoutSeconds),
			OnStart:        w.OnStart,
		}
		if info.Wake.HeaderNames == nil {
			info.Wake.HeaderNames = []string{}
		}
	}
	return info
}

func EffectiveLinkWakeTimeout(seconds int) int {
	if seconds > 0 {
		return seconds
	}
	return defaultLinkWakeTimeoutSeconds
}
