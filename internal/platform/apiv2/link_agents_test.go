// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	linkWakeSecret      = "wake-client-secret"
	linkWakeHeaderValue = "Bearer alice-wake-key"
)

func (a *entrypointAPI) createAgentLink(name string) Entrypoint {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/entrypoints", map[string]any{
		"name":           name,
		"description":    "Door for partner agents to join the mesh.",
		"entrypointType": "agent_link",
		"values":         map[string]any{"maxInflight": 4},
	})
	expectStatus(a.t, rec, http.StatusCreated)
	return decodeData[Entrypoint](a.t, rec)
}

func linkAgentBody(name, entrypointID string) map[string]any {
	return map[string]any{
		"name":         name,
		"description":  "Link agent the test registers.",
		"entrypointId": entrypointID,
		"owner":        "alice@example.com",
		"peers":        []string{"policy-desk"},
		"wake": map[string]any{
			"url":          "https://runtime.example.com/invocations",
			"tokenUrl":     "https://idp.example.com/oauth2/token",
			"clientId":     "claims-client",
			"clientSecret": linkWakeSecret,
			"headers":      map[string]any{"X-Api-Key": linkWakeHeaderValue},
		},
	}
}

func issuedToken(t *testing.T, agent LinkAgentWithToken) string {
	t.Helper()
	if agent.Token == nil {
		t.Fatalf("link agent %s was returned without a token", agent.ID)
	}
	return *agent.Token
}

func createLinkAgent(api *entrypointAPI, body map[string]any) LinkAgentWithToken {
	api.t.Helper()
	return expectCreated[LinkAgentWithToken](api.t, api.call(http.MethodPost, "/linkAgents", body), "linkAgents")
}

func storedLinkAgentSecrets(t *testing.T, api *entrypointAPI, id string) (tokenDigest, wake string) {
	t.Helper()
	var storedWake *string
	if err := api.core.DB().QueryRowContext(t.Context(), `SELECT token_sha256, wake FROM link_agents WHERE id = $1`, id).Scan(&tokenDigest, &storedWake); err != nil {
		t.Fatalf("read the stored link agent: %v", err)
	}
	return tokenDigest, orEmpty(storedWake)
}

func expectNoLinkSecret(t *testing.T, what string, rec *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	for _, secret := range append(slices.Clone(secrets), linkWakeHeaderValue) {
		if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), sha256Hex(secret)) {
			t.Errorf("%s carries a secret or its digest", what)
		}
	}
}

const (
	callersTokenDigest      = "1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f"
	callersOtherTokenDigest = "2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e"
	takenTokenDigest        = "3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d"
)

func (a *entrypointAPI) createLinkAgentWithDigest(name, entrypointID, digest string) LinkAgentWithToken {
	a.t.Helper()
	return createLinkAgent(a, map[string]any{"name": name, "entrypointId": entrypointID, "tokenSha256": digest})
}

func (a *entrypointAPI) deliveredAgentLinkDigests(entrypointID string) []string {
	a.t.Helper()
	_, read := entplatform.EntrypointReaders(a.core.DB, a.core.Config().Namespace, slog.New(slog.DiscardHandler))
	delivered, err := read(a.t.Context(), entrypointID)
	if err != nil || delivered == nil {
		a.t.Fatalf("read what the entrypoint is to serve: %v", err)
	}
	agents, _ := entrypoint.AdapterConfig(delivered.AppConfig)["agents"].([]any)
	digests := []string{}
	for _, agent := range agents {
		settings, _ := agent.(map[string]any)
		digest, _ := settings["token_sha256"].(string)
		digests = append(digests, digest)
	}
	return digests
}

func TestLinkAgents_CreateWithTheCallersTokenDigestStoresItAndReturnsNoToken(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")

	createdRec := api.call(http.MethodPost, "/linkAgents", with(linkAgentBody("Claims Desk", door.ID), "tokenSha256", callersTokenDigest))

	created := expectCreated[LinkAgentWithToken](t, createdRec, "linkAgents")
	if created.Token != nil || created.TokenPrefix != nil {
		t.Errorf("create with tokenSha256 returned a token or a token prefix, want token null and no prefix")
	}
	if token, sent := rawData(t, createdRec)["token"]; !sent || token != nil {
		t.Errorf("create with tokenSha256: token field sent %t, null %t; want it sent as null", sent, token == nil)
	}
	if strings.Contains(createdRec.Body.String(), "samk_") || strings.Contains(createdRec.Body.String(), callersTokenDigest) {
		t.Errorf("create with tokenSha256 carries a generated token or echoes the digest")
	}
	if stored, _ := storedLinkAgentSecrets(t, api, created.ID); stored != callersTokenDigest {
		t.Errorf("stored token digest is not the one the caller sent")
	}
	for what, rec := range map[string]*httptest.ResponseRecorder{
		"GET":  api.call(http.MethodGet, "/linkAgents/"+created.ID, nil),
		"list": api.call(http.MethodGet, "/linkAgents", nil),
	} {
		expectStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), callersTokenDigest) {
			t.Errorf("%s carries the token digest", what)
		}
	}
}

func TestLinkAgents_ReplaceKeepsTheTokenUnlessAnotherDigestIsSent(t *testing.T) {
	tests := []struct {
		name       string
		sent       map[string]any
		wantDigest string
	}{
		{name: "tokenSha256 left out", sent: map[string]any{}, wantDigest: callersTokenDigest},
		{name: "the stored digest sent again", sent: map[string]any{"tokenSha256": callersTokenDigest}, wantDigest: callersTokenDigest},
		{name: "another digest", sent: map[string]any{"tokenSha256": callersOtherTokenDigest}, wantDigest: callersOtherTokenDigest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			link := api.createLinkAgentWithDigest("Claims Desk", door.ID, callersTokenDigest)
			body := map[string]any{"name": "Claims Desk", "entrypointId": door.ID}
			for key, value := range tt.sent {
				body[key] = value
			}

			replaced := api.call(http.MethodPut, "/linkAgents/"+link.ID, body)

			expectStatus(t, replaced, http.StatusOK)
			if stored, _ := storedLinkAgentSecrets(t, api, link.ID); stored != tt.wantDigest {
				t.Errorf("stored token digest after the replace is not the expected one")
			}
			if strings.Contains(replaced.Body.String(), callersTokenDigest) || strings.Contains(replaced.Body.String(), callersOtherTokenDigest) || strings.Contains(replaced.Body.String(), "samk_") {
				t.Errorf("replace carries a token or a token digest")
			}
		})
	}
}

