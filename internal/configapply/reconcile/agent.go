// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/registry"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

var agentsResource = platformResource{path: "/agents"}

const agentErrorFormat = "agent %q: %w"

var builtinAgentToolsets = func() []string {
	names := slices.Collect(maps.Keys(schema.ValidBuiltinGroups))
	slices.Sort(names)
	return names
}()

func init() {
	generic.Register(generic.Kind{
		Name:          "agent",
		Plural:        "agents",
		Order:         40,
		CreateProto:   wire.CreateAgentRequest{},
		UpdateProto:   wire.UpdateAgentRequest{},
		ResponseProto: wire.AgentResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateAgentRequest)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.AgentResponse)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.AgentResponse)
			return r.ID
		},
	})
}

func (c *Client) ListAgents(ctx context.Context) ([]wire.AgentResponse, error) {
	return listResource[wire.AgentResponse](ctx, c, agentsResource)
}

func (c *Client) GetAgent(ctx context.Context, id string) (wire.AgentResponse, error) {
	var resp wire.AgentResponse
	if err := c.callResource(ctx, agentsResource, http.MethodGet, "/"+url.PathEscape(id), nil, &resp); err != nil {
		return wire.AgentResponse{}, err
	}
	return resp, nil
}

func (c *Client) ListAgentsWithSkillConfigs(ctx context.Context) ([]wire.AgentResponse, error) {
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.FillSkillConfigs(ctx, agents); err != nil {
		return nil, err
	}
	return agents, nil
}

func (c *Client) FillSkillConfigs(ctx context.Context, agents []wire.AgentResponse) error {
	for i := range agents {
		if len(agents[i].SkillIDs) == 0 {
			continue
		}
		full, err := c.GetAgent(ctx, agents[i].ID)
		if err != nil {
			return fmt.Errorf("get agent %q for its skill configs: %w", agents[i].Name, err)
		}
		agents[i].SkillConfigs = full.SkillConfigs
	}
	return nil
}

type clientLister struct{ c *Client }

func newClientLister(c *Client) *clientLister { return &clientLister{c: c} }

func NewLister(c *Client) registry.Lister { return newClientLister(c) }

func BuiltinAgentToolsets() []string {
	return append([]string(nil), builtinAgentToolsets...)
}

func (l *clientLister) ListSkills(ctx context.Context) ([]registry.NamedID, error) {
	skills, err := l.c.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]registry.NamedID, 0, len(skills))
	for _, s := range skills {
		out = append(out, registry.NamedID{ID: s.ID, Name: s.Name})
	}
	return out, nil
}

func (l *clientLister) ListConnectors(ctx context.Context) ([]registry.NamedID, error) {
	conns, err := l.c.ListConnectors(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]registry.NamedID, 0, len(conns))
	for _, conn := range conns {
		out = append(out, registry.NamedID{ID: conn.ID, Name: conn.Name})
	}
	return out, nil
}

func (l *clientLister) ListToolsets(ctx context.Context) ([]registry.NamedID, error) {
	tools, err := l.c.ListToolsets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]registry.NamedID, 0, len(tools))
	for _, t := range tools {
		out = append(out, registry.NamedID{ID: t.ID, Name: t.Name})
	}
	return out, nil
}

func agentNamesFromPlan(p *agentKindPlan) []string {
	if p == nil {
		return nil
	}
	return desiredNames(p.Creates, p.Updates, p.Unchanged, func(o agentOp) string { return o.Name })
}

type agentXRefAdapter struct {
	hooks translate.AgentHooks
}

func (a agentXRefAdapter) LookupID(kind, name string) (string, error) {
	switch kind {
	case "skill":
		if a.hooks.LookupSkillID == nil {
			return name, nil
		}
		return a.hooks.LookupSkillID(name)
	case "connector":
		if a.hooks.LookupConnectorID == nil {
			return name, nil
		}
		return a.hooks.LookupConnectorID(name)
	}
	return name, nil
}

