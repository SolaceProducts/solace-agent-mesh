// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var (
	workflowsNamesCreateOnly   = []string{rbac.ScopeWorkflowBuilderCreate}
	workflowsNamesCreateUpdate = []string{rbac.ScopeWorkflowBuilderCreate, rbac.ScopeWorkflowBuilderUpdate}
	workflowsNamesUpdateOnly   = []string{rbac.ScopeWorkflowBuilderUpdate}
)

type workflowsNamesStored struct {
	Name        string
	Description string
	UpdatedBy   string
	UpdatedTime Instant
}

func workflowsNamesBody(name, description string) map[string]any {
	return map[string]any{"name": name, "description": description, "appConfig": workflowAppConfig("SomeAgent")}
}

func workflowsNamesCreate(api *entrypointAPI, name, description string, scopes ...string) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPost, "/workflows", workflowsNamesBody(name, description), scopes...)
}

func workflowsNamesUpdate(api *entrypointAPI, id, name, description string, scopes ...string) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPut, "/workflows/"+id, workflowsNamesBody(name, description), scopes...)
}

func workflowsNamesSeed(api *entrypointAPI, name, description string) string {
	api.t.Helper()
	return expectCreated[Workflow](api.t, workflowsNamesCreate(api, name, description), "workflows").ID
}

func workflowsNamesStoredByID(api *entrypointAPI) map[string]workflowsNamesStored {
	api.t.Helper()
	page := decodeList[Workflow](api.t, api.call(http.MethodGet, "/workflows?pageSize=100", nil))
	byID := make(map[string]workflowsNamesStored, len(page.Data))
	for _, w := range page.Data {
		byID[w.ID] = workflowsNamesStored{Name: w.Name, Description: w.Description, UpdatedBy: w.UpdatedBy, UpdatedTime: w.UpdatedTime}
	}
	return byID
}

func workflowsNamesExpectConflict(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	expectStatus(t, rec, http.StatusConflict)
	if body := decodeError(t, rec); len(body.ValidationDetails) != 0 {
		t.Errorf("a conflict carries validationDetails: %s", rec.Body.String())
	}
}

func workflowsNamesRace(calls int, call func(i int) int) map[int]int {
	codes := make(chan int, calls)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			<-start
			codes <- call(i)
		})
	}
	close(start)
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	return counts
}

func TestWorkflows_CreateNeverUpdatesAnExistingWorkflow(t *testing.T) {
	collidingNames := []struct {
		name    string
		stored  string
		request string
	}{
		{"exact", "ExactFlow", "ExactFlow"},
		{"lowercased", "LowerFlow", "lowerflow"},
		{"uppercased", "UpperFlow", "UPPERFLOW"},
		{"space inserted", "InsertFlow", "Insert Flow"},
		{"space removed", "Removed Flow", "RemovedFlow"},
		{"several spaces", "SeveralFlow", "Several  Flow"},
		{"padded with spaces", "PaddedFlow", " PaddedFlow "},
		{"case and spaces", "MixedFlow", "mixed FLOW"},
		{"non-ASCII exact", "École Flow", "École Flow"},
		{"non-ASCII lowercased", "Élan Flow", "élan flow"},
		{"non-ASCII uppercased", "ïdée flow", "ÏDÉE FLOW"},
		{"non-ASCII with spaces", "ÉtéFlow", "été Flow"},
		{"CJK", "工作流 一", "工作流一"},
	}
	scopeSets := []struct {
		name   string
		scopes []string
	}{
		{"create only", workflowsNamesCreateOnly},
		{"create and update", workflowsNamesCreateUpdate},
		{"every scope", nil},
	}

	shared := newEntrypointAPI(t, nil)
	for _, names := range collidingNames {
		t.Run(names.name, func(t *testing.T) {
			api := shared.forRow(t)
			workflowsNamesSeed(api, names.stored, "The original description.")
			before := workflowsNamesStoredByID(api)

			for _, scopes := range scopeSets {
				workflowsNamesExpectConflict(t, workflowsNamesCreate(api, names.request, "An attempted overwrite.", scopes.scopes...))

				if after := workflowsNamesStoredByID(api); !maps.Equal(after, before) {
					t.Errorf("%s: workflows after the refused create = %+v, want exactly the untouched %+v", scopes.name, after, before)
				}
			}
		})
	}
}

func TestWorkflows_CreateAcceptsANameNoOtherWorkflowHolds(t *testing.T) {
	shared := newEntrypointAPI(t, nil)
	id := workflowsNamesSeed(shared, "CaseFlow", "The original description.")
	workflowsNamesSeed(shared, "École Flow", "A workflow with an accented name.")
	for _, tc := range []struct {
		name    string
		request string
	}{
		{"different word", "OtherFlow"},
		{"suffix", "CaseFlow2"},
		{"hyphen is not a space", "Case-Flow"},
		{"underscore is not a space", "Case_Flow"},
		{"accent is not folded", "Ecole Flow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			before := workflowsNamesStoredByID(api)

			expectStatus(t, workflowsNamesCreate(api, tc.request, "A distinct workflow.", workflowsNamesCreateOnly...), http.StatusCreated)

			if after := workflowsNamesStoredByID(api); len(after) != len(before)+1 || after[id] != before[id] {
				t.Errorf("workflows after the create = %+v, want %+v plus one new row", after, before)
			}
		})
	}
}

