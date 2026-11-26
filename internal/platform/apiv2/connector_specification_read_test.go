// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	connectorSpecJSON        = `{"openapi":"3.1.0","info":{"title":"Pets","version":"1"},"paths":{}}`
	connectorSpecYAML        = "openapi: 3.1\ninfo:\n  title: Pets\n  version: '1'\npaths: {}\n"
	connectorSpecSecretQuery = "token=abc123secret"
)

func connectorSpecRead(api *apiClient, id string) *httptest.ResponseRecorder {
	return api.call(http.MethodGet, "/connectors/"+id+"/specification", nil)
}

func TestConnectorSpecification_ServesWhatTheBrowserCannotRead(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantFormat  string
	}{
		{"json served as html", connectorSpecJSON, "text/html", "json"},
		{"yaml with an unquoted version", connectorSpecYAML, "text/plain", "yaml"},
		{"yaml flow style", "{openapi: 3.0.0, info: {title: Pets}, paths: {}}", "application/octet-stream", "yaml"},
		{"yaml with a duplicated key, left for the browser to report", "openapi: '3.0.0'\ninfo: {title: a}\ninfo: {title: b}\n", "text/plain", "yaml"},
		{"json with a byte order mark", "\ufeff" + connectorSpecJSON, "application/json", "json"},
		{"json after leading whitespace", "\n  " + connectorSpecJSON, "application/json", "json"},
		{"json with the version after other fields", `{"info":{"title":"Pets"},"paths":{},"openapi":"3.0.0"}`, "application/json", "json"},
		{"json with an unquoted version", `{"openapi":3.1,"info":{"title":"Pets"},"paths":{}}`, "application/json", "json"},
		{"yaml with a byte order mark", "\ufeff" + connectorSpecYAML, "text/plain", "yaml"},
		{"yaml after a document marker", "---\n" + connectorSpecYAML, "text/plain", "yaml"},
		{"yaml after a comment", "# yaml-language-server: $schema=none\n" + connectorSpecYAML, "text/plain", "yaml"},
		{"yaml with the version after other fields", "info:\n  title: Pets\nopenapi: '3.0.3'\npaths: {}\n", "text/plain", "yaml"},
		{"swagger 2.0 yaml", "swagger: '2.0'\ninfo:\n  title: Old\npaths: {}\n", "text/plain", "yaml"},
		{"swagger 2.0, so the form can say it is unsupported", `{"swagger":"2.0","info":{"title":"Old","version":"1"},"paths":{}}`, "application/json", "json"},
	}
	api := newRelaxedSSRFAPI(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotHeaders http.Header
			target := newSSRFTarget(t, func(w http.ResponseWriter, r *http.Request) {
				gotHeaders = r.Header.Clone()
				w.Header().Set("Content-Type", tt.contentType)
				_, _ = io.WriteString(w, tt.body)
			})
			id := ssrfSpecConnector(t, api, target.srv.URL+"/spec")

			req := newJSONRequest(t, http.MethodGet, "/connectors/"+id+"/specification", nil)
			req.Header.Set("Authorization", "Bearer inbound-session-token")
			req.Header.Set("Cookie", "sam_session=inbound-cookie")
			rec := httptest.NewRecorder()
			api.handler.ServeHTTP(rec, req)

			expectStatus(t, rec, http.StatusOK)
			data := decodeData[map[string]any](t, rec)
			if data["content"] != tt.body {
				t.Errorf("content = %q, want the body as served", data["content"])
			}
			if data["format"] != tt.wantFormat {
				t.Errorf("format = %v, want %s", data["format"], tt.wantFormat)
			}
			if len(data) != 2 {
				t.Errorf("data has fields beyond content and format: %v", data)
			}
			if accept := gotHeaders.Get("Accept"); !strings.Contains(accept, "application/json") || !strings.Contains(accept, "yaml") {
				t.Errorf("Accept = %q, want it to ask for JSON and YAML", accept)
			}
			for _, header := range []string{"Authorization", "Cookie", "X-Sam-Client-Surface"} {
				if v := gotHeaders.Get(header); v != "" {
					t.Errorf("the platform forwarded %s to the external host: %q", header, v)
				}
			}
		})
	}
}

