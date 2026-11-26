// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var (
	ErrBudgetNotFound              = errors.New("budget: not found")
	ErrBudgetInvalidID             = errors.New("budget: id is not a UUID")
	ErrBudgetUnknownTier           = errors.New("budget: unknown subject type")
	ErrBudgetNameTaken             = errors.New("budget: name already taken")
	ErrBudgetDefaultTaken          = errors.New("budget: tier already has a default")
	ErrBudgetDefaultConflict       = errors.New("budget: concurrent default change; retry")
	ErrBudgetMeshExists            = errors.New("budget: a mesh budget already exists")
	ErrBudgetIsDefault             = errors.New("budget: is its tier's default")
	ErrBudgetAssignedWhileDeleting = errors.New("budget: assigned while being deleted; retry")
	ErrBudgetStale                 = errors.New("budget: changed since it was read")
)

type BudgetAssignmentsDeleted struct {
	Assignments []BudgetAssignment
	Pending     []BudgetPendingAssignment
}

const budgetColumns = `id, name, name_lower, subject_type, amount, warn_percentage,
	is_default, created_by, updated_by, created_time, updated_time`

func (r *BudgetsRepo) budgetSelect() (string, []any, error) {
	assigned := `SELECT COUNT(*) FROM budget_assignments a WHERE a.budget_id = budgets.id`
	hiddenCount := `0`
	var args []any
	if len(r.hiddenSubjectRefs) > 0 {
		in, hidden, err := dbutil.InClause(r.hiddenSubjectRefs)
		if err != nil {
			return "", nil, err
		}
		hiddenCount = `(` + assigned + ` AND a.subject_ref IN (` + in + `))`
		assigned += ` AND a.subject_ref NOT IN (` + in + `)`
		args = append(append(args, hidden...), hidden...)
	}
	return `SELECT ` + budgetColumns + `,
	(` + assigned + `) AS assigned_count,
	` + hiddenCount + ` AS hidden_assigned_count,
	(SELECT COUNT(*) FROM budget_pending_assignments p WHERE p.budget_id = budgets.id) AS pending_count
	FROM budgets`, args, nil
}

type BudgetsRepo struct {
	db                *DB
	hiddenSubjectRefs []string
}

func NewBudgetsRepo(db *DB, hiddenSubjectRefs ...string) *BudgetsRepo {
	return &BudgetsRepo{db: db, hiddenSubjectRefs: hiddenSubjectRefs}
}

func canonicalBudgetID(id string) (string, bool) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func knownBudgetTier(t string) bool {
	return t == BudgetSubjectMesh || t == BudgetSubjectUser
}