func TestWorkflows_ConcurrentCreatesOfOneNameYieldOneWinnerAndConflicts(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	const callers = 24

	counts := workflowsNamesRace(callers, func(int) int {
		return workflowsNamesCreate(api, "RacedFlow", "Created by a racing caller.", workflowsNamesCreateOnly...).Code
	})

	if counts[http.StatusCreated] != 1 || counts[http.StatusConflict] != callers-1 {
		t.Errorf("status counts = %v, want one 201 and %d 409", counts, callers-1)
	}
	if got := len(workflowsNamesStoredByID(api)); got != 1 {
		t.Errorf("stored workflows = %d, want 1", got)
	}
}

func TestWorkflows_ConcurrentRenamesOntoOneNameYieldOneWinnerAndConflicts(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	const callers = 12
	ids := make([]string, callers)
	for i := range ids {
		ids[i] = workflowsNamesSeed(api, "Source"+string(rune('A'+i)), "A workflow awaiting a rename.")
	}

	counts := workflowsNamesRace(callers, func(i int) int {
		return workflowsNamesUpdate(api, ids[i], "ContestedName", "Renamed by a racing caller.", workflowsNamesUpdateOnly...).Code
	})

	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != callers-1 {
		t.Errorf("status counts = %v, want one 200 and %d 409", counts, callers-1)
	}
}

func TestWorkflows_InvalidCreateIsRefusedWithoutDisclosingATakenName(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	workflowsNamesSeed(api, "CaseFlow", "The original description.")
	cyclic := workflowAppConfig("SomeAgent")
	cyclic["workflow"] = map[string]any{
		"output_mapping": map[string]any{"result": "x"},
		"nodes": []any{
			map[string]any{"id": "a", "type": "agent", "agent_name": "SomeAgent", "depends_on": []any{"b"}},
			map[string]any{"id": "b", "type": "agent", "agent_name": "SomeAgent", "depends_on": []any{"a"}},
		},
	}

	rec := api.call(http.MethodPost, "/workflows", map[string]any{
		"name": "CaseFlow", "description": "A body that cannot be saved.", "appConfig": cyclic,
	}, workflowsNamesCreateOnly...)

	expectDetail(t, rec, http.StatusUnprocessableEntity, "appConfig.workflow.nodes")
}

func TestWorkflows_UpdateAppliesCreateNameRuleSoUpdateScopeCannotMintCollision(t *testing.T) {
	shared := newEntrypointAPI(t, nil)
	otherID := workflowsNamesSeed(shared, "OtherFlow", "The other description.")
	accentedID := workflowsNamesSeed(shared, "École", "A workflow with an accented name.")
	for _, tc := range []struct {
		name       string
		subject    string
		renameTo   string
		wantStatus int
	}{
		{"exact", "ExactFlow", "OtherFlow", http.StatusConflict},
		{"lowercased", "LowerFlow", "otherflow", http.StatusConflict},
		{"space inserted", "InsertFlow", "Other Flow", http.StatusConflict},
		{"non-ASCII case variant", "AccentFlow", "école", http.StatusConflict},
		{"its own name", "OwnFlow", "OwnFlow", http.StatusOK},
		{"only the case of its own name", "CaseFlow", "caseflow", http.StatusOK},
		{"only the spacing of its own name", "SpaceFlow", "Space Flow", http.StatusOK},
		{"a free name", "FreeFlow", "RenamedFlow", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			id := workflowsNamesSeed(api, tc.subject, "The original description.")
			before := workflowsNamesStoredByID(api)

			rec := workflowsNamesUpdate(api, id, tc.renameTo, "The renamed description.", workflowsNamesUpdateOnly...)

			after := workflowsNamesStoredByID(api)
			if after[otherID] != before[otherID] || after[accentedID] != before[accentedID] {
				t.Errorf("a bystander workflow changed: %+v -> %+v", before, after)
			}
			if tc.wantStatus == http.StatusConflict {
				workflowsNamesExpectConflict(t, rec)
				if after[id] != before[id] {
					t.Errorf("the refused rename still changed the workflow: %+v -> %+v", before[id], after[id])
				}
				return
			}
			expectStatus(t, rec, tc.wantStatus)
			if after[id].Name != tc.renameTo {
				t.Errorf("name after the update = %q, want %q", after[id].Name, tc.renameTo)
			}
		})
	}
}

func TestWorkflows_UpdateWithOnlyTheCreateScopeIsForbiddenAndChangesNothing(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	id := workflowsNamesSeed(api, "CaseFlow", "The original description.")
	before := workflowsNamesStoredByID(api)

	expectStatus(t, workflowsNamesUpdate(api, id, "CaseFlow", "An attempted overwrite.", workflowsNamesCreateOnly...), http.StatusForbidden)

	if after := workflowsNamesStoredByID(api); after[id] != before[id] {
		t.Errorf("the forbidden update still changed the workflow: %+v -> %+v", before[id], after[id])
	}
}
