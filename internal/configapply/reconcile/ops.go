// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"slices"
)

type PlanOp struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Source string `json:"source,omitempty"`
}

const (
	ActionCreate    = "create"
	ActionUpdate    = "update"
	ActionDelete    = "delete"
	ActionRedeploy  = "redeploy"
	ActionUnchanged = "unchanged"
)

type PlanSummary struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Delete    int `json:"delete"`
	Redeploy  int `json:"redeploy"`
	Unchanged int `json:"unchanged"`
}

func (s PlanSummary) Pending(prune bool) bool {
	n := s.Create + s.Update + s.Redeploy
	if prune {
		n += s.Delete
	}
	return n > 0
}

func Summarize(ops []PlanOp) PlanSummary {
	var s PlanSummary
	for _, op := range ops {
		switch op.Action {
		case ActionCreate:
			s.Create++
		case ActionUpdate:
			s.Update++
		case ActionDelete:
			s.Delete++
		case ActionRedeploy:
			s.Redeploy++
		default:
			s.Unchanged++
		}
	}
	return s
}

func (p *PlanResult) Pending(prune, noDeploy bool) bool {
	ops := p.Ops()
	if p != nil && p.RBACWritesBlocked {
		ops = slices.DeleteFunc(ops, isRBACOp)
	}
	if noDeploy {
		ops = slices.DeleteFunc(ops, func(op PlanOp) bool { return op.Action == ActionRedeploy })
	}
	return Summarize(ops).Pending(prune)
}

var rbacKinds = []string{"rbacRole", "rbacClaimMapping", "rbacGrant", "serviceAccount", "systemUser"}

func isRBACOp(op PlanOp) bool {
	if op.Kind == platformSection {
		return op.Name == "defaultRoles"
	}
	return slices.Contains(rbacKinds, op.Kind)
}

func (p *PlanResult) Ops() []PlanOp {
	ops := []PlanOp{}
	if p == nil {
		return ops
	}
	if k := p.Models; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o modelOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.Connectors; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o connectorOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.Toolsets; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o toolsetOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.Skills; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o skillOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.Agents; k != nil {
		agentRow := func(o agentOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} }
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, nil, agentRow)
		ops = appendUnchangedOps(ops, k.Kind, k.Unchanged, agentRow)
	}
	if k := p.Workflows; k != nil {
		workflowRow := func(o workflowOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} }
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, nil, workflowRow)
		ops = appendUnchangedOps(ops, k.Kind, k.Unchanged, workflowRow)
	}
	if k := p.Gateways; k != nil {
		gatewayRow := func(o gatewayOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} }
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, nil, gatewayRow)
		ops = appendUnchangedOps(ops, k.Kind, k.Unchanged, gatewayRow)
	}
	if k := p.LinkAgents; k != nil {
		linkAgentRow := func(o linkAgentOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} }
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, nil, linkAgentRow)
		ops = appendUnchangedOps(ops, k.Kind, k.Unchanged, linkAgentRow)
	}
	if k := p.Datasets; k != nil {
		datasetRow := func(o datasetOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} }
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, nil, datasetRow)
		for _, o := range k.Unchanged {
			op := datasetRow(o)
			op.Kind, op.Action = k.Kind, ActionUnchanged
			if o.ExampleChanges.any() {
				op.Action = ActionUpdate
			}
			ops = append(ops, op)
		}
	}
	if k := p.Evaluators; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o evaluatorOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.Experiments; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o experimentOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.Budgets; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, slices.Concat(k.Kept, k.Unchanged), func(o budgetOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.BudgetAssignments; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, slices.Concat(k.Moves, k.Rerecords), k.Removes, k.Unchanged, func(o budgetAssignmentOp) PlanOp { return PlanOp{Name: o.name(), Source: o.Source} })
	}
	if k := p.RBACRoles; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o rbacRoleOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.RBACClaimMappings; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o rbacClaimMappingOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	if k := p.RBACGrants; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o rbacGrantOp) PlanOp { return PlanOp{Name: grantName(o), Source: o.Source} })
	}
	if k := p.SystemUsers; k != nil {
		ops = appendPlanOps(ops, k.Kind, k.Creates, k.Updates, k.Deletes, k.Unchanged, func(o systemUserOp) PlanOp { return PlanOp{Name: o.Name, Source: o.Source} })
	}
	for _, row := range platformRows(p.DefaultRoles, p.ProfileProvider, p.WebuiSettings) {
		ops = append(ops, PlanOp{Kind: platformSection, Name: row.name, Action: row.change.action()})
	}
	return ops
}