func TestLinkAgents_ReplaceClearsAGeneratedTokensPrefixOnlyWhenAnotherDigestIsSent(t *testing.T) {
	tests := []struct {
		name       string
		sendDigest func(token string) string
		wantPrefix bool
	}{
		{name: "the digest of the generated token sent again", sendDigest: sha256Hex, wantPrefix: true},
		{name: "another digest", sendDigest: func(string) string { return callersOtherTokenDigest }, wantPrefix: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			link := createLinkAgent(api, map[string]any{"name": "Claims Desk", "entrypointId": door.ID})
			if link.TokenPrefix == nil {
				t.Fatalf("link agent with a generated token was created without a token prefix")
			}
			digest := tt.sendDigest(issuedToken(t, link))

			replaced := api.call(http.MethodPut, "/linkAgents/"+link.ID, map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "tokenSha256": digest})

			expectStatus(t, replaced, http.StatusOK)
			if stored, _ := storedLinkAgentSecrets(t, api, link.ID); stored != digest {
				t.Errorf("stored token digest after the replace is not the one sent")
			}
			prefix, sent := rawData(t, replaced)["tokenPrefix"]
			if !sent {
				t.Fatalf("replace left the tokenPrefix field out, want it sent")
			}
			if tt.wantPrefix && prefix != *link.TokenPrefix {
				t.Errorf("tokenPrefix after the replace = %v, want the generated token's prefix %q kept", prefix, *link.TokenPrefix)
			}
			if !tt.wantPrefix && prefix != nil {
				t.Errorf("tokenPrefix after the replace = %v, want null: the agent no longer holds the generated token", prefix)
			}
		})
	}
}

func TestLinkAgents_AnotherDigestOnADeployedAgentIsAnEditUntilItsNextDeployment(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")
	link := api.createLinkAgentWithDigest("Claims Desk", door.ID, callersTokenDigest)
	expectStatus(t, api.deploy(door.ID), http.StatusCreated)
	expectStatus(t, deployLinkAgent(api, link.ID, "deploy"), http.StatusCreated)
	deployedRaw, _ := rawData(t, api.call(http.MethodGet, "/linkAgents/"+link.ID, nil))["deployed"].(map[string]any)
	if prefix, sent := deployedRaw["tokenPrefix"]; !sent || prefix != nil {
		t.Errorf("deployed.tokenPrefix of an agent deployed with the caller's digest: sent %t, value %v; want it sent as null", sent, prefix)
	}
	deploymentsBefore := len(decodeList[AgentDeployment](t, api.call(http.MethodGet, "/agentDeployments?agentId="+link.ID, nil)).Data)

	replaced := api.call(http.MethodPut, "/linkAgents/"+link.ID, map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "tokenSha256": callersOtherTokenDigest})

	expectStatus(t, replaced, http.StatusOK)
	edited := decodeData[LinkAgent](t, replaced)
	if edited.DeploymentStatus != "deployed" || orEmpty(edited.SyncStatus) != "out_of_sync" {
		t.Errorf("link agent after another digest = deployment %q sync %q, want deployed and out_of_sync", edited.DeploymentStatus, orEmpty(edited.SyncStatus))
	}
	if after := len(decodeList[AgentDeployment](t, api.call(http.MethodGet, "/agentDeployments?agentId="+link.ID, nil)).Data); after != deploymentsBefore {
		t.Errorf("deployments after the replace = %d, want the %d there were: a replace deploys nothing", after, deploymentsBefore)
	}
	served := api.deliveredAgentLinkDigests(door.ID)
	if diff := cmp.Diff([]string{callersTokenDigest}, served); diff != "" {
		t.Errorf("digests the entrypoint serves before the update deployment (-want +got):\n%s", diff)
	}

	expectStatus(t, deployLinkAgent(api, link.ID, "update"), http.StatusCreated)

	if sync := orEmpty(decodeData[LinkAgent](t, api.call(http.MethodGet, "/linkAgents/"+link.ID, nil)).SyncStatus); sync != "in_sync" {
		t.Errorf("syncStatus after the update deployment = %q, want in_sync", sync)
	}
	if diff := cmp.Diff([]string{callersOtherTokenDigest}, api.deliveredAgentLinkDigests(door.ID)); diff != "" {
		t.Errorf("digests the entrypoint serves after the update deployment (-want +got):\n%s", diff)
	}
}

func TestLinkAgents_SecretsDiffSaysWhetherAReplaceWouldKeepTheStoredDigestAndWakeSecret(t *testing.T) {
	tests := []struct {
		name      string
		values    map[string]any
		wantMatch map[string]bool
	}{
		{
			name:      "the stored digest and the stored secret",
			values:    map[string]any{"tokenSha256": callersTokenDigest, "wake": map[string]any{"clientSecret": linkWakeSecret}},
			wantMatch: map[string]bool{"tokenSha256": true, "wake.clientSecret": true},
		},
		{
			name:      "another digest and another secret",
			values:    map[string]any{"tokenSha256": callersOtherTokenDigest, "wake": map[string]any{"clientSecret": "another-wake-secret"}},
			wantMatch: map[string]bool{"tokenSha256": false, "wake.clientSecret": false},
		},
		{
			name:      "nothing sent, and a redacted secret",
			values:    map[string]any{"wake": map[string]any{"clientSecret": "<REDACTED>"}},
			wantMatch: map[string]bool{"tokenSha256": true, "wake.clientSecret": true},
		},
		{
			name:      "a redacted digest, which a replace refuses",
			values:    map[string]any{"tokenSha256": "<REDACTED>"},
			wantMatch: map[string]bool{"tokenSha256": false, "wake.clientSecret": true},
		},
		{
			name:      "the stored header value",
			values:    map[string]any{"wake": map[string]any{"headers": map[string]any{"X-Api-Key": linkWakeHeaderValue}}},
			wantMatch: map[string]bool{"tokenSha256": true, "wake.clientSecret": true, "wake.headers": true},
		},
		{
			name:      "another header value",
			values:    map[string]any{"wake": map[string]any{"headers": map[string]any{"X-Api-Key": "Bearer bob-wake-key"}}},
			wantMatch: map[string]bool{"tokenSha256": true, "wake.clientSecret": true, "wake.headers": false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			link := createLinkAgent(api, with(linkAgentBody("Claims Desk", door.ID), "tokenSha256", callersTokenDigest))

			rec := api.call(http.MethodPut, "/linkAgents/"+link.ID+"/secretsDiff", map[string]any{"values": tt.values})

			expectStatus(t, rec, http.StatusOK)
			got := map[string]bool{}
			for field, match := range decodeData[SecretsDiffResult](t, rec).Secrets {
				got[field] = match.Matches
			}
			if diff := cmp.Diff(tt.wantMatch, got); diff != "" {
				t.Errorf("secretsDiff mismatch (-want +got):\n%s", diff)
			}
			expectNoLinkSecret(t, "secretsDiff", rec, linkWakeSecret, linkWakeHeaderValue)
			if strings.Contains(rec.Body.String(), callersTokenDigest) {
				t.Errorf("secretsDiff carries the stored token digest")
			}
		})
	}
}

