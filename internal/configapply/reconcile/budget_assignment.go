// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var budgetAssignmentsResource = platformResource{path: "/budgetAssignments"}

const (
	budgetAssignmentKind = "budgetAssignment"
	budgetUsersKey       = "users"
	budgetUsersHeading   = "budgets (users)"

	maxBudgetAssignmentPages = 1000

	verbAssign = "assign"
	verbMove   = "move"
	verbRemove = "remove"
)

func (c *Client) ListBudgetAssignments(ctx context.Context) ([]wire.BudgetAssignmentResponse, error) {
	listed := budgetAssignmentsResource.apiPath() + "?expand=identity"
	as, err := listPagesUpTo[wire.BudgetAssignmentResponse](ctx, c, listed, maxBudgetAssignmentPages)
	var herr *HTTPError
	if errors.As(err, &herr) && herr.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("budget assignments: GET %s returned 404: %w", budgetAssignmentsResource.apiPath(), ErrBudgetsUnavailable)
	}
	if err != nil {
		return nil, err
	}
	for _, id := range rbac.UserListingHiddenIDs() {
		hidden, err := listPagesUpTo[wire.BudgetAssignmentResponse](ctx, c, listed+"&subjectType="+store.BudgetSubjectUser+"&subjectId="+url.QueryEscape(id), 1)
		if err != nil {
			return nil, err
		}
		as = append(as, hidden...)
	}
	return uniqueAssignments(as), nil
}

func IsHiddenBudgetSubject(a *wire.BudgetAssignmentResponse) bool {
	return a.Subject.ID != nil && slices.Contains(rbac.UserListingHiddenIDs(), *a.Subject.ID)
}

func uniqueAssignments(as []wire.BudgetAssignmentResponse) []wire.BudgetAssignmentResponse {
	seen := make(map[string]bool, len(as))
	return slices.DeleteFunc(as, func(a wire.BudgetAssignmentResponse) bool {
		repeat := seen[a.ID]
		seen[a.ID] = true
		return repeat
	})
}

func (c *Client) CreateBudgetAssignment(ctx context.Context, req wire.CreateBudgetAssignmentRequest) (wire.BudgetAssignmentResponse, error) {
	var resp wire.BudgetAssignmentResponse
	err := c.callResource(ctx, budgetAssignmentsResource, http.MethodPost, "", req, &resp)
	return resp, err
}

func (c *Client) UpdateBudgetAssignment(ctx context.Context, id string, req wire.UpdateBudgetAssignmentRequest) error {
	return c.callResource(ctx, budgetAssignmentsResource, http.MethodPatch, "/"+url.PathEscape(id), req, nil)
}

