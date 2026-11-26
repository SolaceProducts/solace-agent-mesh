// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

var conformanceDriven = []string{"/entrypoints", "/entrypointSchemas", "/entrypointDeployments"}

var conformanceNotYetDriven = []string{
	"/agentDeployments", "/agentPlatformDefaults", "/agents", "/apiKeys", "/budgetAssignments", "/budgetSubjects", "/budgets", "/connectorSchemas", "/connectors",
	"/evaluations", "/evidence", "/health", "/identityClaims", "/linkAgents", "/modelLookups", "/modelStatus", "/models", "/productAnalytics", "/profileProvider", "/providers",
	"/rbac", "/remoteAgents", "/scopes", "/serverCapabilities", "/skills", "/supportedModels", "/toolsetBuildTarget", "/toolsetUploads",
	"/toolsets", "/usage", "/userUsage", "/users", "/version", "/webuiSettings", "/workflowDeployments", "/workflows",
	"/eventCatalogEntries",
}

func underPrefix(path string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(prefix string) bool {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	})
}

func TestEveryRouteIsDrivenByConformanceOrListedAsNot(t *testing.T) {
	for _, rt := range specRoutes() {
		driven := underPrefix(rt.Path, conformanceDriven)
		listed := underPrefix(rt.Path, conformanceNotYetDriven)
		if driven == listed {
			t.Errorf("%s %s: add it to conformanceNotYetDriven, or drive it in TestEntrypointEndpointsConformToTheirSpec (driven=%v, listed=%v)",
				rt.Method, rt.Path, driven, listed)
		}
	}
}

type specAPI struct {
	*entrypointAPI
	router routers.Router
}

func newSpecAPI(t *testing.T) *specAPI {
	t.Helper()
	rendered, err := openapigen.Marshal(buildSpecs(t).Full)
	if err != nil {
		t.Fatalf("render spec: %v", err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(rendered)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	return &specAPI{entrypointAPI: newEntrypointAPI(t, nil), router: router}
}

func (a *specAPI) check(method, path string, body any) (rec *httptest.ResponseRecorder, requestErr, responseErr error) {
	a.t.Helper()
	req := httptest.NewRequest(method, platpkg.APIV2Prefix+path, strings.NewReader(bodyText(a.t, body)))
	req.Header.Set("Content-Type", "application/json")
	route, pathParams, err := a.router.FindRoute(req)
	if err != nil {
		a.t.Fatalf("%s %s is not in the spec: %v", method, path, err)
	}
	input := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: pathParams, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	requestErr = openapi3filter.ValidateRequest(context.Background(), input)

	rec = a.call(method, path, body)
	responseErr = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
	})
	return rec, requestErr, responseErr
}

func bodyText(t *testing.T, body any) string {
	t.Helper()
	switch b := body.(type) {
	case nil:
		return ""
	case string:
		return b
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEntrypointEndpointsConformToTheirSpec(t *testing.T) {
	api := newSpecAPI(t)
	created := api.createSlack("spec-slack")
	teams := createTeams(t, api.entrypointAPI)

	for _, tc := range []struct {
		name, method, path string
		body               any
		want               int
	}{
		{"create", http.MethodPost, "/entrypoints", map[string]any{
			"name": "spec-create-slack", "description": "Slack entrypoint the spec test creates.",
			"entrypointType": "slack", "values": map[string]any{"bot_token": slackBotToken, "app_token": "xapp-app-token"},
		}, http.StatusCreated},
		{"list", http.MethodGet, "/entrypoints?pageSize=2&pageNumber=1", nil, http.StatusOK},
		{"get", http.MethodGet, "/entrypoints/" + created.ID, nil, http.StatusOK},
		{"update", http.MethodPatch, "/entrypoints/" + created.ID, map[string]any{"name": "spec-renamed"}, http.StatusOK},
		{"update clearing publicUrl", http.MethodPatch, "/entrypoints/" + created.ID, map[string]any{"publicUrl": nil}, http.StatusOK},
		{"schemas", http.MethodGet, "/entrypointSchemas?pageSize=5", nil, http.StatusOK},
		{"networking without ingress", http.MethodGet, "/entrypoints/" + created.ID + "/networkingDetails", nil, http.StatusOK},
		{"networking with shared ingress", http.MethodGet, "/entrypoints/" + teams.ID + "/networkingDetails", nil, http.StatusOK},
		{"secrets diff", http.MethodPut, "/entrypoints/" + created.ID + "/secretsDiff",
			map[string]any{"values": map[string]any{"bot_token": slackBotToken}}, http.StatusOK},
		{"test connection", http.MethodPut, "/entrypoints/testConnection",
			map[string]any{"entrypointType": "slack", "values": map[string]any{}}, http.StatusOK},
		{"deploy", http.MethodPost, "/entrypointDeployments",
			map[string]any{"entrypointId": created.ID, "action": "deploy"}, http.StatusCreated},
		{"list deployments", http.MethodGet, "/entrypointDeployments?entrypointId=" + created.ID, nil, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, reqErr, respErr := api.check(tc.method, tc.path, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body)
			}
			if reqErr != nil {
				t.Errorf("the spec rejects a request the API accepts: %v", reqErr)
			}
			if respErr != nil {
				t.Errorf("the response does not match the spec: %v", respErr)
			}
		})
	}

	t.Run("get and delete deployment", func(t *testing.T) {
		rec := api.deploy(created.ID)
		deployment := decodeData[EntrypointDeployment](t, rec)
		rec, reqErr, respErr := api.check(http.MethodGet, "/entrypointDeployments/"+deployment.ID, nil)
		if rec.Code != http.StatusOK || reqErr != nil || respErr != nil {
			t.Errorf("get deployment: status=%d requestErr=%v responseErr=%v", rec.Code, reqErr, respErr)
		}
		rec, reqErr, respErr = api.check(http.MethodDelete, "/entrypoints/"+teams.ID, nil)
		if rec.Code != http.StatusNoContent || reqErr != nil || respErr != nil {
			t.Errorf("delete: status=%d requestErr=%v responseErr=%v", rec.Code, reqErr, respErr)
		}
	})
}

func TestEntrypointErrorResponsesConformToTheSpec(t *testing.T) {
	api := newSpecAPI(t)

	for _, tc := range []struct {
		name, method, path string
		body               any
		want               int
		specRejectsRequest bool
	}{
		{"unknown id", http.MethodGet, "/entrypoints/" + missingID, nil, http.StatusNotFound, false},
		{"malformed id", http.MethodGet, "/entrypoints/not-a-uuid", nil, http.StatusBadRequest, false},
		{"malformed body", http.MethodPost, "/entrypoints", `{"name":`, http.StatusBadRequest, true},
		{"missing required field", http.MethodPost, "/entrypoints", map[string]any{"entrypointType": "slack"}, http.StatusUnprocessableEntity, true},
		{"page size over the cap", http.MethodGet, "/entrypoints?pageSize=101", nil, http.StatusUnprocessableEntity, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, reqErr, respErr := api.check(tc.method, tc.path, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body)
			}
			if tc.specRejectsRequest && reqErr == nil {
				t.Error("the API rejects this request but the spec would accept it")
			}
			if respErr != nil {
				t.Errorf("the error response does not match the spec: %v", respErr)
			}
		})
	}
}