func TestLinkAgents_SecretsDiffOfAMissingLinkAgentIsNotFound(t *testing.T) {
	api := newEntrypointAPI(t, nil)

	rec := api.call(http.MethodPut, "/linkAgents/"+missingID+"/secretsDiff", map[string]any{"values": map[string]any{"tokenSha256": callersTokenDigest}})

	expectStatus(t, rec, http.StatusNotFound)
}

func TestLinkAgents_Lifecycle(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")

	createdRec := api.call(http.MethodPost, "/linkAgents", linkAgentBody("Claims Desk", door.ID))
	created := expectCreated[LinkAgentWithToken](t, createdRec, "linkAgents")
	if got := createdRec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("create Cache-Control = %q, want no-store", got)
	}
	createdToken := issuedToken(t, created)
	if !strings.HasPrefix(createdToken, "samk_") || created.TokenPrefix == nil || *created.TokenPrefix != createdToken[5:13] {
		t.Errorf("create returned no samk_ token with its prefix")
	}
	wantCreated := LinkAgentWithToken{
		ID: created.ID, Type: "linkAgent", Name: "Claims Desk", InstanceName: "agent_" + strings.ReplaceAll(created.ID, "-", "_"),
		Description: new("Link agent the test registers."), EntrypointID: door.ID,
		ConnectPath: "/gw/" + door.ID + "/v1/connect", TokenPrefix: created.TokenPrefix,
		Enabled: true, Owner: new("alice@example.com"), Peers: []string{"policy-desk"},
		Wake: &LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: new("https://idp.example.com/oauth2/token"),
			ClientID: new("claims-client"), ClientSecret: new("<REDACTED>"), Headers: map[string]string{"X-Api-Key": "<REDACTED>"}, TimeoutSeconds: 900, OnStart: true},
		DeploymentStatus: "not_deployed", RuntimeStatus: RuntimeStatus{Status: "stopped", Problems: []RuntimeProblem{}},
		CreatedBy: created.CreatedBy, UpdatedBy: created.UpdatedBy, CreatedTime: created.CreatedTime, UpdatedTime: created.UpdatedTime,
	}
	withoutToken := created
	withoutToken.Token = nil
	if diff := cmp.Diff(wantCreated, withoutToken); diff != "" {
		t.Errorf("created link agent, its token aside, mismatch (-want +got):\n%s", diff)
	}
	expectNoLinkSecret(t, "create", createdRec, linkWakeSecret)
	digest, wake := storedLinkAgentSecrets(t, api, created.ID)
	if digest != sha256Hex(createdToken) || !strings.Contains(wake, linkWakeSecret) || !strings.Contains(wake, linkWakeHeaderValue) {
		t.Errorf("stored link agent does not hold the token's SHA-256, the wake client secret and the wake header value")
	}

	got := api.call(http.MethodGet, "/linkAgents/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	expectNoLinkSecret(t, "GET", got, createdToken, linkWakeSecret)
	raw := rawData(t, got)
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "token", "tokenSha256", "createdAt", "updatedAt")

	listed := api.call(http.MethodGet, "/linkAgents", nil)
	expectStatus(t, listed, http.StatusOK)
	expectNoLinkSecret(t, "list", listed, createdToken, linkWakeSecret)
	if page := decodeList[LinkAgent](t, listed); len(page.Data) != 1 || page.Data[0].ID != created.ID || page.Meta.Pagination.Count != 1 {
		t.Errorf("list = %+v, want the created link agent alone", page.Data)
	}

	replaced := api.call(http.MethodPut, "/linkAgents/"+created.ID, map[string]any{"name": "Policy Desk", "entrypointId": door.ID})
	expectStatus(t, replaced, http.StatusOK)
	wantReplaced := LinkAgent{
		ID: created.ID, Type: "linkAgent", Name: "Policy Desk", InstanceName: created.InstanceName, EntrypointID: door.ID,
		ConnectPath: created.ConnectPath, TokenPrefix: created.TokenPrefix, Enabled: true, Peers: []string{},
		DeploymentStatus: "not_deployed", RuntimeStatus: RuntimeStatus{Status: "stopped", Problems: []RuntimeProblem{}},
		CreatedBy: created.CreatedBy, UpdatedBy: created.UpdatedBy, CreatedTime: created.CreatedTime,
	}
	gotReplaced := decodeData[LinkAgent](t, replaced)
	wantReplaced.UpdatedTime = gotReplaced.UpdatedTime
	if diff := cmp.Diff(wantReplaced, gotReplaced); diff != "" {
		t.Errorf("replaced link agent mismatch (-want +got):\n%s", diff)
	}
	if digestAfter, wakeAfter := storedLinkAgentSecrets(t, api, created.ID); digestAfter != digest || wakeAfter != "" {
		t.Errorf("replace changed the stored token digest or kept wake settings the request left out")
	}

	rotatedRec := api.call(http.MethodPut, "/linkAgents/"+created.ID+"/rotateToken", nil)
	expectStatus(t, rotatedRec, http.StatusOK)
	rotated := decodeData[LinkAgentWithToken](t, rotatedRec)
	rotatedToken := issuedToken(t, rotated)
	if rotatedToken == createdToken || !strings.HasPrefix(rotatedToken, "samk_") || rotatedRec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("rotateToken did not return a new samk_ token with Cache-Control no-store")
	}
	if digestAfter, _ := storedLinkAgentSecrets(t, api, created.ID); digestAfter != sha256Hex(rotatedToken) {
		t.Errorf("stored digest after rotateToken is not the SHA-256 of the token it returned")
	}
	if rotated.Deployment != nil || rotated.DeploymentRefusal != nil {
		t.Errorf("rotateToken of an undeployed link agent = deployment %v refusal %v, want both null", rotated.Deployment, rotated.DeploymentRefusal)
	}
	afterRotation := api.call(http.MethodGet, "/linkAgents/"+created.ID, nil)
	expectNoLinkSecret(t, "GET after rotateToken", afterRotation, createdToken, rotatedToken)
	if got := decodeData[LinkAgent](t, afterRotation).UpdatedTime; got != rotated.UpdatedTime {
		t.Errorf("rotateToken updatedTime = %v, next GET updatedTime = %v, want them equal", rotated.UpdatedTime, got)
	}

	expectStatus(t, api.call(http.MethodDelete, "/linkAgents/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/linkAgents/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPut, "/linkAgents/"+created.ID+"/rotateToken", nil), http.StatusNotFound)
}

func TestLinkAgents_ReplaceKeepsAWakeSecretTheRequestLeavesOutOrRedacts(t *testing.T) {
	tests := []struct {
		name       string
		secret     any
		wantSecret string
	}{
		{name: "left out", secret: nil, wantSecret: linkWakeSecret},
		{name: "sent redacted", secret: "<REDACTED>", wantSecret: linkWakeSecret},
		{name: "sent empty", secret: "", wantSecret: linkWakeSecret},
		{name: "sent anew", secret: "second-secret", wantSecret: "second-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			created := createLinkAgent(api, linkAgentBody("Claims Desk", door.ID))
			wake := map[string]any{
				"url": "https://runtime.example.com/invocations", "tokenUrl": "https://idp.example.com/oauth2/token",
				"clientId": "claims-client", "clientSecret": tt.secret,
			}
			if tt.secret == nil {
				delete(wake, "clientSecret")
			}

			rec := api.call(http.MethodPut, "/linkAgents/"+created.ID, map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "wake": wake})

			expectStatus(t, rec, http.StatusOK)
			expectNoLinkSecret(t, "replace", rec, linkWakeSecret, "second-secret")
			if got := decodeData[LinkAgent](t, rec).Wake; got == nil || orEmpty(got.ClientSecret) != "<REDACTED>" {
				t.Errorf("replace returned wake %+v, want its client secret redacted", got)
			}
			_, stored := storedLinkAgentSecrets(t, api, created.ID)
			var storedWake struct {
				ClientSecret string `json:"client_secret"`
			}
			if err := json.Unmarshal([]byte(stored), &storedWake); err != nil || storedWake.ClientSecret != tt.wantSecret {
				t.Errorf("stored wake client secret after replace is not the expected one (%v)", err)
			}
		})
	}
}