func (r *BudgetsRepo) Create(ctx context.Context, b *Budget) error {
	if b.ID == "" {
		b.ID = idgen.New()
	} else if canonical, ok := canonicalBudgetID(b.ID); ok {
		b.ID = canonical
	} else {
		return fmt.Errorf("BudgetsRepo.Create(%q): %w", b.ID, ErrBudgetInvalidID)
	}
	if !knownBudgetTier(b.SubjectType) {
		return fmt.Errorf("BudgetsRepo.Create(%q): %w", b.SubjectType, ErrBudgetUnknownTier)
	}
	if b.SubjectType == BudgetSubjectMesh {
		b.IsDefault = true
	}
	b.Name = strings.TrimSpace(b.Name)
	b.NameLower = BudgetNameLower(b.Name)
	if b.CreatedTime == 0 {
		b.CreatedTime = time.Now().UnixMilli()
	}
	if b.UpdatedTime == 0 {
		b.UpdatedTime = b.CreatedTime
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO budgets (`+budgetColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.Name, b.NameLower, b.SubjectType, b.Amount, b.WarnPercentage,
		dbutil.BoolToInt(b.IsDefault), b.CreatedBy, b.UpdatedBy, b.CreatedTime, b.UpdatedTime,
	)
	if err != nil {
		classified := classifyBudgetWriteError(err)
		if b.SubjectType == BudgetSubjectMesh && errors.Is(classified, ErrBudgetDefaultTaken) {
			classified = withSentinel(ErrBudgetMeshExists, err)
		}
		return fmt.Errorf("BudgetsRepo.Create: %w", classified)
	}
	return nil
}

func (r *BudgetsRepo) GetByID(ctx context.Context, id string) (*Budget, error) {
	canonical, ok := canonicalBudgetID(id)
	if !ok {
		return nil, nil
	}
	return r.getByID(ctx, r.db, canonical)
}

func (r *BudgetsRepo) UserMatch(ctx context.Context, id string) (*UserBudgetMatch, error) {
	b, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("resolve budgetId: %w", err)
	}
	if b == nil || b.SubjectType != BudgetSubjectUser {
		return nil, nil
	}
	return &UserBudgetMatch{BudgetID: b.ID, IsDefault: b.IsDefault}, nil
}

func (r *BudgetsRepo) getByID(ctx context.Context, q dbutil.Querier, canonicalID string) (*Budget, error) {
	query, args, err := r.budgetSelect()
	if err != nil {
		return nil, fmt.Errorf("BudgetsRepo.GetByID(%s): %w", canonicalID, err)
	}
	b, err := dbutil.QueryRow[Budget](ctx, q, query+` WHERE id = ?`, append(args, canonicalID)...)
	if err != nil {
		return nil, fmt.Errorf("BudgetsRepo.GetByID(%s): %w", canonicalID, err)
	}
	return b, nil
}

func (r *BudgetsRepo) GetByName(ctx context.Context, name string) (*Budget, error) {
	query, args, err := r.budgetSelect()
	if err != nil {
		return nil, fmt.Errorf("BudgetsRepo.GetByName: %w", err)
	}
	b, err := dbutil.QueryRow[Budget](ctx, r.db, query+` WHERE name_lower = ?`, append(args, BudgetNameLower(name))...)
	if err != nil {
		return nil, fmt.Errorf("BudgetsRepo.GetByName: %w", err)
	}
	return b, nil
}

func (r *BudgetsRepo) MeshBudget(ctx context.Context) (LimitingBudget, error) {
	budgets, err := dbutil.QueryRows[LimitingBudget](ctx, r.db,
		`SELECT id, amount, warn_percentage FROM budgets WHERE subject_type = ?`, BudgetSubjectMesh)
	if err != nil {
		return LimitingBudget{}, fmt.Errorf("BudgetsRepo.MeshBudget: %w", err)
	}
	if len(budgets) != 1 {
		return LimitingBudget{}, fmt.Errorf("BudgetsRepo.MeshBudget: %d mesh budgets, want 1: %w", len(budgets), ErrBudgetNotFound)
	}
	return budgets[0], nil
}

func (r *BudgetsRepo) AnyLimited(ctx context.Context) (bool, error) {
	n, err := dbutil.QueryVal[int64](ctx, r.db, `SELECT COUNT(*) FROM budgets WHERE amount IS NOT NULL`)
	if err != nil {
		return false, fmt.Errorf("BudgetsRepo.AnyLimited: %w", err)
	}
	return n > 0, nil
}

func (r *BudgetsRepo) List(ctx context.Context, f BudgetFilter) ([]*Budget, error) {
	query, args, err := r.budgetSelect()
	if err != nil {
		return nil, fmt.Errorf("BudgetsRepo.List: %w", err)
	}
	where, whereArgs := buildBudgetFilter(f)
	query += where + budgetOrder(f)
	args = append(args, whereArgs...)
	if f.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
	budgets, err := dbutil.QueryRowPtrs[Budget](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("BudgetsRepo.List: %w", err)
	}
	return budgets, nil
}

func budgetOrder(f BudgetFilter) string {
	dir := " ASC"
	if f.Descending {
		dir = " DESC"
	}
	if f.SortBy == BudgetSortAmount {
		return ` ORDER BY CASE WHEN amount IS NULL THEN 1 ELSE 0 END` + dir + `, amount` + dir + `, name_lower, id`
	}
	return ` ORDER BY name_lower` + dir + `, id` + dir
}

func (r *BudgetsRepo) Count(ctx context.Context, f BudgetFilter) (int64, error) {
	where, args := buildBudgetFilter(f)
	n, err := dbutil.QueryVal[int64](ctx, r.db, `SELECT COUNT(*) FROM budgets`+where, args...)
	if err != nil {
		return 0, fmt.Errorf("BudgetsRepo.Count: %w", err)
	}
	return n, nil
}

func buildBudgetFilter(f BudgetFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.SubjectType != "" {
		clauses = append(clauses, "subject_type = ?")
		args = append(args, f.SubjectType)
	}
	if f.DefaultOnly {
		clauses = append(clauses, "is_default = 1")
	}
	if f.Name != "" {
		pred, arg := dbutil.LikeContains("name_lower", BudgetNameLower(f.Name))
		clauses = append(clauses, pred)
		args = append(args, arg)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (r *BudgetsRepo) Update(ctx context.Context, u BudgetUpdate) error {
	canonical, ok := canonicalBudgetID(u.ID)
	if !ok {
		return ErrBudgetNotFound
	}
	name := strings.TrimSpace(u.Name)
	result, err := r.db.ExecContext(ctx,
		`UPDATE budgets SET name = ?, name_lower = ?, amount = ?, warn_percentage = ?,
		        updated_by = ?, updated_time = ?
		  WHERE id = ? AND updated_time = ?`,
		name, BudgetNameLower(name), u.Amount, u.WarnPercentage, u.UpdatedBy, u.UpdatedTime, canonical, u.ExpectedUpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("BudgetsRepo.Update(%s): %w", canonical, classifyBudgetWriteError(err))
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("BudgetsRepo.Update(%s): rows affected: %w", canonical, err)
	}
	if n > 0 {
		return nil
	}
	b, err := r.getByID(ctx, r.db, canonical)
	switch {
	case err != nil:
		return err
	case b == nil:
		return ErrBudgetNotFound
	}
	return ErrBudgetStale
}

func (r *BudgetsRepo) SetDefault(ctx context.Context, id, actor string, nowMs int64) (string, bool, error) {
	canonical, ok := canonicalBudgetID(id)
	if !ok {
		return "", false, ErrBudgetNotFound
	}
	var demoted string
	var promoted bool
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		b, err := r.getByID(ctx, tx, canonical)
		if err != nil {
			return err
		}
		switch {
		case b == nil:
			return ErrBudgetNotFound
		case b.IsDefault:
			return nil
		}
		err = tx.QueryRowContext(ctx,
			`UPDATE budgets SET is_default = 0, updated_by = ?, updated_time = ?
			  WHERE subject_type = ? AND is_default = 1 AND id <> ?
			  RETURNING id`,
			actor, nowMs, b.SubjectType, canonical,
		).Scan(&demoted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("BudgetsRepo.SetDefault(%s): demote: %w", canonical, err)
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE budgets SET is_default = 1, updated_by = ?, updated_time = ? WHERE id = ? AND is_default = 0`,
			actor, nowMs, canonical,
		)
		if err != nil {
			if errors.Is(classifyBudgetWriteError(err), ErrBudgetDefaultTaken) {
				return withSentinel(ErrBudgetDefaultConflict, err)
			}
			return fmt.Errorf("BudgetsRepo.SetDefault(%s): promote: %w", canonical, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("BudgetsRepo.SetDefault(%s): rows affected: %w", canonical, err)
		}
		if n > 0 {
			promoted = true
			return nil
		}
		current, err := r.getByID(ctx, tx, canonical)
		if err != nil {
			return err
		}
		if current == nil {
			return ErrBudgetNotFound
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return demoted, promoted, nil
}

func (r *BudgetsRepo) Delete(ctx context.Context, id string) (BudgetAssignmentsDeleted, error) {
	canonical, ok := canonicalBudgetID(id)
	if !ok {
		return BudgetAssignmentsDeleted{}, ErrBudgetNotFound
	}
	var deleted BudgetAssignmentsDeleted
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		lock := `SELECT is_default FROM budgets WHERE id = ?`
		if tx.Dialect() == "postgres" {
			lock += ` FOR UPDATE`
		}
		var isDefault bool
		if err := tx.QueryRowContext(ctx, lock, canonical).Scan(&isDefault); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrBudgetNotFound
			}
			return fmt.Errorf("BudgetsRepo.Delete(%s): lock: %w", canonical, err)
		}
		if isDefault {
			return ErrBudgetIsDefault
		}
		var err error
		if deleted.Assignments, err = deleteBudgetAssignments(ctx, tx, canonical); err != nil {
			return err
		}
		if deleted.Pending, err = dbutil.QueryRows[BudgetPendingAssignment](ctx, tx,
			`DELETE FROM budget_pending_assignments WHERE budget_id = ? RETURNING `+pendingAssignmentColumns, canonical); err != nil {
			return fmt.Errorf("BudgetsRepo.Delete(%s): delete pending assignments: %w", canonical, err)
		}
		result, err := tx.ExecContext(ctx,
			`DELETE FROM budgets WHERE id = ? AND is_default = 0`, canonical)
		if err != nil {
			return fmt.Errorf("BudgetsRepo.Delete(%s): %w", canonical, classifyBudgetDeleteError(err))
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("BudgetsRepo.Delete(%s): rows affected: %w", canonical, err)
		}
		if n == 0 {
			return ErrBudgetIsDefault
		}
		return nil
	})
	if dbutil.IsDeadlock(err) {
		return BudgetAssignmentsDeleted{}, withSentinel(ErrBudgetAssignedWhileDeleting, err)
	}
	if err != nil {
		return BudgetAssignmentsDeleted{}, err
	}
	return deleted, nil
}

