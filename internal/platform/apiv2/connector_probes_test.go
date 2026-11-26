// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const storedBrokerPassword = "stored-broker-password-value"

func createEventMeshConnector(t *testing.T, app *apiClient) Connector {
	t.Helper()
	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "event-mesh", "description": "The broker the fulfilment agent publishes to.",
		"connectorType": "event_mesh", "connectorSubtype": "solace",
		"values": map[string]any{
			"broker_url": "tcp://broker.example.com:55555", "broker_vpn": "default",
			"broker_username": "user", "broker_password": storedBrokerPassword,
		},
	})
	expectStatus(t, rec, http.StatusCreated)
	return decodeData[Connector](t, rec)
}

func specServer(t *testing.T, doc string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestConnectorTestConnection_FailureIs200WithTheResult(t *testing.T) {
	app := newConnectorAPI(t)

	rec := app.call(http.MethodPut, "/connectors/testConnection", map[string]any{
		"values": map[string]any{"broker_url": "tcp://127.0.0.1:1", "broker_vpn": "default", "broker_username": "u", "broker_password": "p"},
	})
	expectStatus(t, rec, http.StatusOK)
	res := decodeData[TestConnectionResult](t, rec)
	if res.Success {
		t.Fatal("want a failed probe")
	}
	if res.Message == nil || *res.Message == "" {
		t.Fatal("a failed probe must say why")
	}
	for _, leak := range []string{"127.0.0.1", "dial tcp", "connection refused", ":1"} {
		if strings.Contains(*res.Message, leak) {
			t.Errorf("message %q leaks upstream text (%q)", *res.Message, leak)
		}
	}
	for _, key := range []string{`"success"`, `"message"`, `"errorClass"`, `"stage"`} {
		if !bytes.Contains(rec.Body.Bytes(), []byte(key)) {
			t.Errorf("%s must always be sent: %s", key, rec.Body.String())
		}
	}
}

func TestConnectorTestConnection_NeedsAConnectorOrValues(t *testing.T) {
	app := newConnectorAPI(t)
	expectDetailSaying(t, app.call(http.MethodPut, "/connectors/testConnection", map[string]any{}), "values", "connectorId or values must be provided")
}

func TestConnectorDetectAuth_EmptySpecSendsEveryField(t *testing.T) {
	app := newConnectorAPI(t)
	url := specServer(t, `{"openapi":"3.0.0","paths":{}}`)

	raw := app.callRaw(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{"specificationUrl": url})
	for _, want := range []string{`"type":"detectAuthResult"`, `"schemes":[]`, `"suggestedAuth":null`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("want %s for a spec with no schemes, got %s", want, raw)
		}
	}
}

func TestConnectorDetectAuth_DetailsAreTypedAndCamelCase(t *testing.T) {
	app := newConnectorAPI(t)
	url := specServer(t, `{"openapi":"3.0.0","components":{"securitySchemes":{
		"bearerAuth":{"type":"http","scheme":"bearer","bearerFormat":"JWT"},
		"keyAuth":{"type":"apiKey","in":"header","name":"X-API-Key"},
		"oauthAuth":{"type":"oauth2","flows":{"authorizationCode":{
			"authorizationUrl":"https://auth.example.com/authorize","tokenUrl":"https://auth.example.com/token",
			"refreshUrl":"https://auth.example.com/refresh","scopes":{"read":"Read"}}}},
		"oidcAuth":{"type":"openIdConnect","openIdConnectUrl":"https://auth.example.com/.well-known/openid-configuration"},
		"mtlsAuth":{"type":"mutualTLS"}}},"paths":{}}`)

	rec := app.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{"specificationUrl": url})
	expectStatus(t, rec, http.StatusOK)
	raw := rec.Body.Bytes()
	for _, snake := range []string{"bearer_format", "authorization_url", "token_url", "refresh_url", "open_id_connect_url", "openid_connect_url", "mutualTLS", "openIdConnect\""} {
		if bytes.Contains(raw, []byte(snake)) {
			t.Errorf("v1 spelling %q still on the v2 wire: %s", snake, raw)
		}
	}

	byName := map[string]SecurityScheme{}
	for _, s := range decodeData[DetectAuthResult](t, rec).Schemes {
		byName[s.SchemeName] = s
	}
	str := func(v *string) string {
		if v == nil {
			return "<null>"
		}
		return *v
	}
	if s := byName["bearerAuth"]; s.AuthType != "http" || str(s.Details.Scheme) != "bearer" || str(s.Details.BearerFormat) != "JWT" || s.Details.TokenURL != nil {
		t.Errorf("bearerAuth = %+v", s)
	}
	if s := byName["keyAuth"]; s.AuthType != "apikey" || str(s.Details.In) != "header" || str(s.Details.Name) != "X-API-Key" || s.Details.Scheme != nil {
		t.Errorf("keyAuth = %+v", s)
	}
	oauth := byName["oauthAuth"].Details
	if byName["oauthAuth"].AuthType != "oauth" || str(oauth.AuthorizationURL) != "https://auth.example.com/authorize" ||
		str(oauth.TokenURL) != "https://auth.example.com/token" || str(oauth.RefreshURL) != "https://auth.example.com/refresh" {
		t.Errorf("oauthAuth = %+v", byName["oauthAuth"])
	}
	oidc := byName["oidcAuth"]
	if oidc.AuthType != "oauth" || str(oidc.Details.OpenIDConnectURL) != "https://auth.example.com/.well-known/openid-configuration" || oidc.Details.AuthorizationURL != nil {
		t.Errorf("oidcAuth = %+v", oidc)
	}
	if s := byName["mtlsAuth"]; s.AuthType != "mutual_tls" {
		t.Errorf("mtlsAuth authType = %q, want mutual_tls", s.AuthType)
	}

	var wire struct {
		Data struct {
			Schemes []struct {
				Details map[string]json.RawMessage `json:"details"`
			} `json:"schemes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, s := range wire.Data.Schemes {
		if len(s.Details) != 8 {
			t.Errorf("details has %d keys, want all 8: %v", len(s.Details), s.Details)
		}
	}
}

func TestConnectorDetectAuth_SuggestsTheSchemeToConfigure(t *testing.T) {
	app := newConnectorAPI(t)
	url := specServer(t, `{"openapi":"3.0.0","components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},"paths":{}}`)

	res := decodeData[DetectAuthResult](t, app.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{"specificationUrl": url}))
	if res.SuggestedAuth == nil || res.SuggestedAuth.AuthType != "http" || res.SuggestedAuth.Config["schemeName"] != "bearerAuth" {
		t.Errorf("suggestedAuth = %+v", res.SuggestedAuth)
	}
}

func TestConnectorDetectAuth_ReadsAnInlineSpecification(t *testing.T) {
	app := newConnectorAPI(t)
	res := decodeData[DetectAuthResult](t, app.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{
		"specificationContent": `{"openapi":"3.0.0","components":{"securitySchemes":{"k":{"type":"apiKey","in":"query","name":"key"}}},"paths":{}}`,
	}))
	if len(res.Schemes) != 1 || res.Schemes[0].AuthType != "apikey" {
		t.Errorf("schemes = %+v", res.Schemes)
	}
}

