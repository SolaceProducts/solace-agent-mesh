// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const trustedKeys = "https://agents.example.com/.well-known/jwks.json"

func TestRemoteAgents_PinAndCardTrust(t *testing.T) {
	api, _ := newAWEAPI(t)
	body := remoteAgentBody("pinned-agent")
	body["agentCardLocation"] = "well_known"
	body["protocolVersion"] = "v0_3"
	body["trustedCardJwksUrls"] = []string{trustedKeys}
	body["requireSignedCard"] = true
	created := createRemoteAgent(api, body)
	if created.ProtocolVersion != "v0_3" || len(created.TrustedCardJwksUrls) != 1 || !created.RequireSignedCard {
		t.Errorf("created = %q %v %v", created.ProtocolVersion, created.TrustedCardJwksUrls, created.RequireSignedCard)
	}
	if got := api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil).Body.String(); !strings.Contains(got, `"remoteProtocol":null`) {
		t.Errorf("remoteProtocol is not null before the proxy published a card: %s", got)
	}

	put := remoteAgentBody("pinned-agent")
	put["agentCardLocation"] = "well_known"
	rec := api.call(http.MethodPut, "/remoteAgents/"+created.ID, put)
	expectStatus(t, rec, http.StatusOK)
	replaced := decodeData[RemoteAgent](t, rec)
	if replaced.ProtocolVersion != "auto" || replaced.TrustedCardJwksUrls == nil || len(replaced.TrustedCardJwksUrls) != 0 || replaced.RequireSignedCard {
		t.Errorf("a PUT leaving them out = %q %v %v, want auto, [] and false", replaced.ProtocolVersion, replaced.TrustedCardJwksUrls, replaced.RequireSignedCard)
	}

	bad := remoteAgentBody("bad-pin")
	bad["protocolVersion"] = "1.0"
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", bad), http.StatusUnprocessableEntity, "protocolVersion")

	plainHTTP := remoteAgentBody("plain-http-keys")
	plainHTTP["agentCardLocation"] = "well_known"
	plainHTTP["trustedCardJwksUrls"] = []string{"http://agents.example.com/jwks.json"}
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", plainHTTP), http.StatusUnprocessableEntity, "trustedCardJwksUrls")
}

func wellKnownRemoteAgentBody(name string) map[string]any {
	body := remoteAgentBody(name)
	body["agentCardLocation"] = "well_known"
	return body
}

func TestRemoteAgents_PutSetsAndResetsCardTrust(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createRemoteAgent(api, wellKnownRemoteAgentBody("put-card-trust"))
	path := "/remoteAgents/" + created.ID

	put := wellKnownRemoteAgentBody("put-card-trust")
	put["protocolVersion"] = "v0_3"
	put["trustedCardJwksUrls"] = []string{trustedKeys}
	put["requireSignedCard"] = true
	rec := api.call(http.MethodPut, path, put)
	expectStatus(t, rec, http.StatusOK)
	for label, got := range map[string]RemoteAgent{
		"PUT response":  decodeData[RemoteAgent](t, rec),
		"GET after PUT": decodeData[RemoteAgent](t, api.call(http.MethodGet, path, nil)),
	} {
		if got.ProtocolVersion != "v0_3" || !slices.Equal(got.TrustedCardJwksUrls, []string{trustedKeys}) || !got.RequireSignedCard {
			t.Errorf("%s = %q %v %v, want v0_3, [%s] and true", label, got.ProtocolVersion, got.TrustedCardJwksUrls, got.RequireSignedCard, trustedKeys)
		}
	}

	rec = api.call(http.MethodPut, path, wellKnownRemoteAgentBody("put-card-trust"))
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[RemoteAgent](t, rec); got.ProtocolVersion != "auto" || got.TrustedCardJwksUrls == nil || len(got.TrustedCardJwksUrls) != 0 || got.RequireSignedCard {
		t.Errorf("PUT leaving them out = %q %v %v, want auto, [] and false", got.ProtocolVersion, got.TrustedCardJwksUrls, got.RequireSignedCard)
	}

	unverifiable := wellKnownRemoteAgentBody("put-card-trust")
	unverifiable["requireSignedCard"] = true
	expectDetail(t, api.call(http.MethodPut, path, unverifiable), http.StatusUnprocessableEntity, "requireSignedCard")
}

