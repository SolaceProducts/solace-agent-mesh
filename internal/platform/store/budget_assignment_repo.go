// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var (
	ErrBudgetAssignmentNotFound        = errors.New("budget assignment: not found")
	ErrBudgetAssignmentExists          = errors.New("budget assignment: subject already has one")
	ErrBudgetAssignmentNoBudget        = errors.New("budget assignment: no budget with that id at the subject's tier")
	ErrBudgetAssignmentNoSubject       = errors.New("budget assignment: no user with that id")
	ErrBudgetAssignmentConflict        = errors.New("budget assignment: concurrent change; retry")
	ErrBudgetAssignmentPendingIdentity = errors.New("budget assignment: a pending assignment's identity cannot change")
)

const concreteAssignmentsTable = "budget_assignments"

type BudgetAssignmentMissingSubjectsError struct {
	Refs []string
}

func (e *BudgetAssignmentMissingSubjectsError) Error() string {
	return fmt.Sprintf("budget assignment: %d subjects have no users row", len(e.Refs))
}

func (e *BudgetAssignmentMissingSubjectsError) Unwrap() error { return ErrBudgetAssignmentNoSubject }

const (
	budgetAssignmentUsersJoin    = ` LEFT JOIN users u ON CAST(u.id AS TEXT) = a.subject_ref`
	concreteBudgetAssignmentRows = `SELECT a.id, '` + BudgetAssignmentConcrete + `' AS state,
	a.subject_type, a.subject_ref, a.budget_id,
	a.identity_type, a.identity_value, a.issuer AS identity_issuer,
	b.name AS budget_name,
	NULLIF(u.display_name, '') AS subject_display_name,
	NULLIF(u.email, '') AS subject_email,
	` + userEmailSharedSQL + ` AS subject_email_shared,
	COALESCE(u.sub, '') AS subject_sub,
	COALESCE(u.issuer, '') AS subject_issuer,
	CASE WHEN u.provisioned_via = '` + provisionedViaSystem + `' THEN 1 ELSE 0 END AS subject_is_system_user,
	a.created_by, a.updated_by, a.created_time, a.updated_time,
	LOWER(COALESCE(NULLIF(u.display_name, ''), NULLIF(u.email, ''), a.subject_ref)) AS sort_key
	FROM budget_assignments a
	JOIN budgets b ON b.id = a.budget_id` + budgetAssignmentUsersJoin
	pendingBudgetAssignmentRows = `SELECT p.id, '` + BudgetAssignmentPending + `' AS state,
	p.subject_type, '' AS subject_ref, p.budget_id,
	p.identity_type, p.identity_value, p.issuer AS identity_issuer,
	b.name AS budget_name,
	CAST(NULL AS TEXT) AS subject_display_name,
	CAST(NULL AS TEXT) AS subject_email,
	0 AS subject_email_shared,
	'' AS subject_sub,
	'' AS subject_issuer,
	0 AS subject_is_system_user,
	p.created_by, p.updated_by, p.created_time, p.updated_time,
	'' AS sort_key
	FROM budget_pending_assignments p
	JOIN budgets b ON b.id = p.budget_id`
	budgetAssignmentSelect = `SELECT * FROM (` + concreteBudgetAssignmentRows +
		` UNION ALL ` + pendingBudgetAssignmentRows + `) x`
	pendingLast              = `CASE WHEN x.state = '` + BudgetAssignmentPending + `' THEN 1 ELSE 0 END`
	pendingAssignmentColumns = `id, subject_type, identity_type, identity_value, issuer, budget_id,
	created_by, updated_by, created_time, updated_time`
)

type BudgetAssignmentsRepo struct {
	db *DB
}

func NewBudgetAssignmentsRepo(db *DB) *BudgetAssignmentsRepo {
	return &BudgetAssignmentsRepo{db: db}
}