func deleteBudgetAssignments(ctx context.Context, tx *dbutil.Tx, budgetID string) ([]BudgetAssignment, error) {
	type deletedRow struct {
		ID           string `db:"id"`
		SubjectType  string `db:"subject_type"`
		SubjectRef   string `db:"subject_ref"`
		IdentityType string `db:"identity_type"`
	}
	rows, err := dbutil.QueryRows[deletedRow](ctx, tx,
		`DELETE FROM budget_assignments WHERE budget_id = ? RETURNING CAST(id AS TEXT) AS id, subject_type, subject_ref, identity_type`, budgetID)
	if err != nil {
		return nil, fmt.Errorf("BudgetsRepo.Delete(%s): delete assignments: %w", budgetID, err)
	}
	deleted := make([]BudgetAssignment, 0, len(rows))
	for _, row := range rows {
		deleted = append(deleted, BudgetAssignment{
			ID: row.ID, State: BudgetAssignmentConcrete, SubjectType: row.SubjectType,
			SubjectRef: row.SubjectRef, IdentityType: row.IdentityType, BudgetID: budgetID,
		})
	}
	return deleted, nil
}

func classifyBudgetDeleteError(err error) error {
	if dbutil.IsForeignKeyViolation(err) {
		return withSentinel(ErrBudgetAssignedWhileDeleting, err)
	}
	return err
}

