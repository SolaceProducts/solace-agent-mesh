// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/registry"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type PlanResult struct {
	Models            *kindPlan
	Connectors        *connectorKindPlan
	Toolsets          *toolsetPlan
	Skills            *skillPlan
	Agents            *agentKindPlan
	Workflows         *workflowKindPlan
	Gateways          *gatewayKindPlan
	LinkAgents        *linkAgentKindPlan
	Datasets          *datasetKindPlan
	Evaluators        *evaluatorKindPlan
	Experiments       *experimentKindPlan
	Budgets           *budgetPlan
	BudgetAssignments *budgetAssignmentPlan
	ProfileProvider   *profileProviderPlan
	RBACRoles         *rbacRolePlan
	RBACGrants        *rbacGrantPlan
	RBACClaimMappings *rbacClaimMappingPlan
	SystemUsers       *systemUserPlan
	DefaultRoles      *defaultRolesPlan
	WebuiSettings     *webuiSettingsPlan

	Warnings []string

	RBACWritesBlocked bool
}

type ApplyOptions struct {
	Prune             bool
	DryRun            bool
	NoDeploy          bool
	Resolve           resolve.Options
	NoBuild           bool
	BuildSDKVersion   string
	BuildStdout       io.Writer
	BuildStderr       io.Writer
	DeployProgress    io.Writer
	AgentDeployerWrap func(Deployer) Deployer
	AuthResolver      target.OAuthTokenResolver
	SchemaSkewSink    *SchemaSkewSink
	SkipRBACWrites    bool
}

type PlanOptions struct {
	Resolve         resolve.Options
	NoBuild         bool
	BuildSDKVersion string
	BuildStdout     io.Writer
	BuildStderr     io.Writer
	AuthResolver    target.OAuthTokenResolver
	SchemaSkewSink  *SchemaSkewSink
}

type ApplyResult struct {
	Plan *PlanResult

	Models            *kindOutcome
	Connectors        *connectorOutcome
	Toolsets          *toolsetOutcome
	Skills            *skillOutcome
	Agents            *agentKindOutcome
	Workflows         *workflowKindOutcome
	Gateways          *gatewayKindOutcome
	LinkAgents        *linkAgentKindOutcome
	Datasets          *datasetOutcome
	Evaluators        *evaluatorOutcome
	Experiments       *experimentOutcome
	Budgets           *budgetOutcome
	BudgetAssignments *budgetAssignmentOutcome
	ProfileProvider   *profileProviderOutcome
	RBACRoles         *rbacRoleOutcome
	RBACGrants        *rbacGrantOutcome
	RBACClaimMappings *rbacClaimMappingOutcome
	SystemUsers       *systemUserOutcome
	DefaultRoles      *defaultRolesOutcome
	WebuiSettings     *webuiSettingsOutcome
}

type kindPlan struct {
	Kind      string
	Creates   []modelOp
	Updates   []modelOp
	Unchanged []modelOp
	Deletes   []modelOp
}

type agentKindPlan struct {
	Kind      string
	Creates   []agentOp
	Updates   []agentOp
	Unchanged []agentOp
	Deletes   []agentOp
}

type agentOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateAgentRequest
	Update     wire.UpdateAgentRequest
	Actual     *wire.AgentResponse
	Clears     []string
}

type agentOpResult struct {
	Op               agentOp
	Verb             string
	Err              error
	DeploymentStatus string
}

const syncStatusOutOfSync = "out_of_sync"

func isOutOfSync(syncStatus *string) bool {
	return syncStatus != nil && *syncStatus == syncStatusOutOfSync
}

func redeployNeeded(syncStatus *string, deploymentStatus string) bool {
	return isOutOfSync(syncStatus) || deploymentStatus == entplatform.DisplayStatusNotDeployed
}

func actualAs[T any](actual reflect.Value) *T {
	if !actual.IsValid() {
		return nil
	}
	if v, ok := actual.Interface().(T); ok {
		return &v
	}
	return nil
}

type redeployable[R any] interface {
	outOfSync() bool
	redeployResult() R
}

func outOfSyncRedeploys[O redeployable[R], R any](unchanged []O) []R {
	var out []R
	for _, op := range unchanged {
		if op.outOfSync() {
			out = append(out, op.redeployResult())
		}
	}
	return out
}

type agentKindOutcome struct {
	Kind        string
	Created     []agentOpResult
	Updated     []agentOpResult
	Deleted     []agentOpResult
	Skipped     []agentOpResult
	OutOfSync   []agentOpResult
	Failures    []agentOpResult
	Deployments []deployResult
}

type workflowKindPlan struct {
	Kind      string
	Creates   []workflowOp
	Updates   []workflowOp
	Unchanged []workflowOp
	Deletes   []workflowOp

	Warnings []string
}

type workflowOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateWorkflowRequest
	Update     wire.UpdateWorkflowRequest
	Actual     *wire.WorkflowResponse
}

func (op workflowOp) outOfSync() bool {
	if op.Actual == nil {
		return false
	}
	return redeployNeeded(nil, op.Actual.DeploymentStatus) || op.Actual.DeploymentStatus == entplatform.DisplayStatusDeployFailed
}

