// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"io"
	"strings"
	"time"
)

type FormatOptions struct {
	NoColor bool
	Verbose bool
	Prune   bool
}

const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiBlue   = "\x1b[34m"
	ansiRed    = "\x1b[31m"
	ansiDim    = "\x1b[2m"
	ansiYellow = "\x1b[33m"
)

const (
	noResourcesLine           = "%s/\n  (no resources)\n"
	emptyTotalsLine           = "0 %s: 0 create, 0 update, 0 delete, 0 unchanged\n"
	totalsLine                = "%d %s: %d create, %d update, %d delete, %d unchanged\n"
	rowSimple                 = "  %s %s\n"
	rowWithBuild              = "  %s %s%s\n"
	rowWithError              = "  %s %s: %v\n"
	noChangeLabel             = "no change"
	outOfSyncLabel            = "out of sync — will re-deploy"
	lastDeployFailedSuffix    = " (last deploy failed)"
	skipPruneLabel            = "skipped (use --prune to delete)"
	skipMiniIDPLabel          = "skipped (mini_idp_mode)"
	keptCreateConflictedLabel = "kept (a create for this role was refused with 409)"
	keptHeldByCreateLabel     = "kept (a create in this apply resolved to it)"
	failedSuffix              = " failed"
	appliedSuffix             = " applied"
	deploymentsHeading        = "Deployments:\n"
	deployFailedLabel         = "deploy failed"
	deployCompleted           = ") completed"
	deployPrefix              = "deploy ("
	appliedHeading            = "\nApplied %s:\n"
	createPlusDeploy          = "create + deploy"
	updatePlusDeploy          = "update + deploy"
)

const needsPruneSuffix = " (needs --prune)"

func planDeleteLabel(opts FormatOptions) string {
	if opts.Prune {
		return "delete"
	}
	return "delete" + needsPruneSuffix
}

func color(opts FormatOptions, code, s string) string {
	if opts.NoColor || code == "" {
		return s
	}
	return code + s + ansiReset
}

const rbacWritesBlockedNotice = "platform is configured with mini_idp_mode: true. " +
	"RBAC writes are blocked. Plan computed normally; apply will skip RBAC kinds " +
	"with a per-resource notice."

func RenderPlan(w io.Writer, plan *PlanResult, opts FormatOptions) {
	if plan == nil {
		return
	}
	for _, d := range plan.Diagnostics() {
		_, _ = fmt.Fprintf(w, "Warning: %s\n\n", d.Message)
	}
	if plan.Models != nil {
		renderKindPlan(w, plan.Models, "models", opts)
	}
	if plan.Connectors != nil {
		renderConnectorPlan(w, plan.Connectors, "connectors", opts)
	}
	if plan.Toolsets != nil {
		renderToolsetPlan(w, plan.Toolsets, opts)
	}
	if plan.Skills != nil {
		renderSkillPlan(w, plan.Skills, opts)
	}
	if k := plan.Agents; k != nil {
		renderDeployedKindPlan(w, "agents", planRows[agentOp]{k.Creates, k.Updates, k.Deletes, k.Unchanged},
			agentOp.planRow, renderAgentUpdateVerbose, opts)
	}
	if k := plan.Workflows; k != nil {
		renderDeployedKindPlan(w, "workflows", planRows[workflowOp]{k.Creates, k.Updates, k.Deletes, k.Unchanged},
			workflowOp.planRow, nil, opts)
	}
	if k := plan.Gateways; k != nil {
		renderDeployedKindPlan(w, "entrypoints", planRows[gatewayOp]{k.Creates, k.Updates, k.Deletes, k.Unchanged},
			gatewayOp.planRow, renderGatewayUpdateVerbose, opts)
	}
	if k := plan.LinkAgents; k != nil {
		renderDeployedKindPlan(w, linkAgentsPlural, planRows[linkAgentOp]{k.Creates, k.Updates, k.Deletes, k.Unchanged},
			linkAgentOp.planRow, nil, opts)
	}
	if plan.Datasets != nil {
		renderDatasetPlan(w, plan.Datasets, opts)
	}
	if k := plan.Evaluators; k != nil {
		renderNamedKindPlan(w, "evaluators", planRows[evaluatorOp]{k.Creates, k.Updates, k.Deletes, k.Unchanged},
			func(o evaluatorOp) planRow { return planRow{name: o.Name, provenance: o.Provenance} }, opts)
	}
	if k := plan.Experiments; k != nil {
		renderNamedKindPlan(w, "experiments", planRows[experimentOp]{k.Creates, k.Updates, k.Deletes, k.Unchanged},
			func(o experimentOp) planRow { return planRow{name: o.Name, provenance: o.Provenance} }, opts)
	}
	if plan.Budgets != nil {
		renderBudgetPlan(w, plan.Budgets, opts)
	}
	if plan.BudgetAssignments != nil {
		renderBudgetAssignmentPlan(w, plan.BudgetAssignments, opts)
	}
	if plan.RBACRoles != nil {
		renderRBACRolePlan(w, plan.RBACRoles, opts)
	}
	if plan.RBACClaimMappings != nil {
		renderRBACClaimMappingPlan(w, plan.RBACClaimMappings, opts)
	}
	if plan.RBACGrants != nil {
		renderRBACGrantPlan(w, plan.RBACGrants, opts)
	}
	if plan.SystemUsers != nil {
		renderSystemUserPlan(w, plan.SystemUsers, opts)
	}
	renderPlatformPlan(w, plan.DefaultRoles, plan.ProfileProvider, plan.WebuiSettings, opts)
}

