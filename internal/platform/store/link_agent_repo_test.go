// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	linkTestEntrypointID      = "0190f5ec-0000-7000-8000-00000000e001"
	linkTestOtherEntrypointID = "0190f5ec-0000-7000-8000-00000000e002"
	linkTestAgentID           = "0190f5ec-0000-7000-8000-00000000a001"
	linkTestOtherAgentID      = "0190f5ec-0000-7000-8000-00000000a002"
	linkTestDigest            = "abababababababababababababababababababababababababababababababab"
	linkTestOtherDigest       = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
)

func seedLinkEntrypoint(t *testing.T, db *DB, id, name string) {
	t.Helper()
	gw := &Gateway{ID: id, Name: name, Type: "agent_link", CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com"}
	if err := NewGatewayRepo(db).Create(t.Context(), gw); err != nil {
		t.Fatalf("seed entrypoint %q: %v", name, err)
	}
}

func fullLinkAgent() LinkAgent {
	return LinkAgent{
		ID:               linkTestAgentID,
		Name:             "Claims Desk",
		Description:      "Answers claims questions for the partner.",
		EntrypointID:     linkTestEntrypointID,
		TokenSHA256:      linkTestDigest,
		TokenPrefix:      "Ab3dE-ex",
		Enabled:          true,
		Owner:            "alice@example.com",
		Team:             "Claims Operations",
		Peers:            []string{"policy-desk", "agent_0190f5ec_0000_7000_8000_00000000a002"},
		ShareUserProfile: true,
		Wake: &LinkAgentWake{
			URL:            "https://runtime.example.com/invocations",
			TokenURL:       "https://idp.example.com/oauth2/token",
			ClientID:       "claims-client",
			ClientSecret:   "wake-client-secret",
			Scope:          "https://runtime.example.com/.default",
			Headers:        map[string]string{"X-Session": "claims"},
			TimeoutSeconds: 600,
			OnStart:        true,
		},
		CreatedBy:   "alice@example.com",
		UpdatedBy:   "alice@example.com",
		CreatedTime: 1767322800000,
		UpdatedTime: 1767322800000,
	}
}

func bareLinkAgent() LinkAgent {
	return LinkAgent{
		ID:           linkTestAgentID,
		Name:         "Claims Desk",
		EntrypointID: linkTestEntrypointID,
		TokenSHA256:  linkTestDigest,
		Peers:        []string{},
		CreatedBy:    "alice@example.com",
		UpdatedBy:    "alice@example.com",
		CreatedTime:  1767322800000,
		UpdatedTime:  1767322800000,
	}
}

func TestLinkAgentRepo_CreateStoresTheAgentAndItsMirror(t *testing.T) {
	tests := []struct {
		name  string
		agent LinkAgent
	}{
		{name: "every setting", agent: fullLinkAgent()},
		{name: "only what is required", agent: bareLinkAgent()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := TestDB(t)
			seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
			created := tt.agent

			if err := NewLinkAgentRepo(db).Create(t.Context(), &created); err != nil {
				t.Fatalf("Create(%q) = %v", tt.agent.Name, err)
			}

			got, err := NewLinkAgentRepo(db).GetByID(t.Context(), tt.agent.ID)
			if err != nil {
				t.Fatalf("GetByID(%s) = %v", tt.agent.ID, err)
			}
			if diff := cmp.Diff(&tt.agent, got); diff != "" {
				t.Errorf("GetByID(%s) mismatch (-want +got):\n%s", tt.agent.ID, diff)
			}
			mirror, _, _, _, _, err := NewAgentRepo(db).GetByID(t.Context(), tt.agent.ID)
			if err != nil || mirror == nil {
				t.Fatalf("mirror of %s = %v, %v", tt.agent.ID, mirror, err)
			}
			if mirror.Name != tt.agent.Name || mirror.Type != "link" || *mirror.Description != tt.agent.Description {
				t.Errorf("mirror = name %q type %q description %q, want name %q type link description %q",
					mirror.Name, mirror.Type, *mirror.Description, tt.agent.Name, tt.agent.Description)
			}
		})
	}
}

