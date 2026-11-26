// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnessboot_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harnessboot"
)

func TestExecuteLogin(t *testing.T) {
	p, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []harness.FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice"}},
	})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	idps := map[string]*harness.MockOIDCProvider{"primary": p}

	auth := &harness.GatewayAuth{
		FakeIDPs:             []harness.FakeIDPSpec{{Name: "primary", ClientID: "c", ClientSecret: "s", Users: []harness.FakeOIDCUser{{Sub: "alice@example.com"}}}},
		Catalog:              []harness.CatalogEntry{{Name: "primary", IDPRef: "primary"}},
		ExternalAuthProvider: "primary",
		SessionSecretKey:     "test-secret-key",
	}
	catalog := harness.SynthesizeCatalog(auth, idps)
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, catalog)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	step := harness.HTTPFlowStep{
		Step:    "login",
		FakeIDP: "primary",
		AsUser:  "alice@example.com",
		Expect:  harness.HTTPFlowExpect{SessionCookieSet: true},
	}
	if err := harness.ExecuteLogin(t.Context(), t, client, step, handle.BaseURL, idps); err != nil {
		t.Fatalf("ExecuteLogin: %v", err)
	}

	u, _ := url.Parse(handle.BaseURL)
	cookies := jar.Cookies(u)
	var seenSession bool
	for _, c := range cookies {
		if c.Name == "sam_access_token" {
			seenSession = true
		}
	}
	if !seenSession {
		t.Errorf("expected sam_access_token cookie set; got: %+v", cookies)
	}
	if p.AuthorizeCount() != 1 || p.TokenCount() != 1 {
		t.Errorf("authorize=%d token=%d", p.AuthorizeCount(), p.TokenCount())
	}
}

func TestExecuteRequest_AfterLogin(t *testing.T) {
	p, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []harness.FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice"}},
	})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	idps := map[string]*harness.MockOIDCProvider{"primary": p}

	auth := &harness.GatewayAuth{
		FakeIDPs:             []harness.FakeIDPSpec{{Name: "primary", ClientID: "c", ClientSecret: "s", Users: []harness.FakeOIDCUser{{Sub: "alice@example.com"}}}},
		Catalog:              []harness.CatalogEntry{{Name: "primary", IDPRef: "primary"}},
		ExternalAuthProvider: "primary",
		SessionSecretKey:     "test-secret-key",
	}
	catalog := harness.SynthesizeCatalog(auth, idps)
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, catalog)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginStep := harness.HTTPFlowStep{
		Step:    "login",
		FakeIDP: "primary",
		AsUser:  "alice@example.com",
		Expect:  harness.HTTPFlowExpect{SessionCookieSet: true},
	}
	if err := harness.ExecuteLogin(t.Context(), t, client, loginStep, handle.BaseURL, idps); err != nil {
		t.Fatalf("ExecuteLogin: %v", err)
	}

	step := harness.HTTPFlowStep{
		Step:   "request",
		Method: "GET",
		Path:   "/api/v1/user",
		Expect: harness.HTTPFlowExpect{Status: 200, BodyContains: "alice@example.com"},
	}
	if err := harness.ExecuteRequest(t.Context(), t, client, step, handle.BaseURL); err != nil {
		t.Fatalf("ExecuteRequest: %v", err)
	}
}

func TestExecuteBearerRequest_ForeignTokenRejected(t *testing.T) {
	trusted, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "ct", ClientSecret: "st",
		Users: []harness.FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com"}},
	})
	if err != nil {
		t.Fatalf("trusted: %v", err)
	}
	t.Cleanup(func() { _ = trusted.Close() })

	foreign, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "cf", ClientSecret: "sf",
		Users: []harness.FakeOIDCUser{{Sub: "eve@evil.example.com", Email: "eve@evil.example.com"}},
	})
	if err != nil {
		t.Fatalf("foreign: %v", err)
	}
	t.Cleanup(func() { _ = foreign.Close() })

	idps := map[string]*harness.MockOIDCProvider{"trusted": trusted, "foreign": foreign}

	auth := &harness.GatewayAuth{
		FakeIDPs: []harness.FakeIDPSpec{
			{Name: "trusted", ClientID: "ct", ClientSecret: "st"},
			{Name: "foreign", ClientID: "cf", ClientSecret: "sf"},
		},
		Catalog:              []harness.CatalogEntry{{Name: "trusted", IDPRef: "trusted"}},
		ExternalAuthProvider: "trusted",
		SessionSecretKey:     "test-secret-key",
	}
	catalog := harness.SynthesizeCatalog(auth, idps)
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, catalog)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}

	client := &http.Client{}
	step := harness.HTTPFlowStep{
		Step:   "request_with_bearer",
		Method: "GET",
		Path:   "/api/v1/user",
		Bearer: &harness.BearerSpec{
			FromFakeIDP: &harness.BearerFromIDP{Name: "foreign", Sub: "eve@evil.example.com"},
		},
		Expect: harness.HTTPFlowExpect{Status: 401},
	}
	if err := harness.ExecuteBearerRequest(t.Context(), t, client, step, handle.BaseURL, idps); err != nil {
		t.Fatalf("ExecuteBearerRequest: %v", err)
	}
}