func (op workflowOp) planRow() planRow {
	r := planRow{name: op.Name, provenance: op.Provenance}
	if op.outOfSync() {
		r.outOfSync = true
		r.lastDeployFailed = op.Actual.DeploymentStatus == entplatform.DisplayStatusDeployFailed
	}
	return r
}

func (op workflowOp) redeployResult() workflowOpResult {
	return workflowOpResult{Op: op, Verb: "redeploy", DeploymentStatus: op.Actual.DeploymentStatus}
}

func (op workflowOp) desiredAppConfig() map[string]any {
	if op.Update.AppConfig != nil {
		return op.Update.AppConfig
	}
	return op.Create.AppConfig
}

type workflowOpResult struct {
	Op               workflowOp
	Verb             string
	Err              error
	DeploymentStatus string
}

type workflowKindOutcome struct {
	Kind        string
	Created     []workflowOpResult
	Updated     []workflowOpResult
	Deleted     []workflowOpResult
	Skipped     []workflowOpResult
	OutOfSync   []workflowOpResult
	Failures    []workflowOpResult
	Deployments []deployResult
}

type modelOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateModelRequest
	Patch      wire.UpdateModelRequest
	Actual     *wire.ModelResponse
}

type opResult struct {
	Op       modelOp
	Verb     string
	Err      error
	Warnings []string
}

type kindOutcome struct {
	Kind     string
	Created  []opResult
	Updated  []opResult
	Deleted  []opResult
	Skipped  []opResult
	Failures []opResult
}

func Plan(ctx context.Context, t target.Target, repoRoot string, m *manifest.Manifest) (*PlanResult, error) {
	return PlanWithOptions(ctx, t, repoRoot, m, PlanOptions{})
}

func PlanWithOptions(ctx context.Context, t target.Target, repoRoot string, m *manifest.Manifest, opts PlanOptions) (*PlanResult, error) {
	c, err := newClientForTarget(ctx, t, opts.AuthResolver)
	if err != nil {
		return nil, err
	}
	c.SetSchemaSkewSink(opts.SchemaSkewSink)
	r := resolve.New(repoRoot, m, opts.Resolve)
	return planAll(ctx, c, repoRoot, m, r, BuildOptions{
		NoBuild:        opts.NoBuild,
		Strict:         false,
		SDKVersion:     opts.BuildSDKVersion,
		Stdout:         opts.BuildStdout,
		Stderr:         opts.BuildStderr,
		TargetResolver: NewPlatformBuildTargetResolver(c),
	})
}

func planAll(ctx context.Context, c *Client, repoRoot string, m *manifest.Manifest, r *resolve.Resolver, buildOpts BuildOptions) (*PlanResult, error) {
	out := &PlanResult{}

	if err := planAllNonAgentNonEval(ctx, c, repoRoot, m, r, buildOpts, out); err != nil {
		return nil, err
	}
	if err := planAllAgents(ctx, c, m, r, out); err != nil {
		return nil, err
	}
	if err := planAllWorkflowsGateways(ctx, c, m, r, out); err != nil {
		return nil, err
	}
	if err := planAllLinkAgents(ctx, c, m, r, out); err != nil {
		return nil, err
	}
	if err := planAllEval(ctx, c, m, r, out); err != nil {
		return nil, err
	}
	if err := planAllBudgets(ctx, c, m, r, out); err != nil {
		return nil, err
	}
	if err := planAllProfileProvider(ctx, c, m, out); err != nil {
		return nil, err
	}
	if err := planAllRBAC(ctx, c, repoRoot, m, out); err != nil {
		return nil, err
	}
	if err := planAllBudgetAssignments(ctx, c, out); err != nil {
		return nil, err
	}
	if err := planAllDefaultRoles(ctx, c, m, out); err != nil {
		return nil, err
	}
	if err := planAllWebuiSettings(ctx, c, m, out); err != nil {
		return nil, err
	}
	out.Warnings = append(out.Warnings, prunedModelWarnings(out)...)
	out.Warnings = append(out.Warnings, prunedEscalationWarnings(out.Evaluators)...)
	out.Warnings = append(out.Warnings, prunedSystemUserWarnings(out)...)

	return out, nil
}

func planAllDefaultRoles(ctx context.Context, c *Client, m *manifest.Manifest, out *PlanResult) error {
	var desired *manifest.DefaultRolesSpec
	if m.Platform != nil {
		desired = m.Platform.DefaultRoles
	}
	if desired == nil {
		return nil
	}
	plan, err := planDefaultRoles(ctx, c, desired)
	if err != nil {
		return err
	}
	out.DefaultRoles = plan
	return nil
}

func planAllWebuiSettings(ctx context.Context, c *Client, m *manifest.Manifest, out *PlanResult) error {
	var desired *manifest.WebuiSettingsSpec
	if m.Platform != nil {
		desired = m.Platform.WebuiSettings
	}
	if desired == nil && m.Platform == nil {
		return nil
	}
	plan, err := planWebuiSettings(ctx, c, desired)
	if err != nil {
		return err
	}
	out.WebuiSettings = plan
	return nil
}

