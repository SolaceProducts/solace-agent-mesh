// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestConnectorRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewConnectorRepo(db)
	ctx := context.Background()

	desc := "A test SQL connector for testing"
	subtype := "postgres"
	vals := `{"host":"localhost","port":5432}`
	connector := &Connector{
		Name:        "test-connector",
		Description: &desc,
		Type:        "sql",
		Subtype:     &subtype,
		Values:      &vals,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	err := repo.Create(ctx, connector)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if connector.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.GetByID(ctx, connector.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected connector")
	}
	if got.Name != "test-connector" {
		t.Errorf("name = %q, want %q", got.Name, "test-connector")
	}
	if got.Type != "sql" {
		t.Errorf("type = %q, want %q", got.Type, "sql")
	}

	list, total, err := repo.ListPaginated(ctx, 1, 10, ConnectorFilter{}, ConnectorOrder{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Errorf("total=%d len=%d, want 1,1", total, len(list))
	}

	_, total, err = repo.ListPaginated(ctx, 1, 10, ConnectorFilter{Type: "sql"}, ConnectorOrder{})
	if err != nil {
		t.Fatalf("list with filter: %v", err)
	}
	if total != 1 {
		t.Errorf("filtered total = %d, want 1", total)
	}

	_, total, err = repo.ListPaginated(ctx, 1, 10, ConnectorFilter{Type: "openapi"}, ConnectorOrder{})
	if err != nil {
		t.Fatalf("list with no-match filter: %v", err)
	}
	if total != 0 {
		t.Errorf("filtered total = %d, want 0", total)
	}

	got.Name = "renamed-connector"
	got.UpdatedBy = "user2"
	err = repo.Update(ctx, got)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got2, err := repo.GetByID(ctx, connector.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got2.Name != "renamed-connector" {
		t.Errorf("name = %q, want %q", got2.Name, "renamed-connector")
	}

	all, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("all len = %d, want 1", len(all))
	}

	err = repo.Delete(ctx, connector.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	gone, err := repo.GetByID(ctx, connector.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if gone != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestConnectorRepo_ListPaginated_NameFilter(t *testing.T) {
	db := TestDB(t)
	repo := NewConnectorRepo(db)
	ctx := context.Background()

	subtype := "postgres"
	vals := `{}`
	desc := "test connector"
	for _, name := range []string{"Slack", "Slack Webhook", "GitHub API", "Stripe Payments", "50% Discount", "snake_case Conn", `back\slash Conn`} {
		c := &Connector{
			Name: name, Description: &desc, Type: "sql", Subtype: &subtype, Values: &vals,
			CreatedBy: "u", UpdatedBy: "u",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	cases := []struct {
		name   string
		filter string
		want   int
	}{
		{"empty matches all", "", 7},
		{"case-insensitive prefix", "slack", 2},
		{"case-insensitive uppercase", "SLACK", 2},
		{"substring middle", "ub", 1},
		{"no match", "nonexistent", 0},
		{"percent matches only the literal percent", "50%", 1},
		{"underscore matches only the literal underscore", "snake_case", 1},
		{"backslash matches only the literal backslash", `back\slash`, 1},
		{"a bare wildcard is not a wildcard", "%", 1},
		{"a bare underscore is not a wildcard", "_", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list, total, err := repo.ListPaginated(ctx, 1, 10, ConnectorFilter{Name: tc.filter}, ConnectorOrder{})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if total != tc.want || len(list) != tc.want {
				t.Errorf("filter=%q total=%d len=%d, want %d", tc.filter, total, len(list), tc.want)
			}
		})
	}
}

func TestConnectorRepo_AgentCounts(t *testing.T) {
	db := TestDB(t)
	connRepo := NewConnectorRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := context.Background()

	desc := "Connector for agent count test"
	subtype := "postgres"
	connector := &Connector{Name: "conn1", Description: &desc, Type: "sql", Subtype: &subtype, Values: new("{}"), CreatedBy: "u", UpdatedBy: "u"}
	if err := connRepo.Create(ctx, connector); err != nil {
		t.Fatalf("create connector: %v", err)
	}

	agentDesc := "Agent for agent count testing"
	agent := &Agent{Name: "agent1", Description: &agentDesc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, []string{connector.ID}, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	counts, err := connRepo.GetAgentCounts(ctx, []string{connector.ID})
	if err != nil {
		t.Fatalf("get agent counts: %v", err)
	}
	if counts[connector.ID] != 1 {
		t.Errorf("count = %d, want 1", counts[connector.ID])
	}
}

func seedConnectorsOfType(t *testing.T, repo *ConnectorRepo, prefix, connectorType string, n int) {
	t.Helper()
	for i := range n {
		c := &Connector{Name: fmt.Sprintf("%s-%d", prefix, i), Type: connectorType, CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(context.Background(), c); err != nil {
			t.Fatalf("seed %s-%d: %v", prefix, i, err)
		}
	}
}

func TestListPaginated_ExcludedTypesLeaveCountAndPagesCoherent(t *testing.T) {
	repo := NewConnectorRepo(TestDB(t))
	seedConnectorsOfType(t, repo, "visible", "api", 5)
	seedConnectorsOfType(t, repo, "hidden", "event_mesh", 7)

	for page, wantRows := range map[int]int{1: 2, 2: 2, 3: 1} {
		list, total, err := repo.ListPaginated(t.Context(), page, 2, ConnectorFilter{ExcludeTypes: []string{"event_mesh"}}, ConnectorOrder{})
		if err != nil {
			t.Fatalf("ListPaginated page %d: %v", page, err)
		}
		if len(list) != wantRows {
			t.Errorf("page %d: got %d rows, want %d", page, len(list), wantRows)
		}
		if total != 5 {
			t.Errorf("page %d: total = %d, want 5: the count must describe the filtered set, not the table", page, total)
		}
		for _, c := range list {
			if c.Type == "event_mesh" {
				t.Errorf("page %d returned excluded connector %q", page, c.Name)
			}
		}
	}
}

func TestListPaginated_APageOfOnlyExcludedTypesIsEmptyAndSaysSo(t *testing.T) {
	repo := NewConnectorRepo(TestDB(t))
	seedConnectorsOfType(t, repo, "hidden", "event_mesh", 3)

	list, total, err := repo.ListPaginated(t.Context(), 1, 20, ConnectorFilter{ExcludeTypes: []string{"event_mesh"}}, ConnectorOrder{})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if len(list) != 0 || total != 0 {
		t.Errorf("rows=%d total=%d, want 0 and 0: an all-hidden result must not promise a page that does not exist", len(list), total)
	}
}

func TestListPaginated_NilAndEmptyExclusionKeepEveryRow(t *testing.T) {
	repo := NewConnectorRepo(TestDB(t))
	seedConnectorsOfType(t, repo, "a", "api", 2)
	seedConnectorsOfType(t, repo, "b", "event_mesh", 2)

	for name, exclude := range map[string][]string{"nil": nil, "empty": {}} {
		_, total, err := repo.ListPaginated(t.Context(), 1, 20, ConnectorFilter{ExcludeTypes: exclude}, ConnectorOrder{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if total != 4 {
			t.Errorf("%s: total = %d, want 4", name, total)
		}
	}
}

func TestListPaginated_ExcludedTypesCombineWithOtherFilters(t *testing.T) {
	repo := NewConnectorRepo(TestDB(t))
	seedConnectorsOfType(t, repo, "alpha", "api", 2)
	seedConnectorsOfType(t, repo, "alpha-mesh", "event_mesh", 2)
	seedConnectorsOfType(t, repo, "alpha-sql", "sql", 1)
	seedConnectorsOfType(t, repo, "beta", "api", 3)

	list, total, err := repo.ListPaginated(t.Context(), 1, 20, ConnectorFilter{Name: "alpha", ExcludeTypes: []string{"event_mesh", "sql"}}, ConnectorOrder{})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if len(list) != 2 || total != 2 {
		t.Errorf("rows=%d total=%d, want 2 and 2 (name filter AND two excluded types)", len(list), total)
	}
}

func TestListPaginated_OrdersByTheFieldAsked(t *testing.T) {
	repo := NewConnectorRepo(TestDB(t))
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		c := &Connector{Name: name, Type: "api", CreatedBy: "u", UpdatedBy: "u"}
		if err := repo.Create(t.Context(), c); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	for _, tc := range []struct {
		name  string
		order ConnectorOrder
		want  []string
	}{
		{"zero value is newest first", ConnectorOrder{}, []string{"bravo", "alpha", "charlie"}},
		{"name ascending", ConnectorOrder{Field: ConnectorSortName}, []string{"alpha", "bravo", "charlie"}},
		{"name descending", ConnectorOrder{Field: ConnectorSortName, Desc: true}, []string{"charlie", "bravo", "alpha"}},
		{"oldest first", ConnectorOrder{Field: ConnectorSortCreatedTime}, []string{"charlie", "alpha", "bravo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list, _, err := repo.ListPaginated(t.Context(), 1, 20, ConnectorFilter{}, tc.order)
			if err != nil {
				t.Fatalf("ListPaginated: %v", err)
			}
			got := make([]string, len(list))
			for i, c := range list {
				got[i] = c.Name
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("names = %v, want %v", got, tc.want)
			}
		})
	}
}
