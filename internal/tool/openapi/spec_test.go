// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShouldInclude(t *testing.T) {
	tests := []struct {
		name   string
		opID   string
		allow  []string
		deny   []string
		expect bool
	}{
		{"no filters", "op1", nil, nil, true},
		{"in allow list", "op1", []string{"op1", "op2"}, nil, true},
		{"not in allow list", "op3", []string{"op1", "op2"}, nil, false},
		{"in deny list", "op3", nil, []string{"op3"}, false},
		{"not in deny list", "op1", nil, []string{"op3"}, true},
		{"allow takes precedence", "op1", []string{"op1"}, []string{"op1"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldInclude(tt.opID, tt.allow, tt.deny)
			if got != tt.expect {
				t.Errorf("shouldInclude(%q, %v, %v) = %v, want %v", tt.opID, tt.allow, tt.deny, got, tt.expect)
			}
		})
	}
}

func TestLoadSpec_FromRemoteURL(t *testing.T) {
	specJSON := `{"openapi":"3.0.0","info":{"title":"URL Test","version":"1.0"},"paths":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(specJSON))
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{SpecURL: srv.URL + "/openapi.json"}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatalf("loadSpec: %v", err)
	}
	if doc.Info == nil || doc.Info.Title != "URL Test" {
		t.Errorf("title = %v, want URL Test", doc.Info)
	}
}

func TestLoadSpec_AutoDetectFormat_JSON(t *testing.T) {
	jsonSpec := []byte(`{"openapi": "3.0.0", "info": {"title": "Test", "version": "1.0"}, "paths": {}}`)
	if got := detectFormat(jsonSpec); got != "json" {
		t.Errorf("detectFormat(json bytes) = %q, want json", got)
	}

	cfg := &ToolsetConfig{SpecInline: string(jsonSpec)}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatalf("loadSpec: %v", err)
	}
	if doc.Info == nil || doc.Info.Title != "Test" {
		t.Errorf("title = %v, want Test", doc.Info)
	}
}

func TestLoadSpec_JSONWithExtensionsAndNonASCII(t *testing.T) {
	spec := `{"openapi":"3.0.0","x-note":"caf\u00e9 ☕","info":{"title":"Café API","version":"1.0"},` +
		`"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"ok"}}}}}}`

	cfg := &ToolsetConfig{SpecInline: spec}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatalf("loadSpec: %v", err)
	}
	if doc.Info == nil || doc.Info.Title != "Café API" {
		t.Errorf("title = %v, want Café API", doc.Info)
	}
	if doc.Extensions["x-note"] != "café ☕" {
		t.Errorf("x-note = %v, want café ☕", doc.Extensions["x-note"])
	}
	if doc.Paths == nil || doc.Paths.Find("/items") == nil || doc.Paths.Find("/items").Get == nil {
		t.Errorf("paths = %v, want GET /items", doc.Paths)
	}
}

func TestLoadSpec_AutoDetectFormat_YAML(t *testing.T) {
	yamlSpec := []byte("openapi: 3.0.0\ninfo:\n  title: Auto Detect Test\n  version: \"1.0.0\"\npaths: {}\n")
	if got := detectFormat(yamlSpec); got != "yaml" {
		t.Errorf("detectFormat(yaml bytes) = %q, want yaml", got)
	}

	cfg := &ToolsetConfig{SpecInline: string(yamlSpec)}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatalf("loadSpec: %v", err)
	}
	if doc.Info == nil || doc.Info.Title != "Auto Detect Test" {
		t.Errorf("title = %v, want Auto Detect Test", doc.Info)
	}
}

func TestLoadSpec_FileNotFound(t *testing.T) {
	cfg := &ToolsetConfig{SpecFile: filepath.Join(t.TempDir(), "no-such-spec.json")}
	_, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected error for missing spec file")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no such file") && !strings.Contains(msg, "not found") {
		t.Errorf("err should mention file-not-found, got: %v", err)
	}
	if !strings.Contains(msg, "load spec") {
		t.Errorf("err should be wrapped with 'load spec' context, got: %v", err)
	}
}

func TestLoadSpec_InvalidTextFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not_a_spec.txt")
	if err := os.WriteFile(path, []byte("This is just a plain text file, not OpenAPI.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &ToolsetConfig{SpecFile: path}
	_, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected parse error for plain-text file")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "parse") && !strings.Contains(msg, "fail") {
		t.Errorf("err should describe parse failure, got: %v", err)
	}
}

func TestLoadSpec_MalformedJSON(t *testing.T) {
	malformed := `{"openapi": "3.0.0", "info": {"title": "Test", missing closing braces`
	cfg := &ToolsetConfig{SpecInline: malformed, SpecFormat: "json"}
	_, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "parse") && !strings.Contains(msg, "fail") {
		t.Errorf("err should mention parse failure, got: %v", err)
	}
}