func declaredRBACKinds(m *manifest.Manifest) []string {
	var out []string
	for _, k := range []string{"rbacRoles", "rbacClaimMappings", "rbacGrants", "serviceAccounts", "systemUsers"} {
		if _, ok := m.Resources[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

func planAllRBAC(ctx context.Context, c *Client, repoRoot string, m *manifest.Manifest, out *PlanResult) error {
	_, rolesDecl := m.Resources["rbacRoles"]
	_, grantsDecl := m.Resources["rbacGrants"]
	_, mappingsDecl := m.Resources["rbacClaimMappings"]
	_, serviceAccountsDecl := m.Resources["serviceAccounts"]
	_, systemUsersDecl := m.Resources["systemUsers"]
	if _, assignmentsDecl := m.Resources["rbacAssignments"]; assignmentsDecl {
		return fmt.Errorf("resources.rbacAssignments is not supported — declare grants as spec.users on the role file (kind: rbacRole) or as a standalone grant file (kind: rbacGrant)")
	}
	declaredKinds := declaredRBACKinds(m)
	if len(declaredKinds) == 0 {
		return nil
	}
	enabled, err := rbacFeatureProbe(ctx, c)
	if err != nil {
		return fmt.Errorf("rbac feature probe: %w", err)
	}
	if !enabled {
		return &rbacFeatureDisabledError{declaredKinds: declaredKinds}
	}

	manageAllowed, err := rbacManageProbe(ctx, c)
	if err != nil {
		return fmt.Errorf("rbac manage probe: %w", err)
	}
	if !manageAllowed {
		out.RBACWritesBlocked = true
	}

	rolesActual, err := c.ListRBACRoles(ctx)
	if err != nil {
		return err
	}

	var desiredRoles []resolve.ResolvedResource
	if rolesDecl {
		desiredRoles, err = resolve.ResolveRBACRoles(repoRoot, m)
		if err != nil {
			return err
		}
		rp, err := planRBACRoles(desiredRoles, rolesActual)
		if err != nil {
			return err
		}
		out.RBACRoles = rp
	}

	desiredRoleNames := rbacDesiredRoleNames(out.RBACRoles)
	xref := loadRBACRoleRegistry(rolesActual, desiredRoleNames)

	if mappingsDecl {
		desired, err := resolve.ResolveRBACClaimMappings(repoRoot, m)
		if err != nil {
			return err
		}
		actualMappings, err := c.ListRBACClaimMappings(ctx)
		if err != nil {
			return err
		}
		mp, err := planRBACClaimMappings(desired, actualMappings, xref)
		if err != nil {
			return err
		}
		out.RBACClaimMappings = mp
	}

	if rolesDecl || grantsDecl {
		desiredGrants, err := extractRoleUsers(desiredRoles)
		if err != nil {
			return err
		}
		var grantFileGrants []resolve.ResolvedResource
		if grantsDecl {
			var grantResources []resolve.ResolvedResource
			grantResources, err = resolve.ResolveRBACGrants(repoRoot, m)
			if err != nil {
				return err
			}
			grantFileGrants, err = extractGrantResources(grantResources)
			if err != nil {
				return err
			}
			desiredGrants = append(desiredGrants, grantFileGrants...)
		}
		actualGrants, holders, err := c.listRBACGrantsWithHolders(ctx)
		if err != nil {
			return err
		}
		gp, err := planRBACGrants(desiredGrants, actualGrants, holders, xref)
		if err != nil {
			return err
		}
		out.RBACGrants = gp
		out.Warnings = append(out.Warnings, gp.warnings...)
		out.Warnings = append(out.Warnings, legacyGrantAdvisories(desiredGrants)...)
		out.Warnings = append(out.Warnings, configRoleGrantAdvisories(grantFileGrants, desiredRoleNames)...)
	}

	if serviceAccountsDecl || systemUsersDecl {
		desired, err := resolve.ResolveServiceAccounts(repoRoot, m)
		if err != nil {
			return err
		}
		actualSystemUsers, err := c.ListSystemUsers(ctx)
		if err != nil {
			return err
		}
		sp, err := planSystemUsers(desired, actualSystemUsers, xref)
		if err != nil {
			return err
		}
		if systemUsersDecl {
			sp.Kind = resolve.LegacyServiceAccountKind
			out.Warnings = append(out.Warnings, legacyServiceAccountSpellingWarning)
		}
		if systemUsersDecl && serviceAccountsDecl {
			out.Warnings = append(out.Warnings, mixedServiceAccountSpellingWarning)
		}
		out.SystemUsers = sp
	}
	return nil
}

func legacyGrantAdvisories(desired []resolve.ResolvedResource) []string {
	var affected []string
	for _, r := range desired {
		if r.Kind != "rbacGrant" {
			continue
		}
		st, _ := r.Spec["subjectType"].(string)
		if st != "" && st != "legacy" {
			continue
		}
		id, _ := r.Spec["identity"].(string)
		role, _ := r.Spec["roleName"].(string)
		affected = append(affected, role+" → "+id)
	}
	if len(affected) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"rbac: %d grant(s) use a bare/legacy identity, which is not stable across user offboarding or email reuse — prefer a typed subject { type: sub, issuer, value } (or { type: email, value }) where possible. Affected: %s",
		len(affected), strings.Join(affected, ", "))}
}

func rbacDesiredRoleNames(p *rbacRolePlan) []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Creates)+len(p.Updates)+len(p.Unchanged))
	for _, op := range p.Creates {
		out = append(out, op.Name)
	}
	for _, op := range p.Updates {
		out = append(out, op.Name)
	}
	for _, op := range p.Unchanged {
		out = append(out, op.Name)
	}
	return out
}

