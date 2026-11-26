// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

var _ control.Manageable = (*Executor)(nil)

const (
	errRequestBodyRequired = "request body required"
	errInvalidJSONBodyFmt  = "invalid JSON body: %w"
)

func decodeStrictBody(body json.RawMessage, dst any) error {
	if len(body) == 0 {
		return errors.New(errRequestBodyRequired)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf(errInvalidJSONBodyFmt, err)
	}
	return nil
}

func (e *Executor) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{
		{Method: "get", Path: "instances", Description: "List all instances"},
		{Method: "get", Path: "instances/{name}", Description: "Get instance details"},
		{Method: "post", Path: "instances", Description: "Add and start a new instance"},
		{Method: "delete", Path: "instances/{name}", Description: "Stop and remove an instance"},
		{Method: "patch", Path: "instances/{name}", Description: "Enable/disable (start/stop) an instance"},
		{Method: "get", Path: "instances/{name}/config", Description: "Inspect an instance's current config"},
		{Method: "patch", Path: "instances/{name}/config", Description: "Hot-update an instance's config (sparse)"},
		{Method: "post", Path: "instances/{name}/toolAuth/query", Description: "List tool auth requirements and stored-credential status for a user"},
		{Method: "post", Path: "instances/{name}/toolAuth/begin", Description: "Begin a delegated OAuth flow for one credential on a user's behalf"},
		{Method: "post", Path: "instances/{name}/toolAuth/disconnect", Description: "Remove a user's stored credential so the next run must authorize again"},
		{Method: "get", Path: "bindings", Description: "Inspect the AWE's current pod-level bindings"},
		{Method: "patch", Path: "bindings", Description: "Upsert pod-level bindings (sparse)"},
	}
}

func (e *Executor) HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("missing resource path")
	}

	resource := path[0]
	switch resource {
	case "instances":
		return e.handleInstancesResource(ctx, method, path[1:], body, identity)
	case "bindings":
		return e.handleBindingsResource(ctx, method, path[1:], body)
	default:
		return nil, fmt.Errorf("unknown resource: %s", resource)
	}
}

func (e *Executor) handleInstancesResource(ctx context.Context, method string, sub []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	switch {
	case len(sub) == 0 && method == "get":
		return e.ListInstances(), nil

	case len(sub) == 0 && method == "post":
		return e.handleAddInstance(ctx, body)

	case len(sub) == 1 && method == "get":
		return e.GetInstance(sub[0])

	case len(sub) == 1 && method == "delete":
		deprovision, err := parseRemoveInstanceBody(body)
		if err != nil {
			return nil, err
		}
		return nil, e.removeInstance(ctx, sub[0], removalFor(deprovision))

	case len(sub) == 1 && method == "patch":
		return e.handlePatchInstance(ctx, sub[0], body)

	case len(sub) == 2 && sub[1] == "config" && method == "get":
		return e.handleGetInstanceConfig(sub[0])

	case len(sub) == 2 && sub[1] == "config" && method == "patch":
		return e.handlePatchInstanceConfig(sub[0], body)

	case len(sub) == 3 && sub[1] == "toolAuth" && sub[2] == "query" && method == "post":
		return e.handleToolAuthQuery(ctx, sub[0], body, identity)

	case len(sub) == 3 && sub[1] == "toolAuth" && sub[2] == "begin" && method == "post":
		return e.handleToolAuthBegin(ctx, sub[0], body, identity)

	case len(sub) == 3 && sub[1] == "toolAuth" && sub[2] == "disconnect" && method == "post":
		return e.handleToolAuthDisconnect(ctx, sub[0], body, identity)

	default:
		return nil, fmt.Errorf("unsupported: %s instances/%v", method, sub)
	}
}

func (e *Executor) handleBindingsResource(ctx context.Context, method string, sub []string, body json.RawMessage) (any, error) {
	if len(sub) != 0 {
		return nil, fmt.Errorf("unsupported bindings sub-path: %v", sub)
	}
	switch method {
	case "get":
		return e.bindings.Snapshot(), nil
	case "patch":
		return e.handlePatchBindings(ctx, body)
	default:
		return nil, fmt.Errorf("method not allowed on bindings: %s", method)
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

func (e *Executor) handleAddInstance(ctx context.Context, body json.RawMessage) (any, error) {
	if len(body) == 0 {
		return nil, errors.New(errRequestBodyRequired)
	}

	var unexpanded map[string]any
	if err := json.Unmarshal(body, &unexpanded); err != nil {
		return nil, fmt.Errorf(errInvalidJSONBodyFmt, err)
	}

	deploymentID, _ := unexpanded[control.DeploymentIDField].(string)
	delete(unexpanded, control.DeploymentIDField)
	return e.addAndStart(ctx, config.FromMap(unexpanded), deploymentID, false)
}

type patchRequest struct {
	Enabled *bool `json:"enabled"`
}

func (e *Executor) handlePatchInstance(ctx context.Context, name string, body json.RawMessage) (any, error) {
	if len(body) == 0 {
		return nil, errors.New(errRequestBodyRequired)
	}

	var req patchRequest
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

func (e *Executor) handleGetInstanceConfig(name string) (any, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, mi := range e.instances {
		if mi.inst.Name() != name {
			continue
		}
		if mi.cfg == nil {
			return map[string]any{}, nil
		}
		if m := config.AsMap(mi.cfg); m != nil {
			return m, nil
		}
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("instance %q: %w", name, control.ErrResourceNotFound)
}

func (e *Executor) handlePatchInstanceConfig(name string, body json.RawMessage) (any, error) {
	e.mu.RLock()
	found := false
	for _, mi := range e.instances {
		if mi.inst.Name() == name {
			found = true
			break
		}
	}
	e.mu.RUnlock()
	if !found {
		return nil, fmt.Errorf("instance %q: %w", name, control.ErrResourceNotFound)
	}

	if len(body) == 0 {
		return nil, errors.New(errRequestBodyRequired)
	}

	var patch map[string]any
	if err := json.Unmarshal(body, &patch); err != nil {
		return nil, fmt.Errorf(errInvalidJSONBodyFmt, err)
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("request body must contain at least one field")
	}

	var firstField string
	for field := range patch {
		firstField = field
		break
	}
	return nil, fmt.Errorf("unknown or non-dynamic field %q", firstField)
}

type bindingsPatch struct {
	Models map[string]map[string]any `json:"models"`
}

func (e *Executor) handlePatchBindings(ctx context.Context, body json.RawMessage) (any, error) {
	var patch bindingsPatch
	if err := decodeStrictBody(body, &patch); err != nil {
		return nil, err
	}
	if len(patch.Models) == 0 {
		return nil, fmt.Errorf("request body must contain at least one model binding")
	}
	for alias := range patch.Models {
		if alias == "" {
			return nil, fmt.Errorf("model alias must be non-empty")
		}
	}

	for alias, cfg := range patch.Models {
		if cfg == nil {
			e.bindings.DeleteModel(alias)
		} else if err := e.bindings.UpsertModel(ctx, alias, cfg); err != nil {
			return nil, fmt.Errorf("apply model %q: %w", alias, err)
		}
		e.bindings.Refresh(alias)
	}
	return e.bindings.Snapshot(), nil
}
