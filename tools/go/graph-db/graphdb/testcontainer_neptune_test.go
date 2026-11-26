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

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func neptuneOnNeo4j(t *testing.T) *neptuneBackend {
	t.Helper()
	if neo4jSkip != "" {
		t.Skipf("neo4j: %s", neo4jSkip)
	}
	drv, err := neo4j.NewDriverWithContext(neo4jURI, neo4j.BasicAuth(neo4jUser, neo4jPassword, ""))
	if err != nil {
		t.Fatalf("neptune-on-neo4j driver: %v", err)
	}
	if err := drv.VerifyConnectivity(t.Context()); err != nil {
		t.Fatalf("verify connectivity: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close(context.Background()) })
	return &neptuneBackend{driver: drv, database: "neo4j"}
}

func TestNeptuneLogic_ExecuteAndIntrospect(t *testing.T) {
	nb := neptuneOnNeo4j(t)
	label := uniqueLabel(t)
	t.Cleanup(func() {
		_, _ = nb.ExecuteQuery(context.Background(), QueryParams{Query: fmt.Sprintf("MATCH (n:%s) DETACH DELETE n", label)})
	})

	createQ := fmt.Sprintf("CREATE (a:%s {name:'Alice', age:30})-[:KNOWS]->(b:%s {name:'Bob'})", label, label)
	if _, err := nb.ExecuteQuery(t.Context(), QueryParams{Query: createQ}); err != nil {
		t.Fatalf("neptune write path: %v", err)
	}

	readQ := fmt.Sprintf("MATCH (n:%s) RETURN n.name AS name ORDER BY name", label)
	res, err := nb.ExecuteQuery(t.Context(), QueryParams{Query: readQ})
	if err != nil {
		t.Fatalf("neptune read path: %v", err)
	}
	requireRecords(t, res, 2)
	if res.Records[0]["name"] != "Alice" || res.Records[1]["name"] != "Bob" {
		t.Errorf("read names = %v, want [Alice Bob]", res.Records)
	}

	yaml, err := nb.IntrospectSchema(t.Context(), IntrospectConfig{MaxLabels: 200, MaxSampleSize: 50})
	if err != nil {
		t.Fatalf("neptune introspect: %v", err)
	}
	if !strings.Contains(yaml, label) {
		t.Errorf("introspection missing label %q:\n%s", label, yaml)
	}
	if !strings.Contains(yaml, "KNOWS") {
		t.Errorf("introspection missing KNOWS relationship type:\n%s", yaml)
	}
}
