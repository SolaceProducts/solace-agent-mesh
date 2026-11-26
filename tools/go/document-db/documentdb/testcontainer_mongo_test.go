// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package documentdb

import (
	"context"
	"strings"
	"testing"
)

func TestMongo_InsertAggregateReadDelete(t *testing.T) {
	b := connectedMongo(t, uniqueCollection(t))

	ins, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation": "insert",
		"documents": `[{"name":"Alice","role":"Engineer"},{"name":"Bob","role":"Analyst"}]`,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !ins.IsWrite || ins.AffectedCount != 2 {
		t.Fatalf("insert result = %+v, want IsWrite with affected 2", ins)
	}

	read, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation":    "aggregate",
		"pipeline_str": `[{"$match":{"name":"Alice"}}]`,
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	requireDocs(t, read, 1)
	if read.Documents[0]["role"] != "Engineer" {
		t.Errorf("role = %v, want Engineer", read.Documents[0]["role"])
	}

	del, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation": "delete",
		"filter":    `{"name":"Bob"}`,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if del.AffectedCount != 1 {
		t.Errorf("delete affected = %d, want 1", del.AffectedCount)
	}

	remaining, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation":    "aggregate",
		"pipeline_str": `[{"$match":{}}]`,
	})
	if err != nil {
		t.Fatalf("aggregate after delete: %v", err)
	}
	requireDocs(t, remaining, 1)
}

func TestMongo_Update(t *testing.T) {
	b := connectedMongo(t, uniqueCollection(t))

	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation": "insert",
		"documents": `[{"name":"Widget","price":10}]`,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	upd, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation": "update",
		"filter":    `{"name":"Widget"}`,
		"update":    `{"$set":{"price":12}}`,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.AffectedCount != 1 {
		t.Errorf("update modified = %d, want 1", upd.AffectedCount)
	}

	read, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation":    "aggregate",
		"pipeline_str": `[{"$match":{"name":"Widget"}}]`,
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	requireDocs(t, read, 1)
	if got := read.Documents[0]["price"]; toInt(got) != 12 {
		t.Errorf("price = %v (%T), want 12", got, got)
	}
}

func TestMongo_DeleteRequiresFilter(t *testing.T) {
	b := connectedMongo(t, uniqueCollection(t))
	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation": "delete",
		"filter":    `{}`,
	}); err == nil {
		t.Fatal("expected error: an empty delete filter must be rejected")
	}
}

func TestMongo_Introspect(t *testing.T) {
	coll := uniqueCollection(t)
	b := connectedMongo(t, coll)

	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation": "insert",
		"documents": `[{"name":"Alice","age":30}]`,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	yaml, err := b.IntrospectSchema(t.Context(), IntrospectConfig{MaxSampleSize: 10, MaxCollections: 10})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if !strings.Contains(yaml, coll) {
		t.Errorf("introspection output missing collection %q:\n%s", coll, yaml)
	}
}

func TestMongo_InvalidPipeline(t *testing.T) {
	b := connectedMongo(t, uniqueCollection(t))
	if _, err := b.ExecuteQuery(t.Context(), map[string]any{
		"operation":    "aggregate",
		"pipeline_str": `{not valid json`,
	}); err == nil {
		t.Fatal("expected error for malformed pipeline JSON")
	}
}

func TestMongo_Unreachable(t *testing.T) {
	if mongoSkip != "" {
		t.Skipf("mongo: %s", mongoSkip)
	}
	b := newMongoBackend(map[string]any{
		"connection_string": "mongodb://127.0.0.1:1/testdb?serverSelectionTimeoutMS=500",
		"database":          "testdb",
		"collection":        "c",
	})
	if err := b.Connect(t.Context()); err == nil {
		_ = b.Close(context.Background())
		t.Fatal("expected connect error for unreachable mongo")
	}
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return -1
	}
}
