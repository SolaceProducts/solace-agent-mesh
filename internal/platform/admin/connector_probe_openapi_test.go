// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func installPlainProbeClient(t *testing.T) {
	t.Helper()
	prev := openAPIProbeClient
	openAPIProbeClient = func() *http.Client { return &http.Client{Timeout: 2 * time.Second} }
	t.Cleanup(func() { openAPIProbeClient = prev })
}

func TestProbeOpenAPISpecURL_SkipsWhenNothingExternalToReach(t *testing.T) {
	cases := []struct {
		name        string
		values      map[string]any
		hasResource bool
	}{
		{"uploaded_resource", map[string]any{"specification_url": "https://dead.example.invalid/spec.yaml"}, true},
		{"display_path", map[string]any{"specification_url": "/api/v1/platform/connectors/abc/resource"}, false},
		{"no_url", map[string]any{"base_url": "https://api.example.com"}, false},
		{"placeholder", map[string]any{"specification_url": "<<__SAM_REQUIRED__>>"}, false},
		{"non_http_scheme", map[string]any{"specification_url": "objstore://bucket/spec.yaml"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			warnings, err := probeOpenAPISpecURL(t.Context(), discardLogger(), c.values, c.hasResource)
			if err != nil {
				t.Fatalf("probe must never block: %v", err)
			}
			if warnings != nil {
				t.Errorf("warnings = %v, want none (no external URL to probe)", warnings)
			}
		})
	}
}

func TestProbeOpenAPISpecURL_UnreachableURLWarnsWithoutBlocking(t *testing.T) {
	installPlainProbeClient(t)

	warnings, err := probeOpenAPISpecURL(t.Context(), discardLogger(),
		map[string]any{"specification_url": "http://127.0.0.1:1/spec.json?sig=secret-token"}, false)
	if err != nil {
		t.Fatalf("probe must never block: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one reachability warning", warnings)
	}
	if !strings.Contains(warnings[0], "specification_url is not reachable from the platform") {
		t.Errorf("warning = %q, want the reachability copy", warnings[0])
	}
	if !strings.Contains(warnings[0], "specification_file") {
		t.Errorf("warning = %q, must point the operator at the managed-spec alternative", warnings[0])
	}
	if strings.Contains(warnings[0], "sig=secret-token") {
		t.Errorf("warning = %q, must not carry the spec URL's query string", warnings[0])
	}
}

