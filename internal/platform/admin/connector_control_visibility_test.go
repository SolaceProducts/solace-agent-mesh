// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func emailGateRegistry(t *testing.T, enabled bool) *features.Registry {
	t.Helper()
	t.Setenv("SAM_FEATURE_EMAIL_CONNECTOR", "")
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "email_connector", ReleasePhase: features.PhaseExperimental, Default: enabled})
	return reg
}

func seedConnectorRow(t *testing.T, repo *store.ConnectorRepo, name, connType, subtype string) string {
	t.Helper()
	values := "{}"
	conn := &store.Connector{Name: name, Type: connType, Subtype: &subtype, Values: &values, CreatedBy: "tester", UpdatedBy: "tester"}
	if err := repo.Create(t.Context(), conn); err != nil {
		t.Fatalf("seed %s connector: %v", connType, err)
	}
	return conn.ID
}

func controlConnectorTypes(t *testing.T, env *controlTestEnv) (types []string, count int) {
	t.Helper()
	raw, err := env.Client.Custom(t.Context(), "platform", "get", "connectors", nil)
	if err != nil {
		t.Fatalf("list connectors: %v", err)
	}
	var list PaginatedResponse[ConnectorResponse]
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, c := range list.Data {
		types = append(types, c.Type)
	}
	return types, list.Meta.Pagination.Count
}

func controlSchemaTypes(t *testing.T, env *controlTestEnv) []string {
	t.Helper()
	raw, err := env.Client.Custom(t.Context(), "platform", "get", "connectors/schemas", nil)
	if err != nil {
		t.Fatalf("get connector schemas: %v", err)
	}
	var schemas []ConnectorSchemaResponse
	if err := json.Unmarshal(raw, &schemas); err != nil {
		t.Fatalf("decode schemas: %v", err)
	}
	var types []string
	for _, s := range schemas {
		types = append(types, s.Type)
	}
	return types
}

func TestControlProtocol_ConnectorListingsFollowTheTypeFeatureFlag(t *testing.T) {
	for _, tc := range []struct {
		name      string
		registry  func(t *testing.T) *features.Registry
		wantEmail bool
	}{
		{"flag off", func(t *testing.T) *features.Registry { return emailGateRegistry(t, false) }, false},
		{"flag on", func(t *testing.T) *features.Registry { return emailGateRegistry(t, true) }, true},
		{"no registry hides gated types", func(*testing.T) *features.Registry { return nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupControlTestWithDeps(t, func(d *RouteDeps) { d.FeatureRegistry = tc.registry(t) })
			seedConnectorRow(t, env.ConnectorRepo, "warehouse", "sql", "postgres")
			emailID := seedConnectorRow(t, env.ConnectorRepo, "mailer", "email", "smtp")

			if got := slices.Contains(controlSchemaTypes(t, env), "email"); got != tc.wantEmail {
				t.Errorf("get connectors/schemas lists email = %v, want %v", got, tc.wantEmail)
			}
			if !slices.Contains(controlSchemaTypes(t, env), "sql") {
				t.Error("get connectors/schemas dropped sql, which is not gated")
			}

			types, count := controlConnectorTypes(t, env)
			if got := slices.Contains(types, "email"); got != tc.wantEmail {
				t.Errorf("get connectors lists the email row = %v, want %v (types %v)", got, tc.wantEmail, types)
			}
			if !slices.Contains(types, "sql") {
				t.Errorf("get connectors dropped the sql row: %v", types)
			}
			if count != len(types) {
				t.Errorf("pagination count = %d for %d rows: the meta must describe what is returned", count, len(types))
			}

			if _, err := env.Client.Custom(t.Context(), "platform", "get", "connectors/"+emailID, nil); err != nil {
				t.Errorf("get connectors/{id} of a gated type = %v, want it still served", err)
			}
		})
	}
}
