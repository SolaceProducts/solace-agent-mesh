// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"maps"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestPeerRequestPropertiesAreExactlyWhatAPeerIsSent(t *testing.T) {
	for name, tc := range map[string]struct {
		toolCtx *tool.Context
		want    map[string]string
	}{
		"everything the caller's task carries": {
			toolCtx: &tool.Context{
				Identity:      &auth.Identity{UserID: "alice@example.com", AuthToken: "tok-alice"},
				UserID:        "alice@example.com",
				A2AUserConfig: `{"scopes":["a"]}`,
				CallDepth:     3,
				TraceID:       "trace-1",
				Trigger:       "gateway",
				RootKind:      "agent",
				RootID:        "claims-assistant",
				RootNodeID:    "node-1",
				Origin: a2a.Origin{Channel: "slack", UserUUID: "user-uuid", EntrypointUUID: "entry-uuid",
					EntrypointName: "slack-gw"},
			},
			want: map[string]string{
				"replyTo":               "test-ns/a2a/v1/agent/response/claims-assistant/S1",
				"a2aStatusTopic":        "test-ns/a2a/v1/agent/status/claims-assistant/S1",
				"userId":                "alice@example.com",
				"a2aUserConfig":         `{"scopes":["a"]}`,
				"authToken":             "tok-alice",
				"delegating_agent_name": "claims-assistant",
				"callDepth":             "4",
				"traceId":               "trace-1",
				"trigger":               "gateway",
				"rootKind":              "agent",
				"rootId":                "claims-assistant",
				"rootNodeId":            "node-1",
				"channel":               "slack",
				"userUuid":              "user-uuid",
				"entrypointUuid":        "entry-uuid",
				"entrypointName":        "slack-gw",
			},
		},
		"a caller with nothing optional": {
			toolCtx: &tool.Context{UserID: "alice@example.com"},
			want: map[string]string{
				"replyTo":               "test-ns/a2a/v1/agent/response/claims-assistant/S1",
				"a2aStatusTopic":        "test-ns/a2a/v1/agent/status/claims-assistant/S1",
				"userId":                "alice@example.com",
				"delegating_agent_name": "claims-assistant",
				"callDepth":             "1",
			},
		},
		"an identity without a token sends none": {
			toolCtx: &tool.Context{Identity: &auth.Identity{UserID: "alice@example.com"}, UserID: "alice@example.com"},
			want: map[string]string{
				"replyTo":               "test-ns/a2a/v1/agent/response/claims-assistant/S1",
				"a2aStatusTopic":        "test-ns/a2a/v1/agent/status/claims-assistant/S1",
				"userId":                "alice@example.com",
				"delegating_agent_name": "claims-assistant",
				"callDepth":             "1",
			},
		},
	} {
		got := PeerRequestProperties("test-ns", "claims-assistant", "S1", tc.toolCtx)
		if !maps.Equal(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, tc.want)
		}
	}
}
