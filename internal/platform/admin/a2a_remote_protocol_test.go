// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

const jwksURL = "https://keys.example.com/jwks.json"

func remoteAgentCreate(name string) CreateRemoteAgentRequest {
	return CreateRemoteAgentRequest{
		AgentURL:          "https://example.com/agent",
		Name:              name,
		AgentCardLocation: "well_known",
		AgentAuthConfig:   map[string]any{},
	}
}

func TestComputeA2AAgentConfigHash_DefaultsKeepThePreUpgradeHash(t *testing.T) {
	t.Parallel()
	a := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "legacy",
		Description:           strPtr("deployed before the pin"),
		AgentCardLocation:     "well_known",
		AgentAuthType:         strPtr("none"),
		RequestTimeoutSeconds: 120,
	}
	legacy, err := json.Marshal(map[string]any{
		"agentUrl": a.AgentURL, "name": a.Name, "description": "deployed before the pin",
		"agentCardLocation": a.AgentCardLocation, "agentCardUrl": "", "agentAuthType": "none",
		"requestTimeoutSeconds": 120, "skills": "", "inputModes": "", "outputModes": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(legacy)
	want := hex.EncodeToString(sum[:])

	for _, pin := range []string{"", ProtocolPinAuto} {
		a.ProtocolVersion = pin
		if got := computeA2AAgentConfigHash(a); got != want {
			t.Errorf("hash with protocol_version %q = %s, want the pre-upgrade %s", pin, got, want)
		}
	}
}

func TestComputeA2AAgentConfigHash_CardTrustChangesTheHash(t *testing.T) {
	t.Parallel()
	base := &store.A2AAgent{AgentURL: "https://example.com/agent", Name: "a", AgentCardLocation: "well_known", ProtocolVersion: ProtocolPinAuto}
	baseHash := computeA2AAgentConfigHash(base)
	for name, mutate := range map[string]func(*store.A2AAgent){
		"pin v1_0":     func(a *store.A2AAgent) { a.ProtocolVersion = ProtocolPinV10 },
		"pin v0_3":     func(a *store.A2AAgent) { a.ProtocolVersion = ProtocolPinV03 },
		"trusted URLs": func(a *store.A2AAgent) { a.TrustedCardJWKSURLs = strPtr(`["` + jwksURL + `"]`) },
		"require":      func(a *store.A2AAgent) { a.RequireSignedCard = true },
	} {
		changed := *base
		mutate(&changed)
		if computeA2AAgentConfigHash(&changed) == baseHash {
			t.Errorf("%s: hash unchanged", name)
		}
	}
}

func TestBuildA2AProxyAppConfig_ProxyReadsTheCardTrust(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pin, wantVersion string
		urls             *string
		require          bool
	}{
		{ProtocolPinAuto, proxy.ProtocolVersionAuto, nil, false},
		{"", proxy.ProtocolVersionAuto, nil, false},
		{ProtocolPinV10, proxy.ProtocolVersion10, strPtr(`["` + jwksURL + `"]`), true},
		{ProtocolPinV03, proxy.ProtocolVersion03, strPtr(`["` + jwksURL + `"]`), false},
	}
	for _, tt := range tests {
		a := &store.A2AAgent{ID: "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f", AgentURL: "https://example.com/agent", Name: "a",
			AgentCardLocation: "well_known", ProtocolVersion: tt.pin, TrustedCardJWKSURLs: tt.urls, RequireSignedCard: tt.require}
		appConfig := buildA2AProxyAppConfig(a, "test-ns")
		if tt.pin == ProtocolPinAuto || tt.pin == "" {
			for _, key := range []string{"protocol_version", "trusted_card_jwks_urls", "require_signed_card"} {
				if _, ok := appConfig[key]; ok {
					t.Errorf("pin %q: app_config carries %s at its default", tt.pin, key)
				}
			}
		}
		roundTripped := map[string]any{}
		raw, _ := json.Marshal(appConfig)
		if err := json.Unmarshal(raw, &roundTripped); err != nil {
			t.Fatal(err)
		}
		pc, err := proxy.ParseProxyConfig("p", config.FromMap(roundTripped), nil)
		if err != nil {
			t.Fatalf("pin %q: the proxy refused the deployed config: %v", tt.pin, err)
		}
		if pc.ProtocolVersion != tt.wantVersion {
			t.Errorf("pin %q: proxy protocol_version = %q, want %q", tt.pin, pc.ProtocolVersion, tt.wantVersion)
		}
		if wantURLs := tt.urls != nil; (len(pc.TrustedCardJWKSURLs) == 1) != wantURLs || pc.RequireSignedCard != tt.require {
			t.Errorf("pin %q: proxy trust = %q require %v", tt.pin, pc.TrustedCardJWKSURLs, pc.RequireSignedCard)
		}
	}
}