func (a agentXRefAdapter) LookupName(kind, id string) (string, error) {
	switch kind {
	case "skill":
		if a.hooks.LookupSkillName == nil {
			return id, nil
		}
		return a.hooks.LookupSkillName(id)
	case "connector":
		if a.hooks.LookupConnectorName == nil {
			return id, nil
		}
		return a.hooks.LookupConnectorName(id)
	}
	return id, nil
}

func planAgents(ctx context.Context, differ SecretDiffer, desired []resolve.ResolvedResource, actual []wire.AgentResponse, hooks translate.AgentHooks) (*agentKindPlan, error) {
	k, ok := generic.Lookup("agent")
	if !ok {
		return nil, fmt.Errorf("agent kind not registered")
	}
	xref := agentXRefAdapter{hooks: hooks}

	actualByName := make(map[string]wire.AgentResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateAgentRequest, len(desired))
	for _, r := range desired {
		create, err := translateAgent(r)
		if err != nil {
			return nil, err
		}
		canonToolsets, canonConfigs, canonExcluded, canonErr := entplatform.CanonicalizeToolsetRefs(
			create.Toolsets, toolsetConfigBindings(create.ToolsetConfigs), create.ToolsetExcludedTools)
		if canonErr != nil {
			return nil, fmt.Errorf(agentErrorFormat, r.Name, canonErr)
		}
		create.Toolsets, create.ToolsetConfigs, create.ToolsetExcludedTools = canonToolsets, toolsetConfigsFromBindings(canonConfigs), canonExcluded
		if hooks.ValidateToolset != nil {
			for _, name := range create.Toolsets {
				if err := hooks.ValidateToolset(name); err != nil {
					return nil, fmt.Errorf("toolsets: %w", err)
				}
			}
		}
		if err := generic.ResolveForward(&create, xref); err != nil {
			return nil, fmt.Errorf(agentErrorFormat, r.Name, err)
		}
		sortSetValuedFields(&create)
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate agent name %q in resolved resources", r.Name)
		}
		live[r.Name] = create

		hashClone := create
		stripped, stripErr := stripUnsupportedAdditionalConfigKeys(create.AdditionalConfigurations)
		if stripErr != nil {
			return nil, fmt.Errorf(agentErrorFormat, r.Name, stripErr)
		}
		hashClone.AdditionalConfigurations = stripped
		if existing, found := actualByName[r.Name]; found {
			hashClone.ToolsetConfigs = stripToolsetBindingSecrets(create.ToolsetConfigs, existing.ToolsetConfigs)
			hashClone.SkillConfigs = stripSkillBindingSecrets(withStoredSkillNames(create.SkillConfigs, existing.SkillConfigs), existing.SkillConfigs)
			if sameStringSet(platformSkillIDs(create.SkillIDs), existing.SkillIDs) {
				applySkillRefPrecedence(&hashClone)
			}
		}
		hashClone.SkillConfigs = normalizeSkillBindingsForHash(hashClone.SkillConfigs)
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     hashClone,
		})
	}

	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		clone := a
		clone.Toolsets = sortedCopy(a.Toolsets)
		clone.InputModes = sortedCopy(a.InputModes)
		clone.OutputModes = sortedCopy(a.OutputModes)
		clone.ConnectorIDs = sortedCopy(a.ConnectorIDs)
		clone.ToolsetConfigs = stripToolsetBindingSecrets(a.ToolsetConfigs, a.ToolsetConfigs)
		clone.SkillConfigs = normalizeSkillBindingsForHash(stripSkillBindingSecrets(a.SkillConfigs, a.SkillConfigs))
		actualVals = append(actualVals, reflect.ValueOf(clone))
	}
	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	plan := convertAgentPlan(gp, live)
	if err := reclassifyAgentRotations(ctx, differ, plan, live, actualByName); err != nil {
		return nil, err
	}
	return plan, nil
}