func planAllProfileProvider(ctx context.Context, c *Client, m *manifest.Manifest, out *PlanResult) error {
	var desired *manifest.ProfileProviderSpec
	if m.Platform != nil {
		desired = m.Platform.ProfileProvider
	}
	if desired == nil && m.Platform == nil {
		return nil
	}

	var (
		actualToolsets []wire.ToolsetResponse
		err            error
	)
	if desired != nil {
		actualToolsets, err = c.ListToolsets(ctx)
		if err != nil {
			return err
		}
	}
	lookup := newToolsetLookup(actualToolsets, toolsetNamesFromPlan(out.Toolsets))
	plan, err := planProfileProvider(ctx, c, desired, lookup)
	if err != nil {
		return err
	}
	out.ProfileProvider = plan
	return nil
}

func planAllNonAgentNonEval(ctx context.Context, c *Client, repoRoot string, m *manifest.Manifest, r *resolve.Resolver, buildOpts BuildOptions, out *PlanResult) error {
	if _, declared := m.Resources["models"]; declared {
		desired, err := r.Models(ctx)
		if err != nil {
			return err
		}
		actual, err := c.ListModels(ctx)
		if err != nil {
			return err
		}
		if refusal := checkDecisionModelCreatesAllowed(ctx, c, desired, actual); refusal != nil {
			return refusal
		}
		mp, err := planModels(ctx, c, desired, actual)
		if err != nil {
			return err
		}
		out.Models = mp
	}

	if _, declared := m.Resources["connectors"]; declared {
		desired, err := r.Connectors(ctx)
		if err != nil {
			return err
		}
		actual, err := c.ListConnectors(ctx)
		if err != nil {
			return err
		}
		cp, err := planConnectors(ctx, c, desired, actual)
		if err != nil {
			return err
		}
		out.Connectors = cp
	}

	if _, declared := m.Resources["toolsets"]; declared {
		desired, err := r.Toolsets(ctx)
		if err != nil {
			return err
		}
		actual, err := c.ListToolsets(ctx)
		if err != nil {
			return err
		}
		tp, err := planToolsets(ctx, c, desired, actual, repoRoot, buildOpts, c.GetToolsetConfig)
		if err != nil {
			return err
		}
		out.Toolsets = tp
	}

	if _, declared := m.Resources["skills"]; declared {
		desired, err := r.Skills(ctx)
		if err != nil {
			return err
		}
		actual, err := c.ListSkills(ctx)
		if err != nil {
			return err
		}
		sp, err := planSkills(ctx, desired, actual, repoRoot, buildOpts)
		if err != nil {
			return err
		}
		out.Skills = sp
	}
	return nil
}

func planAllAgents(ctx context.Context, c *Client, m *manifest.Manifest, r *resolve.Resolver, out *PlanResult) error {
	if _, declared := m.Resources["agents"]; !declared {
		return nil
	}
	desired, err := r.Agents(ctx)
	if err != nil {
		return err
	}
	reg := registry.New(newClientLister(c))
	reg.SetBuiltinToolsets(builtinAgentToolsets)
	reg.SetBuiltinSkills(builtinskills.Names())
	if loadErr := reg.Load(ctx); loadErr != nil {
		return loadErr
	}
	if out.Skills != nil {
		reg.SetDesiredSkills(skillNamesFromPlan(out.Skills))
	}
	if out.Connectors != nil {
		reg.SetDesiredConnectors(connectorNamesFromPlan(out.Connectors))
	}
	if out.Toolsets != nil {
		reg.SetDesiredToolsets(toolsetNamesFromPlan(out.Toolsets))
	}
	hooks := translate.AgentHooks{
		LookupSkillID:       reg.LookupSkillID,
		LookupConnectorID:   reg.LookupConnectorID,
		ValidateToolset:     reg.ValidateToolset,
		LookupSkillName:     reg.LookupSkillName,
		LookupConnectorName: reg.LookupConnectorName,
	}
	actual, err := c.ListAgentsWithSkillConfigs(ctx)
	if err != nil {
		return err
	}
	ap, err := planAgents(ctx, c, desired, actual, hooks)
	if err != nil {
		return err
	}
	ap.noteClears()
	out.Agents = ap
	return nil
}