func renderToolsetPlan(w io.Writer, k *toolsetPlan, opts FormatOptions) {
	const dirName = "toolsets"
	total := len(k.Creates) + len(k.Updates) + len(k.Deletes) + len(k.Unchanged)
	if total == 0 {
		_, _ = fmt.Fprintf(w, noResourcesLine, dirName)
		_, _ = fmt.Fprintf(w, emptyTotalsLine, dirName)
		return
	}
	_, _ = fmt.Fprintf(w, "%s/\n", dirName)
	for _, op := range k.Creates {
		_, _ = fmt.Fprintf(w, rowWithBuild,
			color(opts, ansiGreen, "+ "+padName(op.Name)),
			color(opts, ansiGreen, "create"),
			buildColumn(op, opts))
	}
	for _, op := range k.Updates {
		label := "update"
		switch {
		case op.UploadNeeded && op.ConfigChanged:
			label = "update (re-upload, config)"
		case op.UploadNeeded:
			label = "update (re-upload)"
		case op.ConfigChanged:
			label = "update (config)"
		}
		_, _ = fmt.Fprintf(w, rowWithBuild,
			color(opts, ansiBlue, "~ "+padName(op.Name)),
			color(opts, ansiBlue, label),
			buildColumn(op, opts)+discoveryColumn(op, opts))
	}
	for _, op := range k.Deletes {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(op.Name)), color(opts, ansiRed, planDeleteLabel(opts)))
	}
	for _, op := range k.Unchanged {
		_, _ = fmt.Fprintf(w, rowWithBuild,
			color(opts, ansiDim, "= "+padName(op.Name)),
			color(opts, ansiDim, noChangeLabel),
			buildColumn(op, opts)+discoveryColumn(op, opts))
	}
	_, _ = fmt.Fprintf(w, totalsLine,
		total, dirName, len(k.Creates), len(k.Updates), len(k.Deletes), len(k.Unchanged))
}

func buildColumn(op toolsetOp, opts FormatOptions) string {
	if op.BuildAction == "" {
		return ""
	}
	target := ""
	if op.BuildTarget.OS != "" || op.BuildTarget.Arch != "" {
		target = " " + op.BuildTarget.OS + "/" + op.BuildTarget.Arch
	}
	var label string
	switch op.BuildAction {
	case BuildActionCacheHit:
		label = "BUILD: cache-hit" + target
	case BuildActionBuilt:
		secs := op.BuildDuration.Round(time.Millisecond * 100).Seconds()
		label = fmt.Sprintf("BUILD: built%s (%.1fs)", target, secs)
	case BuildActionSkipped:
		label = "BUILD: skipped — will run at apply"
	case BuildActionNone:
		label = "BUILD: none"
	default:
		label = "BUILD: " + string(op.BuildAction)
	}
	return "  " + color(opts, ansiDim, "["+label+"]")
}

