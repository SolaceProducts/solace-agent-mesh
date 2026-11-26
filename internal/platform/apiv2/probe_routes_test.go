// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	probeRouteStoredBrokerURL = "tcps://broker.example.com:55443"
	probeRouteStoredMailHost  = "imap.stored.example.com"
	probeRouteStoredMailPort  = 143
	probeRouteStoredSecret    = "stored-secret-value"
	probeRouteRefusal         = "enter the credentials again"
)

type probeRoute struct {
	name           string
	path           string
	identify       map[string]any
	credentialKeys []string
	movedShapes    map[string]map[string]any
	namesTheFix    bool
}

type probeRouteCredentialShape struct {
	name   string
	values func(route probeRoute) map[string]any
}

var probeRouteUnsuppliedCredentials = []probeRouteCredentialShape{
	{name: "no credentials", values: func(probeRoute) map[string]any { return nil }},
	{name: "redacted placeholders", values: func(route probeRoute) map[string]any {
		return probeRouteEachCredential(route, redacted)
	}},
	{name: "blank secrets", values: func(route probeRoute) map[string]any {
		return probeRouteEachCredential(route, "")
	}},
}

var probeRouteScopeSets = map[string][]string{
	"read-only": {rbac.ScopeConnectorRead, rbac.ScopeEntrypointRead},
	"editor":    {rbac.ScopeConnectorRead, rbac.ScopeEntrypointRead, rbac.ScopeConnectorUpdate, rbac.ScopeEntrypointUpdate},
}

func probeRouteEachCredential(route probeRoute, value string) map[string]any {
	values := map[string]any{}
	for _, key := range route.credentialKeys {
		values[key] = value
	}
	return values
}

func probeRouteMovedBrokerURLs() map[string]map[string]any {
	stored := probeRouteStoredBrokerURL
	shapes := map[string]map[string]any{}
	for name, url := range map[string]string{
		"other host":       "tcps://other.example.com:55443",
		"port only":        "tcps://broker.example.com:1",
		"scheme only":      "tcp://broker.example.com:55443",
		"host case only":   "tcps://BROKER.example.com:55443",
		"trailing dot":     "tcps://broker.example.com.:55443",
		"IDN spelling":     "tcps://brökér.example.com:55443",
		"IPv6 loopback":    "tcps://[::1]:55443",
		"IPv4-mapped IPv6": "tcps://[::ffff:127.0.0.1]:55443",
		"decimal IPv4":     "tcps://2130706433:55443",
		"host list":        stored + ",tcps://evil.example.net:55443",
		"userinfo trick":   "tcps://broker.example.com:55443@evil.example.net",
		"suffix trick":     stored + ".evil.example.net",
		"trailing slash":   stored + "/",
		"loopback":         "tcp://127.0.0.1:9",
		"metadata address": "tcp://169.254.169.254:80",
		"padded other URL": "  tcps://other.example.com:55443 ",
	} {
		shapes[name] = map[string]any{"broker_url": url}
	}
	return shapes
}

func probeRouteMovedEventMeshEntrypoint() map[string]map[string]any {
	shapes := probeRouteMovedBrokerURLs()
	maps.Copy(shapes, map[string]map[string]any{
		"TLS verification off with the saved URL":         {"broker_url": probeRouteStoredBrokerURL, "tls_skip_verify": true},
		"TLS verification off as text with the saved URL": {"broker_url": probeRouteStoredBrokerURL, "tls_skip_verify": "true"},
		"TLS verification off, URL not sent":              {"tls_skip_verify": true},
	})
	return shapes
}

func probeRouteMovedMailbox() map[string]map[string]any {
	host := probeRouteStoredMailHost
	return map[string]map[string]any{
		"other host and port":               {"imap_host": "imap.other.example.com", "imap_port": float64(587)},
		"other host, port absent":           {"imap_host": "imap.other.example.com"},
		"host case only":                    {"imap_host": "IMAP.stored.example.com", "imap_port": float64(probeRouteStoredMailPort)},
		"host with trailing dot":            {"imap_host": host + ".", "imap_port": float64(probeRouteStoredMailPort)},
		"port only, as a number":            {"imap_host": host, "imap_port": float64(993)},
		"stored port resent as text":        {"imap_host": host, "imap_port": "143"},
		"stored port resent as padded text": {"imap_host": host, "imap_port": " 143 "},
		"port only, as text":                {"imap_host": host, "imap_port": "993"},
		"port only, as padded text":         {"imap_host": host, "imap_port": " 993 "},
		"port only, non-numeric text":       {"imap_host": host, "imap_port": "abc"},
		"port alone, no host":               {"imap_port": float64(993)},
	}
}

var probeRouteEntrypointSeq atomic.Int32

func probeRouteSeedEntrypoint(t *testing.T, api *apiClient, gatewayType string, values map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	valuesJSON, description := string(encoded), "route table entrypoint"
	gateway := &store.Gateway{
		Name: fmt.Sprintf("route-table-%s-%d", gatewayType, probeRouteEntrypointSeq.Add(1)), Description: &description,
		Type: gatewayType, Values: &valuesJSON, CreatedBy: "test", UpdatedBy: "test",
	}
	if err := store.NewGatewayRepo(api.core.DB()).Create(t.Context(), gateway); err != nil {
		t.Fatalf("seed entrypoint: %v", err)
	}
	return gateway.ID
}

