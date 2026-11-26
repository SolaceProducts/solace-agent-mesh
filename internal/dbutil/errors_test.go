// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsForeignKeyViolation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"postgres foreign_key_violation", &pgconn.PgError{Code: "23503"}, true},
		{"postgres restrict_violation", &pgconn.PgError{Code: "23001"}, true},
		{"postgres wrapped", fmt.Errorf("repo: %w", &pgconn.PgError{Code: "23503"}), true},
		{"postgres unique_violation", &pgconn.PgError{Code: "23505"}, false},
		{"postgres check_violation", &pgconn.PgError{Code: "23514"}, false},
		{"postgres not_null_violation", &pgconn.PgError{Code: "23502"}, false},
		{"sqlite missing parent", errors.New("constraint failed: FOREIGN KEY constraint failed (787)"), true},
		{"sqlite restrict", errors.New("constraint failed: FOREIGN KEY constraint failed (1811)"), true},
		{"sqlite trigger raise", errors.New("constraint failed: assignment frozen (1811)"), false},
		{"sqlite unique", errors.New("constraint failed: UNIQUE constraint failed: budgets.name_lower (2067)"), false},
		{"sqlite check", errors.New("constraint failed: CHECK constraint failed: ck_budgets_warn_range (275)"), false},
		{"unrelated", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := IsForeignKeyViolation(c.err); got != c.want {
				t.Errorf("IsForeignKeyViolation(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestIsDeadlock(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"postgres deadlock_detected", &pgconn.PgError{Code: "40P01"}, true},
		{"postgres wrapped", fmt.Errorf("repo: %w", &pgconn.PgError{Code: "40P01"}), true},
		{"postgres serialization_failure", &pgconn.PgError{Code: "40001"}, false},
		{"postgres lock_not_available", &pgconn.PgError{Code: "55P03"}, false},
		{"unrelated", errors.New("deadlock"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := IsDeadlock(c.err); got != c.want {
				t.Errorf("IsDeadlock(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestIsUniqueViolationOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"postgres name constraint", &pgconn.PgError{Code: "23505", TableName: "workflows", ConstraintName: "workflows_name_key"}, true},
		{"postgres wrapped", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505", TableName: "workflows", ConstraintName: "workflows_name_key"}), true},
		{"postgres primary key", &pgconn.PgError{Code: "23505", TableName: "workflows", ConstraintName: "workflows_pkey"}, false},
		{"postgres other table", &pgconn.PgError{Code: "23505", TableName: "agents", ConstraintName: "agents_name_key"}, false},
		{"postgres other code", &pgconn.PgError{Code: "23503", TableName: "workflows", ConstraintName: "workflows_name_key"}, false},
		{"sqlite name constraint", errors.New("constraint failed: UNIQUE constraint failed: workflows.name (2067)"), true},
		{"sqlite primary key", errors.New("constraint failed: UNIQUE constraint failed: workflows.id (1555)"), false},
		{"sqlite other table", errors.New("constraint failed: UNIQUE constraint failed: agents.name (2067)"), false},
		{"unrelated", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := IsUniqueViolationOn(c.err, "workflows", "name"); got != c.want {
				t.Errorf("IsUniqueViolationOn(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
