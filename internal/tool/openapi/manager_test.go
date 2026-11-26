// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func newStringParam(name, in string, required bool) paramSpec {
	schema := &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}
	style := "form"
	explode := true
	switch paramLocation(in) {
	case locPath, locHeader:
		style = "simple"
		explode = false
	}
	p := paramSpec{
		OriginalName: name,
		In:           paramLocation(in),
		Style:        style,
		Explode:      explode,
		Required:     required,
		Schema:       schema,
		SchemaJSON:   renderSchema(schema, ""),
	}
	assignPyNames([]paramSpec{p})
	p.PyName = pyNameFor(name)
	return p
}

func TestExecuteTool_QueryParamEncoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"search": q.Get("search"),
			"tag":    q.Get("tag"),
		})
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		name:         "test_search",
		method:       "GET",
		pathTemplate: "/search",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{
				newStringParam("search", "query", false),
				newStringParam("tag", "query", false),
			},
		},
	}

	result, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"search": "foo&bar=baz",
		"tag":    "a=b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Data["search"] != "foo&bar=baz" {
		t.Errorf("search param not properly decoded: got %q", result.Data["search"])
	}
	if result.Data["tag"] != "a=b" {
		t.Errorf("tag param not properly decoded: got %q", result.Data["tag"])
	}
}

func TestExecuteTool_PathParamEncodingSafe(t *testing.T) {
	var receivedURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURI = r.RequestURI
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		name:         "get_user",
		method:       "GET",
		pathTemplate: "/users/{id}",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{newStringParam("id", "path", true)},
		},
	}

	tests := []struct {
		name       string
		value      string
		wantInPath string
	}{
		{"path traversal", "../../admin", "..%2F..%2Fadmin"},
		{"query marker", "123?admin=true", "123%3Fadmin=true"},
		{"fragment marker", "123#section", "123%23section"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
				"id": tt.value,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			expected := "/users/" + tt.wantInPath
			if receivedURI != expected {
				t.Errorf("expected URI %q, got %q", expected, receivedURI)
			}
		})
	}
}

func TestExecuteTool_PathParamEscaping(t *testing.T) {
	var receivedURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		name:         "get_item",
		method:       "GET",
		pathTemplate: "/items/{name}",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{newStringParam("name", "path", true)},
		},
	}

	_, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"name": "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedURI != "/items/hello%20world" {
		t.Errorf("expected escaped URI, got %q", receivedURI)
	}
}

func TestExecuteTool_BodyParams(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created": true}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		name:         "create_user",
		method:       "POST",
		pathTemplate: "/users",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			BodyMIME:  "application/json",
			BodyShape: "object",
			Params: []paramSpec{
				{
					OriginalName: "name",
					PyName:       "name",
					In:           locBody,
					Schema:       &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}},
				},
				{
					OriginalName: "email",
					PyName:       "email",
					In:           locBody,
					Schema:       &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}},
				},
			},
		},
	}

	_, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"name":  "test",
		"email": "test@example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedBody["name"] != "test" || receivedBody["email"] != "test@example.com" {
		t.Errorf("unexpected body: %v", receivedBody)
	}
}

func TestExecuteTool_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		name:         "get_thing",
		method:       "GET",
		pathTemplate: "/things/1",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
	}

	result, err := mgr.ExecuteTool(t.Context(), tool, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status for 404, got %q", result.Status)
	}
}

func TestExecuteTool_ResponseSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"big": %q}`, strings.Repeat("x", 1024))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		name:            "big_response",
		method:          "GET",
		pathTemplate:    "/big",
		baseURL:         srv.URL,
		manager:         mgr,
		logger:          slog.Default(),
		maxResponseSize: 100,
	}

	result, err := mgr.ExecuteTool(t.Context(), tool, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status for oversized response, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "exceeds limit") {
		t.Errorf("expected 'exceeds limit' in message, got %q", result.Message)
	}
}

func TestHasNoQueryValue(t *testing.T) {
	type empty struct{}
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"nil", nil, true},
		{"false", false, false},
		{"true", true, false},
		{"int 0", 0, false},
		{"int 1", 1, false},
		{"int64 0", int64(0), false},
		{"float64 0", float64(0), false},
		{"float64 1.5", float64(1.5), false},
		{"empty string", "", true},
		{"non-empty string", "x", false},
		{"empty []any", []any{}, true},
		{"non-empty []any", []any{1}, false},
		{"empty []string", []string{}, true},
		{"non-empty []string", []string{"x"}, false},
		{"empty []int", []int{}, true},
		{"empty map[string]any", map[string]any{}, true},
		{"empty map[string]string", map[string]string{}, true},
		{"non-empty map", map[string]string{"k": "v"}, false},
		{"nil pointer", (*empty)(nil), true},
		{"non-nil pointer", &empty{}, false},
		{"empty []byte", []byte{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasNoQueryValue(tc.v); got != tc.want {
				t.Errorf("hasNoQueryValue(%#v) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}
