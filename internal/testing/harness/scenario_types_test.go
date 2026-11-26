// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGatewayAuth_YAMLRoundTrip(t *testing.T) {
	src := `
gateway_auth:
  fake_idps:
    - name: primary
      client_id: c
      client_secret: s
      users:
        - sub: alice@example.com
          email: alice@example.com
          name: Alice
          claims:
            groups: [sam-users]
  catalog:
    - name: primary
      idp_ref: primary
      scopes: [openid, email]
  external_auth_provider: primary
  session_secret_key: test-key
http_flow:
  - step: login
    fake_idp: primary
    as_user: alice@example.com
    expect:
      final_status: 200
      session_cookie_set: true
  - step: request
    method: GET
    path: /api/v1/me
    expect:
      status: 200
      body_contains: alice@example.com
  - step: request_with_bearer
    method: GET
    path: /api/v1/me
    bearer:
      from_fake_idp:
        name: primary
        sub: alice@example.com
    expect:
      status: 200
assert_oidc_calls:
  - fake_idp: primary
    authorize_count: 1
    token_count: 1
    userinfo_count_at_least: 1
`
	var s Scenario
	if err := yaml.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.GatewayAuth == nil {
		t.Fatal("GatewayAuth nil")
	}
	if len(s.GatewayAuth.FakeIDPs) != 1 || s.GatewayAuth.FakeIDPs[0].Name != "primary" {
		t.Errorf("fake_idps: %+v", s.GatewayAuth.FakeIDPs)
	}
	if len(s.HTTPFlow) != 3 {
		t.Errorf("http_flow len: got %d, want 3", len(s.HTTPFlow))
	}
	if s.HTTPFlow[2].Bearer == nil || s.HTTPFlow[2].Bearer.FromFakeIDP == nil {
		t.Errorf("bearer.from_fake_idp not parsed")
	}
	if len(s.AssertOIDCCalls) != 1 {
		t.Errorf("assert_oidc_calls: %+v", s.AssertOIDCCalls)
	}
	if s.AssertOIDCCalls[0].AuthorizeCount == nil || *s.AssertOIDCCalls[0].AuthorizeCount != 1 {
		t.Errorf("authorize_count not parsed")
	}
	if s.AssertOIDCCalls[0].UserinfoCountAtLeast == nil || *s.AssertOIDCCalls[0].UserinfoCountAtLeast != 1 {
		t.Errorf("userinfo_count_at_least not parsed")
	}

	out, err := yaml.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var s2 Scenario
	if err := yaml.Unmarshal(out, &s2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(s2.HTTPFlow) != 3 {
		t.Errorf("round-trip http_flow lost steps")
	}
	if s2.GatewayAuth == nil || len(s2.GatewayAuth.FakeIDPs) != 1 {
		t.Errorf("round-trip lost gateway_auth.fake_idps")
	}
	if s2.HTTPFlow[2].Bearer == nil || s2.HTTPFlow[2].Bearer.FromFakeIDP == nil ||
		s2.HTTPFlow[2].Bearer.FromFakeIDP.Sub != "alice@example.com" {
		t.Errorf("round-trip lost bearer.from_fake_idp")
	}
	if len(s2.AssertOIDCCalls) != 1 || s2.AssertOIDCCalls[0].AuthorizeCount == nil ||
		*s2.AssertOIDCCalls[0].AuthorizeCount != 1 {
		t.Errorf("round-trip lost assert_oidc_calls.authorize_count pointer")
	}
}

func TestGatewayAuth_AbsentMeansOff(t *testing.T) {
	var s Scenario
	if err := yaml.Unmarshal([]byte("test_case_id: foo"), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.GatewayAuth != nil {
		t.Errorf("absent gateway_auth should yield nil pointer")
	}
	if len(s.HTTPFlow) != 0 {
		t.Errorf("absent http_flow should yield empty slice")
	}
}

func TestScenario_Validate_DuplicateFakeIdPName(t *testing.T) {
	s := &Scenario{GatewayAuth: &GatewayAuth{
		FakeIDPs: []FakeIDPSpec{{Name: "a"}, {Name: "a"}},
	}, HTTPFlow: []HTTPFlowStep{{Step: "request"}}}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("expected error for duplicate fake_idp name")
	}
}