func TestLinkAgentRepo_CreateRefusesANameAnotherAgentHolds(t *testing.T) {
	tests := []struct {
		name     string
		existing string
	}{
		{name: "the same name", existing: "Claims Desk"},
		{name: "a name that differs in case and spaces", existing: "claimsdesk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := TestDB(t)
			seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
			createStandardAgent(t, db, tt.existing)
			agent := bareLinkAgent()

			err := NewLinkAgentRepo(db).Create(t.Context(), &agent)

			if !IsAgentNameViolation(err) {
				t.Errorf("Create(%q) beside agent %q = %v, want an agent name violation", agent.Name, tt.existing, err)
			}
			if n := countRows(t, db, `SELECT COUNT(*) FROM link_agents`); n != 0 {
				t.Errorf("link_agents rows after a refused create = %d, want 0", n)
			}
		})
	}
}

func TestLinkAgentRepo_CreateRefusesATokenAnotherLinkAgentHolds(t *testing.T) {
	db := TestDB(t)
	seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
	first := bareLinkAgent()
	if err := NewLinkAgentRepo(db).Create(t.Context(), &first); err != nil {
		t.Fatalf("Create(first) = %v", err)
	}
	second := bareLinkAgent()
	second.ID, second.Name = linkTestOtherAgentID, "Policy Desk"

	err := NewLinkAgentRepo(db).Create(t.Context(), &second)

	if !dbutil.IsUniqueViolation(err) {
		t.Errorf("Create(second with the first's token digest) = %v, want a unique violation", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM agents WHERE id = ?`, linkTestOtherAgentID); n != 0 {
		t.Errorf("mirror rows of the refused link agent = %d, want 0", n)
	}
}

func TestLinkAgentRepo_UpdateReplacesSettingsAndFollowsTheMirror(t *testing.T) {
	db := TestDB(t)
	seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
	seedLinkEntrypoint(t, db, linkTestOtherEntrypointID, "Second link")
	created := fullLinkAgent()
	if err := NewLinkAgentRepo(db).Create(t.Context(), &created); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	update := bareLinkAgent()
	update.Name, update.Description = "Policy Desk", "Answers policy questions."
	update.EntrypointID = linkTestOtherEntrypointID
	update.UpdatedBy = "bob@example.com"

	if err := NewLinkAgentRepo(db).Update(t.Context(), &update); err != nil {
		t.Fatalf("Update() = %v", err)
	}

	got, err := NewLinkAgentRepo(db).GetByID(t.Context(), linkTestAgentID)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	want := bareLinkAgent()
	want.Name, want.Description = "Policy Desk", "Answers policy questions."
	want.EntrypointID = linkTestOtherEntrypointID
	want.TokenPrefix = "Ab3dE-ex"
	want.UpdatedBy = "bob@example.com"
	want.UpdatedTime = got.UpdatedTime
	if diff := cmp.Diff(&want, got); diff != "" {
		t.Errorf("GetByID() after Update mismatch (-want +got):\n%s", diff)
	}
	if got.UpdatedTime <= 1767322800000 {
		t.Errorf("UpdatedTime after Update = %d, want it later than the creation time 1767322800000", got.UpdatedTime)
	}
	mirror, _, _, _, _, err := NewAgentRepo(db).GetByID(t.Context(), linkTestAgentID)
	if err != nil || mirror == nil {
		t.Fatalf("mirror = %v, %v", mirror, err)
	}
	if mirror.Name != "Policy Desk" || *mirror.Description != "Answers policy questions." || mirror.UpdatedBy != "bob@example.com" {
		t.Errorf("mirror after Update = name %q description %q updatedBy %q, want the update's", mirror.Name, *mirror.Description, mirror.UpdatedBy)
	}
}

func TestLinkAgentRepo_OnlyUpdateWithTokenReplacesTheStoredDigest(t *testing.T) {
	tests := []struct {
		name       string
		write      func(*LinkAgentRepo, *LinkAgent) error
		wantDigest string
		wantPrefix string
	}{
		{
			name:       "Update keeps the stored digest and prefix",
			write:      func(r *LinkAgentRepo, a *LinkAgent) error { return r.Update(t.Context(), a) },
			wantDigest: linkTestDigest,
			wantPrefix: "Ab3dE-ex",
		},
		{
			name:       "UpdateWithToken stores the digest and prefix it is given",
			write:      func(r *LinkAgentRepo, a *LinkAgent) error { return r.UpdateWithToken(t.Context(), a) },
			wantDigest: linkTestOtherDigest,
			wantPrefix: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := TestDB(t)
			seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
			created := fullLinkAgent()
			if err := NewLinkAgentRepo(db).Create(t.Context(), &created); err != nil {
				t.Fatalf("Create() = %v", err)
			}
			update := fullLinkAgent()
			update.TokenSHA256, update.TokenPrefix = linkTestOtherDigest, ""

			if err := tt.write(NewLinkAgentRepo(db), &update); err != nil {
				t.Fatalf("write = %v", err)
			}

			got, err := NewLinkAgentRepo(db).GetByID(t.Context(), linkTestAgentID)
			if err != nil {
				t.Fatalf("GetByID() = %v", err)
			}
			if got.TokenSHA256 != tt.wantDigest || got.TokenPrefix != tt.wantPrefix {
				t.Errorf("stored digest matches the expected one: %t, prefix %q, want prefix %q", got.TokenSHA256 == tt.wantDigest, got.TokenPrefix, tt.wantPrefix)
			}
		})
	}
}

func TestLinkAgentRepo_UpdateWithTokenRefusesADigestAnotherLinkAgentHolds(t *testing.T) {
	db := TestDB(t)
	seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
	repo := NewLinkAgentRepo(db)
	first := bareLinkAgent()
	if err := repo.Create(t.Context(), &first); err != nil {
		t.Fatalf("Create(first) = %v", err)
	}
	second := bareLinkAgent()
	second.ID, second.Name, second.TokenSHA256 = linkTestOtherAgentID, "Policy Desk", linkTestOtherDigest
	if err := repo.Create(t.Context(), &second); err != nil {
		t.Fatalf("Create(second) = %v", err)
	}
	for digest, want := range map[string]bool{linkTestDigest: true, linkTestOtherDigest: false} {
		if held, err := repo.TokenDigestHeldByAnother(t.Context(), digest, linkTestOtherAgentID); err != nil || held != want {
			t.Errorf("TokenDigestHeldByAnother(first's digest: %t, except the second) = %t, %v, want %t", digest == linkTestDigest, held, err, want)
		}
	}
	second.TokenSHA256 = linkTestDigest

	err := repo.UpdateWithToken(t.Context(), &second)

	if !IsLinkAgentTokenViolation(err) {
		t.Errorf("UpdateWithToken(the first's digest) = %v, want a token digest violation", err)
	}
	if got, _ := repo.GetByID(t.Context(), linkTestOtherAgentID); got == nil || got.TokenSHA256 != linkTestOtherDigest {
		t.Errorf("the second link agent's digest changed after a refused update")
	}
}

func TestLinkAgentRepo_SetTokenReplacesOnlyTheToken(t *testing.T) {
	db := TestDB(t)
	seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
	created := fullLinkAgent()
	if err := NewLinkAgentRepo(db).Create(t.Context(), &created); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := NewLinkAgentRepo(db).SetToken(t.Context(), linkTestAgentID, linkTestOtherDigest, "Zz9yX-wv", "bob@example.com"); err != nil {
		t.Fatalf("SetToken() = %v", err)
	}

	got, err := NewLinkAgentRepo(db).GetByID(t.Context(), linkTestAgentID)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	want := fullLinkAgent()
	want.TokenSHA256, want.TokenPrefix, want.UpdatedBy = linkTestOtherDigest, "Zz9yX-wv", "bob@example.com"
	want.UpdatedTime = got.UpdatedTime
	if diff := cmp.Diff(&want, got); diff != "" {
		t.Errorf("GetByID() after SetToken mismatch (-want +got):\n%s", diff)
	}
}

func TestLinkAgentRepo_WritesToAMissingLinkAgentReportNotFound(t *testing.T) {
	db := TestDB(t)
	missing := bareLinkAgent()
	repo := NewLinkAgentRepo(db)
	for name, err := range map[string]error{
		"Update":   repo.Update(t.Context(), &missing),
		"SetToken": repo.SetToken(t.Context(), linkTestAgentID, linkTestDigest, "", "bob@example.com"),
		"Delete":   repo.Delete(t.Context(), linkTestAgentID),
	} {
		if !errors.Is(err, ErrLinkAgentNotFound) {
			t.Errorf("%s(missing link agent) = %v, want ErrLinkAgentNotFound", name, err)
		}
	}
	if got, err := repo.GetByID(t.Context(), linkTestAgentID); got != nil || err != nil {
		t.Errorf("GetByID(missing link agent) = %v, %v, want nil, nil", got, err)
	}
}

func TestLinkAgentRepo_DeleteRemovesTheAgentAndItsMirrorOnly(t *testing.T) {
	db := TestDB(t)
	seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
	createStandardAgent(t, db, "Standard Agent")
	created := fullLinkAgent()
	if err := NewLinkAgentRepo(db).Create(t.Context(), &created); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := NewLinkAgentRepo(db).Delete(t.Context(), linkTestAgentID); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	if n := countRows(t, db, `SELECT COUNT(*) FROM link_agents`); n != 0 {
		t.Errorf("link_agents rows after Delete = %d, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM agents`); n != 1 {
		t.Errorf("agents rows after Delete = %d, want only the standard agent", n)
	}
}

func TestLinkAgentRepo_ListsLinkAgentsNewestFirstAndByEntrypoint(t *testing.T) {
	db := TestDB(t)
	seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
	seedLinkEntrypoint(t, db, linkTestOtherEntrypointID, "Second link")
	older := bareLinkAgent()
	newer := bareLinkAgent()
	newer.ID, newer.Name, newer.TokenSHA256 = linkTestOtherAgentID, "Policy Desk", linkTestOtherDigest
	newer.EntrypointID, newer.CreatedTime = linkTestOtherEntrypointID, 1767322900000
	repo := NewLinkAgentRepo(db)
	for _, a := range []*LinkAgent{&older, &newer} {
		if err := repo.Create(t.Context(), a); err != nil {
			t.Fatalf("Create(%q) = %v", a.Name, err)
		}
	}
	idsOf := func(agents []*LinkAgent) []string {
		ids := make([]string, 0, len(agents))
		for _, a := range agents {
			ids = append(ids, a.ID)
		}
		return ids
	}

	page, total, err := repo.ListPaginated(t.Context(), 1, 1)
	if err != nil || total != 2 {
		t.Fatalf("ListPaginated(1, 1) total = %d, %v, want 2", total, err)
	}
	if diff := cmp.Diff([]string{linkTestOtherAgentID}, idsOf(page)); diff != "" {
		t.Errorf("ListPaginated(1, 1) mismatch (-want +got):\n%s", diff)
	}
	all, err := repo.ListAll(t.Context())
	if err != nil {
		t.Fatalf("ListAll() = %v", err)
	}
	if diff := cmp.Diff([]string{linkTestOtherAgentID, linkTestAgentID}, idsOf(all)); diff != "" {
		t.Errorf("ListAll() mismatch (-want +got):\n%s", diff)
	}
	assigned, err := repo.ListByEntrypoint(t.Context(), linkTestEntrypointID)
	if err != nil {
		t.Fatalf("ListByEntrypoint() = %v", err)
	}
	if diff := cmp.Diff([]string{linkTestAgentID}, idsOf(assigned)); diff != "" {
		t.Errorf("ListByEntrypoint(%s) mismatch (-want +got):\n%s", linkTestEntrypointID, diff)
	}
}

func TestAgentRepo_ListPaginatedLeavesLinkAgentsToTheirOwnList(t *testing.T) {
	db := TestDB(t)
	seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
	createStandardAgent(t, db, "Standard Agent")
	created := bareLinkAgent()
	if err := NewLinkAgentRepo(db).Create(t.Context(), &created); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	agents, total, err := NewAgentRepo(db).ListPaginated(t.Context(), 1, 20)

	if err != nil || total != 1 || len(agents) != 1 {
		t.Fatalf("ListPaginated() = %d agents, total %d, %v, want the standard agent alone", len(agents), total, err)
	}
	if agents[0].Agent.Name != "Standard Agent" {
		t.Errorf("ListPaginated() listed %q, want Standard Agent", agents[0].Agent.Name)
	}
}

type linkDeploymentSeed struct {
	id            string
	agentID       string
	action        string
	status        string
	failureReason string
	createdTime   int64
}

func TestLinkAgentRepo_DeliveredDeployments(t *testing.T) {
	const (
		deployOne   = "0190f5ec-0000-7000-8000-0000000d0001"
		deployTwo   = "0190f5ec-0000-7000-8000-0000000d0002"
		deployThree = "0190f5ec-0000-7000-8000-0000000d0003"
	)
	tests := []struct {
		name        string
		deployments []linkDeploymentSeed
		want        map[string]string
	}{
		{
			name:        "no deployment",
			deployments: nil,
			want:        map[string]string{},
		},
		{
			name: "a successful deploy",
			deployments: []linkDeploymentSeed{
				{id: deployOne, agentID: linkTestAgentID, action: "deploy", status: "success", createdTime: 100},
			},
			want: map[string]string{linkTestAgentID: deployOne},
		},
		{
			name: "the newest successful row wins, an undeploy included",
			deployments: []linkDeploymentSeed{
				{id: deployOne, agentID: linkTestAgentID, action: "deploy", status: "success", createdTime: 100},
				{id: deployTwo, agentID: linkTestAgentID, action: "undeploy", status: "success", createdTime: 200},
			},
			want: map[string]string{linkTestAgentID: deployTwo},
		},
		{
			name: "a rejected update leaves the earlier deploy delivered",
			deployments: []linkDeploymentSeed{
				{id: deployOne, agentID: linkTestAgentID, action: "deploy", status: "success", createdTime: 100},
				{id: deployTwo, agentID: linkTestAgentID, action: "update", status: "failed", failureReason: "rejected", createdTime: 200},
			},
			want: map[string]string{linkTestAgentID: deployOne},
		},
		{
			name: "an update the runtime did not confirm leaves the earlier deploy delivered",
			deployments: []linkDeploymentSeed{
				{id: deployOne, agentID: linkTestAgentID, action: "deploy", status: "success", createdTime: 100},
				{id: deployTwo, agentID: linkTestAgentID, action: "update", status: "failed", failureReason: "runtime_error", createdTime: 200},
			},
			want: map[string]string{linkTestAgentID: deployOne},
		},
		{
			name: "an undeploy the runtime did not confirm still takes the agent down",
			deployments: []linkDeploymentSeed{
				{id: deployOne, agentID: linkTestAgentID, action: "deploy", status: "success", createdTime: 100},
				{id: deployTwo, agentID: linkTestAgentID, action: "undeploy", status: "failed", failureReason: "runtime_error", createdTime: 200},
			},
			want: map[string]string{linkTestAgentID: deployTwo},
		},
		{
			name: "a rejected undeploy leaves the deploy delivered",
			deployments: []linkDeploymentSeed{
				{id: deployOne, agentID: linkTestAgentID, action: "deploy", status: "success", createdTime: 100},
				{id: deployTwo, agentID: linkTestAgentID, action: "undeploy", status: "failed", failureReason: "rejected", createdTime: 200},
			},
			want: map[string]string{linkTestAgentID: deployOne},
		},
		{
			name: "each link agent has its own and a standard agent has none",
			deployments: []linkDeploymentSeed{
				{id: deployOne, agentID: linkTestAgentID, action: "deploy", status: "success", createdTime: 100},
				{id: deployTwo, agentID: linkTestOtherAgentID, action: "deploy", status: "success", createdTime: 200},
				{id: deployThree, agentID: "0190f5ec-0000-7000-8000-00000000a003", action: "deploy", status: "success", createdTime: 300},
			},
			want: map[string]string{linkTestAgentID: deployOne, linkTestOtherAgentID: deployTwo},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := TestDB(t)
			seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
			first := bareLinkAgent()
			second := bareLinkAgent()
			second.ID, second.Name, second.TokenSHA256 = linkTestOtherAgentID, "Policy Desk", linkTestOtherDigest
			repo := NewLinkAgentRepo(db)
			for _, a := range []*LinkAgent{&first, &second} {
				if err := repo.Create(t.Context(), a); err != nil {
					t.Fatalf("Create(%q) = %v", a.Name, err)
				}
			}
			standard := &Agent{ID: "0190f5ec-0000-7000-8000-00000000a003", Name: "Standard Agent", CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com"}
			if err := NewAgentRepo(db).Create(t.Context(), standard, nil, nil, nil, nil, nil); err != nil {
				t.Fatalf("seed the standard agent: %v", err)
			}
			for _, d := range tt.deployments {
				row := &AgentDeployment{ID: d.id, AgentID: d.agentID, Action: d.action, Status: d.status, AgentConfigSnapshot: new("{}"),
					CreatedBy: "alice@example.com", CreatedTime: d.createdTime, UpdatedTime: d.createdTime}
				if d.failureReason != "" {
					row.FailureReason = new(d.failureReason)
				}
				if err := NewAgentDeploymentRepo(db).Create(t.Context(), row); err != nil {
					t.Fatalf("seed deployment %s: %v", d.id, err)
				}
			}

			delivered, err := repo.DeliveredDeployments(t.Context())

			if err != nil {
				t.Fatalf("DeliveredDeployments() = %v", err)
			}
			got := make(map[string]string, len(delivered))
			for agentID, d := range delivered {
				got[agentID] = d.ID
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("DeliveredDeployments() mismatch (-want +got):\n%s", diff)
			}
			ids, err := repo.IDs(t.Context())
			if err != nil {
				t.Fatalf("IDs() = %v", err)
			}
			if diff := cmp.Diff([]string{linkTestAgentID, linkTestOtherAgentID}, ids); diff != "" {
				t.Errorf("IDs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLinkAgentsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "link_agents", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		seedLinkEntrypoint(t, db, linkTestEntrypointID, "Partner link")
		createStandardAgent(t, db, "Standard Agent")
		created := fullLinkAgent()
		if err := NewLinkAgentRepo(db).Create(ctx, &created); err != nil {
			t.Fatalf("create a link agent after up: %v", err)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		if ledgerTableExists(t, db, "link_agents") {
			t.Error("after down: link_agents survived the rollback")
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM agents WHERE type = 'link'`); n != 0 {
			t.Errorf("after down: %d link agent mirrors survived the rollback, want 0", n)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM agents WHERE name = 'Standard Agent'`); n != 1 {
			t.Errorf("after down: standard agents kept = %d, want 1", n)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		recreated := fullLinkAgent()
		if err := NewLinkAgentRepo(db).Create(ctx, &recreated); err != nil {
			t.Errorf("create a link agent after re-apply: %v", err)
		}
	})
}
