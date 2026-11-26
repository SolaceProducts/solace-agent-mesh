// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package graphdb

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestNeo4j_CreateReadDelete(t *testing.T) {
	b := connectedNeo4j(t)
	label := uniqueLabel(t)
	t.Cleanup(func() { deleteLabel(b, label) })

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("CREATE (n:%s {name:'Alice', role:'Engineer'})", label),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	qr, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (n:%s) RETURN n.name AS name, n.role AS role", label),
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	requireRecords(t, qr, 1)
	if qr.Records[0]["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", qr.Records[0]["name"])
	}

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (n:%s {name:'Alice'}) DETACH DELETE n", label),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	qr, err = b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (n:%s) RETURN n.name AS name", label),
	})
	if err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	requireRecords(t, qr, 0)
}

func TestNeo4j_MatchThenWrite(t *testing.T) {
	b := connectedNeo4j(t)
	label := uniqueLabel(t)
	t.Cleanup(func() { deleteLabel(b, label) })

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("CREATE (c:%s {name:'Globex'})", label),
	}); err != nil {
		t.Fatalf("seed company: %v", err)
	}

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (c:%s {name:'Globex'}) CREATE (p:%s {name:'Bob'})-[:WORKS_AT]->(c)", label, label),
	}); err != nil {
		t.Fatalf("match-then-create: %v", err)
	}

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (p:%s {name:'Bob'}) SET p.role = 'Lead'", label),
	}); err != nil {
		t.Fatalf("match-then-set: %v", err)
	}

	qr, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (p:%s {name:'Bob'})-[:WORKS_AT]->(c:%s {name:'Globex'}) RETURN p.role AS role", label, label),
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	requireRecords(t, qr, 1)
	if qr.Records[0]["role"] != "Lead" {
		t.Errorf("role = %v, want Lead", qr.Records[0]["role"])
	}
}

func TestNeo4j_DetachDeleteRemovesRelationships(t *testing.T) {
	b := connectedNeo4j(t)
	label := uniqueLabel(t)
	t.Cleanup(func() { deleteLabel(b, label) })

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("CREATE (a:%s {name:'A'})-[:KNOWS]->(b:%s {name:'B'})", label, label),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (n:%s) DETACH DELETE n", label),
	}); err != nil {
		t.Fatalf("detach delete: %v", err)
	}

	qr, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("MATCH (n:%s) RETURN n", label),
	})
	if err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	requireRecords(t, qr, 0)
}

func TestNeo4j_Introspect(t *testing.T) {
	b := connectedNeo4j(t)
	label := uniqueLabel(t)
	t.Cleanup(func() { deleteLabel(b, label) })

	if _, err := b.ExecuteQuery(t.Context(), QueryParams{
		Query: fmt.Sprintf("CREATE (n:%s {name:'Alice', age:30})", label),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	yaml, err := b.IntrospectSchema(t.Context(), IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if !strings.Contains(yaml, label) {
		t.Errorf("introspection output missing label %q\n%s", label, yaml)
	}
}

func TestNeo4j_InvalidQuery(t *testing.T) {
	b := connectedNeo4j(t)
	if _, err := b.ExecuteQuery(t.Context(), QueryParams{Query: "MATCH (n RETURN n"}); err == nil {
		t.Fatal("expected error for malformed Cypher")
	}
}

func TestNeo4j_Unreachable(t *testing.T) {
	if neo4jSkip != "" {
		t.Skipf("neo4j: %s", neo4jSkip)
	}
	b := newNeo4jBackend(map[string]any{
		"backend":           "neo4j",
		"connection_string": "bolt://127.0.0.1:1",
		"username":          neo4jUser,
		"password":          neo4jPassword,
	})
	if err := b.Connect(t.Context()); err == nil {
		_ = b.Close(context.Background())
		t.Fatal("expected connect error for unreachable neo4j")
	}
}