func applySkillRefPrecedence(create *wire.CreateAgentRequest) {
	hasPlatformSkill := len(platformSkillIDs(create.SkillIDs)) > 0
	if !hasPlatformSkill || len(create.SkillRefs) == 0 {
		return
	}
	kept := make([]string, 0, len(create.SkillRefs))
	for _, ref := range create.SkillRefs {
		if entplatform.KeepSkillRef(ref, true) {
			kept = append(kept, ref)
		}
	}
	create.SkillRefs = kept
}

func platformSkillIDs(ids []string) []string {
	builtins := builtinSkillNameSet()
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := builtins[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

func sameStringSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

func sortSetValuedFields(create *wire.CreateAgentRequest) {
	create.Toolsets = sortedCopy(create.Toolsets)
	create.InputModes = sortedCopy(create.InputModes)
	create.OutputModes = sortedCopy(create.OutputModes)
	create.ConnectorIDs = sortedCopy(create.ConnectorIDs)
}

func reclassifyAgentRotations(ctx context.Context, differ SecretDiffer, plan *agentKindPlan, live map[string]wire.CreateAgentRequest, actualByName map[string]wire.AgentResponse) error {
	kept, promoted, err := classifyRotations(ctx, differ, plan.Unchanged, "agent", rotationClassifier[agentOp]{
		NameOf: func(op agentOp) string { return op.Name },
		SecretFields: func(op agentOp) []string {
			return agentOverlaySecretFieldsSet(live[op.Name], actualByName[op.Name])
		},
		UpdateScope: rbac.ScopeAgentBuilderUpdate,
		Diff: func(ctx context.Context, op agentOp) (bool, error) {
			d := live[op.Name]
			return differ.DiffSecrets(ctx, "agents", op.ID, apiv2.AgentSecretsDiffForm{
				ToolsetConfigs: d.ToolsetConfigs,
				SkillConfigs:   d.SkillConfigs,
			})
		},
		Promote: func(op agentOp) agentOp {
			out := agentOp{
				Name:       op.Name,
				Source:     op.Source,
				Provenance: op.Provenance,
				ID:         op.ID,
				Create:     live[op.Name],
				Update:     agentUpdate(live[op.Name]),
			}
			if a, found := actualByName[op.Name]; found {
				out.Actual = &a
			}
			return out
		},
	})
	plan.Unchanged = kept
	plan.Updates = append(plan.Updates, promoted...)
	return err
}

func agentOverlaySecretFieldsSet(d wire.CreateAgentRequest, a wire.AgentResponse) []string {
	var out []string
	for _, db := range d.ToolsetConfigs {
		for _, ab := range a.ToolsetConfigs {
			if ab.ToolsetName == db.ToolsetName {
				out = append(out, secretFieldsSet("toolsetConfigs."+db.ToolsetName, db.ConfigValues, ab.ConfigValues)...)
				break
			}
		}
	}
	for _, db := range d.SkillConfigs {
		for _, ab := range a.SkillConfigs {
			if skillBindingKey(db) == skillBindingKey(ab) {
				out = append(out, secretFieldsSet("skillConfigs."+cmp.Or(ptrString(db.SkillName), db.SkillID), skillConfigAsAny(db.ConfigValues), skillConfigAsAny(ab.ConfigValues))...)
				break
			}
		}
	}
	return out
}

func (op agentOp) outOfSync() bool {
	return op.Actual != nil && redeployNeeded(op.Actual.SyncStatus, op.Actual.DeploymentStatus)
}

func (op agentOp) planRow() planRow {
	r := planRow{name: op.Name, provenance: op.Provenance, clears: op.Clears}
	if op.outOfSync() {
		r.outOfSync = true
		r.lastDeployFailed = op.Actual.DeploymentStatus == entplatform.DisplayStatusDeployFailed
	}
	return r
}

func (op agentOp) redeployResult() agentOpResult {
	return agentOpResult{Op: op, Verb: "redeploy", DeploymentStatus: op.Actual.DeploymentStatus}
}

func translateAgent(r resolve.ResolvedResource) (wire.CreateAgentRequest, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardAgentV2(in, translate.AgentV2Hooks{})
	if err != nil {
		return create, fmt.Errorf("translate agent %q (%s): %w", r.Name, r.SourceFile, err)
	}
	create.SkillIDs = unionStrings(create.SkillRefs, create.SkillIDs)
	builtins := builtinSkillNameSet()
	builtinRefs := make([]string, 0)
	for _, id := range create.SkillIDs {
		if _, ok := builtins[id]; ok {
			builtinRefs = append(builtinRefs, id)
		}
	}
	create.SkillRefs = unionStrings(create.SkillRefs, builtinRefs)
	return create, nil
}

func stripToolsetBindingSecrets(cfgs, counterpart []apiv2.ToolsetConfig) []apiv2.ToolsetConfig {
	if len(cfgs) == 0 || len(counterpart) == 0 {
		return cfgs
	}
	valuesByName := make(map[string]map[string]any, len(counterpart))
	for _, c := range counterpart {
		valuesByName[c.ToolsetName] = c.ConfigValues
	}
	out := make([]apiv2.ToolsetConfig, len(cfgs))
	for i, c := range cfgs {
		out[i] = c
		cv, ok := valuesByName[c.ToolsetName]
		if !ok || c.ConfigValues == nil {
			continue
		}
		stripped, _ := stripRedactedNested(c.ConfigValues, cv)
		out[i].ConfigValues = mustMap(stripped)
	}
	return out
}

func stripSkillBindingSecrets(cfgs, counterpart []apiv2.SkillConfig) []apiv2.SkillConfig {
	if len(cfgs) == 0 || len(counterpart) == 0 {
		return cfgs
	}
	valuesByKey := make(map[string]map[string]map[string]any, len(counterpart))
	for _, c := range counterpart {
		valuesByKey[skillBindingKey(c)] = c.ConfigValues
	}
	out := make([]apiv2.SkillConfig, len(cfgs))
	for i, c := range cfgs {
		out[i] = c
		cv, ok := valuesByKey[skillBindingKey(c)]
		if !ok || c.ConfigValues == nil {
			continue
		}
		stripped, _ := stripRedactedNested(skillConfigAsAny(c.ConfigValues), skillConfigAsAny(cv))
		out[i].ConfigValues = skillConfigFromAny(stripped)
	}
	return out
}

func skillBindingKey(b apiv2.SkillConfig) string {
	if name := ptrString(b.SkillName); name != "" {
		return "name:" + name
	}
	return "id:" + b.SkillID
}

func withStoredSkillNames(cfgs, stored []apiv2.SkillConfig) []apiv2.SkillConfig {
	if len(cfgs) == 0 || len(stored) == 0 {
		return cfgs
	}
	nameByID := make(map[string]*string, len(stored))
	for _, s := range stored {
		if ptrString(s.SkillName) != "" {
			nameByID[s.SkillID] = s.SkillName
		}
	}
	out := slices.Clone(cfgs)
	for i, c := range out {
		if name, ok := nameByID[c.SkillID]; ok && ptrString(c.SkillName) == "" {
			out[i].SkillName = name
		}
	}
	return out
}

func normalizeSkillBindingsForHash(cfgs []apiv2.SkillConfig) []apiv2.SkillConfig {
	if len(cfgs) == 0 {
		return cfgs
	}
	out := make([]apiv2.SkillConfig, len(cfgs))
	for i, c := range cfgs {
		out[i] = c
		if ptrString(c.SkillName) != "" {
			out[i].SkillID = ""
		}
	}
	return out
}

func skillConfigAsAny(v map[string]map[string]any) map[string]any {
	out := make(map[string]any, len(v))
	for k, vv := range v {
		out[k] = vv
	}
	return out
}

func skillConfigFromAny(v any) map[string]map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]map[string]any, len(m))
	for k, vv := range m {
		if inner, ok := vv.(map[string]any); ok {
			out[k] = inner
		}
	}
	return out
}