func (r *BudgetAssignmentsRepo) Create(ctx context.Context, a *BudgetAssignment) ([]BudgetPendingAssignment, error) {
	wrap := func(err error) error { return fmt.Errorf("BudgetAssignmentsRepo.Create: %w", err) }
	budgetID, ok := canonicalBudgetID(a.BudgetID)
	if !ok {
		return nil, wrap(ErrBudgetAssignmentNoBudget)
	}
	a.BudgetID = budgetID
	if a.ID == "" {
		a.ID = idgen.New()
	}
	if a.CreatedTime == 0 {
		a.CreatedTime = time.Now().UnixMilli()
	}
	if a.UpdatedTime == 0 {
		a.UpdatedTime = a.CreatedTime
	}
	var superseded []BudgetPendingAssignment
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var pending []BudgetPendingAssignment
		if a.SubjectType == BudgetSubjectUser {
			found, err := lockUsers(ctx, tx, []string{a.SubjectRef})
			if err != nil {
				return wrap(err)
			}
			user, ok := found[a.SubjectRef]
			if !ok {
				return wrap(ErrBudgetAssignmentNoSubject)
			}
			if pending, err = lockPendingFor(ctx, tx, user); err != nil {
				return wrap(err)
			}
			if a.IdentityType == "" {
				if a.IdentityType, a.IdentityValue, a.IdentityIssuer, err = currentIdentity(ctx, tx, user); err != nil {
					return wrap(err)
				}
			}
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id,
			                                 identity_type, identity_value, issuer,
			                                 created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.SubjectType, a.SubjectRef, a.BudgetID, a.IdentityType, a.IdentityValue, a.IdentityIssuer,
			a.CreatedBy, a.UpdatedBy, a.CreatedTime, a.UpdatedTime,
		)
		if err != nil {
			return wrap(classifyBudgetAssignmentWriteError(err))
		}
		superseded, err = deletePending(ctx, tx, pending)
		if err != nil {
			return wrap(err)
		}
		return nil
	})
	if dbutil.IsDeadlock(err) {
		return nil, withSentinel(ErrBudgetAssignmentConflict, err)
	}
	if err != nil {
		return nil, err
	}
	return superseded, nil
}

func (r *BudgetAssignmentsRepo) CreatePending(ctx context.Context, p *BudgetPendingAssignment) error {
	wrap := func(err error) error { return fmt.Errorf("BudgetAssignmentsRepo.CreatePending: %w", err) }
	budgetID, ok := canonicalBudgetID(p.BudgetID)
	if !ok {
		return wrap(ErrBudgetAssignmentNoBudget)
	}
	p.BudgetID = budgetID
	if p.ID == "" {
		p.ID = idgen.New()
	}
	if p.CreatedTime == 0 {
		p.CreatedTime = time.Now().UnixMilli()
	}
	if p.UpdatedTime == 0 {
		p.UpdatedTime = p.CreatedTime
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO budget_pending_assignments (`+pendingAssignmentColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.SubjectType, p.IdentityType, p.IdentityValue, p.Issuer, p.BudgetID,
		p.CreatedBy, p.UpdatedBy, p.CreatedTime, p.UpdatedTime,
	)
	if err != nil {
		return wrap(classifyBudgetAssignmentWriteError(err))
	}
	return nil
}

func (r *BudgetAssignmentsRepo) BindPendingForUser(ctx context.Context, userID, actor string, nowMs int64) (BudgetBindResult, error) {
	var result BudgetBindResult
	userID = strings.ToLower(userID)
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result = BudgetBindResult{}
		users, err := lockUsers(ctx, tx, []string{userID})
		if err != nil {
			return err
		}
		user, ok := users[userID]
		if !ok {
			return nil
		}
		matches, err := lockPendingFor(ctx, tx, user)
		if err != nil || len(matches) == 0 {
			return err
		}
		existing, err := lockSubjectAssignments(ctx, tx, BudgetSubjectUser, []string{userID})
		if err != nil {
			return err
		}
		deleted, err := deletePending(ctx, tx, matches)
		if err != nil {
			return err
		}
		var bind *BudgetPendingAssignment
		if _, assigned := existing[userID]; !assigned {
			bind = preferSub(deleted)
		}
		for i := range deleted {
			if bind == nil || deleted[i].ID != bind.ID {
				result.Superseded = append(result.Superseded, deleted[i])
			}
		}
		if bind == nil {
			return nil
		}
		id := idgen.New()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id,
			                                 identity_type, identity_value, issuer,
			                                 created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (subject_type, subject_ref) DO NOTHING`,
			id, BudgetSubjectUser, userID, bind.BudgetID, bind.IdentityType, bind.IdentityValue, bind.Issuer,
			bind.CreatedBy, actor, bind.CreatedTime, nowMs,
		)
		if err != nil {
			return classifyBudgetAssignmentWriteError(err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			result.Superseded = append(result.Superseded, *bind)
			return err
		}
		result.Bound, result.AssignmentID = bind, id
		return nil
	})
	if err != nil {
		return BudgetBindResult{}, fmt.Errorf("BudgetAssignmentsRepo.BindPendingForUser(%s): %w", userID, err)
	}
	return result, nil
}