func TestRemoteAgents_DeployedCarriesCardTrust(t *testing.T) {
	api, _ := newAWEAPI(t)
	body := wellKnownRemoteAgentBody("deployed-card-trust")
	body["protocolVersion"] = "v1_0"
	body["trustedCardJwksUrls"] = []string{trustedKeys}
	body["requireSignedCard"] = true
	created := createRemoteAgent(api, body)
	expectCreated[AgentDeployment](t, deployAgent(api, created.ID), "agentDeployments")

	got := decodeData[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil))
	if got.Deployed == nil {
		t.Fatalf("deployed is null after a successful deploy: %+v", got)
	}
	if d := got.Deployed; d.ProtocolVersion != "v1_0" || !slices.Equal(d.TrustedCardJwksUrls, []string{trustedKeys}) || !d.RequireSignedCard {
		t.Errorf("deployed = %q %v %v, want v1_0, [%s] and true", d.ProtocolVersion, d.TrustedCardJwksUrls, d.RequireSignedCard, trustedKeys)
	}
}

func TestRemoteAgents_RemoteProtocolFromTheProxyCard(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createRemoteAgent(api, wellKnownRemoteAgentBody("remote-protocol"))
	api.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: created.InstanceName, Capabilities: a2a.AgentCapabilities{
		Extensions: []a2a.AgentExtension{a2a.RemoteProtocolExtension(a2a.RemoteProtocol{
			Version: "0.3", Binding: "HTTP+JSON", Pin: "auto", Deprecated: true, CardSignature: a2a.CardSignatureRefused,
		})},
	}})

	got := decodeData[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil))
	want := RemoteAgentProtocol{SelectedVersion: "v0_3", Binding: "http_json", Deprecated: true, CardSignature: "refused"}
	if got.RemoteProtocol == nil || *got.RemoteProtocol != want {
		t.Errorf("remoteProtocol = %+v, want %+v", got.RemoteProtocol, want)
	}
}

func TestRemoteAgents_FetchCardReportsInterfacesAndSignature(t *testing.T) {
	api, _ := newAWEAPI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"weather","description":"Answers weather questions.",` +
			`"supportedInterfaces":[{"url":"https://agents.example.com/rest","protocolBinding":"HTTP+JSON","protocolVersion":"1.0"}],` +
			`"url":"https://agents.example.com/rpc","signatures":[{"protected":"x","signature":"y"}]}`))
	}))
	t.Cleanup(srv.Close)

	rec := api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{"agentCardUrl": srv.URL + "/card"})
	expectStatus(t, rec, http.StatusOK)
	card := decodeData[RemoteAgentCard](t, rec)
	want := []RemoteAgentInterface{{Version: "v1_0", Binding: "http_json"}, {Version: "v0_3", Binding: "jsonrpc"}}
	if !slices.Equal(card.Interfaces, want) || !card.Signed {
		t.Errorf("interfaces = %+v signed = %v, want %+v signed = true", card.Interfaces, card.Signed, want)
	}
}

func TestRemoteAgents_ProtocolVersionTagsMatchThePins(t *testing.T) {
	pins := slices.Sorted(slices.Values(entplatform.A2AProtocolPins()))
	for _, typ := range []reflect.Type{reflect.TypeFor[CreateRemoteAgentRequest](), reflect.TypeFor[UpdateRemoteAgentRequest]()} {
		f, ok := typ.FieldByName("ProtocolVersion")
		if !ok {
			t.Fatalf("%s has no ProtocolVersion field", typ.Name())
		}
		if got := slices.Sorted(slices.Values(oneofValues(f))); !slices.Equal(got, pins) {
			t.Errorf("%s protocolVersion oneof = %v, want %v", typ.Name(), got, pins)
		}
	}
}