func discoveryColumn(op toolsetOp, opts FormatOptions) string {
	if op.DiscoveryStatus != "failed" {
		return ""
	}
	msg := "no diagnostic details"
	if len(op.DiscoveryErrors) > 0 {
		msg = strings.Join(op.DiscoveryErrors, "; ")
	}
	return "  " + color(opts, ansiRed, "[DISCOVERY: failed — "+msg+"]")
}

func renderKindPlan(w io.Writer, k *kindPlan, dirName string, opts FormatOptions) {
	total := len(k.Creates) + len(k.Updates) + len(k.Deletes) + len(k.Unchanged)
	if total == 0 {
		_, _ = fmt.Fprintf(w, noResourcesLine, dirName)
		_, _ = fmt.Fprintf(w, emptyTotalsLine, dirName)
		return
	}

	_, _ = fmt.Fprintf(w, "%s/\n", dirName)
	for _, op := range k.Creates {
		_, _ = fmt.Fprintf(w, rowWithBuild, color(opts, ansiGreen, "+ "+padName(op.Name)), color(opts, ansiGreen, "create"), provenanceSuffix(op.Provenance, opts))
	}
	for _, op := range k.Updates {
		_, _ = fmt.Fprintf(w, rowWithBuild, color(opts, ansiBlue, "~ "+padName(op.Name)), color(opts, ansiBlue, "update"), provenanceSuffix(op.Provenance, opts))
		if opts.Verbose {
			renderModelUpdateVerbose(w, op, opts)
		}
	}
	for _, op := range k.Deletes {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(op.Name)), color(opts, ansiRed, planDeleteLabel(opts)))
	}
	for _, op := range k.Unchanged {
		_, _ = fmt.Fprintf(w, rowWithBuild, color(opts, ansiDim, "= "+padName(op.Name)), color(opts, ansiDim, noChangeLabel), provenanceSuffix(op.Provenance, opts))
	}
	_, _ = fmt.Fprintf(w, totalsLine,
		total, dirName, len(k.Creates), len(k.Updates), len(k.Deletes), len(k.Unchanged))
}

type planRows[T any] struct {
	creates, updates, deletes, unchanged []T
}

func (r planRows[T]) total() int {
	return len(r.creates) + len(r.updates) + len(r.deletes) + len(r.unchanged)
}

type planRow struct {
	name, provenance string
	outOfSync        bool
	lastDeployFailed bool
	clears           []string
}

type planStyle[T any] struct {
	createLabel, updateLabel string
	verbose                  func(io.Writer, T, FormatOptions)
}

func renderNamedKindPlan[T any](w io.Writer, dirName string, rows planRows[T], row func(T) planRow, opts FormatOptions) {
	renderPlanRows(w, dirName, rows, row, planStyle[T]{createLabel: "create", updateLabel: "update"}, opts)
}

func renderDeployedKindPlan[T any](w io.Writer, dirName string, rows planRows[T], row func(T) planRow, verbose func(io.Writer, T, FormatOptions), opts FormatOptions) {
	renderPlanRows(w, dirName, rows, row, planStyle[T]{createLabel: createPlusDeploy, updateLabel: updatePlusDeploy, verbose: verbose}, opts)
}

