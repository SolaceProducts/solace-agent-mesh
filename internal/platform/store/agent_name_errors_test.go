// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsAgentNameViolation_RealViolations(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, db *DB) error
		want  bool
	}{
		{"agent, exact name", func(t *testing.T, db *DB) error { return agentWithName(db, "Taken") }, true},
		{"agent, case variant", func(t *testing.T, db *DB) error { return agentWithName(db, "TAKEN") }, true},
		{"agent, spacing variant", func(t *testing.T, db *DB) error { return agentWithName(db, "Ta ken") }, true},
		{"remote agent over an agent, exact", func(t *testing.T, db *DB) error { return remoteWithName(db, "Taken") }, true},
		{"remote agent over an agent, case variant", func(t *testing.T, db *DB) error { return remoteWithName(db, "tAKEN") }, true},
		{"remote agent over an agent, spacing variant", func(t *testing.T, db *DB) error { return remoteWithName(db, "T aken") }, true},
		{"remote agent over a remote agent, exact", func(t *testing.T, db *DB) error {
			if err := remoteWithName(db, "Other"); err != nil {
				return err
			}
			return remoteWithName(db, "Other")
		}, true},
		{"remote agent over a remote agent, case variant", func(t *testing.T, db *DB) error {
			if err := remoteWithName(db, "Other"); err != nil {
				return err
			}
			return remoteWithName(db, "oTHER")
		}, true},
		{"repeated input mode on a fresh name", func(t *testing.T, db *DB) error {
			return NewAgentRepo(db).Create(t.Context(), &Agent{Name: "Fresh", CreatedBy: "u", UpdatedBy: "u"}, nil, []string{"text", "text"}, nil, nil, nil)
		}, false},
		{"repeated output mode on a fresh name", func(t *testing.T, db *DB) error {
			return NewAgentRepo(db).Create(t.Context(), &Agent{Name: "Fresh", CreatedBy: "u", UpdatedBy: "u"}, nil, nil, []string{"text", "text"}, nil, nil)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := TestDB(t)
			createStandardAgent(t, db, "Taken")

			err := tc.write(t, db)

			if err == nil {
				t.Fatal("the write succeeded, so the case proves nothing")
			}
			if got := IsAgentNameViolation(err); got != tc.want {
				t.Errorf("IsAgentNameViolation(%v) = %v, want %v", err, got, tc.want)
			}
		})
	}
}

func agentWithName(db *DB, name string) error {
	return NewAgentRepo(db).Create(context.Background(), &Agent{Name: name, CreatedBy: "u", UpdatedBy: "u"}, nil, nil, nil, nil, nil)
}

func remoteWithName(db *DB, name string) error {
	return NewA2AAgentRepo(db).CreateWithMirror(context.Background(), remoteFixture(name))
}

func TestIsAgentNameViolation_ClassifiesDriverErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("connection reset"), false},
		{"postgres agents_name_key", &pgconn.PgError{Code: "23505", ConstraintName: "agents_name_key"}, true},
		{"postgres agents normalized index", &pgconn.PgError{Code: "23505", ConstraintName: "idx_agents_name_normalized"}, true},
		{"postgres a2a_agents_name_key", &pgconn.PgError{Code: "23505", ConstraintName: "a2a_agents_name_key"}, true},
		{"postgres a2a_agents normalized index", &pgconn.PgError{Code: "23505", ConstraintName: "idx_a2a_agents_name_normalized"}, true},
		{"postgres agents primary key", &pgconn.PgError{Code: "23505", ConstraintName: "agents_pkey"}, false},
		{"postgres input modes key", &pgconn.PgError{Code: "23505", ConstraintName: "agent_input_modes_pkey"}, false},
		{"postgres name constraint but another code", &pgconn.PgError{Code: "23503", ConstraintName: "agents_name_key"}, false},
		{"sqlite agents.name", errors.New("UNIQUE constraint failed: agents.name (2067)"), true},
		{"sqlite a2a_agents.name", errors.New("UNIQUE constraint failed: a2a_agents.name (2067)"), true},
		{"sqlite agents normalized index", errors.New("UNIQUE constraint failed: index 'idx_agents_name_normalized' (2067)"), true},
		{"sqlite a2a_agents normalized index", errors.New("UNIQUE constraint failed: index 'idx_a2a_agents_name_normalized' (2067)"), true},
		{"sqlite junction key", errors.New("UNIQUE constraint failed: agent_input_modes.agent_id, agent_input_modes.mode (1555)"), false},
		{"sqlite agents primary key", errors.New("UNIQUE constraint failed: agents.id (1555)"), false},
		{"sqlite other message naming agents.name", errors.New("no such column: agents.name"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAgentNameViolation(tc.err); got != tc.want {
				t.Errorf("IsAgentNameViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
