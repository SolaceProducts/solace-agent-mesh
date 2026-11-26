// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var entrypointsResource = platformResource{path: "/entrypoints"}

func init() {
	generic.Register(generic.Kind{
		Name:          "entrypoint",
		Plural:        "entrypoints",
		Order:         60,
		CreateProto:   wire.CreateEntrypointRequest{},
		UpdateProto:   wire.UpdateEntrypointRequest{},
		ResponseProto: wire.EntrypointResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateEntrypointRequest)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.EntrypointResponse)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.EntrypointResponse)
			return r.ID
		},
	})
}

func (c *Client) ListGateways(ctx context.Context) ([]wire.EntrypointResponse, error) {
	return listResource[wire.EntrypointResponse](ctx, c, entrypointsResource)
}

type gatewayKindPlan struct {
	Kind      string
	Creates   []gatewayOp
	Updates   []gatewayOp
	Unchanged []gatewayOp
	Deletes   []gatewayOp
}

type gatewayOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateEntrypointRequest
	Update     wire.UpdateEntrypointRequest
	Actual     *wire.EntrypointResponse
	Clears     []string
}

func (op gatewayOp) outOfSync() bool {
	return op.Actual != nil && redeployNeeded(op.Actual.SyncStatus, op.Actual.DeploymentStatus)
}

func (op gatewayOp) planRow() planRow {
	r := planRow{name: op.Name, provenance: op.Provenance, clears: op.Clears}
	if op.outOfSync() {
		r.outOfSync = true
		r.lastDeployFailed = op.Actual.DeploymentStatus == entplatform.DisplayStatusDeployFailed
	}
	return r
}

func (op gatewayOp) redeployResult() gatewayOpResult {
	return gatewayOpResult{Op: op, Verb: "redeploy", DeploymentStatus: op.Actual.DeploymentStatus}
}

type gatewayOpResult struct {
	Op               gatewayOp
	Verb             string
	Err              error
	DeploymentStatus string
}

type gatewayKindOutcome struct {
	Kind        string
	Created     []gatewayOpResult
	Updated     []gatewayOpResult
	Deleted     []gatewayOpResult
	Skipped     []gatewayOpResult
	OutOfSync   []gatewayOpResult
	Failures    []gatewayOpResult
	Deployments []deployResult
}

func slugChanged(desired string, live wire.EntrypointResponse) bool {
	if desired == "" {
		return false
	}
	return desired != ptrString(live.Slug) || ptrString(live.Slug) == live.ID
}

func planGateways(ctx context.Context, differ SecretDiffer, desired []resolve.ResolvedResource, actual []wire.EntrypointResponse, hooks translate.GatewayHooks) (*gatewayKindPlan, error) {
	k, ok := generic.Lookup("entrypoint")
	if !ok {
		return nil, fmt.Errorf("gateway kind not registered")
	}

	actualByName := make(map[string]wire.EntrypointResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateEntrypointRequest, len(desired))
	for _, r := range desired {
		create, err := translateGateway(r, hooks)
		if err != nil {
			return nil, err
		}
		create.Values = normalizeEntrypointValues(create.EntrypointType, create.Values)
		if existing, found := actualByName[r.Name]; found && existing.EntrypointType != create.EntrypointType {
			return nil, fmt.Errorf("gateway %q: type change (%q -> %q) is not supported on update; delete the gateway and recreate it under the new type",
				r.Name, existing.EntrypointType, create.EntrypointType)
		}
		if existing, found := actualByName[r.Name]; found && slugChanged(create.Slug, existing) {
			if existingSlug := ptrString(existing.Slug); existingSlug == "" || existingSlug == existing.ID {
				return nil, fmt.Errorf("gateway %q: slug %q cannot be added to an existing entrypoint that has no slug; slugs are set at creation, delete and recreate the entrypoint", r.Name, create.Slug)
			}
			return nil, fmt.Errorf("gateway %q: slug change (%q -> %q) is not supported on update; slug is immutable, delete and recreate the entrypoint",
				r.Name, ptrString(existing.Slug), create.Slug)
		}
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate gateway name %q in resolved resources", r.Name)
		}
		live[r.Name] = create
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     create,
		})
	}

	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		if desiredCreate, found := live[a.Name]; found {
			a.Values = valuesNamedIn(a.Values, desiredCreate.Values)
		}
		actualVals = append(actualVals, reflect.ValueOf(a))
	}
	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	plan := convertGatewayPlan(gp, live)
	if err := reclassifyGatewayRotations(ctx, differ, plan, live, actualByName); err != nil {
		return nil, err
	}
	return plan, nil
}

