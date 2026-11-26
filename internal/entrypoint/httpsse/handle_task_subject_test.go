// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func subjectOf(t *testing.T, entry map[string]any) map[string]any {
	t.Helper()
	s, ok := entry["subject"].(map[string]any)
	if !ok {
		t.Fatalf("entry %v has no subject", entry["taskId"])
	}
	return s
}

func TestHandleEvents_StampsSubjectPerEntry(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	root, child := seedTiersTree(t, a, true)
	a.core.AgentRegistry().AddOrUpdate(displayNamedCard(tiersHRSubj, "HR Systems Agent"))

	rec := getTierAs(t, a, "/api/v1/tasks/"+root+"/events", tiersOwner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	byID := map[string]map[string]any{}
	for _, e := range decodeEntries(t, rec) {
		byID[e["taskId"].(string)] = e
	}
	rootSubj := subjectOf(t, byID[root])
	if rootSubj["id"] != tiersRootSubj || rootSubj["type"] != store.SubjectTypeWorkflow {
		t.Errorf("root subject = %v", rootSubj)
	}
	childSubj := subjectOf(t, byID[child])
	if childSubj["id"] != tiersHRSubj || childSubj["type"] != store.SubjectTypeAgent || childSubj["name"] != "HR Systems Agent" {
		t.Errorf("child subject = %v", childSubj)
	}
	if _, present := rootSubj["name"]; !present {
		t.Error("name must always be present, empty when unresolved")
	}
}

func TestCollectTaskEvents_DoesNotStampSubject(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	root, _ := seedTiersTree(t, a, true)
	db := a.core.Store()
	entries, tasks, err := collectTaskEvents(context.Background(), store.NewTaskRepo(db), store.NewTaskEventRepo(db), []string{root}, false, a.logger)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected both task rows back, got %d", len(tasks))
	}
	for id, e := range entries {
		if _, leaked := e["subject"]; leaked {
			t.Errorf("collector output for %s must not carry subject; the share link reads it", id)
		}
	}
}

func displayNamedCard(name, displayName string) a2a.AgentCard {
	return a2a.AgentCard{
		Name: name,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{URI: a2a.ExtensionURIDisplayName, Params: map[string]any{"display_name": displayName}}},
		},
	}
}
