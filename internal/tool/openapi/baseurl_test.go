// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"log/slog"
	"strings"
	"testing"
)

func TestApplyBaseURLOverride_DiscardsSpecPath(t *testing.T) {
	spec := []byte(`
openapi: 3.0.0
info: { title: t, version: "1" }
servers:
  - url: /api/v3
paths: {}
`)
	out, err := applyBaseURLOverride(spec, "http://localhost:9191/api/v3", "yaml")
	if err != nil {
		t.Fatalf("applyBaseURLOverride: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "http://localhost:9191/api/v3") {
		t.Errorf("missing base URL in output:\n%s", s)
	}
	if strings.Contains(s, "api/v3/api/v3") {
		t.Errorf("spec path was appended on top of base_url (doubled), not discarded:\n%s", s)
	}
}

func TestApplyBaseURLOverride_DiscardsAbsoluteServerURL(t *testing.T) {
	spec := []byte(`{
"openapi":"3.0.0",
"info":{"title":"t","version":"1"},
"servers":[{"url":"https://api.example.com/v1"}],
"paths":{}
}`)
	out, err := applyBaseURLOverride(spec, "http://localhost:9191", "json")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "http://localhost:9191") {
		t.Errorf("missing base URL, got: %s", s)
	}
	if strings.Contains(s, "api.example.com") {
		t.Errorf("original host leaked into output: %s", s)
	}
	if strings.Contains(s, "/v1") {
		t.Errorf("original server path leaked into output: %s", s)
	}
}

func TestApplyBaseURLOverride_ServerVariableInHost(t *testing.T) {
	spec := []byte(`{
"openapi":"3.0.0",
"info":{"title":"t","version":"1"},
"servers":[{"url":"https://{account-domain}.aha.io/api/v1"}],
"paths":{}
}`)
	out, err := applyBaseURLOverride(spec, "https://example.aha.io", "json")
	if err != nil {
		t.Fatalf("applyBaseURLOverride: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "https://example.aha.io") {
		t.Errorf("missing base URL, got: %s", s)
	}
	if strings.Contains(s, "{account-domain}") {
		t.Errorf("unresolved server variable leaked into output: %s", s)
	}
	if strings.Contains(s, "aha.io/https://") || strings.Contains(s, "/api/v1") {
		t.Errorf("spec server URL/path leaked into output: %s", s)
	}
}

func TestApplyBaseURLOverride_MultipleServers(t *testing.T) {
	spec := []byte(`{
"openapi":"3.0.0",
"info":{"title":"t","version":"1"},
"servers":[{"url":"/api/v1"},{"url":"/api/v2"}],
"paths":{}
}`)
	out, err := applyBaseURLOverride(spec, "http://localhost:7000/api/v1", "json")
	if err != nil {
		t.Fatalf("applyBaseURLOverride: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "http://localhost:7000/api/v1") {
		t.Errorf("server URLs not rewritten to base:\n%s", s)
	}
	if strings.Contains(s, "api/v1/api/v1") || strings.Contains(s, "api/v2") {
		t.Errorf("spec server paths not discarded (doubled, or /api/v2 leaked):\n%s", s)
	}
}

func TestApplyBaseURLOverride_NoServers(t *testing.T) {
	spec := []byte(`{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{}}`)
	out, err := applyBaseURLOverride(spec, "http://localhost:9191", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "http://localhost:9191") {
		t.Errorf("missing base URL in output: %s", out)
	}
}

func TestLoadSpec_InlineSource(t *testing.T) {
	inline := `{
  "openapi": "3.0.0",
  "info": { "title": "Inline", "version": "1.0" },
  "servers": [{ "url": "http://example.com" }],
  "paths": {
    "/ping": {
      "get": { "operationId": "ping" }
    }
  }
}`
	cfg := &ToolsetConfig{SpecInline: inline, SpecFormat: "json"}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatalf("loadSpec: %v", err)
	}
	if doc.Info.Title != "Inline" {
		t.Errorf("title = %q", doc.Info.Title)
	}
	ops := extractOperations(doc)
	if len(ops) != 1 {
		t.Fatalf("ops = %d", len(ops))
	}
	if ops[0].OperationID != "ping" {
		t.Errorf("opID = %q", ops[0].OperationID)
	}
}

func TestLoadSpec_InlineWithBaseURLOverride(t *testing.T) {
	inline := `{
  "openapi": "3.0.0",
  "info": { "title": "t", "version": "1" },
  "servers": [{ "url": "/v2" }],
  "paths": {}
}`
	cfg := &ToolsetConfig{
		SpecInline: inline,
		SpecFormat: "json",
		BaseURL:    "http://custom:9191",
	}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := getBaseURL(doc)
	if base != "http://custom:9191" {
		t.Errorf("base URL = %q, want http://custom:9191", base)
	}
}

func TestLoadSpec_BaseURLOwnPathPreserved(t *testing.T) {
	inline := `{
  "openapi": "3.0.0",
  "info": { "title": "t", "version": "1" },
  "servers": [{ "url": "https://{account-domain}.aha.io/api/v1" }],
  "paths": {}
}`
	cfg := &ToolsetConfig{
		SpecInline: inline,
		SpecFormat: "json",
		BaseURL:    "https://example.aha.io/api/v1",
	}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := getBaseURL(doc)
	if base != "https://example.aha.io/api/v1" {
		t.Errorf("base URL = %q, want https://example.aha.io/api/v1", base)
	}
}