func TestA2AAgentService_CardTrustRoundTrip(t *testing.T) {
	t.Parallel()
	svc, _ := newSvcForTest(t)
	ctx := t.Context()

	plain, err := svc.Create(ctx, remoteAgentCreate("defaults"), "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if plain.ProtocolVersion != ProtocolPinAuto || plain.TrustedCardJWKSURLs == nil || len(plain.TrustedCardJWKSURLs) != 0 || plain.RequireSignedCard {
		t.Errorf("defaults = %q %v %v, want auto, [] and false", plain.ProtocolVersion, plain.TrustedCardJWKSURLs, plain.RequireSignedCard)
	}

	req := remoteAgentCreate("pinned")
	req.ProtocolVersion = ProtocolPinV03
	req.TrustedCardJWKSURLs = []string{jwksURL}
	req.RequireSignedCard = new(true)
	created, err := svc.Create(ctx, req, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ProtocolVersion != ProtocolPinV03 || !slices.Equal(created.TrustedCardJWKSURLs, []string{jwksURL}) || !created.RequireSignedCard {
		t.Errorf("created = %q %v %v", created.ProtocolVersion, created.TrustedCardJWKSURLs, created.RequireSignedCard)
	}

	kept, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{Description: "only the description"}, "u")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if kept.ProtocolVersion != ProtocolPinV03 || len(kept.TrustedCardJWKSURLs) != 1 || !kept.RequireSignedCard {
		t.Errorf("an update that names none of them changed them: %q %v %v", kept.ProtocolVersion, kept.TrustedCardJWKSURLs, kept.RequireSignedCard)
	}

	cleared, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{ProtocolVersion: ProtocolPinAuto, TrustedCardJWKSURLs: []string{}, RequireSignedCard: new(false)}, "u")
	if err != nil {
		t.Fatalf("update clearing card trust: %v", err)
	}
	if cleared.ProtocolVersion != ProtocolPinAuto || len(cleared.TrustedCardJWKSURLs) != 0 || cleared.RequireSignedCard {
		t.Errorf("cleared = %q %v %v", cleared.ProtocolVersion, cleared.TrustedCardJWKSURLs, cleared.RequireSignedCard)
	}

	replaced, err := svc.Replace(ctx, plain.ID, remoteAgentCreate("defaults"), "u")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if replaced.ProtocolVersion != ProtocolPinAuto || len(replaced.TrustedCardJWKSURLs) != 0 || replaced.RequireSignedCard {
		t.Errorf("replace without them = %q %v %v, want the defaults", replaced.ProtocolVersion, replaced.TrustedCardJWKSURLs, replaced.RequireSignedCard)
	}
}

