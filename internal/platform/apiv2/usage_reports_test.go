// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestUsage_NullIDRowsAreReachableByName(t *testing.T) {
	api := newUsageAPI(t)
	api.file(500, func(e *store.SpendLedgerEntry) { e.AgentName = new("OrchestratorAgent") })

	rows := decodeList[UsageRow](t, api.get("/usage/agents?population=spenders")).Data
	if len(rows) != 1 || rows[0].ID != nil || rows[0].BookedName == nil || *rows[0].BookedName != "OrchestratorAgent" {
		t.Fatalf("rows = %+v, want the YAML agent with no id and its booked name", rows)
	}
	rec := api.get("/usage/agents/byName?name=" + url.QueryEscape(*rows[0].BookedName))
	expectStatus(t, rec, http.StatusOK)
	raw := rawUsageData(t, rec)
	if string(raw["id"]) != "null" || string(raw["type"]) != `"agentUsage"` || string(raw["bookedName"]) != `"OrchestratorAgent"` {
		t.Errorf("by name = %s, want its report with no id", rec.Body.String())
	}
	report := decodeData[UsageReport](t, rec)
	if report.Totals.Cost != "5.0000000000" || report.Totals.ShareOfSpend == nil || len(report.Daily) == 0 {
		t.Errorf("report = %+v, want its $5, a share and its days", report)
	}

	expectDetail(t, api.get("/usage/agents/byName"), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.get("/usage/agents/byName?name="), http.StatusBadRequest, "name")
	expectDetail(t, api.get("/usage/agents/byName?name="+strings.Repeat("a", usageMaxNameBytes+1)), http.StatusUnprocessableEntity, "name")
}

func TestUsageReports_UnknownIDIs404(t *testing.T) {
	api := newUsageAPI(t)
	for _, path := range []string{
		"/usage/agents/" + idgen.New(), "/usage/users/" + idgen.New(), "/usage/workflows/" + idgen.New(),
		"/usage/models/" + idgen.New(), "/usage/models/byName?name=nothing",
	} {
		rec := api.get(path)
		expectStatus(t, rec, http.StatusNotFound)
		if body := decodeError(t, rec); body.Message != msgUsageNotFound {
			t.Errorf("GET %s message = %q, want %q", path, body.Message, msgUsageNotFound)
		}
	}
	expectDetail(t, api.get("/usage/agents/not-a-uuid"), http.StatusBadRequest, "id")
}

func TestUsageWorkflow_Detail(t *testing.T) {
	api := newUsageAPI(t)
	wf := idgen.New()
	api.exec(`INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'Expense Audit', ?, 'test', 'test', 0, 0)`, wf, `{"output_mapping": {"r": "{{review.output}}"}, "nodes": [
		{"id": "review", "type": "agent", "agent_name": "Policy Reviewer"}]}`)
	api.file(2_000, func(e *store.SpendLedgerEntry) {
		e.RootKind, e.RootID, e.RootNodeID = new("workflow"), new(wf), new("review")
	})

	rec := api.get("/usage/workflows/" + wf)
	expectStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); !strings.Contains(body, `"key":"review"`) || strings.Contains(body, "nodeId") {
		t.Errorf("report = %s, want each node's key and no nodeId", body)
	}
	report := decodeData[WorkflowUsageReport](t, rec)
	if report.Type != usageTypeWorkflow || report.ID == nil || *report.ID != wf || len(report.Nodes) != 1 ||
		report.Nodes[0].Name != "Policy Reviewer" || report.Nodes[0].Kind == nil || *report.Nodes[0].Kind != "agent" ||
		report.Nodes[0].Cost != "20.0000000000" {
		t.Errorf("report = %+v, want the review node's $20", report)
	}
}

func TestUsageReports_ModelAndUser(t *testing.T) {
	api := newUsageAPI(t)
	model := idgen.New()
	api.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'General', 'anthropic', 'claude', 'test', 'test', 0, 0)`, model)
	user := api.seedUser("report-user", "Rae Porter", "rae@example.com")
	api.file(700, func(e *store.SpendLedgerEntry) {
		e.ModelName, e.ModelConfigID, e.UserSub, e.UserID = nil, new(model), nil, new(user)
		e.RootKind, e.RootName = new("agent"), new("YAMLHelper")
	})

	rec := api.get("/usage/models/" + model)
	expectStatus(t, rec, http.StatusOK)
	m := decodeData[ModelUsageReport](t, rec)
	if m.Type != usageTypeModel || m.ID == nil || *m.ID != model || m.Totals.Cost != "7.0000000000" || m.Models == nil {
		t.Errorf("model report = %+v, want its $7 and its served models", m)
	}

	rec = api.get("/usage/users/" + user)
	expectStatus(t, rec, http.StatusOK)
	u := decodeData[UserUsageReport](t, rec)
	if u.Type != usageTypeUser || u.ID != user || u.Scope != "tenant" || u.User == nil || *u.User.Email != "rae@example.com" ||
		len(u.TopRoots) != 1 || u.TopRoots[0].ID != nil || u.TopRoots[0].BookedName == nil || *u.TopRoots[0].BookedName != "YAMLHelper" {
		t.Errorf("user report = %+v, want Rae's report with the YAML agent her task started at", u)
	}
}
