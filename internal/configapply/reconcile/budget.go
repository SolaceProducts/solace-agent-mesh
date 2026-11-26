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
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var budgetsResource = platformResource{path: "/budgets"}

const (
	budgetsDir = "budgets"

	verbCreate     = "create"
	verbUpdate     = "update"
	verbDelete     = "delete"
	verbSetDefault = "set default"

	maxExactFloatDigits = 15
)

var ErrBudgetsUnavailable = errors.New("the platform does not serve budgets on /api/v2; upgrade Agent Mesh to " + minV2PlatformRelease + " or later")

func (c *Client) ListBudgets(ctx context.Context) ([]wire.BudgetResponse, error) {
	budgets, err := listResource[wire.BudgetResponse](ctx, c, budgetsResource)
	var herr *HTTPError
	if errors.As(err, &herr) && herr.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("budgets: GET %s returned 404: %w", budgetsResource.apiPath(), ErrBudgetsUnavailable)
	}
	return budgets, err
}

func (c *Client) CreateBudget(ctx context.Context, req wire.CreateBudgetRequest) (wire.BudgetResponse, error) {
	return c.writeBudget(ctx, http.MethodPost, "", req)
}

func (c *Client) UpdateBudget(ctx context.Context, id string, req wire.UpdateBudgetRequest) (wire.BudgetResponse, error) {
	return c.writeBudget(ctx, http.MethodPatch, "/"+url.PathEscape(id), req)
}

func (c *Client) MakeDefaultBudget(ctx context.Context, id string) (wire.BudgetResponse, error) {
	return c.writeBudget(ctx, http.MethodPut, "/"+url.PathEscape(id)+"/makeDefault", nil)
}

func (c *Client) writeBudget(ctx context.Context, method, subPath string, body any) (wire.BudgetResponse, error) {
	var resp wire.BudgetResponse
	if err := c.callResource(ctx, budgetsResource, method, subPath, body, &resp); err != nil {
		return wire.BudgetResponse{}, err
	}
	if resp.ID == "" {
		return wire.BudgetResponse{}, fmt.Errorf("budgets: %s %s returned no budget", method, budgetsResource.apiPath()+subPath)
	}
	return resp, nil
}