func (c *Client) DeleteBudgetAssignment(ctx context.Context, id string) error {
	return c.callResource(ctx, budgetAssignmentsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

type budgetIdentity struct {
	Type   string
	Value  string
	Issuer string
}

func (i budgetIdentity) key() string { return i.Type + "|" + i.Issuer + "|" + i.Value }

func (i budgetIdentity) String() string {
	switch i.Type {
	case "":
		return "(none)"
	case store.BudgetIdentitySystem:
		return rbac.SystemUserPrincipal(i.Value)
	case store.BudgetIdentitySub:
		return i.Value + " (" + i.Issuer + ")"
	}
	return i.Value
}

func (i budgetIdentity) request() *apiv2.BudgetAssignmentIdentityRequest {
	req := &apiv2.BudgetAssignmentIdentityRequest{Type: i.Type, Value: i.Value}
	if i.Issuer != "" {
		req.Issuer = &i.Issuer
	}
	return req
}

func reportedBudgetIdentity(id apiv2.BudgetAssignmentIdentity) budgetIdentity {
	var issuer string
	if id.Issuer != nil {
		issuer = *id.Issuer
	}
	return normalizedBudgetIdentity(id.Type, id.Value, issuer)
}

func normalizedBudgetIdentity(identityType, value, issuer string) budgetIdentity {
	value = entplatform.NormalizeGrantSubjectValue(identityType, value)
	if identityType == store.BudgetIdentitySystem {
		value = rbac.SystemUserName(value)
	}
	return budgetIdentity{Type: identityType, Value: value, Issuer: strings.TrimSpace(issuer)}
}

var budgetUserAllowedKeys = []string{"issuer", "type", "value"}

func readBudgetUsers(spec map[string]any) (users []budgetIdentity, managed bool, err error) {
	raw, present := spec[budgetUsersKey]
	if !present {
		return nil, false, nil
	}
	if raw == nil {
		return nil, true, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, false, fmt.Errorf("spec.users must be a list")
	}
	seen := map[string]int{}
	for i, item := range list {
		identity, err := readBudgetUser(item)
		if err != nil {
			return nil, false, fmt.Errorf("spec.users[%d]: %w", i, err)
		}
		if j, dup := seen[identity.key()]; dup {
			return nil, false, fmt.Errorf("spec.users[%d] repeats spec.users[%d] (%s)", i, j, identity)
		}
		seen[identity.key()] = i
		users = append(users, identity)
	}
	return users, true, nil
}

func readBudgetUser(item any) (budgetIdentity, error) {
	switch v := item.(type) {
	case string:
		s := strings.TrimSpace(v)
		switch {
		case s == "":
			return budgetIdentity{}, fmt.Errorf("is empty")
		case rbac.IsSystemPrincipal(s):
			return checkedBudgetIdentity(store.BudgetIdentitySystem, s, "")
		}
		return checkedBudgetIdentity(store.BudgetIdentityEmail, s, "")
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if !slices.Contains(budgetUserAllowedKeys, key) {
				return budgetIdentity{}, fmt.Errorf("unknown key %q (allowed: %s)", key, strings.Join(budgetUserAllowedKeys, ", "))
			}
		}
		fields := map[string]string{}
		for _, key := range slices.Sorted(maps.Keys(v)) {
			raw := v[key]
			s, ok := raw.(string)
			if !ok && raw != nil {
				return budgetIdentity{}, fmt.Errorf("%s must be a string, got %T; quote it", key, raw)
			}
			fields[key] = s
		}
		return checkedBudgetIdentity(fields["type"], fields["value"], fields["issuer"])
	}
	return budgetIdentity{}, fmt.Errorf("must be an email, system:<name>, or {type, value, issuer}, got %T", item)
}

func checkedBudgetIdentity(identityType, value, issuer string) (budgetIdentity, error) {
	identity := normalizedBudgetIdentity(identityType, value, issuer)
	switch {
	case identity.Type != store.BudgetIdentityEmail && identity.Type != store.BudgetIdentitySub && identity.Type != store.BudgetIdentitySystem:
		return identity, fmt.Errorf("type must be email, sub or system, got %q", identityType)
	case identity.Value == "":
		return identity, fmt.Errorf("value is required")
	case identity.Type == store.BudgetIdentitySub && identity.Issuer == "":
		return identity, fmt.Errorf("issuer is required for a sub")
	case identity.Type != store.BudgetIdentitySub && identity.Issuer != "":
		return identity, fmt.Errorf("issuer is only for a sub")
	case identity.Type != store.BudgetIdentitySystem && rbac.IsSystemPrincipal(identity.Value):
		return identity, fmt.Errorf("write a service account as %s, not as a %s", identity.Value, identity.Type)
	}
	return identity, nil
}

type budgetAssignmentOp struct {
	Source     string
	Budget     string
	BudgetID   string
	Identity   budgetIdentity
	ID         string
	FromBudget string
	Recorded   *budgetIdentity
}

func (op budgetAssignmentOp) name() string { return op.Identity.String() + " → " + op.Budget }

type budgetAssignmentPlan struct {
	Kind      string
	Creates   []budgetAssignmentOp
	Moves     []budgetAssignmentOp
	Rerecords []budgetAssignmentOp
	Removes   []budgetAssignmentOp
	Unchanged []budgetAssignmentOp
}

type BudgetAssignmentIndex struct {
	byKey     map[string]*wire.BudgetAssignmentResponse
	ambiguous map[string]bool
	stale     map[string]bool
	warnings  []string
}

type keyRank int

const (
	rankCurrent keyRank = iota
	rankRecorded
)

type rankedKey struct {
	key  string
	rank keyRank
	held bool
}

type keyCandidate struct {
	assignment *wire.BudgetAssignmentResponse
	rankedKey
}

func IndexBudgetAssignments(actual []wire.BudgetAssignmentResponse) BudgetAssignmentIndex {
	candidates := map[string][]keyCandidate{}
	for i := range actual {
		for _, k := range assignmentKeys(&actual[i]) {
			candidates[k.key] = append(candidates[k.key], keyCandidate{assignment: &actual[i], rankedKey: k})
		}
	}
	idx := BudgetAssignmentIndex{
		byKey:     map[string]*wire.BudgetAssignmentResponse{},
		ambiguous: map[string]bool{},
		stale:     map[string]bool{},
	}
	for key, cs := range candidates {
		concrete := slices.DeleteFunc(slices.Clone(cs), func(c keyCandidate) bool { return c.assignment.State == store.BudgetAssignmentPending })
		held := slices.ContainsFunc(concrete, func(c keyCandidate) bool { return c.held })
		contenders := cs
		if held {
			contenders = concrete
		}
		top := slices.MaxFunc(contenders, func(a, b keyCandidate) int { return cmp.Compare(a.rank, b.rank) }).rank
		winners := slices.DeleteFunc(slices.Clone(contenders), func(c keyCandidate) bool { return c.rank != top })
		if len(winners) > 1 {
			idx.ambiguous[key] = true
		} else {
			idx.byKey[key] = winners[0].assignment
		}
		if !held {
			continue
		}
		for _, c := range cs {
			if c.assignment.State == store.BudgetAssignmentPending && !idx.stale[c.assignment.ID] {
				idx.stale[c.assignment.ID] = true
				idx.warnings = append(idx.warnings, fmt.Sprintf(
					"budget assignment %s on %q is pending for someone who already has an assignment; it is ignored, and is removed at their next sign-in",
					c.assignment.ID, c.assignment.Budget.Name))
			}
		}
	}
	slices.Sort(idx.warnings)
	return idx
}

func (idx BudgetAssignmentIndex) Names(id apiv2.BudgetAssignmentIdentity, a *wire.BudgetAssignmentResponse) bool {
	found := idx.byKey[BudgetIdentityKey(id)]
	return found != nil && found.ID == a.ID
}

func (idx BudgetAssignmentIndex) IsStale(a *wire.BudgetAssignmentResponse) bool {
	return idx.stale[a.ID]
}

func isConcreteUser(a *wire.BudgetAssignmentResponse) bool {
	return a.State != store.BudgetAssignmentPending && a.Subject.User != nil && !IsServiceAccount(a.Subject.User)
}

func IsServiceAccount(u *apiv2.BudgetAssignmentUser) bool {
	return u.IsServiceAccount || u.IsSystemUser
}

func BudgetIdentityKey(id apiv2.BudgetAssignmentIdentity) string {
	return reportedBudgetIdentity(id).key()
}

func assignmentKeys(a *wire.BudgetAssignmentResponse) []rankedKey {
	var keys []rankedKey
	add := func(identity budgetIdentity, rank keyRank, held bool) {
		key := identity.key()
		if i := slices.IndexFunc(keys, func(k rankedKey) bool { return k.key == key }); i >= 0 {
			keys[i].held = keys[i].held || held
			return
		}
		keys = append(keys, rankedKey{key: key, rank: rank, held: held})
	}
	subject := a.Subject
	if id := subject.Identity; id != nil {
		add(reportedBudgetIdentity(*id), rankRecorded, false)
	}
	if isConcreteUser(a) {
		user := subject.User
		if user.Sub != nil && user.Issuer != nil {
			add(normalizedBudgetIdentity(store.BudgetIdentitySub, *user.Sub, *user.Issuer), rankCurrent, true)
		}
		if user.Email != nil && user.EmailShared != nil && !*user.EmailShared {
			add(normalizedBudgetIdentity(store.BudgetIdentityEmail, *user.Email, ""), rankCurrent, true)
		}
	}
	return keys
}

type declaredMatch struct {
	identity budgetIdentity
	budget   string
}

func planBudgetAssignments(budgets *budgetPlan, actual []wire.BudgetAssignmentResponse, systemUsers map[string]bool) (*budgetAssignmentPlan, []string, error) {
	idx := IndexBudgetAssignments(actual)
	budgetIDs := map[string]string{}
	for _, op := range slices.Concat(budgets.Updates, budgets.Unchanged) {
		budgetIDs[op.Name] = op.ID
	}

	plan := &budgetAssignmentPlan{Kind: budgetAssignmentKind}
	declaredOn := map[string]string{}
	matched := map[string]declaredMatch{}
	managed := map[string]bool{}
	for _, d := range budgets.Specs {
		if !d.UsersManaged {
			continue
		}
		budgetID := budgetIDs[d.Name]
		if budgetID != "" {
			managed[budgetID] = true
		}
		for _, identity := range d.Users {
			key := identity.key()
			if other, dup := declaredOn[key]; dup {
				return nil, nil, fmt.Errorf("budget %q (%s): %s is also a user of budget %q; a user has one budget", d.Name, d.Source, identity, other)
			}
			declaredOn[key] = d.Name
			if identity.Type == store.BudgetIdentitySystem && !systemUsers[identity.Value] {
				return nil, nil, fmt.Errorf("budget %q (%s): %w %q on the platform or in this repo", d.Name, d.Source, errUnknownSystemUser, identity.Value)
			}
			op := budgetAssignmentOp{Source: d.Source, Budget: d.Name, BudgetID: budgetID, Identity: identity}
			cur := idx.byKey[key]
			if cur == nil {
				if idx.ambiguous[key] {
					return nil, nil, fmt.Errorf("budget %q (%s): %s was used to assign more than one user; name the user by sub", d.Name, d.Source, identity)
				}
				plan.Creates = append(plan.Creates, op)
				continue
			}
			if prior, dup := matched[cur.ID]; dup {
				return nil, nil, sameUserError(&d, identity, prior, cur)
			}
			matched[cur.ID] = declaredMatch{identity: identity, budget: d.Name}
			op.ID = cur.ID
			op.Recorded = replacedIdentity(cur, identity)
			if cur.Budget.ID == budgetID {
				if op.Recorded != nil {
					plan.Rerecords = append(plan.Rerecords, op)
				} else {
					plan.Unchanged = append(plan.Unchanged, op)
				}
				continue
			}
			op.FromBudget = cur.Budget.Name
			plan.Moves = append(plan.Moves, op)
		}
	}

	for i := range actual {
		a := &actual[i]
		if _, isMatched := matched[a.ID]; !managed[a.Budget.ID] || isMatched || idx.stale[a.ID] || IsHiddenBudgetSubject(a) {
			continue
		}
		identity := budgetIdentity{Value: a.ID}
		if a.Subject.Identity != nil {
			identity = reportedBudgetIdentity(*a.Subject.Identity)
		}
		plan.Removes = append(plan.Removes, budgetAssignmentOp{Budget: a.Budget.Name, BudgetID: a.Budget.ID, Identity: identity, ID: a.ID})
	}
	byName := func(a, b budgetAssignmentOp) int { return strings.Compare(a.name(), b.name()) }
	slices.SortFunc(plan.Removes, byName)
	discountMovedAssignees(budgets, plan, actual)
	return plan, idx.warnings, nil
}

func sameUserError(d *desiredBudget, identity budgetIdentity, prior declaredMatch, cur *wire.BudgetAssignmentResponse) error {
	err := fmt.Errorf("budget %q (%s): %s names the same user as %s on budget %q; a user has one budget", d.Name, d.Source, identity, prior.identity, prior.budget)
	current, recorded := identity, prior.identity
	if replacedIdentity(cur, identity) == nil {
		current, recorded = prior.identity, identity
	}
	if replacedIdentity(cur, recorded) != nil || replacedIdentity(cur, current) == nil {
		return err
	}
	return fmt.Errorf("%w. Their assignment is recorded by %s; if that is now another user's, apply with %s alone first to re-record it, then add %s", err, recorded, current, recorded)
}

func replacedIdentity(a *wire.BudgetAssignmentResponse, declared budgetIdentity) *budgetIdentity {
	if !isConcreteUser(a) {
		return nil
	}
	var recorded budgetIdentity
	if id := a.Subject.Identity; id != nil {
		recorded = reportedBudgetIdentity(*id)
	}
	if recorded.key() == declared.key() {
		return nil
	}
	return &recorded
}

func discountMovedAssignees(budgets *budgetPlan, plan *budgetAssignmentPlan, actual []wire.BudgetAssignmentResponse) {
	leaving := map[string]int{}
	for _, op := range plan.Moves {
		for i := range actual {
			if actual[i].ID == op.ID {
				leaving[actual[i].Budget.ID]++
			}
		}
	}
	for i := range budgets.Deletes {
		op := &budgets.Deletes[i]
		if op.Actual != nil {
			op.Warnings = revertingUsersWarnings(op.Actual.AssignedCount + op.Actual.HiddenAssignedCount + op.Actual.PendingCount - leaving[op.ID])
		}
	}
}

func planAllBudgetAssignments(ctx context.Context, c *Client, out *PlanResult) error {
	if out.Budgets == nil || !slices.ContainsFunc(out.Budgets.Specs, func(d desiredBudget) bool { return d.UsersManaged }) {
		return nil
	}
	actual, err := c.ListBudgetAssignments(ctx)
	if err != nil {
		return err
	}
	systemUsers, platformListed, err := assignableSystemUsers(ctx, c, out)
	if err != nil {
		return err
	}
	plan, warnings, err := planBudgetAssignments(out.Budgets, actual, systemUsers)
	if !platformListed && errors.Is(err, errUnknownSystemUser) {
		return fmt.Errorf("%w; the platform's service accounts could not be read (GET %s and GET %s both returned 404), so only the built-ins and this repo's are known", err, serviceAccountsResource.apiPath(), legacyServiceAccountsResource.apiPath())
	}
	if err != nil {
		return err
	}
	out.BudgetAssignments = plan
	out.Warnings = append(out.Warnings, warnings...)
	return nil
}

func assignableSystemUsers(ctx context.Context, c *Client, out *PlanResult) (names map[string]bool, platformListed bool, err error) {
	names = map[string]bool{}
	for _, u := range rbac.ReservedSystemUsers() {
		names[rbac.SystemUserName(u.Sub)] = true
	}
	if out.SystemUsers != nil {
		for _, op := range out.SystemUsers.Creates {
			names[op.Name] = true
		}
	}
	listed, err := c.ListSystemUsers(ctx)
	var herr *HTTPError
	if errors.As(err, &herr) && herr.StatusCode == http.StatusNotFound {
		return names, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("list service accounts for budget users: %w", err)
	}
	for _, u := range listed {
		names[u.Name] = true
	}
	return names, true, nil
}

type budgetAssignmentOpResult struct {
	Op     budgetAssignmentOp
	Verb   string
	Reason string
	Err    error
}

type budgetAssignmentOutcome struct {
	Kind       string
	Created    []budgetAssignmentOpResult
	Moved      []budgetAssignmentOpResult
	Rerecorded []budgetAssignmentOpResult
	Removed    []budgetAssignmentOpResult
	Skipped    []budgetAssignmentOpResult
	Failures   []budgetAssignmentOpResult
}

const (
	skipReasonBudgetNotCreated = "budget_not_created"
)

func applyBudgetAssignments(ctx context.Context, c *Client, plan *PlanResult, budgets *budgetOutcome, opts ApplyOptions) *budgetAssignmentOutcome {
	p := plan.BudgetAssignments
	outcome := &budgetAssignmentOutcome{Kind: budgetAssignmentKind}
	if p == nil {
		return outcome
	}
	created := map[string]string{}
	if budgets != nil {
		for _, res := range slices.Concat(budgets.Created, budgets.Failures) {
			if res.Err == nil || res.Verb == verbSetDefault {
				created[res.Op.Name] = res.Op.ID
			}
		}
	}
	skippedSystemUsers := map[string]bool{}
	deletedSystemUsers := map[string]bool{}
	if plan.SystemUsers != nil {
		if plan.RBACWritesBlocked {
			for _, op := range plan.SystemUsers.Creates {
				skippedSystemUsers[op.Name] = true
			}
		}
		for _, op := range plan.SystemUsers.Deletes {
			deletedSystemUsers[op.Name] = true
		}
	}
	skip := func(op budgetAssignmentOp, verb, reason string) {
		outcome.Skipped = append(outcome.Skipped, budgetAssignmentOpResult{Op: op, Verb: verb, Reason: reason})
	}
	record := func(op budgetAssignmentOp, verb string, err error, success *[]budgetAssignmentOpResult) {
		res := budgetAssignmentOpResult{Op: op, Verb: verb, Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			return
		}
		*success = append(*success, res)
	}

	for _, op := range p.Creates {
		if op.BudgetID == "" {
			op.BudgetID = created[op.Budget]
		}
		switch {
		case op.BudgetID == "":
			skip(op, verbAssign, skipReasonBudgetNotCreated)
		case op.Identity.Type == store.BudgetIdentitySystem && skippedSystemUsers[op.Identity.Value]:
			skip(op, verbAssign, skipReasonMiniIDP)
		default:
			_, err := c.CreateBudgetAssignment(ctx, wire.CreateBudgetAssignmentRequest{
				SubjectType: store.BudgetSubjectUser, Identity: op.Identity.request(), BudgetID: op.BudgetID,
			})
			record(op, verbAssign, err, &outcome.Created)
		}
	}
	for _, op := range p.Moves {
		if op.BudgetID == "" {
			op.BudgetID = created[op.Budget]
		}
		if op.BudgetID == "" {
			skip(op, verbMove, skipReasonBudgetNotCreated)
			continue
		}
		record(op, verbMove, withReplanHint(updateBudgetAssignment(ctx, c, op, op.BudgetID)), &outcome.Moved)
	}
	for _, op := range p.Rerecords {
		record(op, verbUpdate, withReplanHint(updateBudgetAssignment(ctx, c, op, "")), &outcome.Rerecorded)
	}
	for _, op := range p.Removes {
		if !opts.Prune {
			skip(op, verbRemove, "")
			continue
		}
		err := c.DeleteBudgetAssignment(ctx, op.ID)
		if op.Identity.Type == store.BudgetIdentitySystem && deletedSystemUsers[op.Identity.Value] {
			err = ignoreNotFound(err)
		}
		record(op, verbRemove, withReplanHint(err), &outcome.Removed)
	}
	return outcome
}

func updateBudgetAssignment(ctx context.Context, c *Client, op budgetAssignmentOp, budgetID string) error {
	var req wire.UpdateBudgetAssignmentRequest
	if budgetID != "" {
		req.BudgetID = wire.Patch[string]{Set: true, Value: budgetID}
	}
	if op.Recorded != nil {
		req.Identity = wire.Patch[apiv2.BudgetAssignmentIdentityRequest]{Set: true, Value: *op.Identity.request()}
	}
	return c.UpdateBudgetAssignment(ctx, op.ID, req)
}

var errUnknownSystemUser = errors.New("no service account")

func withReplanHint(err error) error {
	var herr *HTTPError
	if errors.As(err, &herr) && herr.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w (the assignment changed since the plan, as when a pending one binds at sign-in; re-run the apply)", err)
	}
	return err
}

