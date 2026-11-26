// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const agentLinkEntrypointID = "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"

func renderedAgentLinkAdapterConfig(t *testing.T, values *string) map[string]any {
	t.Helper()
	gw := &store.Gateway{ID: agentLinkEntrypointID, Slug: "partner-link", Name: "Partner link", Type: "agent_link", Values: values}
	rendered, err := NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("GenerateGatewayYAML() = %v", err)
	}
	apps, err := schema.ParseAppsConfig([]byte(rendered))
	if err != nil {
		t.Fatalf("ParseAppsConfig(rendered agent_link entrypoint) = %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("rendered agent_link entrypoint has %d apps, want 1", len(apps))
	}
	appConfig, _ := apps[0]["app_config"].(map[string]any)
	adapter, _ := appConfig["gateway_adapter"].(map[string]any)
	if adapter["type"] != "agent_link" {
		t.Fatalf("rendered gateway_adapter.type = %v, want agent_link", adapter["type"])
	}
	return entrypoint.AdapterConfig(appConfig)
}

func TestGenerateGatewayYAML_AgentLinkRendersAPlatformManagedLinkTheAdapterParses(t *testing.T) {
	t.Setenv("FRONTEND_USE_AUTHORIZATION", "")
	tests := []struct {
		name        string
		values      *string
		wantAdapter map[string]any
		wantParsed  *agentlink.Config
	}{
		{
			name:   "no values leaves every limit to the adapter's default",
			values: nil,
			wantAdapter: map[string]any{
				"platform_managed": true,
				"agents":           []any{},
			},
			wantParsed: &agentlink.Config{
				MaxFrameBytes: 8388608, MaxInflight: 16, MaxConnectionsPerAgent: 8, MaxConnectsPerMinute: 60,
				TaskTTL: 2 * time.Hour, PlatformManaged: true, Agents: map[string]*agentlink.AgentConfig{},
			},
		},
		{
			name:   "limits stored as numbers",
			values: new(`{"maxFrameBytes":1048576,"maxInflight":4,"maxConnectionsPerAgent":2,"maxConnectsPerMinute":30,"taskTtlSeconds":600}`),
			wantAdapter: map[string]any{
				"platform_managed":          true,
				"agents":                    []any{},
				"max_frame_bytes":           1048576,
				"max_inflight":              4,
				"max_connections_per_agent": 2,
				"max_connects_per_minute":   30,
				"task_ttl_seconds":          600,
			},
			wantParsed: &agentlink.Config{
				MaxFrameBytes: 1048576, MaxInflight: 4, MaxConnectionsPerAgent: 2, MaxConnectsPerMinute: 30,
				TaskTTL: 10 * time.Minute, PlatformManaged: true, Agents: map[string]*agentlink.AgentConfig{},
			},
		},
		{
			name:   "limits stored as strings become numbers",
			values: new(`{"maxFrameBytes":"1048576","maxInflight":"4","maxConnectionsPerAgent":"2","maxConnectsPerMinute":"30","taskTtlSeconds":"600"}`),
			wantAdapter: map[string]any{
				"platform_managed":          true,
				"agents":                    []any{},
				"max_frame_bytes":           1048576,
				"max_inflight":              4,
				"max_connections_per_agent": 2,
				"max_connects_per_minute":   30,
				"task_ttl_seconds":          600,
			},
			wantParsed: &agentlink.Config{
				MaxFrameBytes: 1048576, MaxInflight: 4, MaxConnectionsPerAgent: 2, MaxConnectsPerMinute: 30,
				TaskTTL: 10 * time.Minute, PlatformManaged: true, Agents: map[string]*agentlink.AgentConfig{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapterConfig := renderedAgentLinkAdapterConfig(t, tt.values)

			if diff := cmp.Diff(tt.wantAdapter, adapterConfig); diff != "" {
				t.Errorf("rendered adapter_config mismatch (-want +got):\n%s", diff)
			}
			parsed, err := agentlink.ParseConfig(adapterConfig)
			if err != nil {
				t.Fatalf("agentlink.ParseConfig(rendered adapter_config) = %v", err)
			}
			if diff := cmp.Diff(tt.wantParsed, parsed); diff != "" {
				t.Errorf("agentlink.ParseConfig(rendered adapter_config) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateGatewayValues_AgentLink(t *testing.T) {
	tests := []struct {
		name        string
		values      map[string]any
		wantDetails map[string][]string
	}{
		{
			name:        "no settings",
			values:      map[string]any{},
			wantDetails: nil,
		},
		{
			name:        "every limit at its lowest accepted value",
			values:      map[string]any{"maxFrameBytes": 1024.0, "maxInflight": 1.0, "maxConnectionsPerAgent": 1.0, "maxConnectsPerMinute": 1.0, "taskTtlSeconds": 1.0},
			wantDetails: nil,
		},
		{
			name:        "every limit at its highest accepted value",
			values:      map[string]any{"maxFrameBytes": 67108864.0, "maxInflight": 1024.0, "maxConnectionsPerAgent": 256.0, "maxConnectsPerMinute": 6000.0, "taskTtlSeconds": 604800.0},
			wantDetails: nil,
		},
		{
			name:   "every limit one below its lowest accepted value",
			values: map[string]any{"maxFrameBytes": 1023.0, "maxInflight": 0.0, "maxConnectionsPerAgent": 0.0, "maxConnectsPerMinute": 0.0, "taskTtlSeconds": 0.0},
			wantDetails: map[string][]string{
				"maxFrameBytes":          {"maxFrameBytes: must be >= 1024"},
				"maxInflight":            {"maxInflight: must be >= 1"},
				"maxConnectionsPerAgent": {"maxConnectionsPerAgent: must be >= 1"},
				"maxConnectsPerMinute":   {"maxConnectsPerMinute: must be >= 1"},
				"taskTtlSeconds":         {"taskTtlSeconds: must be >= 1"},
			},
		},
		{
			name:   "every limit one above its highest accepted value",
			values: map[string]any{"maxFrameBytes": 67108865.0, "maxInflight": 1025.0, "maxConnectionsPerAgent": 257.0, "maxConnectsPerMinute": 6001.0, "taskTtlSeconds": 604801.0},
			wantDetails: map[string][]string{
				"maxFrameBytes":          {"maxFrameBytes: must be <= 67108864"},
				"maxInflight":            {"maxInflight: must be <= 1024"},
				"maxConnectionsPerAgent": {"maxConnectionsPerAgent: must be <= 256"},
				"maxConnectsPerMinute":   {"maxConnectsPerMinute: must be <= 6000"},
				"taskTtlSeconds":         {"taskTtlSeconds: must be <= 604800"},
			},
		},
		{
			name:        "a limit that is not a whole number",
			values:      map[string]any{"maxInflight": 2.5},
			wantDetails: map[string][]string{"maxInflight": {"maxInflight: must be a whole number"}},
		},
		{
			name:        "a limit that is not a number",
			values:      map[string]any{"maxInflight": "many"},
			wantDetails: map[string][]string{"maxInflight": {"maxInflight: must be a number"}},
		},
		{
			name:   "link agents sent with the entrypoint",
			values: map[string]any{"agents": []any{map[string]any{"name": "alice-agent"}}},
			wantDetails: map[string][]string{
				"agents": {"agents is not a setting of an agent_link entrypoint; link agents are managed on their own"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGatewayValues("agent_link", tt.values)

			var gotDetails map[string][]string
			if verr, ok := errors.AsType[*ValidationError](err); ok {
				gotDetails = verr.Details
			} else if err != nil {
				t.Fatalf("validateGatewayValues(agent_link, %v) = %T %v, want a *ValidationError or nil", tt.values, err, err)
			}
			if diff := cmp.Diff(tt.wantDetails, gotDetails); diff != "" {
				t.Errorf("validateGatewayValues(agent_link, %v) details mismatch (-want +got):\n%s", tt.values, diff)
			}
		})
	}
}

func newGatewayServiceWithAgentLinkFlag(t *testing.T, enabled bool) *GatewayService {
	t.Helper()
	t.Setenv("SAM_FEATURE_AGENT_LINK_ENTRYPOINT", "")
	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          features.KeyAgentLinkEntrypoint,
		Name:         "Agent Link Entrypoint",
		ReleasePhase: features.PhaseEarlyAccess,
		Default:      enabled,
	})
	db := testOpenDB(t)
	return NewGatewayService(store.NewGatewayRepo(db), store.NewGatewayDeploymentRepo(db), nil, nil, reg,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGatewayService_AgentLinkTypeFollowsItsFeatureFlag(t *testing.T) {
	tests := []struct {
		name          string
		flagOn        bool
		wantListed    bool
		wantCreateErr string
	}{
		{name: "flag on offers the type and creates it", flagOn: true, wantListed: true, wantCreateErr: ""},
		{name: "flag off hides the type and refuses to create it", flagOn: false, wantListed: false, wantCreateErr: "agent_link entrypoint is not enabled on this deployment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newGatewayServiceWithAgentLinkFlag(t, tt.flagOn)

			if got := hasSchema(svc.GetSchemas(), "agent_link"); got != tt.wantListed {
				t.Errorf("GetSchemas() lists agent_link = %v, want %v", got, tt.wantListed)
			}
			_, err := svc.Create(t.Context(), CreateGatewayRequest{
				Name:        "partner-link",
				Description: "Door for partner agents to join the mesh.",
				Type:        "agent_link",
				Values:      map[string]any{"maxInflight": 4},
			}, "alice@example.com")
			var gotCreateErr string
			if verr, ok := errors.AsType[*ValidationError](err); ok {
				gotCreateErr = verr.Message
			} else if err != nil {
				t.Fatalf("Create(agent_link) = %T %v, want a *ValidationError or nil", err, err)
			}
			if gotCreateErr != tt.wantCreateErr {
				t.Errorf("Create(agent_link) refusal = %q, want %q", gotCreateErr, tt.wantCreateErr)
			}
		})
	}
}

func TestGetNetworking_AgentLinkExposesItsConnectPath(t *testing.T) {
	tests := []struct {
		name        string
		domain      string
		wantURL     string
		wantConnect string
	}{
		{name: "shared ingress", domain: "", wantURL: "/gw/partner-link", wantConnect: "/gw/partner-link/v1/connect"},
		{name: "its own host", domain: "sam.example.com", wantURL: "https://gw-abc12345-mesh1.sam.example.com", wantConnect: "https://gw-abc12345-mesh1.sam.example.com/v1/connect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := store.NewGatewayRepo(store.TestDB(t))
			gw := &store.Gateway{Name: "Partner link", Slug: "partner-link", Type: "agent_link", DNSID: new("abc12345"), CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com"}
			if err := repo.Create(t.Context(), gw); err != nil {
				t.Fatalf("create the agent_link entrypoint: %v", err)
			}

			resp, err := NewGatewayNetworkingService(repo, tt.domain, "mesh1", netSvcNoopLogger()).GetNetworking(t.Context(), gw.ID)

			if err != nil || resp == nil {
				t.Fatalf("GetNetworking(agent_link) = %+v, %v, want its networking", resp, err)
			}
			if resp.URL != tt.wantURL {
				t.Errorf("GetNetworking(agent_link).URL = %q, want %q", resp.URL, tt.wantURL)
			}
			if resp.WebhookURL != tt.wantConnect {
				t.Errorf("GetNetworking(agent_link).WebhookURL = %q, want %q", resp.WebhookURL, tt.wantConnect)
			}
			wantPaths := []GatewayAPIPath{{Path: "/v1/connect", Description: "Agent link WebSocket endpoint that link agents connect to"}}
			if diff := cmp.Diff(wantPaths, resp.APIPaths); diff != "" {
				t.Errorf("GetNetworking(agent_link).APIPaths mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