func (c *Client) DeleteBudget(ctx context.Context, id string) error {
	return c.callResource(ctx, budgetsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

func patchOrNull[T any](value *T) wire.Patch[T] {
	if value == nil {
		return wire.Patch[T]{Set: true, Null: true}
	}
	return wire.Patch[T]{Set: true, Value: *value}
}

type desiredBudget struct {
	Source            string
	Name              string
	SubjectType       string
	Amount            *string
	WarningPercentage *int
	IsDefault         *bool
	Users             []budgetIdentity
	UsersManaged      bool
}

type budgetOp struct {
	Source      string
	Name        string
	SubjectType string
	ID          string

	Desired desiredBudget
	Actual  *wire.BudgetResponse

	Write   bool
	Promote bool

	KeptBecause keptReason
	Warnings    []string
}

type keptReason string

const (
	keptTierDefault keptReason = "kept (tier default)"
)

type budgetPlan struct {
	Kind      string
	Creates   []budgetOp
	Updates   []budgetOp
	Deletes   []budgetOp
	Unchanged []budgetOp
	Kept      []budgetOp
	Specs     []desiredBudget
}

type budgetOpResult struct {
	Op     budgetOp
	Verb   string
	Err    error
	Reason string
}

const skipReasonAssigneesNotMoved = "assignees_not_moved"

type budgetOutcome struct {
	Kind     string
	Created  []budgetOpResult
	Updated  []budgetOpResult
	Deleted  []budgetOpResult
	Skipped  []budgetOpResult
	Failures []budgetOpResult
}

var budgetSpecAllowedKeys = []string{"amount", "isDefault", "subjectType", budgetUsersKey, "warningPercentage"}

func readBudgetSpec(r resolve.ResolvedResource) (desiredBudget, error) {
	fail := func(format string, args ...any) (desiredBudget, error) {
		return desiredBudget{}, fmt.Errorf("budget %q (%s): "+format, append([]any{r.Name, r.SourceFile}, args...)...)
	}
	d := desiredBudget{Source: r.SourceFile, Name: strings.TrimSpace(r.Name)}
	if d.Name == "" {
		return fail("name is required")
	}
	for _, key := range slices.Sorted(maps.Keys(r.Spec)) {
		if !slices.Contains(budgetSpecAllowedKeys, key) {
			return fail("unknown spec key %q (allowed: %s)", key, strings.Join(budgetSpecAllowedKeys, ", "))
		}
	}
	d.SubjectType, _ = r.Spec["subjectType"].(string)
	if d.SubjectType == "" {
		return fail("spec.subjectType is required (%s or %s)", store.BudgetSubjectMesh, store.BudgetSubjectUser)
	}

	rawAmount, present := r.Spec["amount"]
	if !present {
		return fail("spec.amount is required; write null for an unlimited budget")
	}
	amount, err := budgetAmountText(rawAmount)
	if err != nil {
		return fail("spec.amount: %v", err)
	}
	d.Amount = amount

	switch v := r.Spec["warningPercentage"].(type) {
	case nil:
	case int:
		d.WarningPercentage = &v
	default:
		return fail("spec.warningPercentage must be a whole number or null, got %T", v)
	}

	switch v := r.Spec["isDefault"].(type) {
	case nil:
	case bool:
		d.IsDefault = &v
	default:
		return fail("spec.isDefault must be true or false, got %T", v)
	}
	if d.SubjectType == store.BudgetSubjectMesh && d.IsDefault != nil && !*d.IsDefault {
		return fail("the mesh budget is always its tier's default; remove isDefault: false")
	}
	users, managed, err := readBudgetUsers(r.Spec)
	if err != nil {
		return fail("%v", err)
	}
	if managed && d.SubjectType == store.BudgetSubjectMesh {
		return fail("the mesh budget applies to the whole deployment and has no users; remove spec.users")
	}
	d.Users, d.UsersManaged = users, managed
	return d, nil
}

func budgetAmountText(raw any) (*string, error) {
	var text string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		text = v
	case int:
		text = strconv.Itoa(v)
	case int64:
		text = strconv.FormatInt(v, 10)
	case uint64:
		text = strconv.FormatUint(v, 10)
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
		if significantDigits(text) > maxExactFloatDigits {
			return nil, fmt.Errorf("%s has more digits than a YAML number keeps exactly; quote the amount", text)
		}
	default:
		return nil, fmt.Errorf("must be a decimal string, a number, or null, got %T", v)
	}
	if parsed, err := budget.ParseAmount(text); err == nil {
		text = budget.FormatAmount(parsed)
	}
	return &text, nil
}

func significantDigits(decimal string) int {
	return len(strings.Trim(strings.ReplaceAll(decimal, ".", ""), "0"))
}

func planBudgets(desired []resolve.ResolvedResource, actual []wire.BudgetResponse) (*budgetPlan, error) {
	specs := make([]desiredBudget, 0, len(desired))
	seen := map[string]string{}
	meshDeclared := ""
	declaresDefault := map[string]string{}
	for _, r := range desired {
		d, err := readBudgetSpec(r)
		if err != nil {
			return nil, err
		}
		key := store.BudgetNameLower(d.Name)
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("budget %q duplicates %q: budget names are unique regardless of case", d.Name, prev)
		}
		seen[key] = d.Name
		if d.SubjectType == store.BudgetSubjectMesh {
			if meshDeclared != "" {
				return nil, fmt.Errorf("budgets %q and %q are both mesh budgets; the platform has exactly one", meshDeclared, d.Name)
			}
			meshDeclared = d.Name
		}
		if d.SubjectType != store.BudgetSubjectMesh && d.IsDefault != nil && *d.IsDefault {
			if prev, dup := declaresDefault[d.SubjectType]; dup {
				return nil, fmt.Errorf("budgets %q and %q are both marked isDefault for the %s tier; a tier has one default", prev, d.Name, d.SubjectType)
			}
			declaresDefault[d.SubjectType] = d.Name
		}
		specs = append(specs, d)
	}

	byName := make(map[string]*wire.BudgetResponse, len(actual))
	var mesh *wire.BudgetResponse
	for i := range actual {
		a := &actual[i]
		byName[store.BudgetNameLower(a.Name)] = a
		if a.SubjectType == store.BudgetSubjectMesh {
			mesh = a
		}
	}

	plan := &budgetPlan{Kind: "budget", Specs: specs}
	matched := map[string]bool{}
	promotes := map[string]bool{}
	for _, d := range specs {
		cur, err := matchBudget(d, mesh, byName)
		if err != nil {
			return nil, err
		}
		op := budgetOp{Source: d.Source, Name: d.Name, SubjectType: d.SubjectType, Desired: d}
		wantsDefault := d.SubjectType != store.BudgetSubjectMesh && d.IsDefault != nil && *d.IsDefault
		if cur == nil {
			op.Write, op.Promote = true, wantsDefault
			op.Warnings = limitedUserDefaultWarnings(d, wantsDefault)
			plan.Creates = append(plan.Creates, op)
			if op.Promote {
				promotes[d.SubjectType] = true
			}
			continue
		}
		matched[cur.ID] = true
		op.ID, op.Actual = cur.ID, cur
		if d.IsDefault != nil && !*d.IsDefault && cur.IsDefault && declaresDefault[d.SubjectType] == "" {
			return nil, fmt.Errorf("budget %q is the %s tier's default and is marked isDefault: false, but no other budget is marked isDefault: true; a tier always has a default", d.Name, d.SubjectType)
		}
		op.Write = !budgetSatisfied(*cur, d)
		op.Promote = wantsDefault && !cur.IsDefault
		if op.Promote {
			promotes[d.SubjectType] = true
		}
		staysDefault := wantsDefault || (cur.IsDefault && declaresDefault[d.SubjectType] == "")
		op.Warnings = limitedUserDefaultWarnings(d, staysDefault)
		if op.Write || op.Promote {
			plan.Updates = append(plan.Updates, op)
		} else {
			plan.Unchanged = append(plan.Unchanged, op)
		}
	}

	for i := range actual {
		a := &actual[i]
		if matched[a.ID] || a.SubjectType == store.BudgetSubjectMesh {
			continue
		}
		op := budgetOp{Name: a.Name, SubjectType: a.SubjectType, ID: a.ID, Actual: a}
		if a.IsDefault && !promotes[a.SubjectType] {
			op.KeptBecause = keptTierDefault
			op.Warnings = []string{fmt.Sprintf("not deleted: it is the %s tier's default; mark another budget isDefault: true to replace it", a.SubjectType)}
			plan.Kept = append(plan.Kept, op)
			continue
		}
		op.Warnings = revertingUsersWarnings(a.AssignedCount + a.HiddenAssignedCount + a.PendingCount)
		plan.Deletes = append(plan.Deletes, op)
	}
	byOpName := func(a, b budgetOp) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(plan.Deletes, byOpName)
	slices.SortFunc(plan.Kept, byOpName)
	return plan, nil
}