func TestA2AAgentService_CardTrustValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newSvcForTest(t)
	ctx := t.Context()

	tooMany := make([]string, proxy.MaxTrustedCardJWKSURLs+1)
	for i := range tooMany {
		tooMany[i] = jwksURL
	}
	invalid := []struct {
		name  string
		edit  func(*CreateRemoteAgentRequest)
		field string
	}{
		{"unknown pin", func(r *CreateRemoteAgentRequest) { r.ProtocolVersion = "1.0" }, fieldProtocolVersion},
		{"plain http off loopback", func(r *CreateRemoteAgentRequest) {
			r.TrustedCardJWKSURLs = []string{"http://keys.example.com/jwks.json"}
		}, fieldTrustedCardJWKSURLs},
		{"relative URL", func(r *CreateRemoteAgentRequest) { r.TrustedCardJWKSURLs = []string{"/jwks.json"} }, fieldTrustedCardJWKSURLs},
		{"plain http on a private address", func(r *CreateRemoteAgentRequest) { r.TrustedCardJWKSURLs = []string{"http://10.0.0.5/jwks.json"} }, fieldTrustedCardJWKSURLs},
		{"plain http on a host named like localhost", func(r *CreateRemoteAgentRequest) {
			r.TrustedCardJWKSURLs = []string{"http://localhost.evil.com/jwks.json"}
		}, fieldTrustedCardJWKSURLs},
		{"too many URLs", func(r *CreateRemoteAgentRequest) { r.TrustedCardJWKSURLs = tooMany }, fieldTrustedCardJWKSURLs},
		{"require without URLs", func(r *CreateRemoteAgentRequest) { r.RequireSignedCard = new(true) }, fieldRequireSignedCard},
		{"card trust without a fetched card", func(r *CreateRemoteAgentRequest) {
			r.AgentCardLocation = "none"
			r.Skills = []any{map[string]any{"name": "s"}}
			r.TrustedCardJWKSURLs = []string{jwksURL}
		}, fieldTrustedCardJWKSURLs},
	}
	for _, tt := range invalid {
		req := remoteAgentCreate("invalid")
		tt.edit(&req)
		_, err := svc.Create(ctx, req, "u")
		if ve, ok := errors.AsType[*ValidationError](err); !ok || ve.Field != tt.field {
			t.Errorf("%s: error = %v, want a ValidationError on %s", tt.name, err, tt.field)
		}
	}

	loopback := remoteAgentCreate("loopback")
	loopback.TrustedCardJWKSURLs = []string{"http://127.0.0.1:8080/jwks.json", "http://localhost/jwks.json"}
	created, err := svc.Create(ctx, loopback, "u")
	if err != nil {
		t.Fatalf("loopback http URLs refused: %v", err)
	}

	if _, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{RequireSignedCard: new(true)}, "u"); err != nil {
		t.Errorf("require over stored URLs refused: %v", err)
	}
	if _, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{TrustedCardJWKSURLs: []string{}}, "u"); err == nil {
		t.Error("clearing the URLs a stored require needs was accepted")
	}
}