func renderPlanRows[T any](w io.Writer, dirName string, rows planRows[T], row func(T) planRow, style planStyle[T], opts FormatOptions) {
	total := rows.total()
	if total == 0 {
		_, _ = fmt.Fprintf(w, noResourcesLine, dirName)
		_, _ = fmt.Fprintf(w, emptyTotalsLine, dirName)
		return
	}
	_, _ = fmt.Fprintf(w, "%s/\n", dirName)
	emit := func(op T, code, symbol, label string) {
		r := row(op)
		_, _ = fmt.Fprintf(w, rowWithBuild, color(opts, code, symbol+padName(r.name)), color(opts, code, label), provenanceSuffix(r.provenance, opts))
	}
	for _, op := range rows.creates {
		emit(op, ansiGreen, "+ ", style.createLabel)
	}
	for _, op := range rows.updates {
		emit(op, ansiBlue, "~ ", style.updateLabel+clearsSuffix(row(op).clears))
		if opts.Verbose && style.verbose != nil {
			style.verbose(w, op, opts)
		}
	}
	for _, op := range rows.deletes {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(row(op).name)), color(opts, ansiRed, planDeleteLabel(opts)))
	}
	for _, op := range rows.unchanged {
		renderUnchangedRow(w, row(op), opts)
	}
	_, _ = fmt.Fprintf(w, totalsLine,
		total, dirName, len(rows.creates), len(rows.updates), len(rows.deletes), len(rows.unchanged))
}

func provenanceSuffix(provenance string, opts FormatOptions) string {
	if provenance == "" {
		return ""
	}
	return "  " + color(opts, ansiDim, "("+provenance+")")
}

func padName(name string) string {
	const w = 20
	if len(name) >= w {
		return name + " "
	}
	return name + spaces(w-len(name))
}

func spaces(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	return string(out)
}

func RenderApply(w io.Writer, res *ApplyResult, opts FormatOptions) {
	if res == nil {
		return
	}
	RenderPlan(w, res.Plan, opts)

	if res.Models != nil {
		renderApplyKind(w, res.Models, "models", opts)
	}
	if res.Connectors != nil {
		renderApplyConnectorKind(w, res.Connectors, "connectors", opts)
	}
	if res.Toolsets != nil {
		renderToolsetApply(w, res.Toolsets, opts)
	}
	if res.Skills != nil {
		renderSkillApply(w, res.Skills, opts)
	}
	if o := res.Agents; o != nil {
		renderDeployedKindApply(w, "agents", applyRows[agentOpResult]{o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures},
			func(r agentOpResult) (string, string, error) { return r.Op.Name, r.Verb, r.Err }, o.Deployments, opts)
	}
	if o := res.Workflows; o != nil {
		renderDeployedKindApply(w, "workflows", applyRows[workflowOpResult]{o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures},
			func(r workflowOpResult) (string, string, error) { return r.Op.Name, r.Verb, r.Err }, o.Deployments, opts)
	}
	if o := res.Gateways; o != nil {
		renderDeployedKindApply(w, "entrypoints", applyRows[gatewayOpResult]{o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures},
			func(r gatewayOpResult) (string, string, error) { return r.Op.Name, r.Verb, r.Err }, o.Deployments, opts)
	}
	if o := res.LinkAgents; o != nil {
		renderDeployedKindApply(w, linkAgentsPlural, applyRows[linkAgentOpResult]{o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures},
			func(r linkAgentOpResult) (string, string, error) { return r.Op.Name, r.Verb, r.Err }, o.Deployments, opts)
	}
	if res.RBACRoles != nil {
		renderRBACRoleApply(w, res.RBACRoles, opts)
	}
	if res.RBACClaimMappings != nil {
		renderRBACClaimMappingApply(w, res.RBACClaimMappings, opts)
	}
	if res.RBACGrants != nil {
		renderRBACGrantApply(w, res.RBACGrants, opts)
	}
	if res.SystemUsers != nil {
		renderSystemUserApply(w, res.SystemUsers, opts)
	}
	if res.Datasets != nil {
		renderApplyDatasets(w, res.Datasets, opts)
	}
	if o := res.Evaluators; o != nil {
		renderNamedKindApply(w, "evaluators", applyRows[evaluatorOpResult]{o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures},
			func(r evaluatorOpResult) (string, string, error) { return r.Op.Name, r.Verb, r.Err }, opts)
	}
	if o := res.Experiments; o != nil {
		renderNamedKindApply(w, "experiments", applyRows[experimentOpResult]{o.Created, o.Updated, o.Deleted, o.Skipped, o.Failures},
			func(r experimentOpResult) (string, string, error) { return r.Op.Name, r.Verb, r.Err }, opts)
	}
	if res.Budgets != nil {
		renderBudgetApply(w, res.Budgets, opts)
	}
	renderBudgetAssignmentApply(w, res.BudgetAssignments, opts)
	renderPlatformApply(w, res.DefaultRoles, res.ProfileProvider, res.WebuiSettings, opts)
}