func TestLinkAgents_WakeHeaderValuesNeverComeBackAndAreKeptWhenSentRedacted(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")
	createdRec := api.call(http.MethodPost, "/linkAgents", linkAgentBody("Claims Desk", door.ID))
	created := expectCreated[LinkAgentWithToken](t, createdRec, "linkAgents")
	wantHeaders := map[string]string{"X-Api-Key": "<REDACTED>"}
	responses := map[string]*httptest.ResponseRecorder{
		"create": createdRec,
		"GET":    api.call(http.MethodGet, "/linkAgents/"+created.ID, nil),
		"list":   api.call(http.MethodGet, "/linkAgents", nil),
	}
	for what, rec := range responses {
		expectStatus(t, rec, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[what == "create"])
		expectNoLinkSecret(t, what, rec)
	}
	if diff := cmp.Diff(wantHeaders, decodeData[LinkAgent](t, responses["GET"]).Wake.Headers); diff != "" {
		t.Errorf("GET wake headers mismatch (-want +got):\n%s", diff)
	}
	keepBody := func(headers map[string]any) map[string]any {
		return map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "wake": map[string]any{
			"url": "https://runtime.example.com/invocations", "tokenUrl": "https://idp.example.com/oauth2/token", "clientId": "claims-client", "headers": headers,
		}}
	}

	kept := api.call(http.MethodPut, "/linkAgents/"+created.ID, keepBody(map[string]any{"X-Api-Key": "<REDACTED>"}))

	expectStatus(t, kept, http.StatusOK)
	expectNoLinkSecret(t, "replace", kept)
	if diff := cmp.Diff(wantHeaders, decodeData[LinkAgent](t, kept).Wake.Headers); diff != "" {
		t.Errorf("replace wake headers mismatch (-want +got):\n%s", diff)
	}
	if _, wake := storedLinkAgentSecrets(t, api, created.ID); !strings.Contains(wake, linkWakeHeaderValue) {
		t.Errorf("a header sent back as <REDACTED> did not keep its stored value")
	}
	rotated := api.call(http.MethodPut, "/linkAgents/"+created.ID+"/rotateToken", nil)
	expectStatus(t, rotated, http.StatusOK)
	expectNoLinkSecret(t, "rotateToken", rotated)

	unknown := api.call(http.MethodPut, "/linkAgents/"+created.ID, keepBody(map[string]any{"X-Other": "<REDACTED>"}))

	expectStatus(t, unknown, http.StatusUnprocessableEntity)
	if _, ok := decodeError(t, unknown).ValidationDetails["wake.headers"]; !ok {
		t.Errorf("a <REDACTED> header with no stored value was not refused on wake.headers: %s", unknown.Body.String())
	}
	if _, wake := storedLinkAgentSecrets(t, api, created.ID); !strings.Contains(wake, linkWakeHeaderValue) {
		t.Errorf("a refused replace changed the stored header value")
	}
}