func preferSub(pending []BudgetPendingAssignment) *BudgetPendingAssignment {
	var chosen *BudgetPendingAssignment
	for i := range pending {
		if chosen == nil || pending[i].IdentityType == BudgetIdentitySub {
			chosen = &pending[i]
		}
	}
	return chosen
}

func lockPendingFor(ctx context.Context, tx *dbutil.Tx, u lockedUser) ([]BudgetPendingAssignment, error) {
	byUser, err := lockPendingForUsers(ctx, tx, map[string]lockedUser{u.ID: u})
	return byUser[u.ID], err
}

func deletePending(ctx context.Context, tx *dbutil.Tx, pending []BudgetPendingAssignment) ([]BudgetPendingAssignment, error) {
	var deleted []BudgetPendingAssignment
	for _, p := range pending {
		res, err := tx.ExecContext(ctx, `DELETE FROM budget_pending_assignments WHERE id = ?`, p.ID)
		if err != nil {
			return nil, fmt.Errorf("delete pending assignment: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, fmt.Errorf("delete pending assignment: rows affected: %w", err)
		} else if n == 1 {
			deleted = append(deleted, p)
		}
	}
	return deleted, nil
}

type lockedUser struct {
	ID             string  `db:"id"`
	Email          *string `db:"email"`
	Sub            string  `db:"sub"`
	Issuer         string  `db:"issuer"`
	ProvisionedVia string  `db:"provisioned_via"`
}

func lockUsers(ctx context.Context, tx *dbutil.Tx, ids []string) (map[string]lockedUser, error) {
	var uuids []string
	for _, id := range ids {
		if canonical, ok := canonicalBudgetID(id); ok && canonical == id {
			uuids = append(uuids, id)
		}
	}
	found := make(map[string]lockedUser, len(uuids))
	if len(uuids) == 0 {
		return found, nil
	}
	in, args, err := dbutil.InClause(uuids)
	if err != nil {
		return nil, fmt.Errorf("lock users: %w", err)
	}
	query := `SELECT id, email, sub, issuer, provisioned_via FROM users WHERE id IN (` + in + `) ORDER BY id`
	if tx.Dialect() == "postgres" {
		query += ` FOR KEY SHARE`
	}
	rows, err := dbutil.QueryRows[lockedUser](ctx, tx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lock users: %w", err)
	}
	for _, u := range rows {
		u.ID = strings.ToLower(u.ID)
		found[u.ID] = u
	}
	return found, nil
}

func currentIdentity(ctx context.Context, tx *dbutil.Tx, u lockedUser) (identityType, value, issuer string, err error) {
	if u.ProvisionedVia == provisionedViaSystem {
		return BudgetIdentitySystem, rbac.SystemUserName(u.Sub), "", nil
	}
	if u.Email != nil && *u.Email != "" {
		holders, err := dbutil.QueryVal[int](ctx, tx, `SELECT COUNT(*) FROM users WHERE email = ?`, *u.Email)
		if err != nil {
			return "", "", "", fmt.Errorf("count users sharing an email: %w", err)
		}
		if holders == 1 {
			return BudgetIdentityEmail, strings.ToLower(strings.TrimSpace(*u.Email)), "", nil
		}
	}
	if u.Sub == "" {
		return "", "", "", nil
	}
	return BudgetIdentitySub, u.Sub, u.Issuer, nil
}

func (r *BudgetAssignmentsRepo) GetByID(ctx context.Context, id string) (*BudgetAssignment, error) {
	canonical, ok := canonicalBudgetID(id)
	if !ok {
		return nil, nil
	}
	a, err := dbutil.QueryRow[BudgetAssignment](ctx, r.db, budgetAssignmentSelect+` WHERE x.id = ?`, canonical)
	if err != nil {
		return nil, fmt.Errorf("BudgetAssignmentsRepo.GetByID(%s): %w", canonical, err)
	}
	return a, nil
}

func (r *BudgetAssignmentsRepo) List(ctx context.Context, f BudgetAssignmentFilter) ([]*BudgetAssignment, error) {
	where, args, err := buildBudgetAssignmentFilter(f)
	if err != nil {
		return nil, fmt.Errorf("BudgetAssignmentsRepo.List: %w", err)
	}
	query := budgetAssignmentSelect + where + ` ORDER BY ` + pendingLast + `, x.sort_key, x.id`
	if f.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
	as, err := dbutil.QueryRowPtrs[BudgetAssignment](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("BudgetAssignmentsRepo.List: %w", err)
	}
	return as, nil
}

func (r *BudgetAssignmentsRepo) Count(ctx context.Context, f BudgetAssignmentFilter) (int64, error) {
	where, args, err := buildBudgetAssignmentFilter(f)
	if err != nil {
		return 0, fmt.Errorf("BudgetAssignmentsRepo.Count: %w", err)
	}
	n, err := dbutil.QueryVal[int64](ctx, r.db, `SELECT COUNT(*) FROM (`+budgetAssignmentSelect+where+`) counted`, args...)
	if err != nil {
		return 0, fmt.Errorf("BudgetAssignmentsRepo.Count: %w", err)
	}
	return n, nil
}

func buildBudgetAssignmentFilter(f BudgetAssignmentFilter) (string, []any, error) {
	var clauses []string
	var args []any
	if f.BudgetID != "" {
		canonical, ok := canonicalBudgetID(f.BudgetID)
		if !ok {
			return " WHERE 1 = 0", nil, nil
		}
		clauses = append(clauses, "x.budget_id = ?")
		args = append(args, canonical)
	}
	if f.SubjectType != "" {
		clauses = append(clauses, "x.subject_type = ?")
		args = append(args, f.SubjectType)
	}
	if f.SubjectRef != "" {
		clauses = append(clauses, "x.subject_ref = ?")
		args = append(args, f.SubjectRef)
	}
	if f.State != "" {
		clauses = append(clauses, "x.state = ?")
		args = append(args, f.State)
	}
	if len(f.HiddenSubjectRefs) > 0 {
		in, hidden, err := dbutil.InClause(f.HiddenSubjectRefs)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "x.subject_ref NOT IN ("+in+")")
		args = append(args, hidden...)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		namePred, like := dbutil.LikeContainsFold("x.subject_display_name", s)
		emailPred, _ := dbutil.LikeContainsFold("x.subject_email", s)
		preds := []string{namePred, emailPred}
		if f.SearchIdentity {
			identityPred, _ := dbutil.LikeContainsFold("x.identity_value", s)
			preds = append(preds, identityPred)
		}
		clauses = append(clauses, "("+strings.Join(preds, " OR ")+")")
		for range preds {
			args = append(args, like)
		}
	}
	if len(clauses) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

func (r *BudgetAssignmentsRepo) Update(ctx context.Context, id string, u BudgetAssignmentUpdate) (BudgetAssignmentUpdated, error) {
	canonical, ok := canonicalBudgetID(id)
	if !ok {
		return BudgetAssignmentUpdated{}, ErrBudgetAssignmentNotFound
	}
	wrap := func(err error) error { return fmt.Errorf("BudgetAssignmentsRepo.Update(%s): %w", canonical, err) }
	canonicalBudget := ""
	if u.BudgetID != "" {
		if canonicalBudget, ok = canonicalBudgetID(u.BudgetID); !ok {
			return BudgetAssignmentUpdated{}, wrap(ErrBudgetAssignmentNoBudget)
		}
	}
	var res BudgetAssignmentUpdated
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		res = BudgetAssignmentUpdated{}
		cur, table, err := lockAssignment(ctx, tx, canonical)
		if err != nil {
			return wrap(err)
		}
		if cur == "" {
			return ErrBudgetAssignmentNotFound
		}
		res.PreviousBudgetID = cur
		var sets []string
		var args []any
		if canonicalBudget != "" && canonicalBudget != cur {
			sets, args = append(sets, "budget_id = ?"), append(args, canonicalBudget)
			res.Moved = true
		}
		if u.Identity != nil {
			if table != concreteAssignmentsTable {
				return ErrBudgetAssignmentPendingIdentity
			}
			prev, err := dbutil.QueryRow[BudgetIdentity](ctx, tx,
				`SELECT identity_type, identity_value, issuer FROM budget_assignments WHERE id = ?`, canonical)
			if err != nil {
				return wrap(err)
			}
			res.PreviousIdentity = *prev
			if *prev != *u.Identity {
				sets = append(sets, "identity_type = ?", "identity_value = ?", "issuer = ?")
				args = append(args, u.Identity.Type, u.Identity.Value, u.Identity.Issuer)
				res.Rerecorded = true
			}
		}
		if len(sets) == 0 {
			return nil
		}
		sets = append(sets, "updated_by = ?", "updated_time = ?")
		args = append(args, u.Actor, u.NowMs, canonical)
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return wrap(classifyBudgetAssignmentWriteError(err))
		}
		return nil
	})
	if err != nil {
		return BudgetAssignmentUpdated{}, err
	}
	return res, nil
}