func appendPlanOps[T any](ops []PlanOp, kind string, creates, updates, deletes, unchanged []T, row func(T) PlanOp) []PlanOp {
	buckets := []struct {
		action string
		items  []T
	}{{ActionCreate, creates}, {ActionUpdate, updates}, {ActionDelete, deletes}, {ActionUnchanged, unchanged}}
	for _, b := range buckets {
		for _, item := range b.items {
			op := row(item)
			op.Kind, op.Action = kind, b.action
			ops = append(ops, op)
		}
	}
	return ops
}

func appendUnchangedOps[T interface{ outOfSync() bool }](ops []PlanOp, kind string, unchanged []T, row func(T) PlanOp) []PlanOp {
	for _, item := range unchanged {
		op := row(item)
		op.Kind, op.Action = kind, ActionUnchanged
		if item.outOfSync() {
			op.Action = ActionRedeploy
		}
		ops = append(ops, op)
	}
	return ops
}

func grantName(op rbacGrantOp) string {
	role := op.RoleName
	if role == "" {
		role = op.RoleID
	}
	return op.Identity + " → " + role
}

func (c platformChange) action() string {
	switch c {
	case platformUpdated:
		return ActionUpdate
	case platformDeleted:
		return ActionDelete
	default:
		return ActionUnchanged
	}
}

