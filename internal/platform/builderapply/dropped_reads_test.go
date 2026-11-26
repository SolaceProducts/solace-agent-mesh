// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func hideFromTheListing(t *testing.T, svc *Service, id string) {
	t.Helper()
	if _, err := svc.deps.DB.ExecContext(t.Context(), `UPDATE agents SET is_ephemeral = 1 WHERE id = ?`, id); err != nil {
		t.Fatalf("mark ephemeral: %v", err)
	}
	listed, err := svc.deps.listAgents(t.Context())
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	if slices.ContainsFunc(listed, func(a entplatform.AgentResponse) bool { return a.ID == id }) {
		t.Fatal("the agent is still in the listing")
	}
}

func breakTable(t *testing.T, svc *Service, table string) {
	t.Helper()
	if _, err := svc.deps.DB.ExecContext(t.Context(), `ALTER TABLE `+table+` RENAME TO `+table+`_gone`); err != nil {
		t.Fatalf("break %s: %v", table, err)
	}
}

func TestDropped_AResourceMissingFromTheListingButFoundByIDIsListed(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	buildInSession(t, svc, sessionA, false, agentComponent("kept_one"), agentComponent("missed_by_the_listing"))
	missed := agentNamed(t.Context(), t, svc, "missed_by_the_listing")
	seedAgentDeployment(t, svc, missed.ID, "deploy", "success")
	hideFromTheListing(t, svc, missed.ID)

	for _, dryRun := range []bool{true, false} {
		resp := buildInSession(t, svc, sessionA, dryRun, agentComponent("kept_one"))

		want := DroppedResource{Kind: kindAgent, ID: missed.ID, Name: "missed_by_the_listing", CreatedBySession: true, Deployment: deploymentDeployed}
		if len(resp.Dropped) != 1 || resp.Dropped[0] != want || resp.DroppedCheckFailed {
			t.Fatalf("dryRun=%v: dropped = %+v (check failed: %v), want [%+v]: it exists, so it is listed", dryRun, resp.Dropped, resp.DroppedCheckFailed, want)
		}
	}
	if !sessionRecordHolds(t, svc, missed.ID) {
		t.Error("the session record forgot a resource that exists")
	}
}

func sessionRecordHolds(t *testing.T, svc *Service, id string) bool {
	t.Helper()
	rows, err := svc.deps.Ledger.ListForSession(t.Context(), testActor.Email(), sessionA)
	if err != nil {
		t.Fatalf("read the session record: %v", err)
	}
	return slices.ContainsFunc(rows, func(row store.BuilderSessionResource) bool { return row.ResourceID == id })
}

func TestDropped_AReadThatFailsIsSaidNeverTakenForNothingDropped(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arrange func(t *testing.T, svc *Service, id string)
	}{
		{"the by-id read of a resource missing from the listing fails", func(t *testing.T, svc *Service, id string) {
			hideFromTheListing(t, svc, id)
			breakTable(t, svc, "agent_skills")
		}},
		{"the deployment read fails", func(t *testing.T, svc *Service, _ string) { breakTable(t, svc, "agent_deployments") }},
		{"the stored listing fails", func(t *testing.T, svc *Service, _ string) { breakTable(t, svc, "agents") }},
	}
	for _, tc := range cases {
		for _, dryRun := range []bool{true, false} {
			t.Run(tc.name+map[bool]string{true: "/dry run", false: "/apply"}[dryRun], func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				buildInSession(t, svc, sessionA, false, agentComponent("kept_two"), agentComponent("unreadable"))
				unreadable := agentNamed(t.Context(), t, svc, "unreadable")
				tc.arrange(t, svc, unreadable.ID)

				resp, err := svc.Apply(t.Context(), ApplyBuildRequest{BuilderSessionID: sessionA, DryRun: dryRun}, remover())
				if err != nil {
					t.Fatalf("empty plan: %v", err)
				}
				if !resp.DroppedCheckFailed {
					t.Fatalf("droppedCheckFailed = false with dropped = %+v: a read failed, and the answer must not read as complete", resp.Dropped)
				}
				if slices.ContainsFunc(resp.Dropped, func(d DroppedResource) bool { return d.ID == unreadable.ID }) {
					t.Errorf("dropped = %+v lists a resource that could not be read: its state is unknown, so it is not claimed", resp.Dropped)
				}
				if !sessionRecordHolds(t, svc, unreadable.ID) {
					t.Error("the session record forgot a resource it could not read")
				}
			})
		}
	}
}