func matchBudget(d desiredBudget, mesh *wire.BudgetResponse, byName map[string]*wire.BudgetResponse) (*wire.BudgetResponse, error) {
	named := byName[store.BudgetNameLower(d.Name)]
	if d.SubjectType == store.BudgetSubjectMesh {
		if named != nil && (mesh == nil || named.ID != mesh.ID) {
			return nil, fmt.Errorf("budget %q: the mesh budget's name collides with the existing %s budget %q; rename or prune that budget in an earlier apply", d.Name, named.SubjectType, named.Name)
		}
		return mesh, nil
	}
	if named != nil && named.SubjectType == store.BudgetSubjectMesh {
		return nil, fmt.Errorf("budget %q: the mesh budget still has this name, and it is never deleted; declare it with subjectType: mesh, or rename the mesh budget in an earlier apply", d.Name)
	}
	if named != nil && named.SubjectType != d.SubjectType {
		return nil, fmt.Errorf("budget %q: subjectType is %s on the platform and %s here; a budget's tier is fixed when it is created, so delete it and create it again", d.Name, named.SubjectType, d.SubjectType)
	}
	return named, nil
}

func budgetSatisfied(cur wire.BudgetResponse, d desiredBudget) bool {
	return cur.Name == d.Name && equalPtr(cur.Amount, d.Amount) && equalPtr(cur.WarningPercentage, d.WarningPercentage)
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func applyBudgets(ctx context.Context, c *Client, plan *budgetPlan, opts ApplyOptions) *budgetOutcome {
	outcome := &budgetOutcome{Kind: "budget"}
	if plan == nil {
		return outcome
	}
	for _, op := range plan.Creates {
		created, err := c.CreateBudget(ctx, wire.CreateBudgetRequest{
			Name:              op.Desired.Name,
			SubjectType:       op.Desired.SubjectType,
			Amount:            patchOrNull(op.Desired.Amount),
			WarningPercentage: op.Desired.WarningPercentage,
		})
		verb := verbCreate
		if err == nil {
			op.ID = created.ID
			verb, err = promoteBudget(ctx, c, op, verb)
		}
		outcome.record(op, verb, err, &outcome.Created)
	}
	for _, op := range plan.Updates {
		var err error
		if op.Write {
			_, err = c.UpdateBudget(ctx, op.ID, wire.UpdateBudgetRequest{
				Name:              wire.Patch[string]{Set: true, Value: op.Desired.Name},
				Amount:            patchOrNull(op.Desired.Amount),
				WarningPercentage: patchOrNull(op.Desired.WarningPercentage),
			})
		}
		verb := verbUpdate
		if err == nil {
			verb, err = promoteBudget(ctx, c, op, verb)
		}
		outcome.record(op, verb, err, &outcome.Updated)
	}
	return outcome
}

func applyBudgetDeletes(ctx context.Context, c *Client, plan *budgetPlan, assignments *budgetAssignmentOutcome, opts ApplyOptions, outcome *budgetOutcome) {
	if plan == nil || outcome == nil {
		return
	}
	stranded := map[string]bool{}
	if assignments != nil {
		for _, res := range slices.Concat(assignments.Skipped, assignments.Failures) {
			if res.Verb == verbMove {
				stranded[res.Op.FromBudget] = true
			}
		}
	}
	for _, op := range plan.Deletes {
		switch {
		case !opts.Prune:
			outcome.Skipped = append(outcome.Skipped, budgetOpResult{Op: op, Verb: verbDelete})
		case stranded[op.Name]:
			outcome.Skipped = append(outcome.Skipped, budgetOpResult{Op: op, Verb: verbDelete, Reason: skipReasonAssigneesNotMoved})
		default:
			outcome.record(op, verbDelete, c.DeleteBudget(ctx, op.ID), &outcome.Deleted)
		}
	}
}

func revertingUsersWarnings(n int) []string {
	switch {
	case n <= 0:
		return nil
	case n == 1:
		return []string{"deleting it reverts its 1 assigned user to the default user budget"}
	}
	return []string{fmt.Sprintf("deleting it reverts its %d assigned users to the default user budget", n)}
}

func promoteBudget(ctx context.Context, c *Client, op budgetOp, writeVerb string) (string, error) {
	if !op.Promote {
		return writeVerb, nil
	}
	if _, err := c.MakeDefaultBudget(ctx, op.ID); err != nil {
		return verbSetDefault, fmt.Errorf("make it the %s tier's default: %w", op.SubjectType, err)
	}
	return writeVerb, nil
}

func (o *budgetOutcome) record(op budgetOp, verb string, err error, success *[]budgetOpResult) {
	res := budgetOpResult{Op: op, Verb: verb, Err: err}
	if err != nil {
		o.Failures = append(o.Failures, res)
		return
	}
	*success = append(*success, res)
}

func budgetVerb(op budgetOp, present string) string {
	switch {
	case op.Write && op.Promote:
		return present + " + " + verbSetDefault
	case op.Promote:
		return verbSetDefault
	default:
		return present
	}
}

func renderBudgetPlan(w io.Writer, p *budgetPlan, opts FormatOptions) {
	renderRBACGenericPlan(w, budgetsDir, len(p.Creates), len(p.Updates), len(p.Deletes), len(p.Unchanged)+len(p.Kept), func() {
		for _, op := range p.Creates {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(op.Name)), color(opts, ansiGreen, budgetVerb(op, "create")))
		}
		for _, op := range p.Updates {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiYellow, "~ "+padName(op.Name)), color(opts, ansiYellow, budgetVerb(op, "update")))
			if opts.Verbose {
				renderBudgetUpdateVerbose(w, op)
			}
		}
		for _, op := range p.Deletes {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(op.Name)), color(opts, ansiRed, planDeleteLabel(opts)))
		}
		for _, op := range p.Kept {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "= "+padName(op.Name)), color(opts, ansiDim, string(op.KeptBecause)))
		}
		for _, op := range p.Unchanged {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "= "+padName(op.Name)), color(opts, ansiDim, noChangeLabel))
		}
	})
}

