// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"testing"
)

func TestSynthesizeCatalog(t *testing.T) {
	p1, err := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c1", ClientSecret: "s1", Users: []FakeOIDCUser{{Sub: "alice"}}})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = p1.Close() })

	auth := &GatewayAuth{
		FakeIDPs: []FakeIDPSpec{{Name: "primary", ClientID: "c1", ClientSecret: "s1", Users: []FakeOIDCUser{{Sub: "alice"}}}},
		Catalog:  []CatalogEntry{{Name: "primary", IDPRef: "primary", Scopes: []string{"openid"}}},
	}
	idps := map[string]*MockOIDCProvider{"primary": p1}

	catalog := SynthesizeCatalog(auth, idps)
	if len(catalog) != 1 {
		t.Fatalf("catalog len: got %d, want 1", len(catalog))
	}
	rawEntry, ok := catalog["primary"]
	if !ok {
		t.Fatalf("entry %q not present; keys: %v", "primary", keysOf(catalog))
	}
	entry, ok := rawEntry.(map[string]any)
	if !ok {
		t.Fatalf("entry shape: got %T, want map[string]any", rawEntry)
	}
	if _, present := entry["name"]; present {
		t.Errorf("entry must not contain a 'name' field (production loader uses the map key)")
	}
	if entry["issuer"] != p1.URL() {
		t.Errorf("issuer: got %v, want %v", entry["issuer"], p1.URL())
	}
	if entry["client_id"] != "c1" || entry["client_secret"] != "s1" {
		t.Errorf("client creds: %+v", entry)
	}
	scopes, _ := entry["scopes"].([]string)
	if len(scopes) != 1 || scopes[0] != "openid" {
		t.Errorf("scopes: %+v", entry["scopes"])
	}
}

func TestSynthesizeCatalog_MultiEntry(t *testing.T) {
	trusted, err := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "ct", ClientSecret: "st", Users: []FakeOIDCUser{{Sub: "alice"}}})
	if err != nil {
		t.Fatalf("trusted: %v", err)
	}
	t.Cleanup(func() { _ = trusted.Close() })
	foreign, err := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "cf", ClientSecret: "sf", Users: []FakeOIDCUser{{Sub: "eve"}}})
	if err != nil {
		t.Fatalf("foreign: %v", err)
	}
	t.Cleanup(func() { _ = foreign.Close() })

	auth := &GatewayAuth{
		FakeIDPs: []FakeIDPSpec{
			{Name: "trusted", ClientID: "ct", ClientSecret: "st"},
			{Name: "foreign", ClientID: "cf", ClientSecret: "sf"},
		},
		Catalog: []CatalogEntry{{Name: "trusted", IDPRef: "trusted"}},
	}
	idps := map[string]*MockOIDCProvider{"trusted": trusted, "foreign": foreign}

	catalog := SynthesizeCatalog(auth, idps)
	if len(catalog) != 1 {
		t.Fatalf("catalog len: got %d, want 1; keys=%v", len(catalog), keysOf(catalog))
	}
	if _, present := catalog["foreign"]; present {
		t.Errorf("foreign IdP should not appear in catalog (not listed in auth.Catalog)")
	}
	entry, ok := catalog["trusted"].(map[string]any)
	if !ok {
		t.Fatalf("trusted entry: %T", catalog["trusted"])
	}
	if entry["issuer"] != trusted.URL() {
		t.Errorf("trusted issuer: got %v, want %v (foreign URL was %v)", entry["issuer"], trusted.URL(), foreign.URL())
	}
	if _, present := entry["scopes"]; present {
		t.Errorf("entry should not contain a 'scopes' key when Scopes is empty")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
