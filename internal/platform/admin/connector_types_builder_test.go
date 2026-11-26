// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func builderTypeByName(types []BuilderConnectorType, name string) (BuilderConnectorType, bool) {
	for _, bt := range types {
		if bt.Type == name {
			return bt, true
		}
	}
	return BuilderConnectorType{}, false
}

func TestBuilderConnectorTypes_AreTheEnabledTypesWithSubtypeDescriptions(t *testing.T) {
	t.Run("email off", func(t *testing.T) {
		types := builderConnectorTypes(emailGateRegistry(t, false))
		if _, ok := builderTypeByName(types, "email"); ok {
			t.Error("email is listed while email_connector is off")
		}
		sql, ok := builderTypeByName(types, "sql")
		if !ok {
			t.Fatal("sql is missing")
		}
		var subtypes []string
		for _, st := range sql.Subtypes {
			subtypes = append(subtypes, st.Subtype)
		}
		if !slices.Contains(subtypes, "postgres") || !slices.Contains(subtypes, "mysql") {
			t.Errorf("sql subtypes = %v, want postgres and mysql among them", subtypes)
		}
	})

	t.Run("email on", func(t *testing.T) {
		types := builderConnectorTypes(emailGateRegistry(t, true))
		email, ok := builderTypeByName(types, "email")
		if !ok || len(email.Subtypes) != 1 || email.Subtypes[0].Subtype != "smtp" {
			t.Fatalf("email = %+v, want the smtp subtype", email)
		}
		if email.Subtypes[0].Description != "Send emails via SMTP server" {
			t.Errorf("description = %q, want the subtype YAML's description", email.Subtypes[0].Description)
		}
	})

	t.Run("every type has subtypes with descriptions", func(t *testing.T) {
		types := builderConnectorTypes(emailGateRegistry(t, true))
		if len(types) == 0 {
			t.Fatal("no connector types")
		}
		for _, bt := range types {
			if len(bt.Subtypes) == 0 {
				t.Errorf("%s has no subtypes", bt.Type)
			}
			for _, st := range bt.Subtypes {
				if st.Description == "" {
					t.Errorf("%s/%s has no description", bt.Type, st.Subtype)
				}
			}
		}
	})

	t.Run("a registry that knows no flag hides every gated type but never an ungated one", func(t *testing.T) {
		types := builderConnectorTypes(nil)
		for _, gated := range []string{"email", "event_mesh", "document_db", "graph_db", "search"} {
			if _, ok := builderTypeByName(types, gated); ok {
				t.Errorf("%s is listed with no registry", gated)
			}
		}
		for _, always := range []string{"api", "mcp", "sql"} {
			if _, ok := builderTypeByName(types, always); !ok {
				t.Errorf("%s is missing: it is not feature-gated", always)
			}
		}
	})
}

func TestControlProtocol_ConnectorTypesIsAListEnvelopeOfEnabledTypes(t *testing.T) {
	env := setupControlTestWithDeps(t, func(d *RouteDeps) { d.FeatureRegistry = emailGateRegistry(t, false) })

	raw, err := env.Client.Custom(t.Context(), "platform", "get", "connectors/types", nil)
	if err != nil {
		t.Fatalf("get connectors/types: %v", err)
	}
	var list PaginatedResponse[BuilderConnectorType]
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	if len(list.Data) == 0 || list.Meta.Pagination.Count != len(list.Data) {
		t.Fatalf("data has %d entries, count %d", len(list.Data), list.Meta.Pagination.Count)
	}
	if _, ok := builderTypeByName(list.Data, "email"); ok {
		t.Error("the control action lists email while email_connector is off")
	}
	if _, ok := builderTypeByName(list.Data, "sql"); !ok {
		t.Error("the control action dropped sql")
	}
	if !strings.Contains(string(raw), `"subtypes":[`) || strings.Contains(string(raw), `"subtypes":null`) {
		t.Errorf("subtypes must always be an array: %s", raw)
	}
}

func TestControlProtocol_ConnectorTypesNeedsTheConnectorReadScope(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	denied := &auth.Identity{Scopes: []string{rbac.ScopeAgentBuilderRead}}

	_, err := mp.handleConnectorManagement(t.Context(), "get", []string{"types"}, nil, denied)

	if err == nil || !strings.Contains(err.Error(), rbac.ScopeConnectorRead) {
		t.Fatalf("err = %v, want a refusal naming %s", err, rbac.ScopeConnectorRead)
	}
}