func valuesNamedIn(stored, desired map[string]any) map[string]any {
	if stored == nil {
		return nil
	}
	named := make(map[string]any, len(desired))
	for k := range desired {
		if v, ok := stored[k]; ok {
			named[k] = v
		}
	}
	return named
}

func reclassifyGatewayRotations(
	ctx context.Context,
	differ SecretDiffer,
	plan *gatewayKindPlan,
	live map[string]wire.CreateEntrypointRequest,
	actualByName map[string]wire.EntrypointResponse,
) error {
	kept, promoted, err := classifyRotations(ctx, differ, plan.Unchanged, "entrypoint", rotationClassifier[gatewayOp]{
		NameOf: func(op gatewayOp) string { return op.Name },
		SecretFields: func(op gatewayOp) []string {
			return secretFieldsSet("values", live[op.Name].Values, actualByName[op.Name].Values)
		},
		UpdateScope: rbac.ScopeEntrypointUpdate,
		Diff: func(ctx context.Context, op gatewayOp) (bool, error) {
			return differ.DiffSecrets(ctx, "entrypoints", op.ID, live[op.Name].Values)
		},
		Promote: func(op gatewayOp) gatewayOp {
			create := live[op.Name]
			return gatewayOp{
				Name:       op.Name,
				Source:     op.Source,
				Provenance: op.Provenance,
				ID:         op.ID,
				Create:     create,
				Update:     wire.UpdateEntrypointRequest{Values: wire.Patch[map[string]any]{Set: true, Value: create.Values}},
			}
		},
	})
	plan.Unchanged = kept
	plan.Updates = append(plan.Updates, promoted...)
	return err
}

func translateGateway(r resolve.ResolvedResource, hooks translate.GatewayHooks) (wire.CreateEntrypointRequest, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardEntrypointV2(in, hooks)
	if err != nil {
		return create, fmt.Errorf("translate gateway %q (%s): %w", r.Name, r.SourceFile, err)
	}
	if err := validateGatewayAgentRefs(r, create, hooks); err != nil {
		return create, err
	}
	return create, nil
}

func gatewayErrorf(r resolve.ResolvedResource, err error) error {
	return fmt.Errorf("gateway %q (%s): %w", r.Name, r.SourceFile, err)
}

func validateGatewayAgentRefs(r resolve.ResolvedResource, create wire.CreateEntrypointRequest, hooks translate.GatewayHooks) error {
	if hooks.ValidateAgent == nil && hooks.ValidateWorkflow == nil {
		return nil
	}
	values := create.Values
	if values == nil {
		return nil
	}

	switch create.EntrypointType {
	case "event_mesh":
		return validateGatewayEventRules(r, values, hooks)
	case "slack", "teams", "email", "google_chat":
		if hooks.ValidateAgent == nil {
			return nil
		}
		name, err := refStringField(values, "default_agent_name")
		if err != nil {
			return gatewayErrorf(r, err)
		}
		if name == "" {
			return nil
		}
		if err := hooks.ValidateAgent(name); err != nil {
			return fmt.Errorf("gateway %q (%s): default_agent_name: %w", r.Name, r.SourceFile, err)
		}
	case "webhook":
		agent, err := refStringField(values, "targetAgentName")
		if err != nil {
			return gatewayErrorf(r, err)
		}
		workflow, err := refStringField(values, "targetWorkflowName")
		if err != nil {
			return gatewayErrorf(r, err)
		}
		if agent != "" && hooks.ValidateAgent != nil {
			if err := hooks.ValidateAgent(agent); err != nil {
				return fmt.Errorf("gateway %q (%s): targetAgentName: %w", r.Name, r.SourceFile, err)
			}
		}
		if workflow != "" && hooks.ValidateWorkflow != nil {
			if err := hooks.ValidateWorkflow(workflow); err != nil {
				return fmt.Errorf("gateway %q (%s): targetWorkflowName: %w", r.Name, r.SourceFile, err)
			}
		}
	}
	return nil
}

