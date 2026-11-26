// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package searchtool

import (
	"strings"
	"testing"
)

func TestOpenSearch_IndexSearchUpdateDelete(t *testing.T) {
	b := connectedOpenSearch(t)
	idx := uniqueIndex(t)

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Index:     idx,
		Operation: strPtr("index"),
		ID:        strPtr("1"),
		Query:     `{"name":"Alice","role":"Engineer"}`,
	}); err != nil {
		t.Fatalf("index: %v", err)
	}
	refreshIndex(t, osEndpoint, idx)

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
	refreshIndex(t, osEndpoint, idx)

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
	refreshIndex(t, osEndpoint, idx)

	gone, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Query: `{"query":{"match_all":{}}}`})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	requireHits(t, gone, 0)
}

func TestOpenSearch_Count(t *testing.T) {
	b := connectedOpenSearch(t)
	idx := uniqueIndex(t)

	for _, body := range []string{`{"name":"Alice"}`, `{"name":"Bob"}`} {
		if _, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Operation: strPtr("index"), Query: body}); err != nil {
			t.Fatalf("index: %v", err)
		}
	}
	refreshIndex(t, osEndpoint, idx)

	res, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Operation: strPtr("count"), Query: `{}`})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	requireHits(t, res, 1)
	if got := res.Documents[0]["count"]; toFloat(got) != 2 {
		t.Errorf("count = %v, want 2", got)
	}
}

func TestOpenSearch_Introspect(t *testing.T) {
	b := connectedOpenSearch(t)
	idx := uniqueIndex(t)

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Index:     idx,
		Operation: strPtr("index"),
		Query:     `{"name":"Alice","age":30}`,
	}); err != nil {
		t.Fatalf("index: %v", err)
	}
	refreshIndex(t, osEndpoint, idx)

	yaml, err := b.IntrospectSchema(t.Context(), IntrospectConfig{MaxIndices: 20})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if !strings.Contains(yaml, idx) {
		t.Errorf("introspection output missing index %q:\n%s", idx, yaml)
	}
}

func TestOpenSearch_InvalidQuery(t *testing.T) {
	b := connectedOpenSearch(t)
	idx := uniqueIndex(t)
	if _, err := b.ExecuteQuery(t.Context(), QueryParams{Index: idx, Query: `{not valid json`}); err == nil {
		t.Fatal("expected error for malformed query JSON")
	}
}