func TestConnectorDetectAuth_NeedsExactlyOneSource(t *testing.T) {
	app := newConnectorAPI(t)
	neither := app.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{})
	expectDetailSaying(t, neither, "specificationUrl", "send this or specificationContent")
	expectDetailSaying(t, neither, "specificationContent", "send this or specificationUrl")

	both := app.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{
		"specificationUrl": specServer(t, `{"openapi":"3.0.0"}`), "specificationContent": `{"openapi":"3.0.0"}`,
	})
	expectDetailSaying(t, both, "specificationUrl", "not both")
	expectDetailSaying(t, both, "specificationContent", "not both")
}

func TestConnectorDetectAuth_RejectionsNameTheField(t *testing.T) {
	app := newConnectorAPI(t)
	detect := func(body map[string]any) *httptest.ResponseRecorder {
		return app.call(http.MethodPut, "/connectors/openapi/detectAuth", body)
	}

	expectDetailSaying(t, detect(map[string]any{"specificationUrl": "ftp://api.example.com/openapi.json"}), "specificationUrl", "use http or https")
	expectDetailSaying(t, detect(map[string]any{
		"specificationContent": `{"swagger":"2.0","info":{"title":"Orders","version":"1.0"},"paths":{}}`,
	}), "specificationContent", "only OpenAPI 3.x is supported")

	unreadable := detect(map[string]any{"specificationContent": "openapi: [3.0.0"})
	expectDetailSaying(t, unreadable, "specificationContent", "the specification is not valid JSON or YAML")
	if strings.Contains(unreadable.Body.String(), "yaml:") {
		t.Errorf("the parser's text reached the client: %s", unreadable.Body.String())
	}
	expectDetailSaying(t, detect(map[string]any{"specificationUrl": specServer(t, "openapi: [3.0.0")}),
		"specificationUrl", "the specification is not valid JSON or YAML")
}

func TestConnectorSecretsDiff_ReportsPerFieldWithoutValues(t *testing.T) {
	app := newConnectorAPI(t)
	c := createEventMeshConnector(t, app)

	for _, tc := range []struct {
		name, sent string
		matches    bool
	}{
		{"identical", storedBrokerPassword, true},
		{"redacted placeholder", redacted, true},
		{"changed", "a-different-password", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := app.call(http.MethodPut, "/connectors/"+c.ID+"/secretsDiff", map[string]any{
				"values": map[string]any{"broker_password": tc.sent},
			})
			expectStatus(t, rec, http.StatusOK)
			if bytes.Contains(rec.Body.Bytes(), []byte(storedBrokerPassword)) {
				t.Fatalf("the stored secret reached the wire: %s", rec.Body.String())
			}
			res := decodeData[SecretsDiffResult](t, rec)
			if got := res.Secrets["broker_password"].Matches; got != tc.matches {
				t.Errorf("matches = %v, want %v (%s)", got, tc.matches, rec.Body.String())
			}
		})
	}
}

func TestConnectorSecretsDiff_MissingConnectorIs404(t *testing.T) {
	app := newConnectorAPI(t)
	expectStatus(t, app.call(http.MethodPut, "/connectors/"+missingID+"/secretsDiff", map[string]any{"values": map[string]any{}}), http.StatusNotFound)
}

func TestConnectorProbes_PathsExist(t *testing.T) {
	app := newConnectorAPI(t)
	c := createEventMeshConnector(t, app)
	specURL := specServer(t, `{"openapi":"3.0.0","paths":{}}`)

	for _, rt := range []struct {
		path string
		body map[string]any
	}{
		{"/connectors/testConnection", map[string]any{"values": map[string]any{"broker_url": "tcp://127.0.0.1:1", "broker_vpn": "v", "broker_username": "u", "broker_password": "p"}}},
		{"/connectors/" + c.ID + "/secretsDiff", map[string]any{"values": map[string]any{}}},
		{"/connectors/openapi/detectAuth", map[string]any{"specificationUrl": specURL}},
	} {
		t.Run(rt.path, func(t *testing.T) {
			expectStatus(t, app.call(http.MethodPut, rt.path, rt.body), http.StatusOK)
			expectStatus(t, app.call(http.MethodPost, rt.path, rt.body), http.StatusMethodNotAllowed)
		})
	}
}