func TestLinkAgents_ReplaceWithANewWakeURLNeedsSecretsSentAgain(t *testing.T) {
	const resentMessage = "must be sent again when the URL it is sent to changes"
	tests := []struct {
		name        string
		secret      any
		headerValue string
		wantField   string
		wantMessage string
	}{
		{name: "client secret left out", secret: nil, headerValue: linkWakeHeaderValue, wantField: "wake.clientSecret", wantMessage: resentMessage},
		{name: "client secret sent redacted", secret: "<REDACTED>", headerValue: linkWakeHeaderValue, wantField: "wake.clientSecret", wantMessage: resentMessage},
		{name: "header sent redacted", secret: linkWakeSecret, headerValue: "<REDACTED>", wantField: "wake.headers", wantMessage: `wake.headers: the value of "X-Api-Key" ` + resentMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			created := createLinkAgent(api, linkAgentBody("Claims Desk", door.ID))
			wake := map[string]any{
				"url": "https://other.example.com/invocations", "tokenUrl": "https://idp.example.com/oauth2/token",
				"clientId": "claims-client", "headers": map[string]any{"X-Api-Key": tt.headerValue},
			}
			if tt.secret != nil {
				wake["clientSecret"] = tt.secret
			}

			rec := api.call(http.MethodPut, "/linkAgents/"+created.ID, map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "wake": wake})

			expectStatus(t, rec, http.StatusUnprocessableEntity)
			expectDetailSaying(t, rec, tt.wantField, tt.wantMessage)
			if _, stored := storedLinkAgentSecrets(t, api, created.ID); strings.Contains(stored, "other.example.com") {
				t.Errorf("a refused replace changed the stored wake URL")
			}
		})
	}
}

func TestLinkAgents_RefusedRequests(t *testing.T) {
	const doorPlaceholder, slackPlaceholder = "<agent_link entrypoint>", "<slack entrypoint>"
	tests := []struct {
		name        string
		body        map[string]any
		wantStatus  int
		wantDetails []string
	}{
		{
			name:        "a field the API does not know",
			body:        map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder, "token": "samk_chosen-by-the-caller"},
			wantStatus:  http.StatusBadRequest,
			wantDetails: []string{"token"},
		},
		{
			name:        "a name too short and no entrypoint",
			body:        map[string]any{"name": "CD"},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"entrypointId", "name"},
		},
		{
			name:        "an entrypoint of another type",
			body:        map[string]any{"name": "Claims Desk", "entrypointId": slackPlaceholder},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"entrypointId"},
		},
		{
			name: "peers and wake settings the agent link would refuse",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder, "peers": []string{"policy/desk"},
				"wake": map[string]any{"url": "http://runtime.example.com/wake", "tokenUrl": "https://idp.example.com/oauth2/token"}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"peers", "wake.clientId", "wake.clientSecret", "wake.url"},
		},
		{
			name: "a wake URL at the IPv4 cloud metadata address",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder,
				"wake": map[string]any{"url": "https://169.254.169.254/x"}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"wake.url"},
		},
		{
			name: "a wake URL at the IPv6 cloud metadata address",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder,
				"wake": map[string]any{"url": "https://[fd00:ec2::254]/"}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"wake.url"},
		},
		{
			name: "a token URL at the IPv4 cloud metadata address",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder,
				"wake": map[string]any{"url": "https://runtime.example.com/wake", "tokenUrl": "https://169.254.169.254/x", "clientId": "claims-client", "clientSecret": linkWakeSecret}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"wake.tokenUrl"},
		},
		{
			name: "a token URL at the IPv6 cloud metadata address",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder,
				"wake": map[string]any{"url": "https://runtime.example.com/wake", "tokenUrl": "https://[fd00:ec2::254]/", "clientId": "claims-client", "clientSecret": linkWakeSecret}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"wake.tokenUrl"},
		},
		{
			name: "a wake URL with a query string",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder,
				"wake": map[string]any{"url": "https://runtime.example.com/wake?code=abc"}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"wake.url"},
		},
		{
			name: "wake header names that differ only in case",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder,
				"wake": map[string]any{"url": "https://runtime.example.com/wake", "headers": map[string]any{"X-A": "1", "x-a": "2"}}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"wake.headers"},
		},
		{
			name: "an Authorization wake header with a token URL",
			body: map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder,
				"wake": map[string]any{"url": "https://runtime.example.com/wake", "tokenUrl": "https://idp.example.com/oauth2/token", "clientId": "claims-client",
					"clientSecret": linkWakeSecret, "headers": map[string]any{"Authorization": linkWakeHeaderValue}}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"wake.headers"},
		},
		{
			name:        "a token digest that is not 64 lowercase hexadecimal characters",
			body:        map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder, "tokenSha256": strings.ToUpper(callersTokenDigest)},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"tokenSha256"},
		},
		{
			name:        "the digest of an empty token",
			body:        map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder, "tokenSha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"tokenSha256"},
		},
		{
			name:        "the digest of a token another link agent uses",
			body:        map[string]any{"name": "Claims Desk", "entrypointId": doorPlaceholder, "tokenSha256": takenTokenDigest},
			wantStatus:  http.StatusUnprocessableEntity,
			wantDetails: []string{"tokenSha256"},
		},
		{
			name:        "a name an agent already has",
			body:        map[string]any{"name": "taken agent", "entrypointId": doorPlaceholder},
			wantStatus:  http.StatusConflict,
			wantDetails: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			slack := api.createSlack("team-slack")
			api.createLinkAgentWithDigest("Taken Agent", door.ID, takenTokenDigest)
			body := map[string]any{}
			for key, value := range tt.body {
				switch value {
				case doorPlaceholder:
					value = door.ID
				case slackPlaceholder:
					value = slack.ID
				}
				body[key] = value
			}

			rec := api.call(http.MethodPost, "/linkAgents", body)

			expectStatus(t, rec, tt.wantStatus)
			var details []string
			for field := range decodeError(t, rec).ValidationDetails {
				details = append(details, field)
			}
			slices.Sort(details)
			if diff := cmp.Diff(tt.wantDetails, details); diff != "" {
				t.Errorf("validationDetails fields mismatch (-want +got):\n%s\n%s", diff, rec.Body.String())
			}
			if page := decodeList[LinkAgent](t, api.call(http.MethodGet, "/linkAgents", nil)); len(page.Data) != 1 {
				t.Errorf("link agents after a refused create = %d, want only the one that existed", len(page.Data))
			}
		})
	}
}

