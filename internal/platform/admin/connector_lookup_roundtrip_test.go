// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestControlProtocol_ConnectorLookupByName(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	subtype := "openapi"
	resourceKey := "test-ns/connectors/ledger/openapi.json"
	listedValues := `{"allow_list":"findPetsByStatus, getPetById"}`
	blankValues := `{"allow_list":" , "}`
	corruptValues := `{not json`
	emptyKey := ""
	for _, c := range []*store.Connector{
		{Name: "Ledger API", Type: "api", Subtype: &subtype, ResourceKey: &resourceKey, CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Plain Feed", Type: "api", Subtype: &subtype, CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Billing/Invoices #1", Type: "api", Subtype: &subtype, ResourceKey: &resourceKey, CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Corrupt Feed", Type: "api", Subtype: &subtype, Values: &corruptValues, CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Keyless Feed", Type: "api", Subtype: &subtype, ResourceKey: &emptyKey, CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Wiki Search", Type: "mcp", CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Listed Feed", Type: "api", Subtype: &subtype, Values: &listedValues, CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Blank Feed", Type: "api", Subtype: &subtype, Values: &blankValues, CreatedBy: "u", UpdatedBy: "u"},
		{Name: "Über Feed", Type: "api", Subtype: &subtype, ResourceKey: &resourceKey, CreatedBy: "u", UpdatedBy: "u"},
	} {
		if err := env.ConnectorRepo.Create(ctx, c); err != nil {
			t.Fatalf("seed %q: %v", c.Name, err)
		}
	}

	for _, tc := range []struct {
		name string
		want tools.ConnectorLookup
	}{
		{name: "Ledger API", want: tools.ConnectorLookup{Exists: true, Type: "api", HasResource: true}},
		{name: "ledgerapi", want: tools.ConnectorLookup{Exists: true, Type: "api", HasResource: true}},
		{name: "LEDGER API", want: tools.ConnectorLookup{Exists: true, Type: "api", HasResource: true}},
		{name: "PlainFeed", want: tools.ConnectorLookup{Exists: true, Type: "api"}},
		{name: "billing/invoices #1", want: tools.ConnectorLookup{Exists: true, Type: "api", HasResource: true}},
		{name: "ÜberFeed", want: tools.ConnectorLookup{Exists: true, Type: "api", HasResource: true}},
		{name: "corrupt feed", want: tools.ConnectorLookup{Exists: true, Type: "api"}},
		{name: "keylessfeed", want: tools.ConnectorLookup{Exists: true, Type: "api"}},
		{name: "wikisearch", want: tools.ConnectorLookup{Exists: true, Type: "mcp"}},
		{name: "listed feed", want: tools.ConnectorLookup{Exists: true, Type: "api", HasAllowList: true}},
		{name: "BlankFeed", want: tools.ConnectorLookup{Exists: true, Type: "api"}},
		{name: strings.Repeat("long-name-", 25)},
		{name: "No Such Connector"},
	} {
		got, err := tools.LookupConnectorByName(ctx, env.Client, tc.name)
		if err != nil {
			t.Fatalf("lookup %q: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("lookup %q = %+v, want %+v", tc.name, got, tc.want)
		}
	}

	if _, err := env.Client.Custom(ctx, "platform", "post", tools.ConnectorLookupPath, json.RawMessage(`{"name":"  "}`)); err == nil {
		t.Error("a blank name was accepted")
	}
}

func TestConnectorLookupByName_RequiresConnectorRead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testOpenDB(t)
	mp := &ManageablePlatform{
		deps:   &RouteDeps{ConnectorSvc: NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", slog.Default())},
		logger: slog.Default(),
	}
	path := strings.Split(tools.ConnectorLookupPath, "/")
	body := json.RawMessage(`{"name":"Ledger API"}`)

	if _, err := mp.HandleManagement(ctx, "post", path, body, &auth.Identity{Scopes: []string{rbac.ScopeConnectorRead}}); err != nil {
		t.Errorf("connector read scope: %v", err)
	}
	_, err := mp.HandleManagement(ctx, "post", path, body, &auth.Identity{Scopes: []string{rbac.ScopeSkillRead}})
	if err == nil || !strings.Contains(err.Error(), "insufficient permissions") {
		t.Errorf("skill-only identity: err = %v, want insufficient permissions", err)
	}
}

func TestAllowListNamesOperationAgreesWithDeployParsing(t *testing.T) {
	t.Parallel()
	for _, allowList := range []string{"", " ", ",", " , ", ",,a", "a", "a,", " a , b ", "a,,b", "\t,\n"} {
		want := len(parseOperationList(allowList)) > 0
		if got := tools.AllowListNamesOperation(allowList); got != want {
			t.Errorf("AllowListNamesOperation(%q) = %v, deploy parsing says %v", allowList, got, want)
		}
	}
}