func TestConnectorSpecification_ResponseCarriesNoHostControlledContentType(t *testing.T) {
	api := newRelaxedSSRFAPI(t)
	target := newSSRFTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Disposition", "inline")
		_, _ = io.WriteString(w, connectorSpecJSON)
	})
	rec := connectorSpecRead(api, ssrfSpecConnector(t, api, target.srv.URL+"/spec"))

	expectStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q, want none", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestConnectorSpecification_UpstreamFailuresAreLoggedAndLeakNothing(t *testing.T) {
	logs := captureLogs(t)
	api := newRelaxedSSRFAPI(t)

	oversize := strings.Repeat("a", 10<<20+1)
	const (
		notOpenAPI = "did not return an OpenAPI document"
		notUTF8    = "did not return UTF-8 text"
	)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"not found", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }, "(HTTP 404)"},
		{"server error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "(HTTP 500)"},
		{"partial content", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, connectorSpecJSON)
		}, "(HTTP 206)"},
		{"html sign-in page", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<!doctype html><html><body>Sign in "+connectorSpecSecretQuery+"</body></html>")
		}, notOpenAPI},
		{"one-line html page with a colon", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<html><body>Error: not found</body></html>")
		}, notOpenAPI},
		{"json object that is not a spec", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"error":"unauthorized","secret":"internal-config"}`)
		}, notOpenAPI},
		{"yaml mapping that is not a spec", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "name: internal\nport: 15000\n")
		}, notOpenAPI},
		{"empty body", func(http.ResponseWriter, *http.Request) {}, notOpenAPI},
		{"json array", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "[1,2]") }, notOpenAPI},
		{"json null", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "null") }, notOpenAPI},
		{"yaml-shaped invalid utf-8", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("openapi: 3.0.0\ninfo: caf\xe9\n"))
		}, notUTF8},
		{"json-shaped invalid utf-8", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{\"openapi\":\"3.0.0\",\"x\":\"\xff\"}"))
		}, notUTF8},
		{"json whose openapi field is not a version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"openapi":{"enabled":true},"db":{"password":"s3cr3t"}}`)
		}, notOpenAPI},
		{"json with an openapi 2.0 version", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"openapi":"2.0","info":{}}`) }, notOpenAPI},
		{"flow mapping whose openapi field is not a version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "{openapi: see docs, secret: x}")
		}, notOpenAPI},
		{"nul byte", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("openapi: 3.0.0\x00\n")) }, notUTF8},
		{"openapi mentioned mid-line", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<p>Docs: see openapi: 3.0</p>")
		}, notOpenAPI},
		{"openapi only as a nested key", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "config:\n  openapi: 3.0.0\n") }, notOpenAPI},
		{"column-0 openapi line without a version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "swagger:\n  enabled: true\ndb:\n  password: s3cr3t\n")
		}, notOpenAPI},
		{"html with a column-0 openapi line", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<html>\nopenapi: see /docs\n</html>")
		}, notOpenAPI},
		{"json whose swagger key is not a version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"swagger":{"enabled":true},"db":{"password":"s3cr3t"}}`)
		}, notOpenAPI},
		{"newline-delimited json", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "{\"ts\":1,\"cfg\":{\"openapi\":\"3.0.0\"}}\n{\"secret\":\"x\"}")
		}, notOpenAPI},
		{"broken json that only mentions the key", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "{bad json myopenapi: 1, secret: x")
		}, notOpenAPI},
		{"past the size cap", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, oversize) }, "larger than the 10 MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := newSSRFTarget(t, tt.handler)
			rec := connectorSpecRead(api, ssrfSpecConnector(t, api, target.srv.URL+"/spec?"+connectorSpecSecretQuery))

			expectStatus(t, rec, http.StatusBadGateway)
			if msg := decodeError(t, rec).Message; msg != ssrfSpecReadFailed {
				t.Errorf("message = %q, want the handler's fixed sentence", msg)
			}
			if cause := ssrfLoggedCause(t, logs, rec); !strings.Contains(cause, tt.want) {
				t.Errorf("logged cause = %q, want it to contain %q", cause, tt.want)
			}
			for _, leak := range []string{connectorSpecSecretQuery, target.port, "127.0.0.1"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("response leaks %q: %s", leak, rec.Body.String())
				}
			}
		})
	}
}