type ApplyOpResult struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Action string `json:"action,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	StatusCreated  = "created"
	StatusUpdated  = "updated"
	StatusDeleted  = "deleted"
	StatusDeployed = "deployed"
	StatusSkipped  = "skipped"
	StatusFailed   = "failed"
)

const verbDeploy = "deploy"

type outcomeView[R any] struct {
	kind                                       string
	created, updated, deleted, skipped, failed []R
	row                                        func(R) (name, verb string, err error)
}

func (v outcomeView[R]) appendTo(out []ApplyOpResult) []ApplyOpResult {
	add := func(rs []R, status string) {
		for _, r := range rs {
			name, verb, err := v.row(r)
			res := ApplyOpResult{Kind: v.kind, Name: name, Status: status}
			if status == StatusSkipped || status == StatusFailed {
				res.Action = verb
			}
			if err != nil {
				res.Error = err.Error()
			}
			out = append(out, res)
		}
	}
	add(v.created, StatusCreated)
	add(v.updated, StatusUpdated)
	add(v.deleted, StatusDeleted)
	add(v.skipped, StatusSkipped)
	var failed []R
	for _, r := range v.failed {
		if _, verb, _ := v.row(r); verb != verbDeploy {
			failed = append(failed, r)
		}
	}
	add(failed, StatusFailed)
	return out
}

func appendDeployResults(out []ApplyOpResult, deployments []deployResult) []ApplyOpResult {
	for _, d := range deployments {
		res := ApplyOpResult{Kind: d.Kind, Name: d.Name, Status: StatusDeployed, Action: d.Action}
		if d.Err != nil {
			res.Status, res.Error = StatusFailed, d.Err.Error()
		}
		out = append(out, res)
	}
	return out
}

func (r *ApplyResult) Results() []ApplyOpResult {
	out := []ApplyOpResult{}
	if r == nil {
		return out
	}
	if o := r.Models; o != nil {
		out = outcomeView[opResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x opResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.Connectors; o != nil {
		out = outcomeView[connectorOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x connectorOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.Toolsets; o != nil {
		out = outcomeView[toolsetOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x toolsetOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.Skills; o != nil {
		out = outcomeView[skillOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x skillOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.Agents; o != nil {
		out = outcomeView[agentOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x agentOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
		out = appendDeployResults(out, o.Deployments)
	}
	if o := r.Workflows; o != nil {
		out = outcomeView[workflowOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x workflowOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
		out = appendDeployResults(out, o.Deployments)
	}
	if o := r.Gateways; o != nil {
		out = outcomeView[gatewayOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x gatewayOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
		out = appendDeployResults(out, o.Deployments)
	}
	if o := r.LinkAgents; o != nil {
		out = outcomeView[linkAgentOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x linkAgentOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
		out = appendDeployResults(out, o.Deployments)
	}
	if o := r.Datasets; o != nil {
		out = outcomeView[datasetOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x datasetOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
		for _, ex := range unchangedDatasetExamples(o.Examples, o.Created, o.Updated) {
			if ex.Err == nil && ex.Inserts+ex.Updates+ex.Deletes > 0 {
				out = append(out, ApplyOpResult{Kind: o.Kind, Name: ex.Name, Status: StatusUpdated})
			}
		}
	}
	if o := r.Evaluators; o != nil {
		out = outcomeView[evaluatorOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x evaluatorOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.Experiments; o != nil {
		out = outcomeView[experimentOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x experimentOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.Budgets; o != nil {
		out = outcomeView[budgetOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x budgetOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.BudgetAssignments; o != nil {
		out = outcomeView[budgetAssignmentOpResult]{o.Kind, o.Created, slices.Concat(o.Moved, o.Rerecorded), o.Removed, o.Skipped, o.Failures,
			func(x budgetAssignmentOpResult) (string, string, error) { return x.Op.name(), x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.RBACRoles; o != nil {
		out = outcomeView[rbacRoleOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x rbacRoleOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.RBACClaimMappings; o != nil {
		out = outcomeView[rbacClaimMappingOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x rbacClaimMappingOpResult) (string, string, error) { return x.Op.Name, x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.RBACGrants; o != nil {
		out = outcomeView[rbacGrantOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x rbacGrantOpResult) (string, string, error) { return grantName(x.Op), x.Verb, x.Err }}.appendTo(out)
	}
	if o := r.SystemUsers; o != nil {
		out = outcomeView[systemUserOpResult]{o.Kind, o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures,
			func(x systemUserOpResult) (string, string, error) { return x.Op.Name, "", x.Err }}.appendTo(out)
	}
	return appendPlatformResults(out, r)
}

func appendPlatformResults(out []ApplyOpResult, r *ApplyResult) []ApplyOpResult {
	planned := map[string]platformChange{}
	if r.Plan != nil {
		for _, row := range platformRows(r.Plan.DefaultRoles, r.Plan.ProfileProvider, r.Plan.WebuiSettings) {
			planned[row.name] = row.change
		}
	}
	if o := r.DefaultRoles; o != nil {
		out = appendPlatformResult(out, "defaultRoles", planned["defaultRoles"], o.Applied, o.Skipped, o.Failures, func(v defaultRolesOpResult) (string, error) { return string(v.Verb), v.Err })
	}
	if o := r.ProfileProvider; o != nil {
		out = appendPlatformResult(out, "profileProvider", planned["profileProvider"], o.Applied, o.Skipped, o.Failures, func(v profileProviderOpResult) (string, error) { return string(v.Verb), v.Err })
	}
	if o := r.WebuiSettings; o != nil {
		out = appendPlatformResult(out, "webuiSettings", planned["webuiSettings"], o.Applied, o.Skipped, o.Failures, func(v webuiSettingsOpResult) (string, error) { return string(v.Verb), v.Err })
	}
	return out
}

func appendPlatformResult[T any](out []ApplyOpResult, name string, change platformChange, applied, skipped *T, failures []T, row func(T) (verb string, err error)) []ApplyOpResult {
	if applied != nil {
		status := StatusUpdated
		if change == platformDeleted {
			status = StatusDeleted
		}
		out = append(out, ApplyOpResult{Kind: platformSection, Name: name, Status: status})
	}
	if skipped != nil && change != platformUnchanged {
		out = append(out, ApplyOpResult{Kind: platformSection, Name: name, Status: StatusSkipped, Action: change.action()})
	}
	for _, f := range failures {
		verb, err := row(f)
		res := ApplyOpResult{Kind: platformSection, Name: name, Status: StatusFailed, Action: verb}
		if err != nil {
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	return out
}

type Diagnostic struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

const (
	DiagnosticWarning = "warning"
	DiagnosticError   = "error"
)

func Warning(msg string) Diagnostic {
	return Diagnostic{Level: DiagnosticWarning, Message: msg}
}

func (p *PlanResult) Diagnostics() []Diagnostic {
	out := []Diagnostic{}
	if p == nil {
		return out
	}
	for _, w := range p.Warnings {
		out = append(out, Warning(w))
	}
	if p.RBACWritesBlocked {
		out = append(out, Warning(rbacWritesBlockedNotice))
	}
	out = append(out, p.clearsDiagnostics()...)
	if p.Budgets != nil {
		for _, op := range slices.Concat(p.Budgets.Creates, p.Budgets.Updates, p.Budgets.Deletes, p.Budgets.Kept, p.Budgets.Unchanged) {
			for _, w := range op.Warnings {
				out = append(out, Warning(fmt.Sprintf("budget %q: %s", op.Name, w)))
			}
		}
	}
	return out
}

func (r *ApplyResult) Diagnostics() []Diagnostic {
	if r == nil {
		return []Diagnostic{}
	}
	out := r.Plan.Diagnostics()
	if o := r.Connectors; o != nil {
		for _, res := range slices.Concat(o.Created, o.Updated) {
			for _, w := range res.Warnings {
				out = append(out, Warning(fmt.Sprintf("connector %q: %s", res.Op.Name, w)))
			}
		}
	}
	if o := r.Skills; o != nil {
		for _, w := range o.Warnings {
			out = append(out, Warning(w))
		}
	}
	return out
}
