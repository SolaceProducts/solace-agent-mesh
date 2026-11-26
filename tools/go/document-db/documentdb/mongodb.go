// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type mongoBackend struct {
	connStr    string
	database   string
	collection string
	client     *mongo.Client
}

func newMongoBackend(config map[string]any) *mongoBackend {
	return &mongoBackend{
		connStr:    configString(config, "connection_string", ""),
		database:   configString(config, "database", ""),
		collection: configString(config, "collection", ""),
	}
}

func (m *mongoBackend) Connect(ctx context.Context) error {
	if m.connStr == "" {
		return fmt.Errorf("connection_string not configured")
	}

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	opts := options.Client().ApplyURI(m.connStr)
	client, err := mongo.Connect(opts)
	if err != nil {
		return fmt.Errorf("connect to mongodb: %w", err)
	}

	if err := client.Ping(connectCtx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return fmt.Errorf("ping mongodb: %w", err)
	}

	m.client = client

	if m.database == "" {
		m.database = databaseFromURI(m.connStr)
	}
	if m.database == "" {
		return fmt.Errorf("no database specified: set 'database' in tool_config or include it in the connection string path")
	}

	return nil
}

func (m *mongoBackend) Close(ctx context.Context) error {
	if m.client != nil {
		return m.client.Disconnect(ctx)
	}
	return nil
}

func (m *mongoBackend) ExecuteQuery(ctx context.Context, args map[string]any) (*QueryResult, error) {
	if m.collection == "" {
		return nil, fmt.Errorf("missing 'collection' in tool_config")
	}

	var params MongoQueryParams
	if err := decodeTypedParams(args, &params); err != nil {
		return nil, err
	}

	op := "aggregate"
	if params.Operation != nil && strings.TrimSpace(*params.Operation) != "" {
		op = strings.ToLower(strings.TrimSpace(*params.Operation))
	}

	switch op {
	case "aggregate":
		return m.executeAggregate(ctx, params)
	case "insert":
		return m.executeInsert(ctx, params)
	case "update":
		return m.executeUpdate(ctx, params)
	case "delete":
		return m.executeDelete(ctx, params)
	default:
		return nil, fmt.Errorf("unsupported operation: %s (supported: aggregate, insert, update, delete)", op)
	}
}

func (m *mongoBackend) collectionHandle() *mongo.Collection {
	return m.client.Database(m.database).Collection(m.collection)
}

func (m *mongoBackend) executeAggregate(ctx context.Context, params MongoQueryParams) (*QueryResult, error) {
	pipelineStr := strings.TrimSpace(derefString(params.PipelineStr, ""))
	if pipelineStr == "" {
		return nil, fmt.Errorf("pipeline_str is required for aggregate")
	}

	var pipeline []bson.M
	if err := json.Unmarshal([]byte(pipelineStr), &pipeline); err != nil {
		return nil, fmt.Errorf("invalid pipeline format. Expected a valid JSON string. Error: %w", err)
	}

	coll := m.collectionHandle()
	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate: %w", err)
	}
	defer func() { _ = cursor.Close(ctx) }()

	docs, err := collectCursor(ctx, cursor)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Documents:         docs,
		Message:           fmt.Sprintf("Query returned %d documents", len(docs)),
		ResponseFormat:    derefString(params.ResponseFormat, "json"),
		ResultDescription: derefString(params.ResultDescription, ""),
		QueryText:         pipelineStr,
	}, nil
}

