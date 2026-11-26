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

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	cardToken  = "card-token-secret"
	agentToken = "agent-token-secret"
	cardJSON   = `{"name":"weather","description":"Answers weather questions.","skills":[{"id":"forecast","name":"Forecast","description":"Gives a forecast."}],"defaultInputModes":["text"],"defaultOutputModes":["text"]}`
)

func newCardServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cardToken {
			http.Error(w, "upstream says no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cardJSON))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func remoteAgentBody(name string) map[string]any {
	return map[string]any{
		"agentUrl":          "https://agents.example.com/weather",
		"name":              name,
		"agentCardLocation": "none",
		"description":       "Remote agent the test connects.",
		"agentAuthType":     "bearer",
		"agentAuthConfig":   map[string]any{"type": "bearer", "token": agentToken},
		"skills":            []map[string]any{{"name": "Forecast", "description": "Gives a forecast."}},
	}
}

func createRemoteAgent(api *entrypointAPI, body map[string]any) RemoteAgent {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/remoteAgents", body)
	return expectCreated[RemoteAgent](api.t, rec, "remoteAgents")
}

func storedAgentAuthConfig(t *testing.T, api *entrypointAPI, id string) string {
	t.Helper()
	var stored string
	if err := api.core.DB().QueryRowContext(t.Context(), `SELECT agent_auth_config FROM a2a_agents WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read stored auth config: %v", err)
	}
	return stored
}

func TestRemoteAgents_Lifecycle(t *testing.T) {
	api, _ := newAWEAPI(t)
	rec := api.call(http.MethodPost, "/remoteAgents", remoteAgentBody("weather-agent"))
	expectCreated[RemoteAgent](t, rec, "remoteAgents")
	if strings.Contains(rec.Body.String(), agentToken) {
		t.Errorf("create returned the token: %s", rec.Body.String())
	}
	created := decodeData[RemoteAgent](t, rec)
	if created.Type != "remoteAgent" || created.AgentAuthConfig == nil || *created.AgentAuthConfig.Token != redacted {
		t.Errorf("created = %+v, want type remoteAgent with the token redacted", created)
	}
	if created.Skills == nil || created.InputModes == nil || created.OutputModes == nil {
		t.Errorf("a list field is null: %+v", created)
	}

	got := api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	if strings.Contains(got.Body.String(), agentToken) {
		t.Errorf("GET returned the token: %s", got.Body.String())
	}
	raw := rawData(t, got)
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "createdAt", "updatedAt")

	listed := decodeList[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents", nil))
	if !slices.ContainsFunc(listed.Data, func(a RemoteAgent) bool { return a.ID == created.ID }) {
		t.Errorf("list is missing the created remote agent: %+v", listed.Data)
	}

	replace := remoteAgentBody("renamed-agent")
	delete(replace, "description")
	replaced := api.call(http.MethodPut, "/remoteAgents/"+created.ID, replace)
	expectStatus(t, replaced, http.StatusOK)
	if a := decodeData[RemoteAgent](t, replaced); a.Name != "renamed-agent" || a.Description != nil {
		t.Errorf("after PUT: name %q description %q, want the new name and the description cleared", a.Name, orEmpty(a.Description))
	}

	expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil), http.StatusNotFound)
}

func TestRemoteAgents_Paging(t *testing.T) {
	api, _ := newAWEAPI(t)
	for _, name := range []string{"remote-one", "remote-two", "remote-three"} {
		createRemoteAgent(api, remoteAgentBody(name))
	}

	second := decodeList[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents?pageNumber=2&pageSize=2", nil))
	if p := second.Meta.Pagination; len(second.Data) != 1 || p.Count != 3 || p.TotalPages != 2 {
		t.Errorf("page 2 of size 2 = %d items, pagination %+v", len(second.Data), p)
	}
	expectDetail(t, api.call(http.MethodGet, "/remoteAgents?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
}

func TestRemoteAgents_Validation(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createRemoteAgent(api, remoteAgentBody("taken-remote"))

	every := api.call(http.MethodPost, "/remoteAgents", `{"name":"x","agentCardLocation":"somewhere"}`)
	expectStatus(t, every, http.StatusUnprocessableEntity)
	details := decodeError(t, every).ValidationDetails
	for _, field := range []string{"agentUrl", "name", "agentCardLocation", "agentAuthConfig"} {
		if len(details[field]) == 0 {
			t.Errorf("want every field problem at once, %q missing from %v", field, details)
		}
	}

	badType := remoteAgentBody("bad-auth-type")
	badType["agentAuthConfig"] = map[string]any{"type": "kerberos"}
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", badType), http.StatusUnprocessableEntity, "type")

	tokenless := remoteAgentBody("tokenless-agent")
	tokenless["agentAuthConfig"] = map[string]any{"type": "bearer"}
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", tokenless), http.StatusUnprocessableEntity, "agentAuthConfig")

	expectStatus(t, api.call(http.MethodPost, "/remoteAgents", remoteAgentBody("taken-remote")), http.StatusConflict)
	unknown := remoteAgentBody("unknown-field")
	unknown["agentAuth"] = "bearer"
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", unknown), http.StatusBadRequest, "agentAuth")
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", `{"requestTimeoutSeconds":"slow"}`), http.StatusBadRequest, "requestTimeoutSeconds")
	slow := remoteAgentBody("too-slow-agent")
	slow["requestTimeoutSeconds"] = 5
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", slow), http.StatusUnprocessableEntity, "requestTimeoutSeconds")

	expectDetail(t, api.call(http.MethodPut, "/remoteAgents/"+created.ID, `{"name":"no-url"}`), http.StatusUnprocessableEntity, "agentUrl")
	expectDetail(t, api.call(http.MethodGet, "/remoteAgents/nope", nil), http.StatusBadRequest, "id")
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		expectStatus(t, api.call(method, "/remoteAgents/"+missingID, nil), http.StatusNotFound)
	}
	expectStatus(t, api.call(http.MethodPut, "/remoteAgents/"+missingID, remoteAgentBody("missing-remote")), http.StatusNotFound)
}

func TestRemoteAgents_Scopes(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createRemoteAgent(api, remoteAgentBody("scoped-remote"))

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/remoteAgents", `{}`},
		{http.MethodPut, "/remoteAgents/" + created.ID, `{}`},
		{http.MethodDelete, "/remoteAgents/" + created.ID, nil},
		{http.MethodPut, "/remoteAgents/fetchCard", `{}`},
	} {
		if rec := api.call(tc.method, tc.path, tc.body, rbac.ScopeAgentBuilderRead); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with only read = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
	expectStatus(t, api.call(http.MethodGet, "/remoteAgents", nil, rbac.ScopeWorkflowBuilderRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil, rbac.ScopeAgentBuilderRead), http.StatusOK)
}

func TestRemoteAgents_PutKeepsStoredSecrets(t *testing.T) {
	api, _ := newAWEAPI(t)
	srv := newCardServer(t)
	body := remoteAgentBody("card-agent")
	body["agentCardLocation"] = "custom"
	body["agentCardUrl"] = srv.URL + "/card"
	body["agentCardAuthType"] = "bearer"
	body["agentCardAuthConfig"] = map[string]any{"type": "bearer", "token": cardToken}
	created := createRemoteAgent(api, body)

	body["agentCardAuthConfig"] = map[string]any{"type": "bearer", "token": redacted}
	body["agentAuthConfig"] = map[string]any{"type": "bearer"}
	expectStatus(t, api.call(http.MethodPut, "/remoteAgents/"+created.ID, body), http.StatusOK)

	got := api.call(http.MethodGet, "/remoteAgents/"+created.ID+"?expand=card", nil)
	expectStatus(t, got, http.StatusOK)
	a := decodeData[RemoteAgent](t, got)
	if a.CardData == nil || a.CardData.Name != "weather" || a.CardFetchError != nil {
		t.Errorf("after a PUT with the card token redacted, the card fetch = %+v / %v, want the card", a.CardData, a.CardFetchError)
	}
	if !strings.Contains(storedAgentAuthConfig(t, api, created.ID), agentToken) {
		t.Error("a PUT that left the agent token out dropped the stored one")
	}
	if strings.Contains(got.Body.String(), cardToken) || strings.Contains(got.Body.String(), agentToken) {
		t.Errorf("GET returned a token: %s", got.Body.String())
	}
}

func TestRemoteAgents_PutClearsScopesAndHeaderName(t *testing.T) {
	api, _ := newAWEAPI(t)
	body := remoteAgentBody("oauth-agent")
	oauth2 := func(scopes ...string) map[string]any {
		return map[string]any{"type": "oauth2", "tokenEndpoint": "https://auth.example.com/token", "clientId": "sam", "clientSecret": agentToken, "scopes": nonNilList(scopes)}
	}
	body["agentAuthType"] = "oauth2"
	body["agentAuthConfig"] = oauth2("weather.read")
	body["agentCardLocation"] = "custom"
	body["agentCardUrl"] = "https://agents.example.com/card"
	body["agentCardAuthType"] = "apikey"
	body["agentCardAuthConfig"] = map[string]any{"type": "apikey", "apiKey": cardToken, "headerName": "X-Old-Key"}
	created := createRemoteAgent(api, body)

	body["agentAuthConfig"] = oauth2()
	body["agentCardAuthConfig"] = map[string]any{"type": "apikey"}
	expectStatus(t, api.call(http.MethodPut, "/remoteAgents/"+created.ID, body), http.StatusOK)

	got := decodeData[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil))
	if c := got.AgentAuthConfig; c == nil || len(c.Scopes) != 0 {
		t.Errorf("task auth after a PUT with no scopes = %+v, want none", c)
	}
	if c := got.AgentCardAuthConfig; c == nil || c.HeaderName == nil || *c.HeaderName != "X-API-Key" {
		t.Errorf("card auth after a PUT with no headerName = %+v, want the default X-API-Key", c)
	}
}

func TestRemoteAgents_PutThatMovesAKeptSecretIs422(t *testing.T) {
	api, _ := newAWEAPI(t)
	oauth2 := map[string]any{"type": "oauth2", "tokenEndpoint": "https://auth.example.com/token", "clientId": "sam", "clientSecret": agentToken}
	base := func(name string) map[string]any {
		body := remoteAgentBody(name)
		body["agentCardLocation"] = "custom"
		body["agentCardUrl"] = "https://agents.example.com/card"
		body["agentCardAuthType"] = "bearer"
		body["agentCardAuthConfig"] = map[string]any{"type": "bearer", "token": cardToken}
		return body
	}
	for _, tc := range []struct {
		name, field string
		change      func(body map[string]any)
	}{
		{"agentUrl", "agentAuthConfig.token", func(b map[string]any) {
			b["agentUrl"] = "https://elsewhere.example.com/a2a"
			b["agentAuthConfig"] = map[string]any{"type": "bearer"}
		}},
		{"agentCardUrl", "agentCardAuthConfig.token", func(b map[string]any) {
			b["agentCardUrl"] = "https://elsewhere.example.com/card"
			b["agentCardAuthConfig"] = map[string]any{"type": "bearer", "token": redacted}
		}},
		{"tokenEndpoint", "agentAuthConfig.clientSecret", func(b map[string]any) {
			b["agentAuthConfig"] = map[string]any{"type": "oauth2", "tokenEndpoint": "https://elsewhere.example.com/token", "clientId": "sam"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := base("moved-" + tc.name)
			if tc.name == "tokenEndpoint" {
				body["agentAuthType"] = "oauth2"
				body["agentAuthConfig"] = oauth2
			}
			created := createRemoteAgent(api, body)
			tc.change(body)
			expectDetail(t, api.call(http.MethodPut, "/remoteAgents/"+created.ID, body), http.StatusUnprocessableEntity, tc.field)
			if got := decodeData[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil)); got.AgentURL != created.AgentURL || got.AgentCardURL == nil || *got.AgentCardURL != *created.AgentCardURL {
				t.Errorf("a refused PUT changed the agent: %+v", got)
			}
		})
	}

	body := base("moved-and-resent")
	created := createRemoteAgent(api, body)
	body["agentUrl"] = "https://elsewhere.example.com/a2a"
	body["agentCardUrl"] = "https://elsewhere.example.com/card"
	expectStatus(t, api.call(http.MethodPut, "/remoteAgents/"+created.ID, body), http.StatusOK)
}

func TestRemoteAgents_PutWithABlankCardSecretKeepsItsOtherCardAuthChanges(t *testing.T) {
	api, _ := newAWEAPI(t)
	body := remoteAgentBody("card-header-agent")
	body["agentCardLocation"] = "custom"
	body["agentCardUrl"] = "https://agents.example.com/card"
	body["agentCardAuthType"] = "apikey"
	body["agentCardAuthConfig"] = map[string]any{"type": "apikey", "apiKey": cardToken, "headerName": "X-Old-Key"}
	created := createRemoteAgent(api, body)

	body["agentCardAuthConfig"] = map[string]any{"type": "apikey", "apiKey": "", "headerName": "X-New-Key"}
	expectStatus(t, api.call(http.MethodPut, "/remoteAgents/"+created.ID, body), http.StatusOK)

	got := decodeData[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil))
	if c := got.AgentCardAuthConfig; c == nil || c.HeaderName == nil || *c.HeaderName != "X-New-Key" || c.APIKey == nil || *c.APIKey != redacted {
		t.Errorf("card auth after the PUT = %+v, want the new header name and the key still stored", c)
	}
	var stored string
	if err := api.core.DB().QueryRowContext(t.Context(), `SELECT agent_card_auth_config FROM a2a_agents WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored card auth config: %v", err)
	}
	if !strings.Contains(stored, cardToken) {
		t.Error("a PUT with a blank card key dropped the stored one")
	}
}

func TestRemoteAgents_CardFetchErrorIsAFixedSentence(t *testing.T) {
	api, _ := newAWEAPI(t)
	srv := newCardServer(t)
	body := remoteAgentBody("no-card-agent")
	body["agentCardLocation"] = "custom"
	body["agentCardUrl"] = srv.URL + "/card"
	created := createRemoteAgent(api, body)

	got := api.call(http.MethodGet, "/remoteAgents/"+created.ID+"?expand=card", nil)
	expectStatus(t, got, http.StatusOK)
	if a := decodeData[RemoteAgent](t, got); orEmpty(a.CardFetchError) != msgStoredCardFetchFailed || a.CardData != nil {
		t.Errorf("cardFetchError = %v, cardData = %+v; want %q and no card", a.CardFetchError, a.CardData, msgStoredCardFetchFailed)
	}
	if strings.Contains(got.Body.String(), "upstream says no") || strings.Contains(got.Body.String(), "status 401") {
		t.Errorf("the upstream error reached the response: %s", got.Body.String())
	}

	plain := decodeData[RemoteAgent](t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil))
	if plain.CardData != nil || plain.CardFetchError != nil {
		t.Errorf("without ?expand=card the card was fetched: %+v / %v", plain.CardData, plain.CardFetchError)
	}
}

