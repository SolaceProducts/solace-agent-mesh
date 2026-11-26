// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newNameRuleService(t *testing.T) (*WorkflowService, *store.WorkflowRepo, *store.DB) {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewWorkflowRepo(db)
	return NewWorkflowService(repo, store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), nil, nil, "ns", nil), repo, db
}

func nameRuleBody() map[string]any {
	return map[string]any{
		"output_mapping": map[string]any{"result": "x"},
		"nodes":          []any{map[string]any{"id": "step", "type": "agent", "agent_name": "SomeAgent"}},
	}
}

func nameRuleUpdate(name, description string) UpdateWorkflowRequest {
	return UpdateWorkflowRequest{Name: name, Description: description, AppConfig: nameRuleBody()}
}

func seedWorkflowRow(t *testing.T, repo *store.WorkflowRepo, name string) string {
	t.Helper()
	description := "seeded directly through the repository"
	row := &store.Workflow{Name: name, Description: &description, AppConfig: `{}`, CreatedBy: "seed", UpdatedBy: "seed"}
	if err := repo.Create(t.Context(), row, store.WorkflowBindings{}); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
	return row.ID
}

func TestUpdateOfWorkflowsAlreadySharingANormalizedNameOnlyChecksRenames(t *testing.T) {
	t.Parallel()

	twinNames := [2]string{"Order Flow", "orderflow"}
	updates := []struct {
		name     string
		rename   func(own string) string
		wantConf bool
	}{
		{"its own name", func(own string) string { return own }, false},
		{"only the case of its own name", strings.ToUpper, false},
		{"only the spacing of its own name", func(own string) string { return strings.ReplaceAll(own, " ", "") + " " }, false},
		{"a free name", func(string) string { return "Renamed Freely" }, false},
		{"a name another workflow holds", func(string) string { return "bystander flow" }, true},
	}
	for index := range twinNames {
		for _, update := range updates {
			t.Run(twinNames[index]+"/"+update.name, func(t *testing.T) {
				t.Parallel()
				svc, repo, _ := newNameRuleService(t)
				ids := [2]string{seedWorkflowRow(t, repo, twinNames[0]), seedWorkflowRow(t, repo, twinNames[1])}
				seedWorkflowRow(t, repo, "Bystander Flow")

				_, err := svc.Update(t.Context(), ids[index], nameRuleUpdate(update.rename(twinNames[index]), "an edit that must be accepted"), "u")

				if update.wantConf {
					if _, ok := errors.AsType[*ConflictError](err); !ok {
						t.Fatalf("err = %v, want a ConflictError", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("update refused: %v", err)
				}
			})
		}
	}
}

const racingInsertSQLite = `
CREATE TRIGGER wf_name_race_insert BEFORE INSERT ON workflows
WHEN NEW.name = 'RacedFlow'
BEGIN
  INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
  VALUES ('racing-writer-insert', NEW.name, '{}', 'racer', 'racer', 0, 0);
END;
CREATE TRIGGER wf_name_race_update BEFORE UPDATE ON workflows
WHEN NEW.name = 'RacedRename' AND OLD.name <> NEW.name
BEGIN
  INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
  VALUES ('racing-writer-update', NEW.name, '{}', 'racer', 'racer', 0, 0);
END;`

const racingInsertPostgres = `
CREATE FUNCTION wf_name_race() RETURNS trigger AS $$
BEGIN
  IF pg_trigger_depth() = 1 AND NEW.name IN ('RacedFlow', 'RacedRename') AND (TG_OP = 'INSERT' OR OLD.name <> NEW.name) THEN
    INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
    VALUES (gen_random_uuid(), NEW.name, '{}', 'racer', 'racer', 0, 0);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER wf_name_race BEFORE INSERT OR UPDATE ON workflows FOR EACH ROW EXECUTE FUNCTION wf_name_race();`

func installRacingWriter(t *testing.T, db *store.DB) {
	t.Helper()
	script := racingInsertSQLite
	if db.Dialect() == "postgres" {
		script = racingInsertPostgres
	}
	if _, err := db.ExecContext(t.Context(), script); err != nil {
		t.Fatalf("install racing writer: %v", err)
	}
}

func TestLosingTheNameRaceAtTheWriteIsAConflictNotAnInternalError(t *testing.T) {
	t.Parallel()

	t.Run("create", func(t *testing.T) {
		t.Parallel()
		svc, _, db := newNameRuleService(t)
		installRacingWriter(t, db)

		_, err := svc.Create(t.Context(), CreateWorkflowRequest(nameRuleUpdate("RacedFlow", "loses the race at the insert")), "u")

		if _, ok := errors.AsType[*ConflictError](err); !ok {
			t.Fatalf("err = %v, want a ConflictError", err)
		}
	})
	t.Run("rename", func(t *testing.T) {
		t.Parallel()
		svc, repo, db := newNameRuleService(t)
		id := seedWorkflowRow(t, repo, "Source Flow")
		installRacingWriter(t, db)

		_, err := svc.Update(t.Context(), id, nameRuleUpdate("RacedRename", "loses the race at the update"), "u")

		if _, ok := errors.AsType[*ConflictError](err); !ok {
			t.Fatalf("err = %v, want a ConflictError", err)
		}
	})
}

func TestAnUnreadableNameCheckRefusesTheWrite(t *testing.T) {
	t.Parallel()
	svc, _, db := newNameRuleService(t)
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE workflows RENAME TO workflows_unreadable`); err != nil {
		t.Fatalf("break the table: %v", err)
	}

	_, createErr := svc.Create(t.Context(), CreateWorkflowRequest(nameRuleUpdate("Fresh Flow", "created while the check is broken")), "u")

	if createErr == nil || !strings.Contains(createErr.Error(), "check workflow name") {
		t.Errorf("create err = %v, want one from the name check", createErr)
	}
}