func renderDeployments(w io.Writer, deployments []deployResult, opts FormatOptions) {
	if len(deployments) == 0 {
		return
	}
	_, _ = fmt.Fprint(w, deploymentsHeading)
	for _, d := range deployments {
		if d.Err != nil {
			_, _ = fmt.Fprintf(w, rowWithError,
				color(opts, ansiRed, "! "+padName(d.Name)),
				color(opts, ansiRed, deployFailedLabel),
				d.Err)
		} else {
			_, _ = fmt.Fprintf(w, rowSimple,
				color(opts, ansiGreen, "* "+padName(d.Name)),
				color(opts, ansiGreen, deployPrefix+d.Action+deployCompleted))
		}
	}
}

func renderUnchangedRow(w io.Writer, r planRow, opts FormatOptions) {
	marker, label, tint := "= ", noChangeLabel, ansiDim
	if r.outOfSync {
		marker, label, tint = "! ", outOfSyncLabel, ansiYellow
		if r.lastDeployFailed {
			label += lastDeployFailedSuffix
		}
	}
	_, _ = fmt.Fprintf(w, rowWithBuild,
		color(opts, tint, marker+padName(r.name)),
		color(opts, tint, label),
		provenanceSuffix(r.provenance, opts))
}

func renderConnectorPlan(w io.Writer, k *connectorKindPlan, dirName string, opts FormatOptions) {
	total := len(k.Creates) + len(k.Updates) + len(k.Deletes) + len(k.Unchanged)
	if total == 0 {
		_, _ = fmt.Fprintf(w, noResourcesLine, dirName)
		_, _ = fmt.Fprintf(w, emptyTotalsLine, dirName)
		return
	}

	_, _ = fmt.Fprintf(w, "%s/\n", dirName)
	for _, op := range k.Creates {
		_, _ = fmt.Fprintf(w, rowWithBuild, color(opts, ansiGreen, "+ "+padName(op.Name)), color(opts, ansiGreen, "create"+connectorSpecSuffix(op)), provenanceSuffix(op.Provenance, opts))
	}
	for _, op := range k.Updates {
		_, _ = fmt.Fprintf(w, rowWithBuild, color(opts, ansiBlue, "~ "+padName(op.Name)), color(opts, ansiBlue, connectorUpdateLabel(op)), provenanceSuffix(op.Provenance, opts))
	}
	for _, op := range k.Deletes {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(op.Name)), color(opts, ansiRed, planDeleteLabel(opts)))
	}
	for _, op := range k.Unchanged {
		_, _ = fmt.Fprintf(w, rowWithBuild, color(opts, ansiDim, "= "+padName(op.Name)), color(opts, ansiDim, noChangeLabel), provenanceSuffix(op.Provenance, opts))
	}
	_, _ = fmt.Fprintf(w, totalsLine,
		total, dirName, len(k.Creates), len(k.Updates), len(k.Deletes), len(k.Unchanged))
}

func connectorUpdateLabel(op connectorOp) string {
	if !op.SpecUploadNeeded {
		return "update"
	}
	if op.PatchNeeded {
		return "update (values, " + specHashTransition(op) + ")"
	}
	return "update (" + specHashTransition(op) + ")"
}

func connectorSpecSuffix(op connectorOp) string {
	if op.Spec == nil {
		return ""
	}
	return " (spec " + shortHash(op.Spec.Hash) + ")"
}

