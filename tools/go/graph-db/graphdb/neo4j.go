// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"fmt"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type neo4jBackend struct {
	connStr  string
	username string
	password string
	database string
	driver   neo4j.DriverWithContext
	config   map[string]any
}

func newNeo4jBackend(config map[string]any) *neo4jBackend {
	return &neo4jBackend{
		connStr:  configString(config, "connection_string", ""),
		username: configString(config, "username", ""),
		password: configString(config, "password", ""),
		database: configString(config, "database", "neo4j"),
		config:   config,
	}
}

func (n *neo4jBackend) Connect(ctx context.Context) error {
	if n.connStr == "" {
		return fmt.Errorf("connection_string not configured")
	}

	var auth neo4j.AuthToken
	if n.username != "" {
		auth = neo4j.BasicAuth(n.username, n.password, "")
	} else {
		auth = neo4j.NoAuth()
	}

	driver, err := neo4j.NewDriverWithContext(n.connStr, auth)
	if err != nil {
		return fmt.Errorf("create neo4j driver: %w", unwrapURLError(err))
	}

	if err := driver.VerifyConnectivity(ctx); err != nil {
		_ = driver.Close(ctx)
		return fmt.Errorf("verify neo4j connectivity: %w", err)
	}

	n.driver = driver
	return nil
}

func (n *neo4jBackend) Close(ctx context.Context) error {
	if n.driver != nil {
		return n.driver.Close(ctx)
	}
	return nil
}

func (n *neo4jBackend) ExecuteQuery(ctx context.Context, params QueryParams) (*QueryResult, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}

	cypherParams := params.Params
	if cypherParams == nil {
		cypherParams = map[string]any{}
	}

	if params.Limit != nil && *params.Limit > 0 && isReadQuery(query) {
		if !strings.Contains(strings.ToUpper(query), "LIMIT") {
			query += fmt.Sprintf(" LIMIT %d", *params.Limit)
		}
	} else if isReadQuery(query) && !strings.Contains(strings.ToUpper(query), "LIMIT") {
		query += " LIMIT 20"
	}

	session := n.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: n.database,
	})
	defer func() { _ = session.Close(ctx) }()

	if isReadQuery(query) {
		return n.executeRead(ctx, session, query, cypherParams)
	}
	return n.executeWrite(ctx, session, query, cypherParams)
}

func (n *neo4jBackend) executeRead(ctx context.Context, session neo4j.SessionWithContext, query string, params map[string]any) (*QueryResult, error) {
	records, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, err
		}
		return collectRecords(ctx, result)
	})
	if err != nil {
		return nil, fmt.Errorf("read query: %w", err)
	}

	recs := records.([]map[string]any)
	return &QueryResult{
		Records: recs,
		Message: fmt.Sprintf("Query returned %d records", len(recs)),
	}, nil
}

func (n *neo4jBackend) executeWrite(ctx context.Context, session neo4j.SessionWithContext, query string, params map[string]any) (*QueryResult, error) {
	summary, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, err
		}
		return result.Consume(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("write query: %w", err)
	}

	s := summary.(neo4j.ResultSummary)
	counters := s.Counters()

	affected := int64(counters.NodesCreated() + counters.NodesDeleted() +
		counters.RelationshipsCreated() + counters.RelationshipsDeleted() +
		counters.PropertiesSet())

	parts := []string{}
	if c := counters.NodesCreated(); c > 0 {
		parts = append(parts, fmt.Sprintf("%d nodes created", c))
	}
	if c := counters.NodesDeleted(); c > 0 {
		parts = append(parts, fmt.Sprintf("%d nodes deleted", c))
	}
	if c := counters.RelationshipsCreated(); c > 0 {
		parts = append(parts, fmt.Sprintf("%d relationships created", c))
	}
	if c := counters.RelationshipsDeleted(); c > 0 {
		parts = append(parts, fmt.Sprintf("%d relationships deleted", c))
	}
	if c := counters.PropertiesSet(); c > 0 {
		parts = append(parts, fmt.Sprintf("%d properties set", c))
	}

	msg := "Query executed successfully"
	if len(parts) > 0 {
		msg = strings.Join(parts, ", ")
	}

	return &QueryResult{
		AffectedCount: affected,
		Message:       msg,
	}, nil
}

func collectRecords(ctx context.Context, result neo4j.ResultWithContext) ([]map[string]any, error) {
	var records []map[string]any
	for result.Next(ctx) {
		record := result.Record()
		row := make(map[string]any, len(record.Keys))
		for i, key := range record.Keys {
			row[key] = normalizeNeo4jValue(record.Values[i])
		}
		records = append(records, row)
	}
	if err := result.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func normalizeNeo4jValue(v any) any {
	switch val := v.(type) {
	case neo4j.Node:
		node := map[string]any{
			"_labels": val.Labels,
		}
		for k, prop := range val.Props {
			node[k] = normalizeNeo4jValue(prop)
		}
		return node
	case neo4j.Relationship:
		rel := map[string]any{
			"_type": val.Type,
		}
		for k, prop := range val.Props {
			rel[k] = normalizeNeo4jValue(prop)
		}
		return rel
	case neo4j.Path:
		nodes := make([]any, len(val.Nodes))
		for i, n := range val.Nodes {
			nodes[i] = normalizeNeo4jValue(n)
		}
		rels := make([]any, len(val.Relationships))
		for i, r := range val.Relationships {
			rels[i] = normalizeNeo4jValue(r)
		}
		return map[string]any{
			"_nodes":         nodes,
			"_relationships": rels,
		}
	case []any:
		arr := make([]any, len(val))
		for i, item := range val {
			arr[i] = normalizeNeo4jValue(item)
		}
		return arr
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, item := range val {
			m[k] = normalizeNeo4jValue(item)
		}
		return m
	default:
		return v
	}
}