func (m *mongoBackend) executeInsert(ctx context.Context, params MongoQueryParams) (*QueryResult, error) {
	docsStr := strings.TrimSpace(derefString(params.Documents, ""))
	if docsStr == "" {
		return nil, fmt.Errorf("documents is required for insert")
	}

	var docs []any
	if err := json.Unmarshal([]byte(docsStr), &docs); err != nil {
		var single bson.M
		if singleErr := json.Unmarshal([]byte(docsStr), &single); singleErr != nil {
			return nil, fmt.Errorf("invalid documents format. Expected a JSON array of documents. Error: %w", err)
		}
		docs = []any{single}
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("documents must contain at least one document")
	}

	coll := m.collectionHandle()
	res, err := coll.InsertMany(ctx, docs)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	return &QueryResult{
		IsWrite:       true,
		AffectedCount: int64(len(res.InsertedIDs)),
		Message:       fmt.Sprintf("Inserted %d documents", len(res.InsertedIDs)),
	}, nil
}

func (m *mongoBackend) executeUpdate(ctx context.Context, params MongoQueryParams) (*QueryResult, error) {
	filter, err := parseJSONObject(params.Filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}
	update, err := parseJSONObject(params.Update)
	if err != nil {
		return nil, fmt.Errorf("invalid update: %w", err)
	}
	if len(update) == 0 {
		return nil, fmt.Errorf("update is required for update (use MongoDB operators, e.g. {\"$set\": {...}})")
	}
	if len(filter) == 0 {
		return nil, fmt.Errorf("filter is required for update; an empty filter would update every document")
	}

	coll := m.collectionHandle()
	res, err := coll.UpdateMany(ctx, filter, update)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	return &QueryResult{
		IsWrite:       true,
		AffectedCount: res.ModifiedCount,
		Message:       fmt.Sprintf("Matched %d, modified %d documents", res.MatchedCount, res.ModifiedCount),
	}, nil
}

func (m *mongoBackend) executeDelete(ctx context.Context, params MongoQueryParams) (*QueryResult, error) {
	filter, err := parseJSONObject(params.Filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}
	if len(filter) == 0 {
		return nil, fmt.Errorf("filter is required for delete; an empty filter would delete every document")
	}

	coll := m.collectionHandle()
	res, err := coll.DeleteMany(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}

	return &QueryResult{
		IsWrite:       true,
		AffectedCount: res.DeletedCount,
		Message:       fmt.Sprintf("Deleted %d documents", res.DeletedCount),
	}, nil
}

func parseJSONObject(s *string) (bson.M, error) {
	if s == nil {
		return bson.M{}, nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return bson.M{}, nil
	}
	var m bson.M
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return nil, fmt.Errorf("expected a JSON object: %w", err)
	}
	return m, nil
}

func collectCursor(ctx context.Context, cursor *mongo.Cursor) ([]map[string]any, error) {
	var docs []map[string]any
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode document: %w", err)
		}
		docs = append(docs, normalizeBSON(doc))
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor: %w", err)
	}
	return docs, nil
}

func normalizeBSON(doc bson.M) map[string]any {
	result := make(map[string]any, len(doc))
	for k, v := range doc {
		result[k] = normalizeBSONValue(v)
	}
	return result
}

func normalizeBSONValue(v any) any {
	switch val := v.(type) {
	case bson.M:
		return normalizeBSON(val)
	case bson.A:
		arr := make([]any, len(val))
		for i, item := range val {
			arr[i] = normalizeBSONValue(item)
		}
		return arr
	case bson.ObjectID:
		return val.Hex()
	default:
		return val
	}
}

func databaseFromURI(uri string) string {
	trimmed := uri
	for _, prefix := range []string{"mongodb+srv://", "mongodb://"} {
		if strings.HasPrefix(trimmed, prefix) {
			trimmed = strings.TrimPrefix(trimmed, prefix)
			break
		}
	}

	if idx := strings.Index(trimmed, "@"); idx != -1 {
		trimmed = trimmed[idx+1:]
	}

	if idx := strings.Index(trimmed, "/"); idx != -1 {
		trimmed = trimmed[idx+1:]
	} else {
		return ""
	}

	if idx := strings.Index(trimmed, "?"); idx != -1 {
		trimmed = trimmed[:idx]
	}

	return trimmed
}

func derefString(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}
