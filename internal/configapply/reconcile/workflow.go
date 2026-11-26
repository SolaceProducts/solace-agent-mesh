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
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

var workflowsResource = platformResource{path: "/workflows"}

func init() {
	generic.Register(generic.Kind{
		Name:          "workflow",
		Plural:        "workflows",
		Order:         50,
		CreateProto:   workflowHashView{},
		UpdateProto:   workflowHashView{},
		ResponseProto: workflowHashView{},
		NameFromCreate: func(v any) string {
			c, _ := v.(workflowHashView)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(workflowHashView)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(workflowHashView)
			return r.ID
		},
	})
}

type workflowHashView struct {
	ID          string         `json:"id" cfgapply:"read-only"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	AppConfig   map[string]any `json:"appConfig"`
}

func (c *Client) ListWorkflows(ctx context.Context) ([]wire.WorkflowResponse, error) {
	return listResource[wire.WorkflowResponse](ctx, c, workflowsResource)
}

func planWorkflows(desired []resolve.ResolvedResource, actual []wire.WorkflowResponse, hooks translate.WorkflowHooks) (*workflowKindPlan, error) {
	k, ok := generic.Lookup("workflow")
	if !ok {
		return nil, fmt.Errorf("workflow kind not registered")
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateWorkflowRequest, len(desired))
	var advisories []string
	for _, r := range desired {
		create, err := translateWorkflow(r, hooks)
		if err != nil {
			return nil, err
		}
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate workflow name %q in resolved resources", r.Name)
		}
		live[r.Name] = create
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     newWorkflowHashView("", create.Name, create.Description, create.AppConfig),
		})
	}

	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		actualVals = append(actualVals, reflect.ValueOf(newWorkflowHashView(a.ID, a.Name, a.Description, a.AppConfig)))
	}

	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	plan := convertWorkflowPlan(gp, live, actual)

	unchanged := make(map[string]bool, len(plan.Unchanged))
	for _, op := range plan.Unchanged {
		unchanged[op.Name] = true
	}
	for _, r := range desired {
		warnings, err := validateWorkflowToolFields(r, live[r.Name].AppConfig, unchanged[r.Name])
		if err != nil {
			return nil, err
		}
		advisories = append(advisories, warnings...)
		advisories = append(advisories, workflowDisplayNameAdvisories(r, live[r.Name].AppConfig)...)
		advisories = append(advisories, flatWorkflowSpecAdvisories(r)...)
	}
	plan.Warnings = advisories
	return plan, nil
}

func translateWorkflow(r resolve.ResolvedResource, hooks translate.WorkflowHooks) (wire.CreateWorkflowRequest, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	in["description"] = r.Description
	create, err := translate.ForwardWorkflowV2(in, hooks)
	if err != nil {
		return create, fmt.Errorf("translate workflow %q (%s): %w", r.Name, r.SourceFile, err)
	}
	if _, wrapped := r.Spec[workflowAppConfigWrapperKey]; !wrapped {
		create.AppConfig = workflowAppConfigFromInput(in)
	}
	if err := validateWorkflowNodeRefs(r, create.AppConfig, hooks); err != nil {
		return create, err
	}
	return create, nil
}

func validateWorkflowNodeRefs(r resolve.ResolvedResource, appConfig map[string]any, hooks translate.WorkflowHooks) error {
	if hooks.ValidateAgent == nil && hooks.ValidateWorkflow == nil && hooks.ValidateConnector == nil && hooks.ValidateToolset == nil {
		return nil
	}
	if err := validateWorkflowToolsetRefs(r, appConfig, hooks); err != nil {
		return err
	}
	body, _ := appConfig["workflow"].(map[string]any)
	if body == nil {
		body = appConfig
	}
	if err := validateWorkflowConnectorRefs(r, body, hooks); err != nil {
		return err
	}
	rawNodes, ok := body["nodes"].([]any)
	if !ok {
		return nil
	}
	for i, raw := range rawNodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		nodeID, _ := node["id"].(string)
		nodeType, _ := node["type"].(string)
		switch nodeType {
		case "agent":
			if hooks.ValidateAgent == nil {
				continue
			}
			name, err := refStringField(node, "agent_name")
			if err != nil {
				return fmt.Errorf("workflow %q (%s): node %q (index %d, type=agent): %w",
					r.Name, r.SourceFile, displayRefID(nodeID, i), i, err)
			}
			if name == "" {
				continue
			}
			if err := hooks.ValidateAgent(name); err != nil {
				return fmt.Errorf("workflow %q (%s): node %q (index %d, type=agent): %w",
					r.Name, r.SourceFile, displayRefID(nodeID, i), i, err)
			}
		case "workflow":
			if hooks.ValidateWorkflow == nil {
				continue
			}
			name, err := refStringField(node, "workflow_name")
			if err != nil {
				return fmt.Errorf("workflow %q (%s): node %q (index %d, type=workflow): %w",
					r.Name, r.SourceFile, displayRefID(nodeID, i), i, err)
			}
			if name == "" {
				continue
			}
			if err := hooks.ValidateWorkflow(name); err != nil {
				return fmt.Errorf("workflow %q (%s): node %q (index %d, type=workflow): %w",
					r.Name, r.SourceFile, displayRefID(nodeID, i), i, err)
			}
		}
	}
	return nil
}

func validateWorkflowToolsetRefs(r resolve.ResolvedResource, appConfig map[string]any, hooks translate.WorkflowHooks) error {
	if hooks.ValidateToolset == nil {
		return nil
	}
	carriers := []map[string]any{appConfig}
	if body, ok := appConfig["workflow"].(map[string]any); ok {
		carriers = append(carriers, body)
	}
	seen := map[string]struct{}{}
	for _, m := range carriers {
		raw, present := m["toolsets"]
		if !present {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("workflow %q (%s): toolsets must be a list of toolset names, got %T",
				r.Name, r.SourceFile, raw)
		}
		for i, item := range list {
			name, ok := item.(string)
			if !ok || name == "" {
				return fmt.Errorf("workflow %q (%s): toolsets[%d] must be a non-empty toolset name",
					r.Name, r.SourceFile, i)
			}
			if _, dup := seen[name]; dup {
				return fmt.Errorf("workflow %q (%s): toolsets lists %q more than once",
					r.Name, r.SourceFile, name)
			}
			seen[name] = struct{}{}
			if err := hooks.ValidateToolset(name); err != nil {
				return fmt.Errorf("workflow %q (%s): toolsets[%d]: %w", r.Name, r.SourceFile, i, err)
			}
		}
	}
	return nil
}

func validateWorkflowConnectorRefs(r resolve.ResolvedResource, body map[string]any, hooks translate.WorkflowHooks) error {
	if _, present := body["connector_refs"]; present {
		return fmt.Errorf("workflow %q (%s): connector_refs is no longer supported; tool nodes reference connectors directly via their 'connector' field",
			r.Name, r.SourceFile)
	}
	rawNodes, _ := body["nodes"].([]any)
	seen := map[string]struct{}{}
	for i, raw := range rawNodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		nodeID, _ := node["id"].(string)
		name, err := refStringField(node, "connector")
		if err != nil {
			return fmt.Errorf("workflow %q (%s): node %q (index %d): %w",
				r.Name, r.SourceFile, displayRefID(nodeID, i), i, err)
		}
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup || hooks.ValidateConnector == nil {
			continue
		}
		seen[name] = struct{}{}
		if err := hooks.ValidateConnector(name); err != nil {
			return fmt.Errorf("workflow %q (%s): node %q (index %d): %w",
				r.Name, r.SourceFile, displayRefID(nodeID, i), i, err)
		}
	}
	return nil
}

func workflowValidationHooks(
	ctx context.Context,
	c *Client,
	desiredWorkflows []resolve.ResolvedResource,
	actualWorkflows []wire.WorkflowResponse,
	desiredAgentNames []string,
	desiredConnectorNames []string,
	desiredToolsetNames []string,
) (translate.WorkflowHooks, error) {
	lookups, err := buildAgentWorkflowLookups(ctx, c, desiredWorkflows, actualWorkflows, desiredAgentNames)
	if err != nil {
		return translate.WorkflowHooks{}, err
	}
	return translate.WorkflowHooks{
		ValidateAgent:     lookups.validateAgent,
		ValidateWorkflow:  lookups.validateWorkflow,
		ValidateConnector: lazyConnectorValidator(ctx, c, desiredConnectorNames),
		ValidateToolset:   lazyToolsetValidator(ctx, c, desiredToolsetNames),
	}, nil
}

func lazyConnectorValidator(ctx context.Context, c *Client, desiredNames []string) func(string) error {
	desired := make(map[string]struct{}, len(desiredNames))
	for _, n := range desiredNames {
		desired[n] = struct{}{}
	}
	var (
		once     sync.Once
		live     map[string]struct{}
		fetchErr error
	)
	return func(name string) error {
		if _, ok := desired[name]; ok {
			return nil
		}
		once.Do(func() {
			conns, err := c.ListConnectors(ctx)
			if err != nil {
				fetchErr = fmt.Errorf("list connectors for reference validation: %w", err)
				return
			}
			live = make(map[string]struct{}, len(conns))
			for _, conn := range conns {
				live[conn.Name] = struct{}{}
			}
		})
		if fetchErr != nil {
			return fetchErr
		}
		if _, ok := live[name]; ok {
			return nil
		}
		return fmt.Errorf("connector %q not found on platform and not declared in the desired manifest", name)
	}
}

func lazyToolsetValidator(ctx context.Context, c *Client, desiredNames []string) func(string) error {
	desired := make(map[string]struct{}, len(desiredNames))
	for _, n := range desiredNames {
		desired[n] = struct{}{}
	}
	builtins := make(map[string]struct{}, len(builtinAgentToolsets))
	for _, n := range builtinAgentToolsets {
		builtins[n] = struct{}{}
	}
	var (
		once     sync.Once
		live     map[string]struct{}
		fetchErr error
	)
	return func(name string) error {
		if _, ok := desired[name]; ok {
			return nil
		}
		if _, ok := builtins[name]; ok {
			return nil
		}
		once.Do(func() {
			toolsets, err := c.ListToolsets(ctx)
			if err != nil {
				fetchErr = fmt.Errorf("list toolsets for reference validation: %w", err)
				return
			}
			live = make(map[string]struct{}, len(toolsets))
			for _, ts := range toolsets {
				live[ts.Name] = struct{}{}
			}
		})
		if fetchErr != nil {
			return fetchErr
		}
		if _, ok := live[name]; ok {
			return nil
		}
		return fmt.Errorf("toolset %q not found on platform and not declared in the desired manifest", name)
	}
}

func newWorkflowHashView(id, name, description string, body map[string]any) workflowHashView {
	var bodyCopy map[string]any
	if body != nil {
		bodyCopy = make(map[string]any, len(body))
		maps.Copy(bodyCopy, body)
	}
	return workflowHashView{ID: id, Name: name, Description: description, AppConfig: bodyCopy}
}

func convertWorkflowPlan(gp *generic.KindPlan, live map[string]wire.CreateWorkflowRequest, actual []wire.WorkflowResponse) *workflowKindPlan {
	out := &workflowKindPlan{Kind: "workflow"}
	actualByName := make(map[string]wire.WorkflowResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, workflowOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, Create: live[op.Name]})
	}
	for _, op := range gp.Updates {
		out.Updates = append(out.Updates, workflowOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Update:     wire.UpdateWorkflowRequest(live[op.Name]),
		})
	}
	for _, op := range gp.Unchanged {
		unchanged := workflowOp{Name: op.Name, Source: op.Source, Provenance: op.Provenance, ID: op.ID, Create: live[op.Name]}
		if a, ok := actualByName[op.Name]; ok {
			unchanged.Actual = &a
		}
		out.Unchanged = append(out.Unchanged, unchanged)
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, workflowOp{Name: op.Name, ID: op.ID})
	}
	return out
}

func applyWorkflows(ctx context.Context, c *Client, plan *workflowKindPlan, opts ApplyOptions) *workflowKindOutcome {
	outcome := &workflowKindOutcome{Kind: "workflow"}
	if plan == nil {
		return outcome
	}
	for _, op := range plan.Creates {
		var resp wire.WorkflowResponse
		err := c.callResource(ctx, workflowsResource, http.MethodPost, "", op.Create, &resp)
		res := workflowOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		var resp wire.WorkflowResponse
		err := c.callResource(ctx, workflowsResource, http.MethodPut, "/"+url.PathEscape(op.ID), op.Update, &resp)
		res := workflowOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		res.DeploymentStatus = resp.DeploymentStatus
		outcome.Updated = append(outcome.Updated, res)
	}
	outcome.OutOfSync = outOfSyncRedeploys(plan.Unchanged)
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, workflowOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.callResource(ctx, workflowsResource, http.MethodDelete, "/"+url.PathEscape(op.ID), nil, nil)
		res := workflowOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
	return outcome
}