func TestLoadSpec_MalformedYAML(t *testing.T) {
	malformed := `
openapi: 3.0.0
info:
  title: Test
  version: 1.0.0
paths:
  /test:
    get:
      - this is not valid yaml structure
      - missing proper indentation
`
	cfg := &ToolsetConfig{SpecInline: malformed, SpecFormat: "yaml"}
	_, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected parse error for malformed YAML")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "fail") && !strings.Contains(msg, "error") && !strings.Contains(msg, "parse") {
		t.Errorf("err should describe failure, got: %v", err)
	}
}

func TestShouldInclude_DenyListExcludesAll(t *testing.T) {
	ops := []string{"allowedOperation1", "allowedOperation2", "deniedOperation1", "deniedOperation2"}
	for _, op := range ops {
		if shouldInclude(op, nil, ops) {
			t.Errorf("shouldInclude(%q, nil, %v) = true, want false (every op denied)", op, ops)
		}
	}
}

func TestValidateOpenAPIVersion_RejectsSwagger2_FromRemoteURL(t *testing.T) {
	swaggerSpec := `{
	  "swagger": "2.0",
	  "info": {"title": "Remote Legacy API", "version": "1.0"},
	  "host": "petstore.swagger.io",
	  "basePath": "/v2",
	  "paths": {
	    "/pet": {
	      "post": {"operationId": "addPet", "responses": {"200": {"description": "OK"}}}
	    }
	  }
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(swaggerSpec))
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{SpecURL: srv.URL + "/swagger.json"}
	mgr := NewManager(slog.Default(), nil)
	_, err := mgr.LoadAndDiscoverTools(cfg)
	if err == nil {
		t.Fatal("expected error rejecting Swagger 2.0 from URL")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Swagger 2.0") {
		t.Errorf("err should mention 'Swagger 2.0': %v", err)
	}
	if !strings.Contains(msg, "OpenAPI 3.x") {
		t.Errorf("err should mention 'OpenAPI 3.x' target: %v", err)
	}
}

func TestGenerateOperationID(t *testing.T) {
	tests := []struct {
		method string
		path   string
		expect string
	}{
		{"GET", "/users", "get_users"},
		{"POST", "/users/{id}/posts", "post_users_by_id_posts"},
		{"DELETE", "/items/{item-id}", "delete_items_by_item_id"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			got := generateOperationID(tt.method, tt.path)
			if got != tt.expect {
				t.Errorf("generateOperationID(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.expect)
			}
		})
	}
}

type fakeSpecStore struct {
	data map[string][]byte
	err  error
}

func (f *fakeSpecStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.data[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func TestReadSpecBytes_ObjStoreScheme(t *testing.T) {
	spec := `{"openapi":"3.0.0","info":{"title":"X","version":"1.0"},"paths":{"/ping":{"get":{"operationId":"ping","responses":{"200":{"description":"ok"}}}}}}`
	store := &fakeSpecStore{data: map[string][]byte{"ns/connectors/id/openapi.json": []byte(spec)}}
	mgr := NewManager(slog.Default(), store)

	_, err := mgr.LoadAndDiscoverTools(&ToolsetConfig{
		SpecURL: "objstore://ns/connectors/id/openapi.json",
		BaseURL: "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
}

func TestReadSpecBytes_ObjStoreNilStore(t *testing.T) {
	mgr := NewManager(slog.Default(), nil)
	_, err := mgr.LoadAndDiscoverTools(&ToolsetConfig{
		SpecURL: "objstore://ns/connectors/id/openapi.json",
		BaseURL: "https://api.example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "connector resource store not configured") {
		t.Fatalf("want 'connector resource store not configured' error, got %v", err)
	}
}

func TestReadSpecBytes_ObjStoreMissingKey(t *testing.T) {
	store := &fakeSpecStore{data: map[string][]byte{}}
	mgr := NewManager(slog.Default(), store)
	_, err := mgr.LoadAndDiscoverTools(&ToolsetConfig{
		SpecURL: "objstore://ns/connectors/id/missing.json",
		BaseURL: "https://api.example.com",
	})
	if err == nil {
		t.Fatal("expected error for missing object-store key, got nil")
	}
	if !strings.Contains(err.Error(), "ns/connectors/id/missing.json") {
		t.Errorf("error should name the missing key, got: %v", err)
	}
}

func TestReadSpecBytes_ObjStoreOversizedSpec(t *testing.T) {
	prefix := `{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{`
	huge := make([]byte, (10<<20)+1024)
	copy(huge, prefix)
	for i := len(prefix); i < len(huge); i++ {
		huge[i] = 'a'
	}
	store := &fakeSpecStore{data: map[string][]byte{"ns/c/big.json": huge}}
	mgr := NewManager(slog.Default(), store)
	_, err := mgr.LoadAndDiscoverTools(&ToolsetConfig{
		SpecURL: "objstore://ns/c/big.json",
		BaseURL: "https://api.example.com",
	})
	if err == nil {
		t.Fatal("expected parse error for truncated oversized spec, got nil")
	}
}