func TestProbeOpenAPISpecURL_HTTPErrorStatusWarns(t *testing.T) {
	installPlainProbeClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	warnings, err := probeOpenAPISpecURL(t.Context(), discardLogger(),
		map[string]any{"specification_url": srv.URL + "/spec.json"}, false)
	if err != nil {
		t.Fatalf("probe must never block: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "HTTP 404") {
		t.Errorf("warnings = %v, want one warning naming HTTP 404", warnings)
	}
}

func TestProbeOpenAPISpecURL_ReachableURLNoWarning(t *testing.T) {
	installPlainProbeClient(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"openapi":"3.0.0"}`))
	}))
	defer srv.Close()

	warnings, err := probeOpenAPISpecURL(t.Context(), discardLogger(),
		map[string]any{"specification_url": srv.URL + "/spec.json"}, false)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want none for a reachable URL", warnings)
	}
}

func TestConnectorService_CreateOpenAPI_DeadSpecURLWarnsButSaves(t *testing.T) {
	installPlainProbeClient(t)
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())

	resp, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "figma-api",
		Description: "OpenAPI connector with an unreachable spec URL",
		Type:        "api",
		Subtype:     "openapi",
		Values: map[string]any{
			"base_url":          "https://api.figma.com",
			"specification_url": "http://127.0.0.1:1/openapi.yaml",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("Create must succeed despite the dead spec URL, got %v", err)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "not reachable from the platform") {
		t.Errorf("Warnings = %v, want one reachability warning", resp.Warnings)
	}

	got, err := svc.GetByID(t.Context(), resp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Warnings != nil {
		t.Errorf("GetByID Warnings = %v, want none (warnings are write-response-only)", got.Warnings)
	}
}

func TestConnectorService_UpdateOpenAPI_ProbeWarningAndResourceSkip(t *testing.T) {
	installPlainProbeClient(t)
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	svc := NewConnectorService(repo, nil, "test-ns", discardLogger())

	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "openapi-update",
		Description: "OpenAPI connector seeded for the update probe path",
		Type:        "api",
		Subtype:     "openapi",
		Values:      map[string]any{"base_url": "https://api.example.com"},
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	deadValues := map[string]any{
		"base_url":          "https://api.example.com",
		"specification_url": "http://127.0.0.1:1/openapi.yaml",
	}

	warnings, err := svc.PreflightUpdate(t.Context(), created.ID, UpdateConnectorRequest{Values: deadValues})
	if err != nil {
		t.Fatalf("PreflightUpdate must not block on reachability: %v", err)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want one reachability warning", warnings)
	}

	if upErr := repo.UpdateResourceKey(t.Context(), created.ID, "test-ns/connectors/"+created.ID+"/spec.yaml", "abc123"); upErr != nil {
		t.Fatalf("UpdateResourceKey: %v", upErr)
	}
	warnings, err = svc.PreflightUpdate(t.Context(), created.ID, UpdateConnectorRequest{Values: deadValues})
	if err != nil {
		t.Fatalf("PreflightUpdate: %v", err)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want none for a resource-backed connector", warnings)
	}
}

func TestConnectorHandler_CollectAgentRegistrations(t *testing.T) {
	db := testOpenDB(t)
	connRepo := store.NewConnectorRepo(db)
	agentRepo := store.NewAgentRepo(db)
	registry := platpkg.NewAgentRegistry()
	svc := NewConnectorService(connRepo, nil, "test-ns", discardLogger())
	svc.SetRedeployDeps(db, agentRepo, nil, nil)
	svc.SetAgentRegistry(registry)

	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "figma-api-agg",
		Description: "OpenAPI connector for the aggregation test",
		Type:        "api",
		Subtype:     "openapi",
		Values: map[string]any{
			"base_url": "https://api.figma.com",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}

	makeAgent := func(name string) *store.Agent {
		desc := "agent for connector aggregation test"
		prompt := "You are an assistant."
		a := &store.Agent{Name: name, Type: "standard", Description: &desc, SystemPrompt: &prompt, CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(t.Context(), a, nil, []string{"text"}, []string{"text"}, []string{created.ID}, nil); err != nil {
			t.Fatalf("create agent %s: %v", name, err)
		}
		return a
	}
	reporting := makeAgent("Reporting Agent")
	silent := makeAgent("Silent Agent")

	registry.AddOrUpdate(a2a.AgentCard{
		Name: AgentInstanceName(reporting),
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
			URI: platpkg.ExtensionURIToolRegistrations,
			Params: map[string]any{"entries": []any{
				map[string]any{
					"tool_type":    "openapi",
					"connector_id": created.ID,
					"status":       "failed",
					"error":        "load spec: HTTP 500",
					"tool_count":   0,
				},
				map[string]any{
					"tool_type":    "mcp",
					"connector_id": "some-other-connector",
					"status":       "ready",
					"tool_count":   3,
				},
			}},
		}}},
	})
	registry.AddOrUpdate(a2a.AgentCard{Name: AgentInstanceName(silent), Description: "python agent, no extension"})

	regs, incomplete := svc.CollectAgentRegistrations(t.Context(), created.ID)
	if incomplete {
		t.Error("incomplete = true, want false when every agent page listed cleanly")
	}
	if len(regs) != 1 {
		t.Fatalf("registrations = %+v, want exactly the reporting agent's entry for this connector", regs)
	}
	got := regs[0]
	if got.Agent.ID != reporting.ID || got.Agent.Name != "Reporting Agent" {
		t.Errorf("agent ref = %+v, want the reporting agent", got.Agent)
	}
	if got.Status != "failed" || got.Error == "" {
		t.Errorf("status = %q error = %q, want the failed entry with its error summary", got.Status, got.Error)
	}
}

func TestProbeOpenAPISpecURL_LogRedactsCredentialsAndQuery(t *testing.T) {
	installPlainProbeClient(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	_, err := probeOpenAPISpecURL(t.Context(), logger,
		map[string]any{"specification_url": "https://alice:s3cret@127.0.0.1:1/spec.json?sig=x"}, false)
	if err != nil {
		t.Fatalf("probe must never block: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "https://xxxxx@127.0.0.1:1/spec.json") {
		t.Errorf("log output = %q, want the redacted specURL field", logged)
	}
	if strings.Contains(logged, "s3cret") {
		t.Errorf("log output = %q, must not leak the basic-auth password", logged)
	}
	if strings.Contains(logged, "sig=x") {
		t.Errorf("log output = %q, must not carry the query string", logged)
	}
}