func TestRemoteAgents_FetchCard(t *testing.T) {
	logs := captureLogs(t)
	api, _ := newAWEAPI(t)
	srv := newCardServer(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			_, _ = w.Write([]byte(`{"name":`))
			return
		}
		http.Error(w, "database password is hunter2", http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)

	ok := api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{
		"agentCardUrl": srv.URL + "/card", "authConfig": map[string]any{"type": "bearer", "token": cardToken},
	})
	expectStatus(t, ok, http.StatusOK)
	if card := decodeData[RemoteAgentCard](t, ok); card.Name != "weather" || len(card.Skills) != 1 || !slices.Equal(card.InputModes, []string{"text"}) {
		t.Errorf("card = %+v", card)
	}

	missing := api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{
		"agentCardUrl": "ftp://cards.example.com/x", "authConfig": map[string]any{"type": "oauth2", "clientId": "id"},
	})
	expectStatus(t, missing, http.StatusUnprocessableEntity)
	details := decodeError(t, missing).ValidationDetails
	for _, field := range []string{"agentCardUrl", "authConfig.tokenEndpoint", "authConfig.clientSecret"} {
		if len(details[field]) == 0 {
			t.Errorf("want every problem at once, %q missing from %v", field, details)
		}
	}
	expectDetail(t, api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{
		"agentCardUrl": srv.URL + "/card", "authConfig": map[string]any{"type": "bearer"},
	}), http.StatusUnprocessableEntity, "authConfig.token")
	expectDetail(t, api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{"agentCardUrl": "HTTP://cards.example.com/x"}),
		http.StatusUnprocessableEntity, "agentCardUrl")
	expectDetail(t, api.call(http.MethodPut, "/remoteAgents/fetchCard", `{"agentCardUrl":"https://x.example.com","authConfig":{"type":"magic"}}`),
		http.StatusUnprocessableEntity, "type")
	expectDetail(t, api.call(http.MethodPut, "/remoteAgents/fetchCard", `{"agentCardUrl":"https://x.example.com","authenticationType":"bearer"}`),
		http.StatusBadRequest, "authenticationType")

	for _, target := range []string{broken.URL + "/fail", broken.URL + "/json", srv.URL + "/card", "http://127.0.0.1:1/card?sig=topsecret"} {
		rec := api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{"agentCardUrl": target})
		expectStatus(t, rec, http.StatusBadGateway)
		if body := decodeError(t, rec); body.Message != msgCardFetchFailed || len(body.ValidationDetails) != 0 {
			t.Errorf("%s: body = %+v, want only %q", target, body, msgCardFetchFailed)
		}
		for _, leak := range []string{"hunter2", "upstream says no", "status", "JSON"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s: %q reached the response: %s", target, leak, rec.Body.String())
			}
		}
	}
	if strings.Contains(logs.String(), "topsecret") {
		t.Errorf("a card URL's query string reached the logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "connect: connection refused") {
		t.Errorf("the redacted cause of a failed fetch never reached the logs: %s", logs.String())
	}

	blocked := api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{"agentCardUrl": "http://169.254.169.254/latest/meta-data/"})
	expectStatus(t, blocked, http.StatusBadGateway)
	if body := decodeError(t, blocked); body.Message != msgCardFetchFailed {
		t.Errorf("metadata target: body = %+v, want only %q", body, msgCardFetchFailed)
	}
	if !strings.Contains(logs.String(), "address rejected by SSRF guard") {
		t.Errorf("the guard's rejection was not logged with its cause: %s", logs.String())
	}
}