func TestConnectorSpecification_OnlyAnExternalURLIsFetched(t *testing.T) {
	api := newRelaxedSSRFAPI(t)
	target := newSSRFTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, connectorSpecJSON) })
	externalURL := target.srv.URL + "/spec"

	cases := map[string]string{
		"no specification_url":        ssrfSeedConnector(t, api, "api", "openapi", map[string]any{"base_url": "https://api.example.com"}, ""),
		"platform display path":       ssrfSpecConnector(t, api, "/api/v2/platform/connectors/abc/resource"),
		"placeholder":                 ssrfSpecConnector(t, api, "${SPEC_URL}"),
		"non-http scheme":             ssrfSpecConnector(t, api, "ftp://"+target.srv.Listener.Addr().String()+"/spec"),
		"stored resource wins":        ssrfSeedConnector(t, api, "api", "openapi", map[string]any{"specification_url": externalURL}, "test-ns/connectors/x/spec.json"),
		"other connector type":        ssrfSeedConnector(t, api, "mcp", "streamable_http", map[string]any{"specification_url": externalURL}, ""),
		"other api connector subtype": ssrfSeedConnector(t, api, "api", "graphql", map[string]any{"specification_url": externalURL}, ""),
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, connectorSpecRead(api, id), http.StatusNotFound)
		})
	}
	if hits := target.hits.Load(); hits != 0 {
		t.Fatalf("external host received %d request(s) for a connector with no external URL", hits)
	}

	t.Run("unknown connector", func(t *testing.T) {
		expectStatus(t, connectorSpecRead(api, "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77"), http.StatusNotFound)
	})
	t.Run("malformed id is refused before any lookup", func(t *testing.T) {
		expectStatus(t, connectorSpecRead(api, "not-a-uuid"), http.StatusBadRequest)
	})
}

func TestConnectorSpecification_RequiresTheConnectorReadScope(t *testing.T) {
	api := newRelaxedSSRFAPI(t)
	target := newSSRFTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, connectorSpecJSON) })
	id := ssrfSpecConnector(t, api, target.srv.URL+"/spec")
	path := "/connectors/" + id + "/specification"

	t.Run("no scopes", func(t *testing.T) {
		req := newJSONRequest(t, http.MethodGet, path, nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "reader", Authenticated: true})))
		expectStatus(t, serveBare(t, api.core, req), http.StatusForbidden)
	})
	for name, scopes := range map[string][]string{
		"connector update only":  {rbac.ScopeConnectorUpdate},
		"another category read":  {rbac.ScopeAgentBuilderRead},
		"connector collection":   {rbac.ScopeConnectorCreate},
		"read of another family": {"entrypoint:*:read"},
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, api.call(http.MethodGet, path, nil, scopes...), http.StatusForbidden)
		})
	}
	if hits := target.hits.Load(); hits != 0 {
		t.Fatalf("an unauthorized caller made the platform fetch the URL %d time(s)", hits)
	}

	t.Run("unauthenticated", func(t *testing.T) {
		expectStatus(t, api.anonymous().call(http.MethodGet, path, nil), http.StatusUnauthorized)
	})
	t.Run("connector read is enough", func(t *testing.T) {
		expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeConnectorRead), http.StatusOK)
	})
}

func TestConnectorSpecification_UnreadableStoredValuesAreAServerFault(t *testing.T) {
	api := newRelaxedSSRFAPI(t)
	id := ssrfSeedConnector(t, api, "api", "openapi", "{not json", "")

	expectStatus(t, connectorSpecRead(api, id), http.StatusInternalServerError)
}

func TestConnectorSpecification_UpdateResponseCarriesTheReachabilityWarning(t *testing.T) {
	api := newConnectorAPI(t)
	created := createNamedConnector(t, api, "openapi-patch-wire")

	rec := api.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{
		"values": map[string]any{"base_url": connectorBaseURL, "specification_url": "http://127.0.0.1:1/openapi.yaml"},
	})

	expectStatus(t, rec, http.StatusOK)
	if warnings := decodeData[Connector](t, rec).Warnings; len(warnings) != 1 || !strings.Contains(warnings[0], "not reachable from the platform") {
		t.Errorf("warnings = %v, want one reachability warning", warnings)
	}
}

func TestConnectorSpecification_UploadedSpecIsReferencedByItsDisplayPathAndBacksAnAgent(t *testing.T) {
	api := newConnectorAPI(t)
	created := createNamedConnector(t, api, "petstore-api")

	upload := uploadSpec(t, api, created.ID, "spec.json",
		`{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/ping":{"get":{"operationId":"ping","responses":{"200":{"description":"ok"}}}}}}`)
	expectStatus(t, upload, http.StatusOK)
	if !decodeData[Connector](t, upload).HasResource {
		t.Error("hasResource = false after the upload")
	}

	rec := api.call(http.MethodGet, "/connectors/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	want := "/api/v2/platform/connectors/" + created.ID + "/resource"
	if got := decodeData[Connector](t, rec).Values["specification_url"]; got != want {
		t.Errorf("specification_url = %v, want the display path %q", got, want)
	}

	expectCreated[Agent](t, api.call(http.MethodPost, "/agents", map[string]any{
		"name": "petstore-agent", "description": "Agent that uses the OpenAPI connector under test",
		"systemPrompt": strings.Repeat("You are a helpful assistant that calls the petstore API on the user's behalf. ", 3),
		"connectorIds": []string{created.ID}, "inputModes": []string{"text"}, "outputModes": []string{"text"},
	}), "agents")
}
