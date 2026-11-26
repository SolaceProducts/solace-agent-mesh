// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildRequestContext_Sources(t *testing.T) {
	body := `{"order":{"id":"abc"}}`
	req := httptest.NewRequest("POST", "/orders/abc?since=2026", strings.NewReader(body))
	req.Header.Set("X-Event-Type", "created")
	req.Header.Set("Authorization", "Bearer supersecret")

	route := &RouteConfig{
		Name:    "r",
		Headers: map[string]string{"event": "X-Event-Type"},
	}
	ctx := buildRequestContext(req, route, []byte(body), map[string]string{"id": "abc"}, slog.Default())

	v, err := ctx.GetData("input.payload:order.id")
	if err != nil {
		t.Fatalf("GetData payload order.id: %v", err)
	}
	if v != "abc" {
		t.Errorf("payload order.id = %v, want abc", v)
	}
	v, err = ctx.GetData("user_data.path:id")
	if err != nil {
		t.Fatalf("GetData path id: %v", err)
	}
	if v != "abc" {
		t.Errorf("path id = %v, want abc", v)
	}
	v, err = ctx.GetData("user_data.query:since")
	if err != nil {
		t.Fatalf("GetData query since: %v", err)
	}
	if v != "2026" {
		t.Errorf("query since = %v, want 2026", v)
	}
	v, err = ctx.GetData("user_data.headers:event")
	if err != nil {
		t.Fatalf("GetData header event: %v", err)
	}
	if v != "created" {
		t.Errorf("header event = %v, want created", v)
	}
	v, err = ctx.GetData("user_data.headers:Authorization")
	if err != nil {
		t.Fatalf("GetData Authorization: %v", err)
	}
	if v != nil {
		t.Errorf("Authorization should not be referenceable, got %v", v)
	}
}

func TestBuildRequestContext_RawBody(t *testing.T) {
	body := "not json"
	req := httptest.NewRequest("POST", "/h", strings.NewReader(body))
	ctx := buildRequestContext(req, &RouteConfig{Name: "r"}, []byte(body), nil, slog.Default())
	v, err := ctx.GetData("input.payload:")
	if err != nil {
		t.Fatalf("GetData raw payload: %v", err)
	}
	if v != "not json" {
		t.Errorf("raw payload = %v, want %q", v, body)
	}
}

func TestBuildRequestContext_QueryTokenStripped(t *testing.T) {
	req := httptest.NewRequest("POST", "/h?token=s3cr3t&keep=yes", nil)
	route := &RouteConfig{
		Name: "r",
		Auth: &AuthConfig{Token: &TokenAuthConfig{Location: "query", Name: "token", Value: "s3cr3t"}},
	}
	ctx := buildRequestContext(req, route, nil, nil, slog.Default())

	v, err := ctx.GetData("user_data.query:token")
	if err != nil {
		t.Fatalf("GetData query token: %v", err)
	}
	if v != nil {
		t.Errorf("query-located auth token must be stripped, got %v", v)
	}
	v, err = ctx.GetData("user_data.query:keep")
	if err != nil {
		t.Fatalf("GetData query keep: %v", err)
	}
	if v != "yes" {
		t.Errorf("non-auth query param should remain, got %v", v)
	}
}