func planAllWorkflowsGateways(ctx context.Context, c *Client, m *manifest.Manifest, r *resolve.Resolver, out *PlanResult) error {
	var (
		desiredWorkflows []resolve.ResolvedResource
		actualWorkflows  []wire.WorkflowResponse
	)
	desiredAgentNames := slices.Concat(agentNamesFromPlan(out.Agents), declaredLinkAgentNames(m))
	if _, declared := m.Resources["workflows"]; declared {
		desired, err := r.Workflows(ctx)
		if err != nil {
			return err
		}
		actual, err := c.ListWorkflows(ctx)
		if err != nil {
			return err
		}
		hooks, err := workflowValidationHooks(ctx, c, desired, actual, desiredAgentNames, connectorNamesFromPlan(out.Connectors), toolsetNamesFromPlan(out.Toolsets))
		if err != nil {
			return err
		}
		wp, err := planWorkflows(desired, actual, hooks)
		if err != nil {
			return err
		}
		out.Workflows = wp
		out.Warnings = append(out.Warnings, wp.Warnings...)
		desiredWorkflows, actualWorkflows = desired, actual
	}

	if _, legacy := m.Resources["gateways"]; legacy {
		return fmt.Errorf("manifest resource key %q was renamed to %q", "gateways", "entrypoints")
	}
	if _, declared := m.Resources["entrypoints"]; declared {
		desired, err := r.Gateways(ctx)
		if err != nil {
			return err
		}
		actual, err := c.ListGateways(ctx)
		if err != nil {
			return err
		}
		if _, declared := m.Resources["workflows"]; !declared {
			actualWorkflows, err = c.ListWorkflows(ctx)
			if err != nil {
				return err
			}
		}
		hooks, err := gatewayValidationHooks(ctx, c, desiredWorkflows, actualWorkflows, desiredAgentNames)
		if err != nil {
			return err
		}
		gp, err := planGateways(ctx, c, desired, actual, hooks)
		if err != nil {
			return err
		}
		gp.noteClears()
		out.Gateways = gp
	}
	return nil
}

func declaredLinkAgentNames(m *manifest.Manifest) []string {
	entries := m.Resources[linkAgentsPlural]
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

func planAllLinkAgents(ctx context.Context, c *Client, m *manifest.Manifest, r *resolve.Resolver, out *PlanResult) error {
	if _, declared := m.Resources[linkAgentsPlural]; !declared {
		return nil
	}
	desired, err := r.LinkAgents(ctx)
	if err != nil {
		return err
	}
	actual, err := c.ListLinkAgents(ctx)
	if err != nil {
		return err
	}
	entrypoints, err := c.ListGateways(ctx)
	if err != nil {
		return fmt.Errorf("list entrypoints for link agent reference validation: %w", err)
	}
	lp, err := planLinkAgents(ctx, c, desired, actual, newLinkEntrypoints(entrypoints, out.Gateways))
	if err != nil {
		return err
	}
	if err := checkLinkAgentPeers(ctx, c, lp, actual, agentNamesFromPlan(out.Agents)); err != nil {
		return err
	}
	out.LinkAgents = lp
	return nil
}

func checkLinkAgentPeers(ctx context.Context, c *Client, plan *linkAgentKindPlan, linkAgents []wire.LinkAgentResponse, desiredAgentNames []string) error {
	namesPeers := slices.ContainsFunc(slices.Concat(plan.Creates, plan.Updates, plan.Unchanged), func(op linkAgentOp) bool { return len(op.Create.Peers) > 0 })
	if !namesPeers {
		return nil
	}
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("list agents for link agent peer checks: %w", err)
	}
	meshNames, instanceNameOf := map[string]struct{}{}, map[string]string{}
	for _, name := range desiredAgentNames {
		instanceNameOf[name] = ""
	}
	for _, op := range slices.Concat(plan.Creates, plan.Updates, plan.Unchanged) {
		instanceNameOf[op.Name] = ""
	}
	for _, a := range agents {
		meshNames[a.InstanceName], instanceNameOf[a.Name] = struct{}{}, a.InstanceName
	}
	for _, a := range linkAgents {
		meshNames[a.InstanceName], instanceNameOf[a.Name] = struct{}{}, a.InstanceName
	}
	return linkAgentPeerErrors(plan, meshNames, instanceNameOf)
}

func planAllEval(ctx context.Context, c *Client, m *manifest.Manifest, r *resolve.Resolver, out *PlanResult) error {
	var (
		datasetsActual   []wire.DatasetResponse
		evaluatorsActual []wire.EvaluatorResponse
		evalRegLoaded    bool
	)
	if _, declared := m.Resources["datasets"]; declared {
		desired, err := r.Datasets(ctx)
		if err != nil {
			return err
		}
		datasetsActual, err = c.ListDatasets(ctx)
		if err != nil {
			return err
		}
		evalRegLoaded = true
		dp, err := planDatasets(desired, datasetsActual)
		if err != nil {
			return err
		}
		if err := planDatasetExamples(ctx, dp, c.ListAllExamples); err != nil {
			return err
		}
		out.Datasets = dp
	}
	if _, declared := m.Resources["evaluators"]; declared {
		desired, err := r.Evaluators(ctx)
		if err != nil {
			return err
		}
		evaluatorsActual, err = c.ListEvaluators(ctx)
		if err != nil {
			return err
		}
		evalRegLoaded = true
		ep, err := planEvaluators(desired, evaluatorsActual)
		if err != nil {
			return err
		}
		out.Evaluators = ep
	}
	if _, declared := m.Resources["experiments"]; !declared {
		return nil
	}
	return planAllExperiments(ctx, c, r, out, datasetsActual, evaluatorsActual, evalRegLoaded)
}