func TestRemoteAgents_DeleteKeepsTheAgentWhenUndeployFails(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createRemoteAgent(api, remoteAgentBody("remote-undeploy"))
	expectCreated[AgentDeployment](t, deployAgent(api, created.ID), "agentDeployments")
	awe.failRemoves.Store(true)

	expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+created.ID, nil), http.StatusBadGateway)
	expectStatus(t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil), http.StatusOK)

	awe.failRemoves.Store(false)
	before := awe.removes.Load()
	expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil), http.StatusNotFound)
	if awe.removes.Load() == before {
		t.Error("the retried delete removed the record without undeploying the agent")
	}
}

func TestRemoteAgents_DeleteAfterAFailedDeployUndeploys(t *testing.T) {
	api, awe := newAWEAPI(t)
	created := createRemoteAgent(api, remoteAgentBody("remote-deploy-failed"))
	awe.failAdds.Store(true)
	expectFailedDeployment[AgentDeployment](api, deployAgent(api, created.ID), msgAgentDeployFailed, "agentDeployments")

	before := awe.removes.Load()
	expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+created.ID, nil), http.StatusNoContent)
	if awe.removes.Load() == before {
		t.Error("the delete left the failed deploy's queues behind")
	}
}

func TestRemoteAgents_EmptyFieldsAreNull(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createRemoteAgent(api, remoteAgentBody("null-fields-remote"))

	data := rawData(t, api.call(http.MethodGet, "/remoteAgents/"+created.ID, nil))
	expectNull(t, data, "agentCardUrl", "agentCardHeaders", "agentCardAuthType", "agentCardAuthConfig", "taskHeaders",
		"cardData", "cardFetchError", "syncStatus", "deployed")
	auth := data["agentAuthConfig"].(map[string]any)
	expectNull(t, auth, "apiKey", "headerName", "tokenEndpoint", "clientId", "clientSecret", "username", "password")
	expectEmptyList(t, auth, "scopes")
	skill := data["skills"].([]any)[0].(map[string]any)
	expectNull(t, skill, "id")
	expectEmptyList(t, skill, "tags", "examples", "inputModes", "outputModes")

	var stored string
	if err := api.core.DB().QueryRowContext(t.Context(), `SELECT skills FROM a2a_agents WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored skills: %v", err)
	}
	if strings.Contains(stored, "null") {
		t.Errorf("stored skills = %s, want the fields the request left out absent, as v1 stores them", stored)
	}
}

func TestRemoteAgents_CantBeRolledBack(t *testing.T) {
	api, _ := newAWEAPI(t)
	created := createRemoteAgent(api, remoteAgentBody("remote-rollback"))
	first := decodeData[AgentDeployment](t, deployAgent(api, created.ID))

	rec := api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": created.ID, "action": "update", "deploymentId": first.ID})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "deploymentId")
	if got := decodeError(t, rec).ValidationDetails["deploymentId"]; !slices.Contains(got, "a remote agent can't be rolled back") {
		t.Errorf("deploymentId details = %v, want the remote-agent refusal", got)
	}
}

func TestDeployedRemoteAgent_CoversTheConfig(t *testing.T) {
	shown := map[string]string{
		"agent_url": "agentUrl", "name": "name", "description": "description", "agent_card_location": "agentCardLocation",
		"agent_card_url": "agentCardUrl", "agent_card_auth_type": "agentCardAuthType", "agent_auth_type": "agentAuthType",
		"request_timeout_seconds": "requestTimeoutSeconds", "skills": "skills", "input_modes": "inputModes", "output_modes": "outputModes",
		"protocol_version": "protocolVersion", "trusted_card_jwks_urls": "trustedCardJwksUrls", "require_signed_card": "requireSignedCard",
	}
	leftOut := map[string]string{
		"id":                     "the remote agent's own id",
		"agent_card_auth_config": "holds credentials; the deployed summary carries no auth config",
		"agent_auth_config":      "holds credentials; the deployed summary carries no auth config",
		"agent_card_headers":     "the deploy's snapshot does not record headers, so there is no deployed value to show",
		"task_headers":           "the deploy's snapshot does not record headers, so there is no deployed value to show",
		"created_by":             "the remote agent's audit field; deployedBy records the deploy",
		"updated_by":             "the remote agent's audit field; deployedBy records the deploy",
		"created_time":           "the remote agent's audit field; deployedTime records the deploy",
		"updated_time":           "the remote agent's audit field; deployedTime records the deploy",
	}
	expectDeployedCoversConfig(t, reflect.TypeFor[store.A2AAgent](), reflect.TypeFor[DeployedRemoteAgent](), shown, leftOut)
}
