// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build neptuneiam

package graphdb

import (
	"cmp"
	"os"
	"testing"
)

func TestNeptuneIAMAuth_LiveCluster(t *testing.T) {
	endpoint := os.Getenv("NEPTUNE_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set NEPTUNE_TEST_ENDPOINT (e.g. bolt+ssc://host:8182) to run")
	}

	cfg := map[string]any{
		"backend":           "neptune",
		"connection_string": endpoint,
		"region":            cmp.Or(os.Getenv("NEPTUNE_TEST_REGION"), "us-east-1"),
	}

	be := newNeptuneBackend(cfg)
	ctx := t.Context()
	if err := be.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = be.Close(ctx) }()

	res, err := be.ExecuteQuery(ctx, QueryParams{
		Query: "MATCH (a:Author)-[:WROTE]->(b:Book) RETURN a.name AS author, b.title AS book",
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res.Records) == 0 {
		t.Fatal("expected seeded Author->Book records, got 0")
	}
	t.Logf("authenticated and returned %d records", len(res.Records))
}