func TestLinkAgents_EachRouteNeedsItsScope(t *testing.T) {
	const idPlaceholder = "<link agent id>"
	tests := []struct {
		name      string
		method    string
		path      string
		body      map[string]any
		scope     string
		alsoNeeds []string
		wantOK    int
	}{
		{name: "list", method: http.MethodGet, path: "/linkAgents", scope: rbac.ScopeAgentBuilderRead, wantOK: http.StatusOK},
		{name: "get", method: http.MethodGet, path: "/linkAgents/" + idPlaceholder, scope: rbac.ScopeAgentBuilderRead, wantOK: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/linkAgents", body: map[string]any{"name": "Billing Desk"}, scope: rbac.ScopeAgentBuilderCreate, wantOK: http.StatusCreated},
		{name: "replace", method: http.MethodPut, path: "/linkAgents/" + idPlaceholder, body: map[string]any{"name": "Claims Desk"}, scope: rbac.ScopeAgentBuilderUpdate, wantOK: http.StatusOK},
		{name: "rotate the token", method: http.MethodPut, path: "/linkAgents/" + idPlaceholder + "/rotateToken", scope: rbac.ScopeAgentBuilderDeploy, alsoNeeds: []string{rbac.ScopeAgentBuilderUpdate}, wantOK: http.StatusOK},
		{name: "compare secrets", method: http.MethodPut, path: "/linkAgents/" + idPlaceholder + "/secretsDiff", body: map[string]any{"values": map[string]any{"tokenSha256": callersTokenDigest}}, scope: rbac.ScopeAgentBuilderUpdate, wantOK: http.StatusOK},
		{name: "delete", method: http.MethodDelete, path: "/linkAgents/" + idPlaceholder, scope: rbac.ScopeAgentBuilderDelete, wantOK: http.StatusNoContent},
	}
	otherScopes := []string{rbac.ScopeAgentBuilderRead, rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderUpdate, rbac.ScopeAgentBuilderDelete, rbac.ScopeAgentBuilderDeploy}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			existing := createLinkAgent(api, map[string]any{"name": "Claims Desk", "entrypointId": door.ID})
			path := strings.ReplaceAll(tt.path, idPlaceholder, existing.ID)
			var body map[string]any
			if tt.body != nil {
				body = tt.body
				if _, comparesSecrets := body["values"]; !comparesSecrets {
					body = with(body, "entrypointId", door.ID)
				}
			}
			without := slices.DeleteFunc(slices.Clone(otherScopes), func(s string) bool { return s == tt.scope })

			refused := api.call(tt.method, path, body, without...)
			allowed := api.call(tt.method, path, body, append([]string{tt.scope}, tt.alsoNeeds...)...)

			expectStatus(t, refused, http.StatusForbidden)
			expectStatus(t, allowed, tt.wantOK)
			if strings.Contains(refused.Body.String(), "samk_") {
				t.Errorf("a refused %s carries a token", tt.name)
			}
		})
	}
}

func TestLinkAgents_RotateTokenNeedsTheUpdateScopeAsWellAsDeploy(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")
	link := createLinkAgent(api, linkAgentBody("Claims Desk", door.ID))
	rotatePath := "/linkAgents/" + link.ID + "/rotateToken"
	digestBefore, _ := storedLinkAgentSecrets(t, api, link.ID)

	refused := api.call(http.MethodPut, rotatePath, nil, rbac.ScopeAgentBuilderDeploy)

	expectStatus(t, refused, http.StatusForbidden)
	if strings.Contains(refused.Body.String(), "samk_") {
		t.Errorf("a refused rotateToken carries a token")
	}
	if digestAfter, _ := storedLinkAgentSecrets(t, api, link.ID); digestAfter != digestBefore {
		t.Errorf("a rotateToken refused for the missing update scope changed the stored token digest")
	}

	allowed := api.call(http.MethodPut, rotatePath, nil, rbac.ScopeAgentBuilderDeploy, rbac.ScopeAgentBuilderUpdate)

	expectStatus(t, allowed, http.StatusOK)
	rotated := decodeData[LinkAgentWithToken](t, allowed)
	if digestAfter, _ := storedLinkAgentSecrets(t, api, link.ID); digestAfter == digestBefore || digestAfter != sha256Hex(issuedToken(t, rotated)) {
		t.Errorf("rotateToken with update and deploy did not store the digest of the token it returned")
	}
}

func TestLinkAgents_AReplaceThatChangesTheDigestAlsoNeedsTheDeployScope(t *testing.T) {
	tests := []struct {
		name       string
		sent       map[string]any
		scopes     []string
		wantStatus int
		wantDigest string
	}{
		{name: "another digest with update alone", sent: map[string]any{"tokenSha256": callersOtherTokenDigest}, scopes: []string{rbac.ScopeAgentBuilderUpdate}, wantStatus: http.StatusForbidden, wantDigest: callersTokenDigest},
		{name: "another digest with update and deploy", sent: map[string]any{"tokenSha256": callersOtherTokenDigest}, scopes: []string{rbac.ScopeAgentBuilderUpdate, rbac.ScopeAgentBuilderDeploy}, wantStatus: http.StatusOK, wantDigest: callersOtherTokenDigest},
		{name: "the stored digest sent again with update alone", sent: map[string]any{"tokenSha256": callersTokenDigest}, scopes: []string{rbac.ScopeAgentBuilderUpdate}, wantStatus: http.StatusOK, wantDigest: callersTokenDigest},
		{name: "the digest left out with update alone", scopes: []string{rbac.ScopeAgentBuilderUpdate}, wantStatus: http.StatusOK, wantDigest: callersTokenDigest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			link := api.createLinkAgentWithDigest("Claims Desk", door.ID, callersTokenDigest)
			body := map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "owner": "bob@example.com"}
			maps.Copy(body, tt.sent)

			replaced := api.call(http.MethodPut, "/linkAgents/"+link.ID, body, tt.scopes...)

			expectStatus(t, replaced, tt.wantStatus)
			if stored, _ := storedLinkAgentSecrets(t, api, link.ID); stored != tt.wantDigest {
				t.Errorf("stored token digest after the replace is not the expected one")
			}
			wantOwner := "bob@example.com"
			if tt.wantStatus != http.StatusOK {
				wantOwner = ""
			}
			if owner := orEmpty(decodeData[LinkAgent](t, api.call(http.MethodGet, "/linkAgents/"+link.ID, nil)).Owner); owner != wantOwner {
				t.Errorf("owner after the replace = %q, want %q", owner, wantOwner)
			}
		})
	}
}