func planAllExperiments(ctx context.Context, c *Client, r *resolve.Resolver, out *PlanResult, datasetsActual []wire.DatasetResponse, evaluatorsActual []wire.EvaluatorResponse, evalRegLoaded bool) error {
	desired, err := r.Experiments(ctx)
	if err != nil {
		return err
	}
	if !evalRegLoaded || datasetsActual == nil {
		datasetsActual, err = c.ListDatasets(ctx)
		if err != nil {
			return err
		}
	}
	if !evalRegLoaded || evaluatorsActual == nil {
		evaluatorsActual, err = c.ListEvaluators(ctx)
		if err != nil {
			return err
		}
	}
	hooks := evalExperimentHooks(datasetsActual, evaluatorsActual,
		datasetCreateNameSet(out.Datasets), evaluatorCreateNameSet(out.Evaluators))
	actual, err := c.ListExperiments(ctx)
	if err != nil {
		return err
	}
	xp, err := planExperiments(desired, actual, hooks)
	if err != nil {
		return err
	}
	if refErr := validateExperimentWorkflowRefs(ctx, c, r, changedExperiments(desired, xp)); refErr != nil {
		return refErr
	}
	out.Experiments = xp
	return nil
}

func changedExperiments(desired []resolve.ResolvedResource, plan *experimentKindPlan) []resolve.ResolvedResource {
	changed := map[string]bool{}
	for _, op := range slices.Concat(plan.Creates, plan.Updates) {
		changed[op.Name] = true
	}
	return slices.DeleteFunc(slices.Clone(desired), func(d resolve.ResolvedResource) bool { return !changed[d.Name] })
}