func specHashTransition(op connectorOp) string {
	from := "none"
	if op.PriorSpecHash != "" {
		from = shortHash(op.PriorSpecHash)
	}
	return "spec " + from + " → " + shortHash(op.Spec.Hash)
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func renderApplyConnectorKind(w io.Writer, o *connectorOutcome, dirName string, opts FormatOptions) {
	if len(o.Created) == 0 && len(o.Updated) == 0 && len(o.Deleted) == 0 && len(o.Failures) == 0 && len(o.Skipped) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, dirName)
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(r.Op.Name)), color(opts, ansiGreen, "created"))
		renderOpWarnings(w, r.Warnings, opts)
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(r.Op.Name)), color(opts, ansiBlue, "updated"))
		renderOpWarnings(w, r.Warnings, opts)
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.Name)), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(r.Op.Name)), color(opts, ansiDim, skipPruneLabel))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(r.Op.Name)), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}

func renderOpWarnings(w io.Writer, warnings []string, opts FormatOptions) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiYellow, "  warning:"), color(opts, ansiYellow, warning))
	}
}

func renderToolsetApply(w io.Writer, o *toolsetOutcome, opts FormatOptions) {
	const dirName = "toolsets"
	if len(o.Created) == 0 && len(o.Updated) == 0 && len(o.Deleted) == 0 && len(o.Failures) == 0 && len(o.Skipped) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, dirName)
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(r.Op.Name)), color(opts, ansiGreen, "created"))
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(r.Op.Name)), color(opts, ansiBlue, "updated"))
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.Name)), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(r.Op.Name)), color(opts, ansiDim, skipPruneLabel))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(r.Op.Name)), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}

type applyRows[R any] struct {
	created, updated, deleted, skipped, failures []R
}

func (r applyRows[R]) empty() bool {
	return len(r.created) == 0 && len(r.updated) == 0 && len(r.deleted) == 0 && len(r.skipped) == 0 && len(r.failures) == 0
}

func renderNamedKindApply[R any](w io.Writer, dirName string, rows applyRows[R], row func(R) (name, verb string, err error), opts FormatOptions) {
	if rows.empty() {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, dirName)
	renderApplyRows(w, rows, row, opts)
}

func renderDeployedKindApply[R any](w io.Writer, dirName string, rows applyRows[R], row func(R) (name, verb string, err error), deployments []deployResult, opts FormatOptions) {
	if rows.empty() && len(deployments) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, dirName)
	renderApplyRows(w, rows, row, opts)
	renderDeployments(w, deployments, opts)
}

func renderApplyRows[R any](w io.Writer, rows applyRows[R], row func(R) (name, verb string, err error), opts FormatOptions) {
	emit := func(rs []R, code, symbol, label string) {
		for _, r := range rs {
			name, _, _ := row(r)
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, code, symbol+padName(name)), color(opts, code, label))
		}
	}
	emit(rows.created, ansiGreen, "+ ", "created")
	emit(rows.updated, ansiBlue, "~ ", "updated")
	emit(rows.deleted, ansiRed, "- ", "deleted")
	emit(rows.skipped, ansiDim, "- ", skipPruneLabel)
	for _, r := range rows.failures {
		name, verb, err := row(r)
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(name)), color(opts, ansiRed, verb+failedSuffix), err)
	}
}

func renderApplyKind(w io.Writer, o *kindOutcome, dirName string, opts FormatOptions) {
	if len(o.Created) == 0 && len(o.Updated) == 0 && len(o.Deleted) == 0 && len(o.Failures) == 0 && len(o.Skipped) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, dirName)
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(r.Op.Name)), color(opts, ansiGreen, "created"))
		renderOpWarnings(w, r.Warnings, opts)
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(r.Op.Name)), color(opts, ansiBlue, "updated"))
		renderOpWarnings(w, r.Warnings, opts)
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.Name)), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		if r.Err != nil {
			_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiDim, "- "+padName(r.Op.Name)), color(opts, ansiDim, r.Verb+" skipped"), r.Err)
			continue
		}
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(r.Op.Name)), color(opts, ansiDim, skipPruneLabel))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(r.Op.Name)), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}