func validateGatewayEventRules(r resolve.ResolvedResource, values map[string]any, hooks translate.GatewayHooks) error {
	rawRules, ok := values["event_rules"].([]any)
	if !ok {
		return nil
	}
	for i, raw := range rawRules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ruleName, _ := rule["name"].(string)
		if hooks.ValidateAgent != nil {
			name, err := refStringField(rule, "targetAgent")
			if err != nil {
				return fmt.Errorf("gateway %q (%s): event_rules[%s]: %w",
					r.Name, r.SourceFile, displayRefID(ruleName, i), err)
			}
			if name != "" {
				if err := hooks.ValidateAgent(name); err != nil {
					return fmt.Errorf("gateway %q (%s): event_rules[%s]: targetAgent: %w",
						r.Name, r.SourceFile, displayRefID(ruleName, i), err)
				}
			}
		}
		if hooks.ValidateWorkflow != nil {
			name, err := refStringField(rule, "targetWorkflowName")
			if err != nil {
				return fmt.Errorf("gateway %q (%s): event_rules[%s]: %w",
					r.Name, r.SourceFile, displayRefID(ruleName, i), err)
			}
			if name != "" {
				if err := hooks.ValidateWorkflow(name); err != nil {
					return fmt.Errorf("gateway %q (%s): event_rules[%s]: targetWorkflowName: %w",
						r.Name, r.SourceFile, displayRefID(ruleName, i), err)
				}
			}
		}
	}
	return nil
}

func gatewayValidationHooks(
	ctx context.Context,
	c *Client,
	desiredWorkflows []resolve.ResolvedResource,
	actualWorkflows []wire.WorkflowResponse,
	desiredAgentNames []string,
) (translate.GatewayHooks, error) {
	lookups, err := buildAgentWorkflowLookups(ctx, c, desiredWorkflows, actualWorkflows, desiredAgentNames)
	if err != nil {
		return translate.GatewayHooks{}, err
	}
	return translate.GatewayHooks{
		ValidateAgent:    lookups.validateAgent,
		ValidateWorkflow: lookups.validateWorkflow,
	}, nil
}

func convertGatewayPlan(gp *generic.KindPlan, live map[string]wire.CreateEntrypointRequest) *gatewayKindPlan {
	out := &gatewayKindPlan{Kind: "entrypoint"}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, gatewayOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			Create:     live[op.Name],
		})
	}
	for _, op := range gp.Updates {
		patch, _ := op.Patch.Interface().(wire.UpdateEntrypointRequest)
		out.Updates = append(out.Updates, gatewayOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Create:     live[op.Name],
			Update:     patch,
			Actual:     actualAs[wire.EntrypointResponse](op.Actual),
		})
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, gatewayOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Actual:     actualAs[wire.EntrypointResponse](op.Actual),
		})
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, gatewayOp{Name: op.Name, ID: op.ID})
	}
	return out
}

func applyGatewayUpserts(ctx context.Context, c *Client, plan *gatewayKindPlan) *gatewayKindOutcome {
	outcome := &gatewayKindOutcome{Kind: "entrypoint"}
	if plan == nil {
		return outcome
	}
	for _, op := range plan.Creates {
		var resp wire.EntrypointResponse
		err := c.callResource(ctx, entrypointsResource, http.MethodPost, "", op.Create, &resp)
		res := gatewayOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		var resp wire.EntrypointResponse
		err := c.updateEntrypoint(ctx, op, &resp)
		res := gatewayOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		res.DeploymentStatus = resp.DeploymentStatus
		outcome.Updated = append(outcome.Updated, res)
	}
	outcome.OutOfSync = outOfSyncRedeploys(plan.Unchanged)
	return outcome
}

func applyGatewayDeletes(ctx context.Context, c *Client, plan *gatewayKindPlan, opts ApplyOptions, outcome *gatewayKindOutcome) {
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, gatewayOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.callResource(ctx, entrypointsResource, http.MethodDelete, "/"+url.PathEscape(op.ID), nil, nil)
		res := gatewayOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
}

func normalizeEntrypointValues(entrypointType string, values map[string]any) map[string]any {
	normalized, _ := entplatform.NormalizeGatewayValues(entrypointType, values).(map[string]any)
	return normalized
}

func (c *Client) updateEntrypoint(ctx context.Context, op gatewayOp, resp *wire.EntrypointResponse) error {
	return c.callResource(ctx, entrypointsResource, http.MethodPatch, "/"+url.PathEscape(op.ID), op.Update, resp)
}
