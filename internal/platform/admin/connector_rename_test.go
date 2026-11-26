// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestConnectorRename_EveryUpdatePathAppliesTheCreateNameRules(t *testing.T) {
	svc, db, _, _ := newRedeployServiceForTest(t)
	connID, _ := seedSQLConnectorBoundToAgent(t, svc, db)
	repo := store.NewConnectorRepo(db)
	const storedName = "redeploy-sql"

	updates := map[string]func(name string) error{
		"Update": func(name string) error {
			_, err := svc.Update(t.Context(), connID, UpdateConnectorRequest{Name: name}, "u1")
			return err
		},
		"UpdateWithRedeploy": func(name string) error {
			_, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Name: name}, "u1", ConnectorUpdateOptions{})
			return err
		},
		"UpdateConnectorWithRedeploy": func(name string) error {
			_, err := UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{
				ID: connID, Request: UpdateConnectorRequest{Name: name}, UserID: "u1",
			}, discardLogger())
			return err
		},
	}
	badNames := map[string]string{
		"too short":           "ab",
		"too long":            strings.Repeat("a", 256),
		"a control character": "orders\u0007api",
		"only whitespace":     "   ",
	}

	for path, update := range updates {
		for reason, badName := range badNames {
			t.Run(path+" refuses a name that is "+reason, func(t *testing.T) {
				verr := requireValidationError(t, update(badName))
				if len(verr.Details["name"]) == 0 {
					t.Errorf("details = %v, want a problem on name", verr.Details)
				}
				row, err := repo.GetByID(t.Context(), connID)
				if err != nil || row == nil {
					t.Fatalf("load connector: %v", err)
				}
				if row.Name != storedName {
					t.Errorf("stored name = %q, want %q: a refused rename must change nothing", row.Name, storedName)
				}
			})
		}
	}

	t.Run("a refused rename carries the problems create reports for the same name", func(t *testing.T) {
		createErr := validateRequest(&CreateConnectorRequest{
			Name: "ab", Description: "A connector seeded for the rename tests", Type: "sql", Subtype: "postgres",
		})
		want := requireValidationError(t, createErr)
		got := requireValidationError(t, updates["Update"]("ab"))
		if !slices.Equal(got.Details["name"], want.Details["name"]) {
			t.Errorf("name details = %v, want create's %v", got.Details["name"], want.Details["name"])
		}
	})

	for reason, goodName := range map[string]string{
		"an ordinary name":       "Renamed SQL connector",
		"exactly 3 characters":   "abc",
		"exactly 255 characters": strings.Repeat("b", 255),
	} {
		t.Run("a rename to "+reason+" succeeds and is stored", func(t *testing.T) {
			if err := updates["Update"](goodName); err != nil {
				t.Fatalf("rename: %v", err)
			}
			row, err := repo.GetByID(t.Context(), connID)
			if err != nil || row == nil {
				t.Fatalf("load connector: %v", err)
			}
			if row.Name != goodName {
				t.Errorf("stored name = %q, want %q", row.Name, goodName)
			}
		})
	}
}

func TestConnectorRename_ToANameAnotherConnectorHoldsIsAConflict(t *testing.T) {
	svc, db, _, _ := newRedeployServiceForTest(t)
	connID, _ := seedSQLConnectorBoundToAgent(t, svc, db)
	repo := store.NewConnectorRepo(db)
	other, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "billing-sql", Description: "A second connector holding the name taken",
		Type: "sql", Subtype: "postgres",
		Values: map[string]any{
			"hostname": "localhost", "port": "5432", "database": "testdb", "username": "testuser", "password": "testpass",
		},
	}, "u1")
	if err != nil {
		t.Fatalf("seed second connector: %v", err)
	}

	_, err = svc.Update(t.Context(), connID, UpdateConnectorRequest{Name: other.Name}, "u1")
	if _, ok := errors.AsType[*ConflictError](err); !ok {
		t.Fatalf("error = %T (%v), want *ConflictError", err, err)
	}
	row, err := repo.GetByID(t.Context(), connID)
	if err != nil || row == nil {
		t.Fatalf("load connector: %v", err)
	}
	if row.Name != "redeploy-sql" {
		t.Errorf("stored name = %q, want it unchanged", row.Name)
	}
}

func TestConnectorRename_KeepsAStoredNameThatBreaksTheRules(t *testing.T) {
	svc, db, _, _ := newRedeployServiceForTest(t)
	repo := store.NewConnectorRepo(db)
	subtype := "postgres"
	const storedName = "legacy\tname"
	legacy := &store.Connector{Name: storedName, Type: "sql", Subtype: &subtype, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), legacy); err != nil {
		t.Fatalf("seed a connector whose name create would refuse: %v", err)
	}

	for name, req := range map[string]UpdateConnectorRequest{
		"a patch without a name":     {Description: "Now describes the legacy connector."},
		"a patch repeating the name": {Name: storedName, Description: "Now describes the legacy connector."},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := svc.Update(t.Context(), legacy.ID, req, "u1")
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if resp.Name != storedName {
				t.Errorf("name = %q, want the stored name kept", resp.Name)
			}
		})
	}
}