func TestLinkAgents_AnAPIKeyWithTheUpdateScopeAloneCannotChangeTheDigest(t *testing.T) {
	core := apiKeysPresentedCore(t, rbac.ScopeAgentBuilderUpdate)
	send := func(method, path, bearer string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return apiKeysPresent(t, core, method, platpkg.APIV2Prefix+path, string(raw), bearer)
	}
	doorRec := send(http.MethodPost, "/entrypoints", apiKeysPersonToken, map[string]any{"name": "partner-link", "description": "Door for partner agents to join the mesh.", "entrypointType": "agent_link", "values": map[string]any{"maxInflight": 4}})
	expectStatus(t, doorRec, http.StatusCreated)
	door := decodeData[Entrypoint](t, doorRec)
	linkRec := send(http.MethodPost, "/linkAgents", apiKeysPersonToken, map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "tokenSha256": callersTokenDigest})
	expectStatus(t, linkRec, http.StatusCreated)
	link := decodeData[LinkAgentWithToken](t, linkRec)
	replaceWith := func(digest string) *httptest.ResponseRecorder {
		return send(http.MethodPut, "/linkAgents/"+link.ID, apiKeysPresentedSecret, map[string]any{"name": "Claims Desk", "entrypointId": door.ID, "tokenSha256": digest})
	}

	refused := replaceWith(callersOtherTokenDigest)
	kept := replaceWith(callersTokenDigest)

	expectStatus(t, refused, http.StatusForbidden)
	expectStatus(t, kept, http.StatusOK)
	var stored string
	if err := core.DB().QueryRowContext(t.Context(), `SELECT token_sha256 FROM link_agents WHERE id = $1`, link.ID).Scan(&stored); err != nil {
		t.Fatalf("read the stored link agent: %v", err)
	}
	if stored != callersTokenDigest {
		t.Errorf("an API key holding agent_builder:*:update alone changed the stored token digest")
	}
}

func TestLinkAgents_RotateTokenRouteDeclaresBothScopesThroughItsGate(t *testing.T) {
	core, deps := newTestCore(t)
	for _, rt := range routeTable(deps, core, core.Logger()) {
		if rt.Method != http.MethodPut || rt.Path != "/linkAgents/{id}/rotateToken" {
			continue
		}
		if rt.Gate != GateLinkAgentRotateToken || rt.Scope != "" || len(rt.AnyOfScopes) != 0 {
			t.Errorf("rotateToken route = gate %q scope %q any-of %v, want only %q", rt.Gate, rt.Scope, rt.AnyOfScopes, GateLinkAgentRotateToken)
		}
		text := gateDocs[rt.Gate].Text
		for _, scope := range []string{rbac.ScopeAgentBuilderUpdate, rbac.ScopeAgentBuilderDeploy} {
			if !strings.Contains(text, "`"+scope+"`") {
				t.Errorf("the rotateToken gate's description %q does not name %s", text, scope)
			}
		}
		return
	}
	t.Fatal("no PUT /linkAgents/{id}/rotateToken route")
}

func TestLinkAgents_RotateTokenReportsARefusedRedeployOnTheWire(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")
	link := createLinkAgent(api, linkAgentBody("Claims Desk", door.ID))
	expectStatus(t, api.deploy(door.ID), http.StatusCreated)
	expectStatus(t, deployLinkAgent(api, link.ID, "deploy"), http.StatusCreated)
	_, err := api.core.DB().ExecContext(t.Context(),
		`INSERT INTO gateway_deployments (id, gateway_id, status, action, created_by, created_time, updated_time) VALUES ($1, $2, 'success', 'undeploy', 'alice@example.com', $3, $3)`,
		"0190f5ec-0000-7000-8000-00000000f001", door.ID, int64(4_102_444_800_000))
	if err != nil {
		t.Fatalf("record the entrypoint's undeploy: %v", err)
	}

	rec := api.call(http.MethodPut, "/linkAgents/"+link.ID+"/rotateToken", nil)

	expectStatus(t, rec, http.StatusOK)
	rotated := decodeData[LinkAgentWithToken](t, rec)
	const wantRefusal = `the link agent's entrypoint "partner-link" is not deployed; deploy the entrypoint first`
	if rotated.Deployment != nil || orEmpty(rotated.DeploymentRefusal) != wantRefusal {
		t.Errorf("rotateToken = deployment %v refusal %q, want no deployment and %q", rotated.Deployment, orEmpty(rotated.DeploymentRefusal), wantRefusal)
	}
	raw := rawData(t, rec)
	if got, ok := raw["deploymentRefusal"].(string); !ok || got != wantRefusal {
		t.Errorf("wire deploymentRefusal = %v, want the sentence %q", raw["deploymentRefusal"], wantRefusal)
	}
	if value, present := raw["deployment"]; !present || value != nil {
		t.Errorf("wire deployment = %v (present %t), want null", value, present)
	}
	if digest, _ := storedLinkAgentSecrets(t, api, link.ID); issuedToken(t, rotated) == issuedToken(t, link) || digest != sha256Hex(issuedToken(t, rotated)) {
		t.Errorf("a refused redeploy did not leave the new token stored and returned")
	}
}

func TestLinkAgents_DeleteFailsWith502WhenTheUndeployFails(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")
	link := createLinkAgent(api, linkAgentBody("Claims Desk", door.ID))
	expectStatus(t, api.deploy(door.ID), http.StatusCreated)
	expectStatus(t, deployLinkAgent(api, link.ID, "deploy"), http.StatusCreated)
	api.gateway.failAdds.Store(true)

	started := time.Now()
	rec := api.call(http.MethodDelete, "/linkAgents/"+link.ID, nil)

	expectStatus(t, rec, http.StatusBadGateway)
	if took := time.Since(started); took > 30*time.Second {
		t.Errorf("the failed undeploy took %s; the Executor's failure for this attempt should end it, not the confirmation timeout", took)
	}
	expectStatus(t, api.call(http.MethodGet, "/linkAgents/"+link.ID, nil), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/agents/"+link.ID, nil), http.StatusOK)
}

func TestAgents_WritersRefuseALinkAgentsMirror(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   map[string]any
	}{
		{name: "PUT renames it", method: http.MethodPut, body: map[string]any{"name": "hijacked-mirror", "description": "Renamed through the agents route.", "systemPrompt": "x"}},
		{name: "DELETE removes it", method: http.MethodDelete, body: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			door := api.createAgentLink("partner-link")
			link := createLinkAgent(api, map[string]any{"name": "Claims Desk", "entrypointId": door.ID})

			rec := api.call(tt.method, "/agents/"+link.ID, tt.body)

			expectStatus(t, rec, http.StatusConflict)
			got := api.call(http.MethodGet, "/linkAgents/"+link.ID, nil)
			expectStatus(t, got, http.StatusOK)
			if name := decodeData[LinkAgent](t, got).Name; name != "Claims Desk" {
				t.Errorf("link agent name after the refused write = %q, want Claims Desk", name)
			}
			mirror := api.call(http.MethodGet, "/agents/"+link.ID, nil)
			expectStatus(t, mirror, http.StatusOK)
			if agentType := decodeData[Agent](t, mirror).AgentType; agentType != "link" {
				t.Errorf("GET /agents/{id} of a link agent's mirror has agentType %q, want link", agentType)
			}
			if page := decodeList[Agent](t, api.call(http.MethodGet, "/agents", nil)); slices.ContainsFunc(page.Data, func(a Agent) bool { return a.ID == link.ID }) {
				t.Errorf("the agents list carries the link agent's mirror")
			}
		})
	}
}