func probeRoutes(t *testing.T, api *apiClient) []probeRoute {
	t.Helper()
	brokerValues := map[string]any{
		"broker_url": probeRouteStoredBrokerURL, "broker_vpn": "stored-vpn", "broker_username": "stored-user", "broker_password": probeRouteStoredSecret,
	}
	connectorID := ssrfSeedConnector(t, api, "event_mesh", "solace", brokerValues, "")
	brokerEntrypointID := probeRouteSeedEntrypoint(t, api, "event_mesh", brokerValues)
	basicMailID := probeRouteSeedEntrypoint(t, api, "email", map[string]any{
		"mailbox": "ops@example.com", "imap_host": probeRouteStoredMailHost, "imap_port": probeRouteStoredMailPort, "auth_mode": "basic", "imap_password": probeRouteStoredSecret,
	})
	oauthMailID := probeRouteSeedEntrypoint(t, api, "email", map[string]any{
		"mailbox": "ops@example.com", "imap_host": probeRouteStoredMailHost, "imap_port": probeRouteStoredMailPort, "auth_mode": "oauth2",
		"oauth2_tenant_id": "tenant", "oauth2_client_id": "client", "oauth2_client_secret": probeRouteStoredSecret,
	})
	return []probeRoute{
		{name: "connector testConnection", path: "/connectors/testConnection", identify: map[string]any{"connectorId": connectorID},
			credentialKeys: []string{"broker_password"}, movedShapes: probeRouteMovedBrokerURLs()},
		{name: "entrypoint testConnection (event mesh)", path: "/entrypoints/testConnection", identify: map[string]any{"entrypointId": brokerEntrypointID},
			credentialKeys: []string{"broker_password"}, movedShapes: probeRouteMovedEventMeshEntrypoint(), namesTheFix: true},
		{name: "entrypoint testConnection (email, basic)", path: "/entrypoints/testConnection", identify: map[string]any{"entrypointId": basicMailID},
			credentialKeys: []string{"imap_password"}, movedShapes: probeRouteMovedMailbox(), namesTheFix: true},
		{name: "entrypoint testConnection (email, oauth2)", path: "/entrypoints/testConnection", identify: map[string]any{"entrypointId": oauthMailID},
			credentialKeys: []string{"oauth2_client_secret"}, movedShapes: probeRouteMovedMailbox(), namesTheFix: true},
	}
}

func probeRouteBody(route probeRoute, values map[string]any) map[string]any {
	body := maps.Clone(route.identify)
	body["values"] = values
	return body
}

func TestEveryConnectionTestRouteRefusesStoredSecretsForAMovedDestination(t *testing.T) {
	api := newConnectorAPI(t)
	for _, route := range probeRoutes(t, api) {
		t.Run(route.name, func(t *testing.T) {
			t.Run("a caller without the read scope reaches nothing", func(t *testing.T) {
				rec := api.call(http.MethodPut, route.path, probeRouteBody(route, nil), rbac.ScopeAgentBuilderRead)
				expectStatus(t, rec, http.StatusForbidden)
			})
			for scopeName, scopes := range probeRouteScopeSets {
				for shapeName, shape := range route.movedShapes {
					for _, credentials := range probeRouteUnsuppliedCredentials {
						t.Run(scopeName+"/"+shapeName+"/"+credentials.name, func(t *testing.T) {
							values := maps.Clone(shape)
							maps.Copy(values, credentials.values(route))

							rec := api.call(http.MethodPut, route.path, probeRouteBody(route, values), scopes...)

							expectProbeRouteRefusal(t, route, rec)
						})
					}
				}
			}
		})
	}
}

func expectProbeRouteRefusal(t *testing.T, route probeRoute, rec *httptest.ResponseRecorder) {
	t.Helper()
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if strings.Contains(rec.Body.String(), probeRouteStoredSecret) {
		t.Fatalf("the response carries the stored secret: %s", rec.Body.String())
	}
	apiErr := decodeError(t, rec)
	if !route.namesTheFix {
		return
	}
	said := []string{apiErr.Message}
	for _, details := range apiErr.ValidationDetails {
		said = append(said, details...)
	}
	if !strings.Contains(strings.Join(said, " "), probeRouteRefusal) {
		t.Fatalf("body = %s, want the refusal to %s", rec.Body.String(), probeRouteRefusal)
	}
}

func TestConnectorTestConnection_ARefusalIsNotLoggedAsAnError(t *testing.T) {
	logs := captureLogs(t)
	api := newConnectorAPI(t)
	connectorID := ssrfSeedConnector(t, api, "event_mesh", "solace", map[string]any{
		"broker_url": probeRouteStoredBrokerURL, "broker_vpn": "stored-vpn", "broker_username": "stored-user", "broker_password": probeRouteStoredSecret,
	}, "")

	rec := api.call(http.MethodPut, "/connectors/testConnection", map[string]any{
		"connectorId": connectorID, "values": map[string]any{"broker_url": "tcps://evil.example.net:55443"},
	})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("an expected refusal was logged as an error: %s", logs.String())
	}
}