func TestRemoteProtocolStatus(t *testing.T) {
	t.Parallel()
	card := func(version, binding string, signature a2a.CardSignature) *a2a.AgentCard {
		return &a2a.AgentCard{Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			a2a.RemoteProtocolExtension(a2a.RemoteProtocol{Version: version, Binding: binding, Pin: "auto", Deprecated: version == "0.3", CardSignature: signature}),
		}}}
	}
	tests := []struct {
		name string
		card *a2a.AgentCard
		want *RemoteProtocolStatus
	}{
		{"1.0 over JSON-RPC, verified", card("1.0", "JSONRPC", a2a.CardSignatureVerified), &RemoteProtocolStatus{ProtocolPinV10, RemoteBindingJSONRPC, false, string(a2a.CardSignatureVerified)}},
		{"1.0 over HTTP+JSON, refused", card("1.0", "HTTP+JSON", a2a.CardSignatureRefused), &RemoteProtocolStatus{ProtocolPinV10, RemoteBindingHTTPJSON, false, string(a2a.CardSignatureRefused)}},
		{"0.3 from a proxy older than card checks", card("0.3", "JSONRPC", ""), &RemoteProtocolStatus{ProtocolPinV03, RemoteBindingJSONRPC, true, string(a2a.CardSignatureUnchecked)}},
		{"unknown version", card("2.0", "JSONRPC", a2a.CardSignatureVerified), nil},
		{"unknown binding", card("1.0", "GRPC", a2a.CardSignatureVerified), nil},
		{"unknown signature outcome", card("1.0", "JSONRPC", "trust-me"), nil},
		{"no extension", &a2a.AgentCard{}, nil},
		{"no card", nil, nil},
	}
	for _, tt := range tests {
		got := remoteProtocolStatus(tt.card)
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("%s: status = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestA2AAgentService_ResponseCarriesTheProxyProtocol(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	registry := platpkg.NewAgentRegistry()
	svc := NewA2AAgentService(a2aRepo, store.NewAgentDeploymentRepo(db), store.NewAgentRepo(db), registry, nil, nil, "test-ns", nil)
	ctx := t.Context()

	created, err := svc.Create(ctx, remoteAgentCreate("status"), "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.RemoteProtocol != nil {
		t.Errorf("remoteProtocol before the proxy published a card = %+v, want nil", created.RemoteProtocol)
	}
	stored, _ := a2aRepo.GetByID(ctx, created.ID)
	name := A2AAgentInstanceName(stored)
	registry.AddOrUpdate(a2a.AgentCard{Name: name, URL: "solace:test-ns/a2a/v1/agent/request/" + name, Capabilities: a2a.AgentCapabilities{
		Extensions: []a2a.AgentExtension{a2a.RemoteProtocolExtension(a2a.RemoteProtocol{Version: "0.3", Binding: "JSONRPC", Pin: "auto", Deprecated: true, CardSignature: a2a.CardSignatureUnsigned})},
	}})

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := RemoteProtocolStatus{ProtocolPinV03, RemoteBindingJSONRPC, true, string(a2a.CardSignatureUnsigned)}
	if got.RemoteProtocol == nil || *got.RemoteProtocol != want {
		t.Errorf("remoteProtocol = %+v, want %+v", got.RemoteProtocol, want)
	}
}

func TestFetchAgentCard_ReportsInterfacesAndSignature(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		card       string
		want       []AdvertisedInterface
		wantSigned bool
	}{
		{"A2A 1.0 card with both bindings and a 0.3 url",
			`{"supportedInterfaces":[{"url":"https://a/rpc","protocolBinding":"JSONRPC","protocolVersion":"1.0"},{"url":"https://a/rest","protocolBinding":"HTTP+JSON","protocolVersion":"1.0.1"},{"url":"https://a/grpc","protocolBinding":"GRPC","protocolVersion":"1.0"},{"url":"https://a/rpc2","protocolBinding":"jsonrpc","protocolVersion":"1.0"}],"url":"https://a/v03","signatures":[{"protected":"x","signature":"y"}]}`,
			[]AdvertisedInterface{{ProtocolPinV10, RemoteBindingJSONRPC}, {ProtocolPinV10, RemoteBindingHTTPJSON}, {ProtocolPinV03, RemoteBindingJSONRPC}}, true},
		{"A2A 0.3 card", `{"url":"https://a/v03","protocolVersion":"0.3.0"}`, []AdvertisedInterface{{ProtocolPinV03, RemoteBindingJSONRPC}}, false},
		{"nothing the proxy speaks", `{"supportedInterfaces":[{"url":"https://a/grpc","protocolBinding":"GRPC","protocolVersion":"1.0"}]}`, []AdvertisedInterface{}, false},
		{"A2A 0.3 over HTTP+JSON, which the proxy does not speak",
			`{"supportedInterfaces":[{"url":"https://a/rest","protocolBinding":"HTTP+JSON","protocolVersion":"0.3"}]}`, []AdvertisedInterface{}, false},
		{"HTTP+JSON with a tenant that is not one path segment",
			`{"supportedInterfaces":[{"url":"https://a/rest","protocolBinding":"HTTP+JSON","protocolVersion":"1.0","tenant":"../admin"},{"url":"https://a/rpc","protocolBinding":"JSONRPC","protocolVersion":"0.3"}]}`,
			[]AdvertisedInterface{{ProtocolPinV03, RemoteBindingJSONRPC}}, false},
		{"interface list that does not decode, read as its 0.3 url",
			`{"supportedInterfaces":[{"url":"https://a/rpc","protocolBinding":"JSONRPC","protocolVersion":1}],"url":"https://a/v03"}`,
			[]AdvertisedInterface{{ProtocolPinV03, RemoteBindingJSONRPC}}, false},
		{"signatures that are not a list", `{"url":"https://a/v03","signatures":"abc"}`, []AdvertisedInterface{{ProtocolPinV03, RemoteBindingJSONRPC}}, false},
	}
	for _, tt := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Replace(tt.card, "{", `{"name":"n","description":"d",`, 1))
		}))
		svc, _ := newSvcForTest(t)
		got, err := svc.FetchAgentCard(t.Context(), FetchAgentCardRequest{AgentCardURL: srv.URL + "/card.json"})
		srv.Close()
		if err != nil {
			t.Fatalf("%s: FetchAgentCard: %v", tt.name, err)
		}
		if !slices.Equal(got.Interfaces, tt.want) || got.Signed != tt.wantSigned {
			t.Errorf("%s: interfaces = %v signed = %v, want %v signed = %v", tt.name, got.Interfaces, got.Signed, tt.want, tt.wantSigned)
		}
	}
}
