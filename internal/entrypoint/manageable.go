// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

var _ control.Manageable = (*GatewayExecutor)(nil)

const errInvalidJSONBodyFmt = "invalid JSON body: %w"

func (e *GatewayExecutor) ManagementEndpoints() []control.Endpoint {
	endpoints := []control.Endpoint{
		{Method: "get", Path: "instances", Description: "List all gateway instances"},
		{Method: "get", Path: "instances/{name}", Description: "Get gateway instance details"},
		{Method: "post", Path: "instances", Description: "Add and start a new gateway instance"},
		{Method: "delete", Path: "instances/{name}", Description: "Stop and remove a gateway instance"},
		{Method: "patch", Path: "instances/{name}", Description: "Enable/disable (start/stop) a gateway instance"},
	}
	if e.svc != nil {
		reg := e.svc.FeatureRegistry()
		if reg != nil && reg.IsEnabled(features.KeyObservabilityAgent) {
			endpoints = append(endpoints, analyticsControlEndpoints()...)
		}
		if reg != nil && reg.IsEnabled(features.KeyActivityMonitor) {
			endpoints = append(endpoints, activityControlEndpoints()...)
		}
	}
	if _, _, err := e.scheduledTaskService(); err == nil {
		endpoints = append(endpoints, scheduledTaskControlEndpoints()...)
	}
	if e.webUICore() != nil {
		endpoints = append(endpoints, control.Endpoint{Method: "get", Path: "webuiSettings", Description: "Inspect this gateway's effective web UI settings and their source"})
	}
	return endpoints
}

func (e *GatewayExecutor) HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("missing resource path")
	}

	resource := path[0]
	if resource == "analytics" {
		return e.handleAnalytics(ctx, method, path[1:], body, identity)
	}
	if resource == "activity" {
		return e.handleActivity(ctx, method, path[1:], body, identity)
	}
	if resource == "scheduledTasks" {
		return e.handleScheduledTasks(ctx, method, path[1:], body, identity)
	}
	if resource == "webuiSettings" {
		return e.handleWebuiSettingsInspect(ctx, method, identity)
	}
	if resource != "instances" {
		return nil, fmt.Errorf("unknown resource: %s", resource)
	}

	switch {
	case len(path) == 1 && method == "get":
		return e.ListInstances(), nil

	case len(path) == 1 && method == "post":
		return e.handleAddInstance(ctx, body)

	case len(path) == 2 && method == "get":
		return e.GetInstance(path[1])

	case len(path) == 2 && method == "delete":
		deprovision, err := parseRemoveInstanceBody(body)
		if err != nil {
			return nil, err
		}
		return nil, e.removeInstance(ctx, path[1], deprovision)

	case len(path) == 2 && method == "patch":
		return e.handlePatchInstance(ctx, path[1], body)

	default:
		return nil, fmt.Errorf("unsupported: %s %v", method, path)
	}
}

type removeInstanceRequest struct {
	Deprovision *bool `json:"deprovision"`
}

func parseRemoveInstanceBody(body json.RawMessage) (deprovision bool, err error) {
	if len(body) == 0 {
		return true, nil
	}
	var req removeInstanceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return false, fmt.Errorf(errInvalidJSONBodyFmt, err)
	}
	if req.Deprovision == nil {
		return true, nil
	}
	return *req.Deprovision, nil
}

type addGatewayRequest struct {
	Name      string         `json:"name"`
	AppConfig map[string]any `json:"app_config"`
}

func (e *GatewayExecutor) handleAddInstance(ctx context.Context, body json.RawMessage) (any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("request body required")
	}

	var req addGatewayRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf(errInvalidJSONBodyFmt, err)
	}

	if req.Name == "" {
		return nil, fmt.Errorf("'name' field required")
	}
	if req.AppConfig == nil {
		return nil, fmt.Errorf("'app_config' field required")
	}

	return e.AddAndStart(ctx, req.Name, req.AppConfig)
}

type gatewayPatchRequest struct {
	Enabled *bool `json:"enabled"`
}

func (e *GatewayExecutor) handlePatchInstance(ctx context.Context, name string, body json.RawMessage) (any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("request body required")
	}

	var req gatewayPatchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf(errInvalidJSONBodyFmt, err)
	}

	if req.Enabled == nil {
		return nil, fmt.Errorf("'enabled' field required")
	}

	if *req.Enabled {
		if err := e.StartInstance(ctx, name); err != nil {
			return nil, err
		}
	} else {
		if err := e.StopInstance(ctx, name); err != nil {
			return nil, err
		}
	}

	return e.GetInstance(name)
}
