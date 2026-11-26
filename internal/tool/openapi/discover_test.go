// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadAndDiscoverTools_CamelCaseEndToEnd(t *testing.T) {
	var (
		gotPath      string
		gotRawQuery  string
		gotHeaders   http.Header
		gotBody      string
		gotMethod    string
		gotUserAgent string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotHeaders = r.Header.Clone()
		gotMethod = r.Method
		gotUserAgent = r.Header.Get("User-Agent")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true, "userId": "42"}`))
	}))
	defer srv.Close()

	spec := `{
  "openapi": "3.0.0",
  "info": { "title": "TestAPI", "version": "1.0" },
  "servers": [{ "url": "/v1" }],
  "paths": {
    "/users/{userId}": {
      "get": {
        "operationId": "getUser",
        "parameters": [
          {
            "name": "userId",
            "in": "path",
            "required": true,
            "schema": { "type": "string" }
          },
          {
            "name": "includeDeleted",
            "in": "query",
            "schema": { "type": "boolean" }
          },
          {
            "name": "X-Request-Id",
            "in": "header",
            "schema": { "type": "string" }
          }
        ],
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": { "type": "object" }
              }
            }
          }
        }
      }
    }
  }
}`

	cfg := &ToolsetConfig{
		SpecInline: spec,
		SpecFormat: "json",
		BaseURL:    srv.URL + "/v1",
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0]
	if tool.name != "get_user" {
		t.Errorf("tool name = %q, want get_user", tool.name)
	}

	props := tool.parameters.Properties
	if _, ok := props["user_id"]; !ok {
		t.Errorf("missing user_id in exposed schema: %v", props)
	}
	if _, ok := props["include_deleted"]; !ok {
		t.Errorf("missing include_deleted in exposed schema: %v", props)
	}
	if _, ok := props["x_request_id"]; !ok {
		t.Errorf("missing x_request_id in exposed schema: %v", props)
	}

	ctx := t.Context()
	result, err := mgr.ExecuteTool(ctx, tool, map[string]any{
		"user_id":         "42",
		"include_deleted": true,
		"x_request_id":    "req-abc",
	})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("result status = %q, want success", result.Status)
	}

	if gotMethod != "GET" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/v1/users/42" {
		t.Errorf("path = %q, want /v1/users/42", gotPath)
	}
	if gotRawQuery != "includeDeleted=true" {
		t.Errorf("query = %q, want includeDeleted=true", gotRawQuery)
	}
	if gotHeaders.Get("X-Request-Id") != "req-abc" {
		t.Errorf("X-Request-Id header = %q", gotHeaders.Get("X-Request-Id"))
	}
	if gotUserAgent == "" {
		t.Logf("note: no User-Agent header on outgoing request")
	}
	if gotBody != "" {
		t.Errorf("expected empty body for GET, got %q", gotBody)
	}
}

func TestLoadAndDiscoverTools_DeduplicatesColliding(t *testing.T) {
	spec := `{
  "openapi": "3.0.0",
  "info": { "title": "t", "version": "1" },
  "servers": [{ "url": "http://example.com" }],
  "paths": {
    "/x": {
      "get": {
        "operationId": "dedup",
        "parameters": [
          { "name": "userId",  "in": "query", "schema": {"type": "string"} },
          { "name": "user_id", "in": "query", "schema": {"type": "string"} },
          { "name": "User_Id", "in": "query", "schema": {"type": "string"} }
        ]
      }
    }
  }
}`
	cfg := &ToolsetConfig{SpecInline: spec, SpecFormat: "json"}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for k := range tools[0].parameters.Properties {
		names[k] = true
	}
	if !names["user_id"] {
		t.Errorf("missing user_id; got %v", names)
	}
	if !names["user_id_0"] {
		t.Errorf("missing user_id_0; got %v", names)
	}
	if !names["user_id_1"] {
		t.Errorf("missing user_id_1; got %v", names)
	}
}

func TestLoadAndDiscoverTools_TruncatesLongOperationID(t *testing.T) {
	longID := strings.Repeat("aaaa_", 20)
	spec := `{
  "openapi": "3.0.0",
  "info": { "title": "t", "version": "1" },
  "servers": [{ "url": "http://example.com" }],
  "paths": {
    "/x": { "get": { "operationId": "` + longID + `" } }
  }
}`
	cfg := &ToolsetConfig{SpecInline: spec, SpecFormat: "json"}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools[0].name) > 60 {
		t.Errorf("operation name not truncated: len=%d", len(tools[0].name))
	}
}

func TestLoadAndDiscoverTools_OperationNamesMatchPython(t *testing.T) {
	spec := `{
  "openapi": "3.0.0",
  "info": { "title": "t", "version": "1" },
  "servers": [{ "url": "http://example.com" }],
  "paths": {
    "/users/{id}": { "get": { "operationId": "getUser" } },
    "/orders":     { "post": { "operationId": "createOrder" } }
  }
}`
	cfg := &ToolsetConfig{
		SpecInline: spec,
		SpecFormat: "json",
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(tools))
	for _, tool := range tools {
		got[tool.name] = true
	}
	for _, w := range []string{"get_user", "create_order"} {
		if !got[w] {
			t.Errorf("missing tool %q; got %v", w, got)
		}
	}
}