func unionStrings(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range slices.Concat(a, b) {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func convertAgentPlan(gp *generic.KindPlan, live map[string]wire.CreateAgentRequest) *agentKindPlan {
	out := &agentKindPlan{Kind: "agent"}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, agentOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			Create:     live[op.Name],
		})
	}
	for _, op := range gp.Updates {
		ao := agentOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Create:     live[op.Name],
			Update:     agentUpdate(live[op.Name]),
		}
		ao.Actual = actualAs[wire.AgentResponse](op.Actual)
		out.Updates = append(out.Updates, ao)
	}
	for _, op := range gp.Unchanged {
		ao := agentOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
		}
		ao.Actual = actualAs[wire.AgentResponse](op.Actual)
		out.Unchanged = append(out.Unchanged, ao)
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, agentOp{Name: op.Name, ID: op.ID})
	}
	return out
}

func sortedCopy(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	out := slices.Clone(xs)
	slices.Sort(out)
	return out
}

func applyAgents(ctx context.Context, c *Client, plan *agentKindPlan, opts ApplyOptions) *agentKindOutcome {
	outcome := &agentKindOutcome{Kind: "agent"}
	if plan == nil {
		return outcome
	}
	for _, op := range plan.Creates {
		var resp wire.AgentResponse
		err := c.callResource(ctx, agentsResource, http.MethodPost, "", op.Create, &resp)
		res := agentOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		var resp wire.AgentResponse
		err := c.callResource(ctx, agentsResource, http.MethodPut, "/"+url.PathEscape(op.ID), op.Update, &resp)
		res := agentOpResult{Op: op, Verb: "update", Err: err}
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
			outcome.Skipped = append(outcome.Skipped, agentOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.callResource(ctx, agentsResource, http.MethodDelete, "/"+url.PathEscape(op.ID), nil, nil)
		res := agentOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
	return outcome
}

func agentUpdate(create wire.CreateAgentRequest) wire.UpdateAgentRequest {
	update, _ := generic.ToUpdate(create, wire.UpdateAgentRequest{}).(wire.UpdateAgentRequest)
	if update.ToolsetConfigs == nil {
		update.ToolsetConfigs = []apiv2.ToolsetConfig{}
	}
	if update.SkillConfigs == nil {
		update.SkillConfigs = []apiv2.SkillConfig{}
	}
	return update
}

func toolsetConfigBindings(cfgs []apiv2.ToolsetConfig) []entplatform.ToolsetConfigBinding {
	if cfgs == nil {
		return nil
	}
	out := make([]entplatform.ToolsetConfigBinding, len(cfgs))
	for i, c := range cfgs {
		out[i] = entplatform.ToolsetConfigBinding(c)
	}
	return out
}

func toolsetConfigsFromBindings(bindings []entplatform.ToolsetConfigBinding) []apiv2.ToolsetConfig {
	if bindings == nil {
		return nil
	}
	out := make([]apiv2.ToolsetConfig, len(bindings))
	for i, b := range bindings {
		out[i] = apiv2.ToolsetConfig(b)
	}
	return out
}

func stripUnsupportedAdditionalConfigKeys(configurations map[string]any) (map[string]any, error) {
	if len(configurations) == 0 {
		return configurations, nil
	}
	raw, err := json.Marshal(configurations)
	if err != nil {
		return nil, fmt.Errorf("encode additionalConfigurations: %w", err)
	}
	var stripped map[string]any
	if err := json.Unmarshal(entplatform.StripUnsupportedAdditionalConfigKeys(raw), &stripped); err != nil {
		return nil, fmt.Errorf("decode additionalConfigurations: %w", err)
	}
	return stripped, nil
}