func renderBudgetApply(w io.Writer, o *budgetOutcome, opts FormatOptions) {
	if o == nil || (len(o.Created)+len(o.Updated)+len(o.Deleted)+len(o.Skipped)+len(o.Failures)) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, budgetsDir)
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(r.Op.Name)), color(opts, ansiGreen, budgetVerb(r.Op, "created")))
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiYellow, "~ "+padName(r.Op.Name)), color(opts, ansiYellow, budgetVerb(r.Op, "updated")))
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.Name)), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		label := skipPruneLabel
		if r.Reason != "" {
			label = "skipped (" + r.Reason + ")"
		}
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(r.Op.Name)), color(opts, ansiDim, label))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(r.Op.Name)), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}

func planAllBudgets(ctx context.Context, c *Client, m *manifest.Manifest, r *resolve.Resolver, out *PlanResult) error {
	if _, declared := m.Resources["budgets"]; !declared {
		return nil
	}
	desired, err := r.Budgets(ctx)
	if err != nil {
		return err
	}
	actual, err := c.ListBudgets(ctx)
	if err != nil {
		return err
	}
	out.Budgets, err = planBudgets(desired, actual)
	return err
}

const limitedUserDefaultWarning = "this is the user tier's default and has an amount, so it also limits every service account without an assigned budget"

func limitedUserDefaultWarnings(d desiredBudget, isDefault bool) []string {
	if d.SubjectType != store.BudgetSubjectUser || !isDefault || d.Amount == nil {
		return nil
	}
	return []string{limitedUserDefaultWarning}
}