func ignoreNotFound(err error) error {
	var herr *HTTPError
	if errors.As(err, &herr) && herr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

func planRemoveLabel(opts FormatOptions) string {
	if opts.Prune {
		return verbRemove
	}
	return verbRemove + needsPruneSuffix
}

func renderBudgetAssignmentPlan(w io.Writer, p *budgetAssignmentPlan, opts FormatOptions) {
	renderRBACGenericPlan(w, budgetUsersHeading, len(p.Creates), len(p.Moves)+len(p.Rerecords), len(p.Removes), len(p.Unchanged), func() {
		for _, op := range p.Creates {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(op.name())), color(opts, ansiGreen, verbAssign))
		}
		for _, op := range p.Moves {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiYellow, "~ "+padName(op.name())), color(opts, ansiYellow, "move from "+op.FromBudget))
			renderReplacedIdentity(w, op)
		}
		for _, op := range p.Rerecords {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(op.name())), color(opts, ansiBlue, verbUpdate))
			renderReplacedIdentity(w, op)
		}
		for _, op := range p.Removes {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(op.name())), color(opts, ansiRed, planRemoveLabel(opts)))
		}
		for _, op := range p.Unchanged {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "= "+padName(op.name())), color(opts, ansiDim, noChangeLabel))
		}
	})
}

func renderReplacedIdentity(w io.Writer, op budgetAssignmentOp) {
	if op.Recorded != nil {
		_, _ = fmt.Fprintf(w, "      identity: %s → %s (same user)\n", op.Recorded, op.Identity)
	}
}

func renderBudgetAssignmentApply(w io.Writer, o *budgetAssignmentOutcome, opts FormatOptions) {
	if o == nil || len(o.Created)+len(o.Moved)+len(o.Rerecorded)+len(o.Removed)+len(o.Skipped)+len(o.Failures) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, budgetUsersHeading)
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(r.Op.name())), color(opts, ansiGreen, "assigned"))
	}
	for _, r := range o.Moved {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiYellow, "~ "+padName(r.Op.name())), color(opts, ansiYellow, "moved from "+r.Op.FromBudget))
	}
	for _, r := range o.Rerecorded {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(r.Op.name())), color(opts, ansiBlue, "updated"))
	}
	for _, r := range o.Removed {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.name())), color(opts, ansiRed, "removed"))
	}
	for _, r := range o.Skipped {
		label := skipPruneLabel
		if r.Reason != "" {
			label = "skipped (" + r.Reason + ")"
		}
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(r.Op.name())), color(opts, ansiDim, label))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(r.Op.name())), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}
