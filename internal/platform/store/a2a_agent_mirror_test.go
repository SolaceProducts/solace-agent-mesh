// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func remoteFixture(name string) *A2AAgent {
	desc := "remote " + name
	authType, authConfig := "none", "{}"
	return &A2AAgent{
		AgentAuthType:         &authType,
		AgentAuthConfig:       &authConfig,
		AgentURL:              "https://example.com/a2a",
		Name:                  name,
		Description:           &desc,
		AgentCardLocation:     "well_known",
		RequestTimeoutSeconds: 120,
		CreatedBy:             "creator",
		UpdatedBy:             "creator",
	}
}

func createStandardAgent(t *testing.T, db *DB, name string) {
	t.Helper()
	if err := NewAgentRepo(db).Create(t.Context(), &Agent{Name: name, CreatedBy: "u", UpdatedBy: "u"}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed agent %q: %v", name, err)
	}
}

func countRows(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), db, query, args...)
	if err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func requireMirror(t *testing.T, db *DB, remote *A2AAgent) {
	t.Helper()
	agent, _, _, _, _, err := NewAgentRepo(db).GetByID(t.Context(), remote.ID)
	if err != nil || agent == nil {
		t.Fatalf("mirror of %q: %v, %v", remote.Name, agent, err)
	}
	if agent.Name != remote.Name {
		t.Errorf("mirror name = %q, want %q", agent.Name, remote.Name)
	}
	if agent.Type != AgentTypeA2AProxy {
		t.Errorf("mirror type = %q, want %q", agent.Type, AgentTypeA2AProxy)
	}
	if want := defaultStrPtr(remote.Description, ""); *agent.Description != *want {
		t.Errorf("mirror description = %q, want %q", *agent.Description, *want)
	}
}

func TestA2AAgentRepo_CreateWithMirror(t *testing.T) {
	cases := []struct {
		name      string
		seedAgent string
		seedA2A   string
		create    string
		wantTaken bool
	}{
		{name: "free name", create: "Fresh Remote"},
		{name: "exact name of an agent", seedAgent: "Taken", create: "Taken", wantTaken: true},
		{name: "case variant of an agent", seedAgent: "Taken", create: "tAKEN", wantTaken: true},
		{name: "spacing variant of an agent", seedAgent: "Taken Agent", create: "TakenAgent", wantTaken: true},
		{name: "exact non-ASCII name of an agent", seedAgent: "Ünïcode Agent", create: "Ünïcode Agent", wantTaken: true},
		{name: "exact name of a remote agent", seedA2A: "Taken", create: "Taken", wantTaken: true},
		{name: "case variant of a remote agent", seedA2A: "Taken", create: "tAKEN", wantTaken: true},
		{name: "spacing variant of a remote agent", seedA2A: "Taken Remote", create: "TakenRemote", wantTaken: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := TestDB(t)
			repo := NewA2AAgentRepo(db)
			ctx := t.Context()
			if tc.seedAgent != "" {
				createStandardAgent(t, db, tc.seedAgent)
			}
			if tc.seedA2A != "" {
				if err := repo.CreateWithMirror(ctx, remoteFixture(tc.seedA2A)); err != nil {
					t.Fatalf("seed remote: %v", err)
				}
			}
			remotesBefore := countRows(t, db, `SELECT COUNT(*) FROM a2a_agents`)
			agentsBefore := countRows(t, db, `SELECT COUNT(*) FROM agents`)

			remote := remoteFixture(tc.create)
			err := repo.CreateWithMirror(ctx, remote)

			if tc.wantTaken {
				if !dbutil.IsUniqueViolation(err) {
					t.Fatalf("err = %v, want a unique violation", err)
				}
				if got := countRows(t, db, `SELECT COUNT(*) FROM a2a_agents`); got != remotesBefore {
					t.Errorf("a2a_agents rows = %d, want %d: a rejected create must roll the remote agent back", got, remotesBefore)
				}
				if got := countRows(t, db, `SELECT COUNT(*) FROM agents`); got != agentsBefore {
					t.Errorf("agents rows = %d, want %d", got, agentsBefore)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateWithMirror: %v", err)
			}
			requireMirror(t, db, remote)
		})
	}
}

