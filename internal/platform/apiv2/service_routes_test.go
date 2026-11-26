// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func serveBare(t *testing.T, core *platpkg.PlatformCore, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	core.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHealth_AnswersWithoutSignIn(t *testing.T) {
	core, _ := newTestCore(t)
	req := httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/health", nil)

	if !auth.ShouldSkipAuth(req) {
		t.Fatalf("auth.SkipPaths does not list %s, so the outer middleware would answer 401", req.URL.Path)
	}
	rec := serveBare(t, core, req)
	expectStatus(t, rec, http.StatusOK)
	if got, want := decodeData[Health](t, rec), (Health{Status: "healthy", Service: "Platform Service"}); got != want {
		t.Errorf("health = %+v, want %+v", got, want)
	}
}

func TestPublicRoutesAreInSkipPaths(t *testing.T) {
	core, deps := newTestCore(t)
	checked := 0
	for _, rt := range routeTable(deps, core, core.Logger()) {
		if rt.Gate != GatePublic {
			continue
		}
		checked++
		if path := platpkg.APIV2Prefix + rt.Path; !auth.SkipPaths[path] {
			t.Errorf("%s %s is GatePublic but %s is not in auth.SkipPaths", rt.Method, rt.Path, path)
		}
	}
	if checked == 0 {
		t.Error("no GatePublic route in routeTable, so the check proved nothing")
	}
}

func TestVersion_SignedInReads(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Get(srv.URL + platpkg.APIV2Prefix + "/version")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	var products []map[string]any
	if err := json.Unmarshal(body.Data["products"], &products); err != nil {
		t.Fatalf("decode products: %v", err)
	}
	want := []VersionProduct{
		{Key: "sam-gateway-go", Name: "Solace Agent Mesh", Version: samversion.Version},
		{Key: "sam-str-go", Name: "Solace Agent Mesh Secure Tool Runtime", Version: samversion.STRVersion},
	}
	if len(products) != len(want) {
		t.Fatalf("products = %v, want %d entries", products, len(want))
	}
	for i, p := range products {
		for _, dropped := range []string{"id", "description", "dependencies"} {
			if _, ok := p[dropped]; ok {
				t.Errorf("product %d sends %q", i, dropped)
			}
		}
		if p["key"] != want[i].Key || p["name"] != want[i].Name || p["version"] != want[i].Version {
			t.Errorf("product %d = %v, want %+v", i, p, want[i])
		}
	}

	for key, value := range map[string]string{"minClientVersion": samversion.MinClientVersion, "apiVersion": samversion.APIVersion} {
		raw, ok := body.Data[key]
		if !ok {
			t.Errorf("%s is missing from %v", key, body.Data)
			continue
		}
		var got *string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("decode %s: %v", key, err)
		}
		switch {
		case value == "" && got != nil:
			t.Errorf("%s = %q, want null", key, *got)
		case value != "" && (got == nil || *got != value):
			t.Errorf("%s = %v, want %q", key, got, value)
		}
	}
}

func TestVersion_AnswersWithoutSignIn(t *testing.T) {
	core, _ := newTestCore(t)
	req := httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/version", nil)

	if !auth.ShouldSkipAuth(req) {
		t.Fatalf("auth.SkipPaths does not list %s, so the outer middleware would answer 401", req.URL.Path)
	}
	rec := serveBare(t, core, req)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Version](t, rec); len(got.Products) == 0 {
		t.Errorf("version = %+v, want the build's products", got)
	}
}