func validateExperimentWorkflowRefs(ctx context.Context, c *Client, r *resolve.Resolver, desired []resolve.ResolvedResource) error {
	var refs []resolve.ResolvedResource
	for _, d := range desired {
		if name, _ := d.Spec["targetWorkflow"].(string); name != "" {
			refs = append(refs, d)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	desiredWorkflows, err := r.Workflows(ctx)
	if err != nil {
		return err
	}
	actualWorkflows, err := c.ListWorkflows(ctx)
	if err != nil {
		return fmt.Errorf("list workflows for reference validation: %w", err)
	}
	known := knownWorkflowNames(desiredWorkflows, actualWorkflows)
	for _, d := range refs {
		name, _ := d.Spec["targetWorkflow"].(string)
		if _, ok := known[name]; !ok {
			return fmt.Errorf("experiment %q (%s): targetWorkflow: workflow %q not found on platform and not declared in the desired manifest",
				d.Name, d.SourceFile, name)
		}
	}
	return nil
}

func evalExperimentHooks(datasets []wire.DatasetResponse, evaluators []wire.EvaluatorResponse, desiredDatasets, desiredEvaluators map[string]struct{}) translate.ExperimentHooks {
	dsByName := make(map[string]string, len(datasets))
	dsByID := make(map[string]string, len(datasets))
	for _, d := range datasets {
		dsByName[d.Name] = d.ID
		dsByID[d.ID] = d.Name
	}
	evByName := make(map[string]string, len(evaluators))
	evByID := make(map[string]string, len(evaluators))
	for _, e := range evaluators {
		evByName[e.Name] = e.ID
		evByID[e.ID] = e.Name
	}
	return translate.ExperimentHooks{
		LookupDatasetID: func(name string) (string, error) {
			if id, ok := dsByName[name]; ok {
				return id, nil
			}
			if _, ok := desiredDatasets[name]; ok {
				return name, nil
			}
			return "", fmt.Errorf("dataset %q not found on platform and not declared in the desired manifest", name)
		},
		LookupEvaluatorID: func(name string) (string, error) {
			if id, ok := evByName[name]; ok {
				return id, nil
			}
			if _, ok := desiredEvaluators[name]; ok {
				return name, nil
			}
			return "", fmt.Errorf("evaluator %q not found on platform and not declared in the desired manifest", name)
		},
		LookupDatasetName: func(id string) (string, error) {
			if name, ok := dsByID[id]; ok {
				return name, nil
			}
			return "", fmt.Errorf("dataset ID %q not found on platform", id)
		},
		LookupEvaluatorName: func(id string) (string, error) {
			if name, ok := evByID[id]; ok {
				return name, nil
			}
			return "", fmt.Errorf("evaluator ID %q not found on platform", id)
		},
	}
}

func NewClientForTarget(ctx context.Context, t target.Target, oauthResolver target.OAuthTokenResolver) (*Client, error) {
	return newClientForTarget(ctx, t, oauthResolver)
}

func newClientForTarget(ctx context.Context, t target.Target, oauthResolver target.OAuthTokenResolver) (*Client, error) {
	resolved, err := t.Resolve(ctx, oauthResolver)
	if err != nil {
		return nil, err
	}
	if rf, ok := oauthResolver.(TokenRefresher); ok && rf != nil && resolved.FromOAuthCache {
		return NewClientWithAutoRefresh(resolved, t, rf), nil
	}
	return NewClient(resolved), nil
}

func Apply(ctx context.Context, t target.Target, repoRoot string, m *manifest.Manifest, opts ApplyOptions) (*ApplyResult, error) {
	c, err := newClientForTarget(ctx, t, opts.AuthResolver)
	if err != nil {
		return nil, err
	}
	c.SetSchemaSkewSink(opts.SchemaSkewSink)
	r := resolve.New(repoRoot, m, opts.Resolve)
	plan, err := planAll(ctx, c, repoRoot, m, r, BuildOptions{
		NoBuild:        opts.NoBuild,
		Strict:         true,
		SDKVersion:     opts.BuildSDKVersion,
		Stdout:         opts.BuildStdout,
		Stderr:         opts.BuildStderr,
		TargetResolver: NewPlatformBuildTargetResolver(c),
	})
	if err != nil {
		return nil, err
	}
	if opts.Prune {
		if err := errors.Join(checkPrunedModelReferences(plan), checkPrunedEscalationTargets(plan.Evaluators), checkPrunedSystemUserAssignments(plan)); err != nil {
			return nil, err
		}
	}
	out := &ApplyResult{Plan: plan}

	if opts.DryRun {
		return out, nil
	}

	if err := applyPlan(ctx, c, plan, opts, out); err != nil {
		return out, err
	}
	if anyFailures(out) {
		return out, ErrApplyHadFailures
	}
	return out, nil
}

func applyPlan(ctx context.Context, c *Client, plan *PlanResult, opts ApplyOptions, out *ApplyResult) error {
	modelDeletes := &pendingModelDeletes{plan: plan.Models}
	if err := applyConfigSync(ctx, c, plan, opts, out, modelDeletes); err != nil {
		modelDeletes.skip(opts, out.Models)
		return err
	}
	if !opts.NoDeploy {
		runDeployments(ctx, c, opts, out)
	}
	modelDeletes.run(ctx, c, opts, out.Models)
	return nil
}

func runDeployments(ctx context.Context, c *Client, opts ApplyOptions, out *ApplyResult) {
	progress := opts.DeployProgress
	if progress == nil {
		progress = io.Discard
	}
	if out.Agents != nil {
		var d Deployer = newAgentDeployer(c)
		if opts.AgentDeployerWrap != nil {
			d = opts.AgentDeployerWrap(d)
		}
		runAgentDeployments(ctx, d, progress, out.Agents)
	}
	if out.Workflows != nil {
		runWorkflowDeployments(ctx, newWorkflowDeployer(c), progress, out.Workflows)
	}
	if out.Gateways != nil {
		runGatewayDeployments(ctx, newGatewayDeployer(c), progress, out.Gateways)
	}
	if out.LinkAgents != nil {
		runLinkAgentDeployments(ctx, newAgentDeployer(c), progress, out.LinkAgents)
	}
}

func applyConfigSync(ctx context.Context, c *Client, plan *PlanResult, opts ApplyOptions, out *ApplyResult, modelDeletes *pendingModelDeletes) error {
	if plan.Models != nil {
		out.Models = applyModelUpserts(ctx, c, plan.Models)
	}
	if plan.Connectors != nil {
		out.Connectors = applyConnectors(ctx, c, plan.Connectors, opts)
	}
	if plan.Toolsets != nil {
		out.Toolsets = applyToolsets(ctx, c, plan.Toolsets, opts)
	}
	if plan.Skills != nil {
		out.Skills = applySkills(ctx, c, plan.Skills, opts)
	}
	if plan.Agents != nil {
		if err := resolvePlaceholderSkillIDs(ctx, c, plan.Agents); err != nil {
			return fmt.Errorf("resolve skill placeholders: %w", err)
		}
		if plan.Connectors != nil {
			if err := resolvePlaceholderConnectorIDs(ctx, c, plan.Agents); err != nil {
				return fmt.Errorf("resolve connector placeholders: %w", err)
			}
		}
		ready, held := holdAgentsOnUndiscoveredSkills(plan.Agents, undiscoveredSkillIDs(out.Skills))
		out.Agents = applyAgents(ctx, c, ready, opts)
		out.Agents.Failures = append(out.Agents.Failures, held...)
	}
	if plan.Workflows != nil {
		out.Workflows = applyWorkflows(ctx, c, plan.Workflows, opts)
	}
	if opts.NoDeploy {
		modelDeletes.run(ctx, c, opts, out.Models)
	}
	if plan.LinkAgents != nil {
		out.LinkAgents = applyLinkAgentDeletes(ctx, c, plan.LinkAgents, opts)
	}
	if plan.Gateways != nil {
		out.Gateways = applyGatewayUpserts(ctx, c, plan.Gateways)
	}
	if plan.LinkAgents != nil {
		applyLinkAgentUpserts(ctx, c, plan.LinkAgents, out.LinkAgents)
	}
	if plan.Gateways != nil {
		applyGatewayDeletes(ctx, c, plan.Gateways, opts, out.Gateways)
	}
	if plan.Datasets != nil {
		out.Datasets = applyDatasets(ctx, c, plan.Datasets, opts)
	}
	if plan.Evaluators != nil {
		out.Evaluators = applyEvaluators(ctx, c, plan.Evaluators, opts)
	}
	if plan.Budgets != nil {
		out.Budgets = applyBudgets(ctx, c, plan.Budgets, opts)
	}
	if plan.ProfileProvider != nil {
		out.ProfileProvider = applyProfileProvider(ctx, c, plan.ProfileProvider, opts)
	}
	if plan.WebuiSettings != nil {
		out.WebuiSettings = applyWebuiSettings(ctx, c, plan.WebuiSettings, opts)
	}
	applyRBACPhase(ctx, c, plan, opts, out)
	if plan.Experiments != nil {
		if plan.Datasets != nil || plan.Evaluators != nil {
			if err := resolvePlaceholderExperimentRefs(ctx, c, plan.Experiments); err != nil {
				return fmt.Errorf("resolve experiment placeholders: %w", err)
			}
		}
		out.Experiments = applyExperiments(ctx, c, plan.Experiments, opts)
	}
	if plan.BudgetAssignments != nil {
		out.BudgetAssignments = applyBudgetAssignments(ctx, c, plan, out.Budgets, opts)
	}
	applyBudgetDeletes(ctx, c, plan.Budgets, out.BudgetAssignments, opts, out.Budgets)
	return nil
}

func applyModelUpserts(ctx context.Context, c *Client, plan *kindPlan) *kindOutcome {
	outcome := &kindOutcome{Kind: "model"}
	for _, op := range plan.Creates {
		resp, err := c.CreateModel(ctx, op.Create)
		res := opResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			res.Warnings = pricingNotReturnedWarning(op.Create.Pricing, resp)
			outcome.Created = append(outcome.Created, res)
		}
	}
	for _, op := range plan.Updates {
		resp, err := c.UpdateModel(ctx, op)
		res := opResult{Op: op, Verb: "update", Err: err, Warnings: pricingNotReturnedWarning(updatePricingSent(op), resp)}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Updated = append(outcome.Updated, res)
		}
	}
	return outcome
}