func TestA2AAgentRepo_UpdateWithMirror(t *testing.T) {
	cases := []struct {
		name         string
		seedAgent    string
		seedOther    string
		rename       string
		describeAs   string
		dropMirror   bool
		staleMirror  bool
		wantConflict bool
	}{
		{name: "rename to a free name", rename: "Subject Renamed"},
		{name: "description only", describeAs: "a new description of the remote agent"},
		{name: "rename and describe", rename: "Subject Renamed", describeAs: "a new description of the remote agent"},
		{name: "case-only rename of itself", rename: "SUBJECT"},
		{name: "spacing-only rename of itself", rename: "Sub ject"},
		{name: "rename onto an agent", seedAgent: "Taken Agent", rename: "Taken Agent", wantConflict: true},
		{name: "rename onto an agent, case variant", seedAgent: "Taken Agent", rename: "taken agent", wantConflict: true},
		{name: "rename onto an agent, spacing variant", seedAgent: "Taken Agent", rename: "TakenAgent", wantConflict: true},
		{name: "rename onto a non-ASCII agent", seedAgent: "Ünïcode Agent", rename: "Ünïcode Agent", wantConflict: true},
		{name: "rename onto another remote agent", seedOther: "Taken Remote", rename: "Taken Remote", wantConflict: true},
		{name: "rename onto another remote agent, case variant", seedOther: "Taken Remote", rename: "TAKEN REMOTE", wantConflict: true},
		{name: "update heals a mirror that kept an old name", staleMirror: true, describeAs: "a new description of the remote agent"},
		{name: "update creates a mirror a legacy remote agent lacks", dropMirror: true, describeAs: "a new description of the remote agent"},
		{name: "rename creates a mirror a legacy remote agent lacks", dropMirror: true, rename: "Subject Renamed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := TestDB(t)
			repo := NewA2AAgentRepo(db)
			ctx := context.Background()
			subject := remoteFixture("Subject")
			if err := repo.CreateWithMirror(ctx, subject); err != nil {
				t.Fatalf("seed subject: %v", err)
			}
			if tc.seedAgent != "" {
				createStandardAgent(t, db, tc.seedAgent)
			}
			if tc.seedOther != "" {
				if err := repo.CreateWithMirror(ctx, remoteFixture(tc.seedOther)); err != nil {
					t.Fatalf("seed other remote: %v", err)
				}
			}
			if tc.dropMirror {
				if _, err := db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, subject.ID); err != nil {
					t.Fatalf("drop mirror: %v", err)
				}
			}
			if tc.staleMirror {
				if _, err := db.ExecContext(ctx, `UPDATE agents SET name = 'Stale Old Name', description = 'stale' WHERE id = ?`, subject.ID); err != nil {
					t.Fatalf("stale mirror: %v", err)
				}
			}

			updated := *subject
			if tc.rename != "" {
				updated.Name = tc.rename
			}
			if tc.describeAs != "" {
				updated.Description = &tc.describeAs
			}
			updated.UpdatedBy = "editor"
			err := repo.UpdateWithMirror(ctx, &updated)

			if tc.wantConflict {
				if !dbutil.IsUniqueViolation(err) {
					t.Fatalf("err = %v, want a unique violation", err)
				}
				stored, getErr := repo.GetByID(ctx, subject.ID)
				if getErr != nil || stored == nil {
					t.Fatalf("reload subject: %v, %v", stored, getErr)
				}
				if stored.Name != subject.Name {
					t.Errorf("remote agent name = %q after a rejected rename, want %q: the update must roll back with the mirror", stored.Name, subject.Name)
				}
				requireMirror(t, db, subject)
				return
			}
			if err != nil {
				t.Fatalf("UpdateWithMirror: %v", err)
			}
			stored, getErr := repo.GetByID(ctx, subject.ID)
			if getErr != nil || stored == nil {
				t.Fatalf("reload subject: %v, %v", stored, getErr)
			}
			if stored.Name != updated.Name {
				t.Errorf("remote agent name = %q, want %q", stored.Name, updated.Name)
			}
			requireMirror(t, db, stored)
		})
	}
}

func TestA2AAgentRepo_DeleteWithMirror(t *testing.T) {
	cases := []struct {
		name       string
		dropMirror bool
	}{
		{name: "removes the remote agent and its mirror"},
		{name: "a legacy remote agent without a mirror", dropMirror: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := TestDB(t)
			repo := NewA2AAgentRepo(db)
			ctx := t.Context()
			subject, keeper := remoteFixture("Subject"), remoteFixture("Keeper")
			for _, r := range []*A2AAgent{subject, keeper} {
				if err := repo.CreateWithMirror(ctx, r); err != nil {
					t.Fatalf("seed %q: %v", r.Name, err)
				}
			}
			if tc.dropMirror {
				if _, err := db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, subject.ID); err != nil {
					t.Fatalf("drop mirror: %v", err)
				}
			}

			if err := repo.DeleteWithMirror(ctx, subject.ID); err != nil {
				t.Fatalf("DeleteWithMirror: %v", err)
			}

			if n := countRows(t, db, `SELECT COUNT(*) FROM a2a_agents WHERE id = ?`, subject.ID); n != 0 {
				t.Errorf("remote agent rows = %d, want 0", n)
			}
			if n := countRows(t, db, `SELECT COUNT(*) FROM agents WHERE id = ?`, subject.ID); n != 0 {
				t.Errorf("mirror rows = %d, want 0: the name stays reserved by an orphaned mirror", n)
			}
			requireMirror(t, db, keeper)
		})
	}
}

func TestA2AAgentRepo_DeleteWithMirror_UnknownRemoteLeavesMirrorsAlone(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	keeper := remoteFixture("Keeper")
	if err := repo.CreateWithMirror(t.Context(), keeper); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := repo.DeleteWithMirror(t.Context(), "ffffffff-ffff-ffff-ffff-ffffffffffff"); err == nil {
		t.Fatal("DeleteWithMirror of an unknown id succeeded")
	}
	requireMirror(t, db, keeper)
}