func (r *BudgetAssignmentsRepo) Delete(ctx context.Context, id string) (budgetID string, retErr error) {
	canonical, ok := canonicalBudgetID(id)
	if !ok {
		return "", ErrBudgetAssignmentNotFound
	}
	retErr = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		cur, table, err := lockAssignment(ctx, tx, canonical)
		if err != nil {
			return fmt.Errorf("BudgetAssignmentsRepo.Delete(%s): %w", canonical, err)
		}
		if cur == "" {
			return ErrBudgetAssignmentNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, canonical); err != nil {
			return fmt.Errorf("BudgetAssignmentsRepo.Delete(%s): %w", canonical, err)
		}
		budgetID = cur
		return nil
	})
	if retErr != nil {
		return "", retErr
	}
	return budgetID, nil
}

func (r *BudgetAssignmentsRepo) ApplyBudgetChanges(ctx context.Context, c BudgetAssignmentChanges) ([]BudgetAssignmentChange, error) {
	budgetID, ok := canonicalBudgetID(c.BudgetID)
	if !ok {
		return nil, fmt.Errorf("BudgetAssignmentsRepo.ApplyBudgetChanges: %w", ErrBudgetAssignmentNoBudget)
	}
	out := make([]BudgetAssignmentChange, 0, len(c.Add)+len(c.Remove))
	if len(c.Add)+len(c.Remove) == 0 {
		return out, nil
	}
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		out = out[:0]
		var users map[string]lockedUser
		var pending map[string][]BudgetPendingAssignment
		if c.SubjectType == BudgetSubjectUser && len(c.Add) > 0 {
			var err error
			if users, err = lockUsers(ctx, tx, c.Add); err != nil {
				return err
			}
			var missing []string
			for _, ref := range c.Add {
				if _, ok := users[ref]; !ok {
					missing = append(missing, ref)
				}
			}
			if len(missing) > 0 {
				return &BudgetAssignmentMissingSubjectsError{Refs: missing}
			}
			if pending, err = lockPendingForUsers(ctx, tx, users); err != nil {
				return err
			}
		}
		existing, err := lockSubjectAssignments(ctx, tx, c.SubjectType, append(append([]string{}, c.Add...), c.Remove...))
		if err != nil {
			return err
		}
		for _, ref := range c.Add {
			change, err := r.applyAdd(ctx, tx, c, budgetID, ref, users[ref], existing[ref])
			if err != nil {
				return err
			}
			if change.Superseded, err = deletePending(ctx, tx, pending[ref]); err != nil {
				return err
			}
			out = append(out, change)
		}
		for _, ref := range c.Remove {
			cur, ok := existing[ref]
			if !ok || cur.BudgetID != budgetID {
				out = append(out, BudgetAssignmentChange{
					Outcome: BudgetAssignmentUnchanged, SubjectRef: ref, AssignmentID: cur.ID, Reason: BudgetAssignmentNotAssigned,
				})
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM budget_assignments WHERE id = ?`, cur.ID); err != nil {
				return err
			}
			out = append(out, BudgetAssignmentChange{Outcome: BudgetAssignmentRemoved, SubjectRef: ref, AssignmentID: cur.ID})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("BudgetAssignmentsRepo.ApplyBudgetChanges(%s): %w", budgetID, classifyBudgetChangesError(err))
	}
	return out, nil
}

func lockPendingForUsers(ctx context.Context, tx *dbutil.Tx, users map[string]lockedUser) (map[string][]BudgetPendingAssignment, error) {
	out := make(map[string][]BudgetPendingAssignment, len(users))
	if len(users) == 0 {
		return out, nil
	}
	ids := slices.Sorted(maps.Keys(users))
	names := make([]string, 0, 2*len(ids))
	args := []any{BudgetSubjectUser}
	for _, id := range ids {
		u := users[id]
		names = append(names, `(identity_type = ? AND identity_value = ? AND issuer = '')`, `(identity_type = ? AND identity_value = ? AND issuer = ?)`)
		args = append(args, BudgetIdentityEmail, pendingEmailOf(u), BudgetIdentitySub, u.Sub, u.Issuer)
	}
	query := `SELECT ` + pendingAssignmentColumns + ` FROM budget_pending_assignments
		WHERE subject_type = ? AND (` + strings.Join(names, " OR ") + `)
		ORDER BY id`
	if tx.Dialect() == "postgres" {
		query += ` FOR UPDATE`
	}
	rows, err := dbutil.QueryRows[BudgetPendingAssignment](ctx, tx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lock pending assignments: %w", err)
	}
	for i := range rows {
		rows[i].ID, rows[i].BudgetID = strings.ToLower(rows[i].ID), strings.ToLower(rows[i].BudgetID)
	}
	for _, id := range ids {
		for _, p := range rows {
			if pendingNames(p, users[id]) {
				out[id] = append(out[id], p)
			}
		}
	}
	return out, nil
}

func pendingEmailOf(u lockedUser) string {
	if u.Email == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*u.Email))
}

func pendingNames(p BudgetPendingAssignment, u lockedUser) bool {
	switch p.IdentityType {
	case BudgetIdentityEmail:
		return p.Issuer == "" && p.IdentityValue == pendingEmailOf(u)
	case BudgetIdentitySub:
		return p.IdentityValue == u.Sub && p.Issuer == u.Issuer
	}
	return false
}

func (r *BudgetAssignmentsRepo) applyAdd(ctx context.Context, tx *dbutil.Tx, c BudgetAssignmentChanges, budgetID, ref string, user lockedUser, cur lockedAssignment) (BudgetAssignmentChange, error) {
	switch {
	case cur.ID == "":
		var identityType, identityValue, issuer string
		if c.SubjectType == BudgetSubjectUser {
			var err error
			if identityType, identityValue, issuer, err = currentIdentity(ctx, tx, user); err != nil {
				return BudgetAssignmentChange{}, err
			}
		}
		id := idgen.New()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id,
			                                 identity_type, identity_value, issuer,
			                                 created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, c.SubjectType, ref, budgetID, identityType, identityValue, issuer, c.Actor, c.Actor, c.NowMs, c.NowMs,
		); err != nil {
			return BudgetAssignmentChange{}, err
		}
		return BudgetAssignmentChange{Outcome: BudgetAssignmentAdded, SubjectRef: ref, AssignmentID: id}, nil
	case cur.BudgetID == budgetID:
		return BudgetAssignmentChange{
			Outcome: BudgetAssignmentUnchanged, SubjectRef: ref, AssignmentID: cur.ID, Reason: BudgetAssignmentAlreadyAssigned,
		}, nil
	default:
		if _, err := tx.ExecContext(ctx,
			`UPDATE budget_assignments SET budget_id = ?, updated_by = ?, updated_time = ? WHERE id = ?`,
			budgetID, c.Actor, c.NowMs, cur.ID,
		); err != nil {
			return BudgetAssignmentChange{}, err
		}
		return BudgetAssignmentChange{
			Outcome: BudgetAssignmentMoved, SubjectRef: ref, AssignmentID: cur.ID,
			FromBudgetID: cur.BudgetID, FromBudgetName: cur.BudgetName,
		}, nil
	}
}

type lockedAssignment struct {
	ID         string `db:"id"`
	SubjectRef string `db:"subject_ref"`
	BudgetID   string `db:"budget_id"`
	BudgetName string `db:"budget_name"`
}

func lockSubjectAssignments(ctx context.Context, tx *dbutil.Tx, subjectType string, refs []string) (map[string]lockedAssignment, error) {
	in, refArgs, err := dbutil.InClause(refs)
	if err != nil {
		return nil, fmt.Errorf("lock subject assignments: %w", err)
	}
	args := append([]any{subjectType}, refArgs...)
	query := `SELECT id, subject_ref, budget_id FROM budget_assignments
		WHERE subject_type = ? AND subject_ref IN (` + in + `)
		ORDER BY subject_ref`
	if tx.Dialect() == "postgres" {
		query += ` FOR UPDATE`
	}
	rows, err := dbutil.QueryRowPtrs[lockedAssignment](ctx, tx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lock subject assignments: %w", err)
	}
	out := make(map[string]lockedAssignment, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	budgetIDs := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		row.ID, row.BudgetID = strings.ToLower(row.ID), strings.ToLower(row.BudgetID)
		if !seen[row.BudgetID] {
			seen[row.BudgetID] = true
			budgetIDs = append(budgetIDs, row.BudgetID)
		}
	}
	in, budgetArgs, err := dbutil.InClause(budgetIDs)
	if err != nil {
		return nil, fmt.Errorf("read assigned budgets' names: %w", err)
	}
	names, err := dbutil.QueryRowPtrs[lockedAssignment](ctx, tx,
		`SELECT id AS budget_id, name AS budget_name FROM budgets WHERE id IN (`+in+`)`, budgetArgs...)
	if err != nil {
		return nil, fmt.Errorf("read assigned budgets' names: %w", err)
	}
	nameOf := make(map[string]string, len(names))
	for _, n := range names {
		nameOf[strings.ToLower(n.BudgetID)] = n.BudgetName
	}
	for _, row := range rows {
		row.BudgetName = nameOf[row.BudgetID]
		out[row.SubjectRef] = *row
	}
	return out, nil
}

func classifyBudgetChangesError(err error) error {
	var missing *BudgetAssignmentMissingSubjectsError
	switch {
	case errors.As(err, &missing):
		return err
	case dbutil.IsForeignKeyViolation(err):
		return withSentinel(ErrBudgetAssignmentNoBudget, err)
	case isSubjectUniqueViolation(err) || dbutil.IsDeadlock(err):
		return withSentinel(ErrBudgetAssignmentConflict, err)
	}
	return err
}

func lockAssignment(ctx context.Context, tx *dbutil.Tx, id string) (budgetID, table string, err error) {
	for _, table := range []string{concreteAssignmentsTable, "budget_pending_assignments"} {
		query := `SELECT budget_id FROM ` + table + ` WHERE id = ?`
		if tx.Dialect() == "postgres" {
			query += ` FOR UPDATE`
		}
		ids, err := dbutil.QueryVals[string](ctx, tx, query, id)
		if err != nil {
			return "", "", err
		}
		if len(ids) > 0 {
			return strings.ToLower(ids[0]), table, nil
		}
	}
	return "", "", nil
}

func classifyBudgetAssignmentWriteError(err error) error {
	switch {
	case dbutil.IsForeignKeyViolation(err):
		return withSentinel(ErrBudgetAssignmentNoBudget, err)
	case isSubjectUniqueViolation(err), isPendingIdentityUniqueViolation(err):
		return withSentinel(ErrBudgetAssignmentExists, err)
	}
	return err
}

func isSubjectUniqueViolation(err error) bool {
	return violatedBudgetConstraint(err) == uqBudgetAssignmentsSubject
}

func isPendingIdentityUniqueViolation(err error) bool {
	return violatedBudgetConstraint(err) == uqBudgetPendingIdentity
}

func (r *BudgetAssignmentsRepo) UserBudgetIDs(ctx context.Context) (map[string]string, error) {
	type row struct {
		SubjectRef string `db:"subject_ref"`
		BudgetID   string `db:"budget_id"`
	}
	rows, err := dbutil.QueryRows[row](ctx, r.db,
		`SELECT subject_ref, budget_id FROM budget_assignments WHERE subject_type = ?`, BudgetSubjectUser)
	if err != nil {
		return nil, fmt.Errorf("BudgetAssignmentsRepo.UserBudgetIDs: %w", err)
	}
	byUser := make(map[string]string, len(rows))
	for _, a := range rows {
		byUser[a.SubjectRef] = strings.ToLower(a.BudgetID)
	}
	return byUser, nil
}