func classifyBudgetWriteError(err error) error {
	var sentinel error
	switch violatedBudgetConstraint(err) {
	case uxBudgetsName:
		sentinel = ErrBudgetNameTaken
	case uxBudgetsDefault:
		sentinel = ErrBudgetDefaultTaken
	case uxBudgetsMeshSingleton:
		sentinel = ErrBudgetMeshExists
	}
	if sentinel == nil {
		return err
	}
	return withSentinel(sentinel, err)
}

func withSentinel(sentinel, err error) error {
	return fmt.Errorf("%w: %w", sentinel, err)
}

const (
	uxBudgetsName              = "ux_budgets_name"
	uxBudgetsDefault           = "ux_budgets_default"
	uxBudgetsMeshSingleton     = "ux_budgets_mesh_singleton"
	uqBudgetAssignmentsSubject = "uq_budget_assignments_subject"
	uqBudgetPendingIdentity    = "uq_budget_pending_assignments_identity"
)

var sqliteBudgetConstraints = map[string]string{
	"budgets.name_lower":                                              uxBudgetsName,
	"budgets.subject_type, budgets.is_default":                        uxBudgetsDefault,
	"budgets.subject_type":                                            uxBudgetsMeshSingleton,
	"budget_assignments.subject_type, budget_assignments.subject_ref": uqBudgetAssignmentsSubject,
	"budget_pending_assignments.subject_type, budget_pending_assignments.identity_type, " +
		"budget_pending_assignments.identity_value, budget_pending_assignments.issuer": uqBudgetPendingIdentity,
}

func violatedBudgetConstraint(err error) string {
	if !dbutil.IsUniqueViolation(err) {
		return ""
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.ConstraintName
	}
	_, rest, ok := strings.Cut(err.Error(), "UNIQUE constraint failed: ")
	if !ok {
		return ""
	}
	columns, _, _ := strings.Cut(rest, " (")
	return sqliteBudgetConstraints[columns]
}
