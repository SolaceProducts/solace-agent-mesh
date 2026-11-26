// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokenbroker

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const testAPIBase = "https://llm.example.test"

func TestCreateRefusesAGrantItCannotKeepSafe(t *testing.T) {
	f := newFixture(t)
	valid := func() *store.ModelOAuthGrant {
		return &store.ModelOAuthGrant{
			Name: "Example ID", Flow: store.ModelOAuthGrantFlowAuthorizationCode,
			Issuer: "https://id.example.test/", TokenEndpoint: "https://id.example.test/oauth/token",
			ClientID: "desktop", APIBase: testAPIBase, AccountSubject: "user|1", RefreshToken: "refresh-0",
		}
	}
	cases := []struct {
		name   string
		change func(*store.ModelOAuthGrant)
	}{
		{"no account, which any account could then replace", func(g *store.ModelOAuthGrant) { g.AccountSubject = " " }},
		{"a token endpoint over plain http", func(g *store.ModelOAuthGrant) { g.TokenEndpoint = "http://id.example.test/oauth/token" }},
		{"a relative token endpoint", func(g *store.ModelOAuthGrant) { g.TokenEndpoint = "/oauth/token" }},
		{"a token endpoint with credentials in it", func(g *store.ModelOAuthGrant) { g.TokenEndpoint = "https://u:p@id.example.test/oauth/token" }},
		{"no API base", func(g *store.ModelOAuthGrant) { g.APIBase = "" }},
		{"an API base over plain http", func(g *store.ModelOAuthGrant) { g.APIBase = "http://llm.example.test" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			grant := valid()
			c.change(grant)
			if err := f.svc.Create(t.Context(), grant, "desktop"); !errors.Is(err, ErrInvalidGrant) {
				t.Fatalf("Create = %v, want ErrInvalidGrant", err)
			}
			if grant.ID != "" {
				if row, _ := f.svc.Get(t.Context(), grant.ID); row != nil {
					t.Error("a refused grant was stored")
				}
			}
		})
	}
}

func localGrant(host string) *store.ModelOAuthGrant {
	return &store.ModelOAuthGrant{
		Name: "Local", Flow: store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer: "http://" + host + ":8080/", TokenEndpoint: "http://" + host + ":8080/oauth/token",
		ClientID: "desktop", APIBase: "http://" + host + ":4000", AccountSubject: "user|1", RefreshToken: "refresh-0",
	}
}

var thisMachine = []string{"localhost", "127.0.0.1", "[::1]"}

func TestCreateAcceptsPlainHTTPToThisMachineWhereThePlatformAllowsIt(t *testing.T) {
	f := newFixture(t)
	for _, host := range thisMachine {
		if err := f.svc.Create(t.Context(), localGrant(host), "desktop"); err != nil {
			t.Errorf("Create on %s = %v, want it stored", host, err)
		}
	}
}

func TestCreateAndSaveRefusePlainHTTPToThisMachineByDefault(t *testing.T) {
	f := newFixture(t)
	shared := New(f.db, nil)
	for _, host := range thisMachine {
		if err := shared.Create(t.Context(), localGrant(host), "desktop"); !errors.Is(err, ErrInvalidGrant) {
			t.Errorf("Create on %s = %v, want ErrInvalidGrant", host, err)
		}
		saved := localGrant(host)
		saved.ID = savedGrantID
		if err := shared.Save(t.Context(), saved, "desktop", modelNamesIt(false)); !errors.Is(err, ErrInvalidGrant) {
			t.Errorf("Save on %s = %v, want ErrInvalidGrant", host, err)
		}
	}
}