var errModelDeletesNotReached = errors.New("apply stopped before model deletes ran")

type pendingModelDeletes struct {
	plan *kindPlan
	done bool
}

func (p *pendingModelDeletes) run(ctx context.Context, c *Client, opts ApplyOptions, outcome *kindOutcome) {
	if p.done || p.plan == nil {
		return
	}
	p.done = true
	applyModelDeletes(ctx, c, p.plan, opts, outcome)
}

func (p *pendingModelDeletes) skip(opts ApplyOptions, outcome *kindOutcome) {
	if p.done || p.plan == nil {
		return
	}
	p.done = true
	var reason error
	if opts.Prune {
		reason = errModelDeletesNotReached
	}
	for _, op := range p.plan.Deletes {
		outcome.Skipped = append(outcome.Skipped, opResult{Op: op, Verb: "delete", Err: reason})
	}
}

func applyModelDeletes(ctx context.Context, c *Client, plan *kindPlan, opts ApplyOptions, outcome *kindOutcome) {
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, opResult{Op: op, Verb: "delete"})
			continue
		}
		err := deleteUnusedModel(ctx, c, op)
		res := opResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
}

func pricingNotReturnedWarning(sent *apiv2.Pricing, resp *wire.ModelResponse) []string {
	if resp == nil || resp.Pricing != nil || !statesARate(sent) {
		return nil
	}
	return []string{"pricing was sent but the platform returned none: either this credential lacks model_config:*:read, so the platform stored the rates but does not return them, or the platform is an older release that ignores pricing because token governance (SAM_FEATURE_TOKEN_GOVERNANCE) is off"}
}

func updatePricingSent(op modelOp) *apiv2.Pricing {
	if !op.Patch.Pricing.Set {
		return nil
	}
	return op.Create.Pricing
}

func statesARate(p *apiv2.Pricing) bool {
	if p == nil {
		return false
	}
	for _, rate := range []*string{p.Input, p.Output, p.CacheRead, p.CacheWrite} {
		if rate != nil && strings.TrimSpace(*rate) != "" {
			return true
		}
	}
	return false
}

func anyFailures(r *ApplyResult) bool {
	if r == nil {
		return false
	}
	if r.Models != nil && len(r.Models.Failures) > 0 {
		return true
	}
	if r.Connectors != nil && len(r.Connectors.Failures) > 0 {
		return true
	}
	if r.Toolsets != nil && len(r.Toolsets.Failures) > 0 {
		return true
	}
	if r.Skills != nil && len(r.Skills.Failures) > 0 {
		return true
	}
	if r.Agents != nil && len(r.Agents.Failures) > 0 {
		return true
	}
	if r.Workflows != nil && len(r.Workflows.Failures) > 0 {
		return true
	}
	if r.Gateways != nil && len(r.Gateways.Failures) > 0 {
		return true
	}
	if r.LinkAgents != nil && len(r.LinkAgents.Failures) > 0 {
		return true
	}
	if r.Datasets != nil && len(r.Datasets.Failures) > 0 {
		return true
	}
	if r.Evaluators != nil && len(r.Evaluators.Failures) > 0 {
		return true
	}
	if r.Experiments != nil && len(r.Experiments.Failures) > 0 {
		return true
	}
	if r.Budgets != nil && len(r.Budgets.Failures) > 0 {
		return true
	}
	if r.BudgetAssignments != nil && len(r.BudgetAssignments.Failures) > 0 {
		return true
	}
	if r.ProfileProvider != nil && len(r.ProfileProvider.Failures) > 0 {
		return true
	}
	if r.RBACRoles != nil && len(r.RBACRoles.Failures) > 0 {
		return true
	}
	if r.RBACGrants != nil && len(r.RBACGrants.Failures) > 0 {
		return true
	}
	if r.SystemUsers != nil && len(r.SystemUsers.Failures) > 0 {
		return true
	}
	if r.RBACClaimMappings != nil && len(r.RBACClaimMappings.Failures) > 0 {
		return true
	}
	if r.DefaultRoles != nil && len(r.DefaultRoles.Failures) > 0 {
		return true
	}
	if r.WebuiSettings != nil && len(r.WebuiSettings.Failures) > 0 {
		return true
	}
	return false
}