func TestScenario_Validate_BadIdPRef(t *testing.T) {
	s := &Scenario{GatewayAuth: &GatewayAuth{
		FakeIDPs: []FakeIDPSpec{{Name: "a"}},
		Catalog:  []CatalogEntry{{Name: "x", IDPRef: "missing"}},
	}, HTTPFlow: []HTTPFlowStep{{Step: "request"}}}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("expected error for unknown idp_ref")
	}
}

func TestScenario_Validate_GatewayAuthRequiresHttpFlow(t *testing.T) {
	s := &Scenario{GatewayAuth: &GatewayAuth{
		FakeIDPs: []FakeIDPSpec{{Name: "a"}},
		Catalog:  []CatalogEntry{{Name: "x", IDPRef: "a"}},
	}}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("gateway_auth without http_flow should error")
	}
}

func TestScenario_Validate_BearerMutex(t *testing.T) {
	s := &Scenario{
		GatewayAuth: &GatewayAuth{FakeIDPs: []FakeIDPSpec{{Name: "a"}}, Catalog: []CatalogEntry{{Name: "x", IDPRef: "a"}}},
		HTTPFlow: []HTTPFlowStep{{
			Step: "request_with_bearer",
			Bearer: &BearerSpec{
				Literal:     "tok",
				FromFakeIDP: &BearerFromIDP{Name: "a", Sub: "s"},
			},
		}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("both literal and from_fake_idp should error")
	}
}

func TestScenario_Validate_CounterMutex(t *testing.T) {
	one := 1
	s := &Scenario{
		GatewayAuth: &GatewayAuth{FakeIDPs: []FakeIDPSpec{{Name: "a"}}, Catalog: []CatalogEntry{{Name: "x", IDPRef: "a"}}},
		HTTPFlow:    []HTTPFlowStep{{Step: "request"}},
		AssertOIDCCalls: []OIDCCallAssert{{
			FakeIDP:               "a",
			AuthorizeCount:        &one,
			AuthorizeCountAtLeast: &one,
		}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("both exact and at_least counter should error")
	}
}

func TestScenario_Validate_ExpectFieldsByStep(t *testing.T) {
	s := &Scenario{
		GatewayAuth: &GatewayAuth{
			FakeIDPs:             []FakeIDPSpec{{Name: "a"}},
			Catalog:              []CatalogEntry{{Name: "x", IDPRef: "a"}},
			ExternalAuthProvider: "x",
		},
		HTTPFlow: []HTTPFlowStep{{
			Step:   "request",
			Expect: HTTPFlowExpect{FinalStatus: 200},
		}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("final_status on non-login step should error")
	}
}

func TestScenario_Validate_BearerFromFakeIDPUnknownName(t *testing.T) {
	s := &Scenario{
		GatewayAuth: &GatewayAuth{
			FakeIDPs: []FakeIDPSpec{{Name: "a"}},
			Catalog:  []CatalogEntry{{Name: "x", IDPRef: "a"}},
		},
		HTTPFlow: []HTTPFlowStep{{
			Step: "request_with_bearer",
			Bearer: &BearerSpec{
				FromFakeIDP: &BearerFromIDP{Name: "missing", Sub: "alice"},
			},
		}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("bearer.from_fake_idp.name referencing unknown IdP should error")
	}
}

func TestScenario_Validate_AssertOIDCCallsUnknownIDP(t *testing.T) {
	s := &Scenario{
		GatewayAuth: &GatewayAuth{
			FakeIDPs: []FakeIDPSpec{{Name: "a"}},
			Catalog:  []CatalogEntry{{Name: "x", IDPRef: "a"}},
		},
		HTTPFlow:        []HTTPFlowStep{{Step: "request"}},
		AssertOIDCCalls: []OIDCCallAssert{{FakeIDP: "missing"}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("assert_oidc_calls.fake_idp referencing unknown IdP should error")
	}
}

func TestScenario_Validate_RequestWithBearerRequiresBearer(t *testing.T) {
	s := &Scenario{
		GatewayAuth: &GatewayAuth{
			FakeIDPs: []FakeIDPSpec{{Name: "a"}},
			Catalog:  []CatalogEntry{{Name: "x", IDPRef: "a"}},
		},
		HTTPFlow: []HTTPFlowStep{{Step: "request_with_bearer"}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("request_with_bearer step without bearer block should error")
	}
}

func TestScenario_Validate_PlainRequestRejectsBearer(t *testing.T) {
	s := &Scenario{
		GatewayAuth: &GatewayAuth{
			FakeIDPs: []FakeIDPSpec{{Name: "a"}},
			Catalog:  []CatalogEntry{{Name: "x", IDPRef: "a"}},
		},
		HTTPFlow: []HTTPFlowStep{{
			Step:   "request",
			Bearer: &BearerSpec{Literal: "tok"},
		}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("plain request step with bearer block should error")
	}
}

func TestScenario_Validate_DuplicateCatalogName(t *testing.T) {
	s := &Scenario{
		GatewayAuth: &GatewayAuth{
			FakeIDPs: []FakeIDPSpec{{Name: "a"}},
			Catalog: []CatalogEntry{
				{Name: "primary", IDPRef: "a"},
				{Name: "primary", IDPRef: "a"},
			},
		},
		HTTPFlow: []HTTPFlowStep{{Step: "request"}},
	}
	if err := s.validateGatewayAuth(); err == nil {
		t.Error("duplicate catalog name should error")
	}
}

func TestScenario_Validate_Valid(t *testing.T) {
	one := 1
	s := &Scenario{
		GatewayAuth: &GatewayAuth{
			FakeIDPs: []FakeIDPSpec{
				{Name: "primary", ClientID: "c", ClientSecret: "s"},
				{Name: "foreign", ClientID: "c2", ClientSecret: "s2"},
			},
			Catalog:              []CatalogEntry{{Name: "primary", IDPRef: "primary"}},
			ExternalAuthProvider: "primary",
			SessionSecretKey:     "test-key",
		},
		HTTPFlow: []HTTPFlowStep{
			{Step: "login", FakeIDP: "primary", AsUser: "alice@example.com",
				Expect: HTTPFlowExpect{FinalStatus: 200, SessionCookieSet: true}},
			{Step: "request", Method: "GET", Path: "/api/v1/me",
				Expect: HTTPFlowExpect{Status: 200, BodyContains: "alice"}},
			{Step: "request_with_bearer", Method: "GET", Path: "/api/v1/me",
				Bearer: &BearerSpec{FromFakeIDP: &BearerFromIDP{Name: "foreign", Sub: "eve"}},
				Expect: HTTPFlowExpect{Status: 401}},
		},
		AssertOIDCCalls: []OIDCCallAssert{{
			FakeIDP:              "primary",
			AuthorizeCount:       &one,
			TokenCount:           &one,
			UserinfoCountAtLeast: &one,
		}},
	}
	if err := s.validateGatewayAuth(); err != nil {
		t.Errorf("well-formed scenario should validate; got %v", err)
	}
}

func TestValidateLogOmissions_RejectsCanaryOnPureProxyScenario(t *testing.T) {
	canary := []LogOmissionAssert{{
		Markers:        []string{"CANARY-must-not-leak"},
		RequirePresent: "some anchor",
		Description:    "pure-proxy scenarios have no capture",
	}}

	pureProxy := &TestScenario{Tags: []string{"proxy"}, AssertLogsOmit: canary}
	if err := pureProxy.validateLogOmissions(); err == nil {
		t.Error("a canary on a pure-proxy scenario must be rejected at load, not silently skipped at run time")
	}

	hybrid := &TestScenario{
		Tags:            []string{"proxy"},
		LLMInteractions: []LLMInteraction{{}},
		AssertLogsOmit:  canary,
	}
	if err := hybrid.validateLogOmissions(); err != nil {
		t.Errorf("hybrid proxy runs on the standard harness, so its canary is valid: %v", err)
	}

	plain := &TestScenario{AssertLogsOmit: canary}
	if err := plain.validateLogOmissions(); err != nil {
		t.Errorf("non-proxy scenario must still load: %v", err)
	}
}
