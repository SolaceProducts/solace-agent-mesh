// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type neptuneBackend struct {
	connStr  string
	database string
	driver   neo4j.DriverWithContext
	config   map[string]any
}

func newNeptuneBackend(config map[string]any) *neptuneBackend {
	return &neptuneBackend{
		connStr:  configString(config, "connection_string", ""),
		database: configString(config, "database", ""),
		config:   config,
	}
}

func (n *neptuneBackend) Connect(ctx context.Context) error {
	if n.connStr == "" {
		return fmt.Errorf("connection_string not configured")
	}

	endpoint, err := neptuneEndpointFromConnStr(n.connStr)
	if err != nil {
		return err
	}

	auth := authFromConfig(n.config)
	awsCfg, err := loadAWSConfig(ctx, auth)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	tokenManager := newNeptuneAuthTokenManager(auth.region, endpoint, awsCfg.Credentials)

	driver, err := neo4j.NewDriverWithContext(n.connStr, tokenManager)
	if err != nil {
		return fmt.Errorf("create neptune driver: %w", unwrapURLError(err))
	}

	if err := driver.VerifyConnectivity(ctx); err != nil {
		_ = driver.Close(ctx)
		return fmt.Errorf("verify neptune connectivity: %w", err)
	}

	n.driver = driver
	return nil
}

func (n *neptuneBackend) Close(ctx context.Context) error {
	if n.driver != nil {
		return n.driver.Close(ctx)
	}
	return nil
}

func (n *neptuneBackend) ExecuteQuery(ctx context.Context, params QueryParams) (*QueryResult, error) {
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

	sessionCfg := neo4j.SessionConfig{}
	if n.database != "" {
		sessionCfg.DatabaseName = n.database
	}

	session := n.driver.NewSession(ctx, sessionCfg)
	defer func() { _ = session.Close(ctx) }()

	if isReadQuery(query) {
		records, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			result, err := tx.Run(ctx, query, cypherParams)
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

	summary, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, query, cypherParams)
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

	return &QueryResult{
		AffectedCount: affected,
		Message:       "Query executed successfully",
	}, nil
}

func (n *neptuneBackend) IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error) {
	if cfg.MaxSampleSize <= 0 {
		cfg.MaxSampleSize = 100
	}
	if cfg.MaxLabels <= 0 {
		cfg.MaxLabels = 50
	}

	sessionCfg := neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead}
	if n.database != "" {
		sessionCfg.DatabaseName = n.database
	}
	session := n.driver.NewSession(ctx, sessionCfg)
	defer func() { _ = session.Close(ctx) }()

	schema := &graphSchema{
		NodeLabels: make(map[string]*labelSchema),
	}

	labels, err := neptuneGetLabels(ctx, session)
	if err != nil {
		return "", fmt.Errorf("get labels: %w", err)
	}
	if len(labels) > cfg.MaxLabels {
		labels = labels[:cfg.MaxLabels]
	}

	for _, label := range labels {
		ls := &labelSchema{
			Properties: make(map[string]*propInfo),
		}

		if count, err := getLabelCount(ctx, session, label); err == nil {
			ls.Count = &count
		}

		if props, err := sampleProperties(ctx, session, label, cfg.MaxSampleSize); err == nil {
			ls.Properties = props
		}

		schema.NodeLabels[label] = ls
	}

	if rels, err := neptuneGetRelationshipTypes(ctx, session); err == nil {
		schema.RelationshipTypes = rels
	}

	return marshalGraphYAML(schema, labels)
}

func neptuneGetLabels(ctx context.Context, session neo4j.SessionWithContext) ([]string, error) {
	result, err := session.Run(ctx, "MATCH (n) RETURN DISTINCT labels(n) AS labels", nil)
	if err != nil {
		return nil, err
	}

	labelSet := make(map[string]struct{})
	for result.Next(ctx) {
		if labelList, ok := result.Record().Values[0].([]any); ok {
			for _, l := range labelList {
				if s, ok := l.(string); ok {
					labelSet[s] = struct{}{}
				}
			}
		}
	}
	if err := result.Err(); err != nil {
		return nil, err
	}

	labels := make([]string, 0, len(labelSet))
	for l := range labelSet {
		labels = append(labels, l)
	}
	slices.Sort(labels)
	return labels, nil
}

func neptuneGetRelationshipTypes(ctx context.Context, session neo4j.SessionWithContext) ([]relTypeInfo, error) {
	result, err := session.Run(ctx, "MATCH ()-[r]->() RETURN DISTINCT type(r) AS type", nil)
	if err != nil {
		return nil, err
	}

	var types []string
	for result.Next(ctx) {
		if t, ok := result.Record().Values[0].(string); ok {
			types = append(types, t)
		}
	}
	if err := result.Err(); err != nil {
		return nil, err
	}

	slices.Sort(types)
	var rels []relTypeInfo
	for _, t := range types {
		ri := relTypeInfo{Type: t}

		countQuery := fmt.Sprintf("MATCH ()-[r:`%s`]->() RETURN count(r) AS count", escapeCypher(t))
		countResult, err := session.Run(ctx, countQuery, nil)
		if err == nil && countResult.Next(ctx) {
			if count, ok := countResult.Record().Values[0].(int64); ok {
				ri.Count = &count
			}
		}

		rels = append(rels, ri)
	}

	return rels, nil
}
