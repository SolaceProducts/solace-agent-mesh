// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (a *usageAPI) seedAgent(name string) string {
	a.t.Helper()
	id := idgen.New()
	a.exec(`INSERT INTO agents (id, name, type, is_ephemeral, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, 'standard', 0, 'test', 'test', 0, 0)`, id, name)
	return id
}

func rawRows(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var list struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Data == nil {
		t.Fatalf("decode list %s: %v", body, err)
	}
	return list.Data
}

func TestUsageLists_PerKind(t *testing.T) {
	api := newUsageAPI(t)
	user := api.seedUser("per-kind", "Pat Kind", "pat@example.com")
	agent := api.seedAgent("Support triage")
	api.file(300, func(e *store.SpendLedgerEntry) {
		e.UserSub, e.UserID, e.AgentID = nil, new(user), new(agent)
		e.RootKind, e.RootName = new("workflow"), new("nightly-report")
	})

	for path, wantType := range map[string]string{
		"/usage/agents": usageTypeAgent, "/usage/workflows": usageTypeWorkflow, "/usage/models": usageTypeModel, "/usage/users": usageTypeUser,
	} {
		rec := api.get(path + "?population=spenders")
		expectStatus(t, rec, http.StatusOK)
		rows := rawRows(t, rec.Body.Bytes())
		if len(rows) != 1 {
			t.Fatalf("GET %s = %s, want the one spender", path, rec.Body.String())
		}
		row := rows[0]
		if string(row["type"]) != `"`+wantType+`"` {
			t.Errorf("GET %s type = %s, want %s", path, row["type"], wantType)
		}
		if string(row["daily"]) != "null" {
			t.Errorf("GET %s daily = %s, want null without expand=daily", path, row["daily"])
		}
		_, hasModels := row["models"]
		_, hasBooked := row["bookedName"]
		_, hasBudget := row["budget"]
		switch path {
		case "/usage/models":
			if !hasModels || !hasBooked {
				t.Errorf("model row %s, want models and bookedName", rec.Body.String())
			}
		case "/usage/users":
			if hasBooked || !hasBudget || row["user"] == nil || row["budgetUsage"] == nil {
				t.Errorf("user row %s, want user, budget and budgetUsage and no bookedName", rec.Body.String())
			}
			if !strings.Contains(string(row["user"]), "pat@example.com") || string(row["id"]) != `"`+user+`"` {
				t.Errorf("user row %s, want Pat by id", rec.Body.String())
			}
		default:
			if hasModels || hasBudget || !hasBooked {
				t.Errorf("GET %s row %s, want the plain row", path, rec.Body.String())
			}
		}
	}
	rows := rawRows(t, api.get("/usage/agents?population=spenders").Body.Bytes())
	if string(rows[0]["id"]) != `"`+agent+`"` || string(rows[0]["bookedName"]) != "null" || string(rows[0]["cost"]) != `"3.0000000000"` {
		t.Errorf("agent row = %v, want the agent by id at $3", rows[0])
	}
}

func TestUsageLists_Paging(t *testing.T) {
	api := newUsageAPI(t)
	api.file(100, nil)
	list := decodeList[UsageRow](t, api.get("/usage/agents"))
	if list.Meta.Pagination.PageSize != defaultPageSize {
		t.Errorf("default page size = %d, want %d", list.Meta.Pagination.PageSize, defaultPageSize)
	}
	expectDetail(t, api.get("/usage/agents?expand=daily&pageSize=26"), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.get("/usage/agents?pageSize=101"), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.get("/usage/agents?pageSize=ten"), http.StatusBadRequest, "pageSize")
	expectDetail(t, api.get("/usage/agents?expand=hourly"), http.StatusUnprocessableEntity, "expand")
	expectDetail(t, api.get("/usage/agents?expand=,"), http.StatusUnprocessableEntity, "expand")
	expectDetail(t, api.get("/usage/agents?population=most"), http.StatusUnprocessableEntity, "population")

	daily := decodeList[ModelUsageRow](t, api.get("/usage/models?expand=daily&pageSize=25&population=spenders"))
	if len(daily.Data) != 1 || len(daily.Data[0].Daily) == 0 {
		t.Errorf("expanded rows = %+v, want each with its days", daily.Data)
	}
	past := decodeList[UsageRow](t, api.get("/usage/agents?pageNumber=1000"))
	if len(past.Data) != 0 {
		t.Errorf("a page past the end = %+v, want []", past.Data)
	}
}

func TestUsageLists_Sort(t *testing.T) {
	api := newUsageAPI(t)
	cheap, dear := api.seedAgent("Cheap"), api.seedAgent("Dear")
	api.file(100, func(e *store.SpendLedgerEntry) { e.AgentID = new(cheap) })
	api.file(900, func(e *store.SpendLedgerEntry) { e.AgentID = new(dear) })

	ids := func(path string) []string {
		t.Helper()
		var out []string
		for _, row := range decodeList[UsageRow](t, api.get(path)).Data {
			out = append(out, *row.ID)
		}
		return out
	}
	if got := ids("/usage/agents?population=spenders"); !slices.Equal(got, []string{dear, cheap}) {
		t.Errorf("default order = %v, want the costliest first", got)
	}
	if got := ids("/usage/agents?population=spenders&sort=name:asc"); !slices.Equal(got, []string{cheap, dear}) {
		t.Errorf("by name = %v, want Cheap then Dear", got)
	}
	if got := ids("/usage/agents?population=spenders&sort=name"); !slices.Equal(got, []string{cheap, dear}) {
		t.Errorf("by bare name = %v, want ascending, Cheap then Dear", got)
	}
	expectDetail(t, api.get("/usage/agents?sort=name:up"), http.StatusBadRequest, "sort")
	expectDetail(t, api.get("/usage/agents?sort=budget:asc"), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.get("/usage/agents?sort=model:asc"), http.StatusUnprocessableEntity, "sort")
	expectStatus(t, api.get("/usage/users?sort=budget:asc"), http.StatusOK)
	expectStatus(t, api.get("/usage/users?sort=budgetUsage:desc"), http.StatusOK)
	expectStatus(t, api.get("/usage/models?sort=model:asc"), http.StatusOK)
}

func TestUsageLists_Search(t *testing.T) {
	api := newUsageAPI(t)
	triage, billing := api.seedAgent("Support triage"), api.seedAgent("Billing")
	api.file(100, func(e *store.SpendLedgerEntry) { e.AgentID = new(triage) })
	api.file(200, func(e *store.SpendLedgerEntry) { e.AgentID = new(billing) })

	got := decodeList[UsageRow](t, api.get("/usage/agents?population=spenders&search=TRIAGE"))
	if len(got.Data) != 1 || *got.Data[0].ID != triage {
		t.Errorf("search = %+v, want only Support triage", got.Data)
	}
	expectDetail(t, api.get("/usage/agents?search="+strings.Repeat("a", usageMaxNameBytes+1)), http.StatusUnprocessableEntity, "search")
}

func TestUsageLists_UsersByBudget(t *testing.T) {
	api := newUsageAPI(t)
	expectDetail(t, api.get("/usage/users?budgetId=nope"), http.StatusBadRequest, "budgetId")
	expectDetail(t, api.get("/usage/users?budgetId="), http.StatusBadRequest, "budgetId")
	if got := decodeList[UserUsageRow](t, api.get("/usage/users?budgetId="+idgen.New())); len(got.Data) != 0 {
		t.Errorf("an id naming no budget = %+v, want no one", got.Data)
	}
}