func deployLinkAgent(api *entrypointAPI, id, action string) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": id, "action": action})
}

func TestLinkAgents_DeployThroughTheirEntrypoint(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")
	link := createLinkAgent(api, linkAgentBody("Claims Desk", door.ID))

	early := deployLinkAgent(api, link.ID, "deploy")
	expectDetailSaying(t, early, "agentId", `the link agent's entrypoint "partner-link" is not deployed; deploy the entrypoint first`)

	expectStatus(t, api.deploy(door.ID), http.StatusCreated)
	deployed := deployLinkAgent(api, link.ID, "deploy")
	expectStatus(t, deployed, http.StatusCreated)
	linkToken := issuedToken(t, link)
	expectNoLinkSecret(t, "deploy", deployed, linkToken, linkWakeSecret)
	read := decodeData[LinkAgent](t, api.call(http.MethodGet, "/linkAgents/"+link.ID, nil))
	if read.DeploymentStatus != "deployed" || orEmpty(read.SyncStatus) != "in_sync" || read.RuntimeStatus.Status != "disconnected" || read.Deployed == nil {
		t.Errorf("deployed link agent = deployment %q sync %q runtime %q deployed %v, want deployed, in_sync, disconnected and a deployed snapshot",
			read.DeploymentStatus, orEmpty(read.SyncStatus), read.RuntimeStatus.Status, read.Deployed)
	}
	if read.Deployed != nil && orEmpty(read.Deployed.TokenPrefix) != orEmpty(link.TokenPrefix) {
		t.Errorf("deployed.tokenPrefix = %q, want the generated token's prefix %q", orEmpty(read.Deployed.TokenPrefix), orEmpty(link.TokenPrefix))
	}

	rotatedRec := api.call(http.MethodPut, "/linkAgents/"+link.ID+"/rotateToken", nil)
	expectStatus(t, rotatedRec, http.StatusOK)
	rotated := decodeData[LinkAgentWithToken](t, rotatedRec)
	wantRollout := &AgentDeployment{Type: "agentDeployment", AgentID: link.ID, Status: "success", Action: "update"}
	if got := rotated.Deployment; got != nil {
		wantRollout.ID, wantRollout.CreatedBy, wantRollout.CreatedTime, wantRollout.UpdatedTime = got.ID, got.CreatedBy, got.CreatedTime, got.UpdatedTime
	}
	if diff := cmp.Diff(wantRollout, rotated.Deployment); diff != "" {
		t.Errorf("rotateToken of a deployed link agent returned another deployment (-want +got):\n%s", diff)
	}
	if sameToken := issuedToken(t, rotated) == linkToken; sameToken || orEmpty(rotated.SyncStatus) != "in_sync" || rotated.DeploymentRefusal != nil {
		t.Errorf("rotateToken of a deployed link agent = same token %t sync %q refusal %v, want a new token, in_sync and no refusal",
			sameToken, orEmpty(rotated.SyncStatus), rotated.DeploymentRefusal)
	}
	if rotated.Deployment != nil && rotated.Deployment.ID == decodeData[AgentDeployment](t, deployed).ID {
		t.Errorf("rotateToken returned the earlier deployment, want the update it started")
	}

	moved := api.call(http.MethodPut, "/linkAgents/"+link.ID, map[string]any{"name": "Claims Desk", "entrypointId": api.createAgentLink("second-link").ID})
	expectDetailSaying(t, moved, "entrypointId", "undeploy the link agent before moving it to another entrypoint")

	undeployDoor := api.call(http.MethodPost, "/entrypointDeployments", map[string]any{"entrypointId": door.ID, "action": "undeploy"})
	expectDetailSaying(t, undeployDoor, "entrypointId", "cannot undeploy this entrypoint while link agents are deployed on it: Claims Desk; undeploy them first")
	deleteDoor := api.call(http.MethodDelete, "/entrypoints/"+door.ID, nil)
	expectDetailSaying(t, deleteDoor, "entrypointId", "cannot delete this entrypoint while link agents are deployed on it: Claims Desk; undeploy them first")

	rollback := api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": link.ID, "action": "update", "deploymentId": decodeData[AgentDeployment](t, deployed).ID})
	expectDetailSaying(t, rollback, "deploymentId", "a link agent can't be rolled back")

	expectStatus(t, api.call(http.MethodDelete, "/linkAgents/"+link.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/linkAgents/"+link.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPost, "/entrypointDeployments", map[string]any{"entrypointId": door.ID, "action": "undeploy"}), http.StatusCreated)
}

func TestLinkAgents_CreateKeepsAWakeURLThatCarriesAnAPIVersion(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	door := api.createAgentLink("partner-link")
	const wakeURL = "https://runtime.example.com/invocations?api-version=v1"
	body := linkAgentBody("Claims Desk", door.ID)
	body["wake"].(map[string]any)["url"] = wakeURL

	created := createLinkAgent(api, body)

	if created.Wake == nil || created.Wake.URL != wakeURL {
		t.Errorf("created wake = %+v, want url %q", created.Wake, wakeURL)
	}
	got := decodeData[LinkAgent](t, api.call(http.MethodGet, "/linkAgents/"+created.ID, nil))
	if got.Wake == nil || got.Wake.URL != wakeURL {
		t.Errorf("read wake = %+v, want url %q", got.Wake, wakeURL)
	}
}
