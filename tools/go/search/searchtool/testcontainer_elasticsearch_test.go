// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package searchtool

import (
	"context"
	"strings"
	"testing"
)

func TestES_IndexSearchUpdateDelete(t *testing.T) {
	b := connectedES(t)
	idx := uniqueIndex(t)

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Index:     idx,
		Operation: strPtr("index"),
		ID:        strPtr("1"),
		Query:     `{"name":"Alice","role":"Engineer"}`,
	}); err != nil {
		t.Fatalf("index: %v", err)
	}
	refreshIndex(t, esEndpoint, idx)

	read, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Query: `{"query":{"match_all":{}}}`})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	requireHits(t, read, 1)
	if read.Documents[0]["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", read.Documents[0]["name"])
	}

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Index:     idx,
		Operation: strPtr("update"),
		ID:        strPtr("1"),
		Query:     `{"role":"Lead"}`,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	refreshIndex(t, esEndpoint, idx)

	updated, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Query: `{"query":{"match_all":{}}}`})
	if err != nil {
		t.Fatalf("search after update: %v", err)
	}
	requireHits(t, updated, 1)
	if updated.Documents[0]["role"] != "Lead" {
		t.Errorf("role = %v, want Lead", updated.Documents[0]["role"])
	}

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Index:     idx,
		Operation: strPtr("delete"),
		ID:        strPtr("1"),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	refreshIndex(t, esEndpoint, idx)

	gone, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Query: `{"query":{"match_all":{}}}`})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	requireHits(t, gone, 0)
}

func TestES_Count(t *testing.T) {
	b := connectedES(t)
	idx := uniqueIndex(t)

	for _, body := range []string{`{"name":"Alice"}`, `{"name":"Bob"}`} {
		if _, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Operation: strPtr("index"), Query: body}); err != nil {
			t.Fatalf("index: %v", err)
		}
	}
	refreshIndex(t, esEndpoint, idx)

	res, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Operation: strPtr("count"), Query: `{}`})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	requireHits(t, res, 1)
	if got := res.Documents[0]["count"]; toFloat(got) != 2 {
		t.Errorf("count = %v, want 2", got)
	}
}

func TestES_Introspect(t *testing.T) {
	b := connectedES(t)
	idx := uniqueIndex(t)

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Index:     idx,
		Operation: strPtr("index"),
		Query:     `{"name":"Alice","age":30}`,
	}); err != nil {
		t.Fatalf("index: %v", err)
	}
	refreshIndex(t, esEndpoint, idx)

	yaml, err := b.IntrospectSchema(t.Context(), IntrospectConfig{MaxIndices: 20})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if !strings.Contains(yaml, idx) {
		t.Errorf("introspection output missing index %q:\n%s", idx, yaml)
	}
}

func TestES_InvalidQuery(t *testing.T) {
	b := connectedES(t)
	idx := uniqueIndex(t)
	if _, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Query: `{not valid json`}); err == nil {
		t.Fatal("expected error for malformed query JSON")
	}
}

func TestES_BasicAuth(t *testing.T) {
	if esSkip != "" {
		t.Skipf("elasticsearch: %s", esSkip)
	}
	const password = "test_password_123"
	endpoint, stop, err := startSecuredElasticsearch(t.Context(), password)
	if err != nil {
		t.Fatalf("start secured elasticsearch: %v", err)
	}
	t.Cleanup(func() { _ = stop(context.Background()) })

	bad := newElasticsearchBackend(map[string]any{
		"backend":           "elasticsearch",
		"connection_string": endpoint,
		"username":          "elastic",
		"password":          "wrong-password",
	})
	if err := bad.Connect(t.Context()); err == nil {
		_ = bad.Close(context.Background())
		t.Fatal("expected connect to fail with wrong basic-auth password")
	}

	b := newElasticsearchBackend(map[string]any{
		"backend":           "elasticsearch",
		"connection_string": endpoint,
		"username":          "elastic",
		"password":          password,
	})
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect with basic auth: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })

	idx := uniqueIndex(t)
	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Index:     idx,
		Operation: strPtr("index"),
		ID:        strPtr("1"),
		Query:     `{"name":"Alice"}`,
	}); err != nil {
		t.Fatalf("index under basic auth: %v", err)
	}
	refreshIndex(t, endpoint, idx, "elastic", password)

	read, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Query: `{"query":{"match_all":{}}}`})
	if err != nil {
		t.Fatalf("search under basic auth: %v", err)
	}
	requireHits(t, read, 1)
}

func TestES_Unreachable(t *testing.T) {
	if esSkip != "" {
		t.Skipf("elasticsearch: %s", esSkip)
	}
	b := newElasticsearchBackend(map[string]any{
		"backend":           "elasticsearch",
		"connection_string": "http://127.0.0.1:1",
	})
	if err := b.Connect(t.Context()); err == nil {
		_ = b.Close(context.Background())
		t.Fatal("expected connect error for unreachable elasticsearch")
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return -1
	}
}
