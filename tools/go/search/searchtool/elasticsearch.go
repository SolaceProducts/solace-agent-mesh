// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

type elasticsearchBackend struct {
	client *elasticsearch.Client
	writer *elasticsearch.Client
	config map[string]any
}

func newElasticsearchBackend(config map[string]any) *elasticsearchBackend {
	return &elasticsearchBackend{config: config}
}

func (e *elasticsearchBackend) Connect(ctx context.Context) error {
	cfg := elasticsearch.Config{}

	if connStr := configString(e.config, "connection_string", ""); connStr != "" {
		cfg.Addresses = []string{connStr}
	}

	if cloudID := configString(e.config, "cloud_id", ""); cloudID != "" {
		cfg.CloudID = cloudID
	}

	if apiKey := configString(e.config, "api_key", ""); apiKey != "" {
		cfg.APIKey = apiKey
	}

	if user := configString(e.config, "username", ""); user != "" {
		cfg.Username = user
		cfg.Password = configString(e.config, "password", "")
	}

	cfg.Transport = outboundHTTPTransport(configBool(e.config, "insecure_skip_verify", false))
	cfg.RetryOnError = retryableTransportError

	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return stripEndpointValues(fmt.Errorf("create elasticsearch client: %w", err), endpointValues(e.config)...)
	}

	writeCfg := cfg
	writeCfg.DisableRetry = true
	writer, err := elasticsearch.NewClient(writeCfg)
	if err != nil {
		return stripEndpointValues(fmt.Errorf("create elasticsearch write client: %w", err), endpointValues(e.config)...)
	}

	res, err := client.Info(client.Info.WithContext(ctx))
	if err != nil {
		return stripEndpointValues(fmt.Errorf("ping elasticsearch: %w", err), endpointValues(e.config)...)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return fmt.Errorf("elasticsearch cluster error: %s", res.String())
	}

	e.client = client
	e.writer = writer
	return nil
}

func retryableTransportError(_ *http.Request, err error) bool {
	var netErr net.Error
	return !errors.As(err, &netErr) || !netErr.Timeout()
}

func (e *elasticsearchBackend) Close(_ context.Context) error {
	return nil
}

func (e *elasticsearchBackend) ExecuteQuery(ctx context.Context, params QueryParams) (*QueryResult, error) {
	if params.Index == "" {
		return nil, fmt.Errorf("index is required")
	}

	op := "search"
	if params.Operation != nil && *params.Operation != "" {
		op = strings.ToLower(*params.Operation)
	}

	id := ""
	if params.ID != nil {
		id = strings.TrimSpace(*params.ID)
	}

	query := strings.TrimSpace(params.Query)
	if query == "" && (op != "delete" || id == "") {
		return nil, fmt.Errorf("empty query")
	}

	switch op {
	case "search":
		return e.executeSearch(ctx, params.Index, query, params.Limit)
	case "index":
		return e.executeIndex(ctx, params.Index, id, query)
	case "update":
		return e.executeUpdate(ctx, params.Index, id, query)
	case "delete":
		return e.executeDelete(ctx, params.Index, id, query)
	case "count":
		return e.executeCount(ctx, params.Index, query)
	default:
		return nil, fmt.Errorf("unsupported operation: %s (supported: search, index, update, delete, count)", op)
	}
}

func (e *elasticsearchBackend) executeSearch(ctx context.Context, index, query string, limit *int) (*QueryResult, error) {
	size := 20
	if limit != nil && *limit > 0 {
		size = *limit
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(query), &body); err != nil {
		return nil, fmt.Errorf("invalid JSON query: %w", err)
	}

	body["size"] = size

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	res, err := e.client.Search(
		e.client.Search.WithContext(ctx),
		e.client.Search.WithIndex(index),
		e.client.Search.WithBody(bytes.NewReader(buf)),
	)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("search error: %s", readBody(res.Body))
	}

	var result searchResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	docs := make([]map[string]any, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		doc := hit.Source
		doc["_id"] = hit.ID
		doc["_score"] = hit.Score
		docs = append(docs, doc)
	}

	qr := &QueryResult{
		Documents: docs,
		TotalHits: result.Hits.Total.Value,
		Message:   fmt.Sprintf("Search returned %d documents (total hits: %d)", len(docs), result.Hits.Total.Value),
	}

	if result.Aggregations != nil {
		qr.Aggregations = result.Aggregations
	}

	return qr, nil
}

func (e *elasticsearchBackend) executeIndex(ctx context.Context, index, id, query string) (*QueryResult, error) {
	opts := []func(*esapi.IndexRequest){e.writer.Index.WithContext(ctx)}
	if id != "" {
		opts = append(opts, e.writer.Index.WithDocumentID(id))
	}
	res, err := e.writer.Index(
		index,
		strings.NewReader(query),
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("index error: %s", readBody(res.Body))
	}

	var result indexResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &QueryResult{
		AffectedCount: 1,
		Message:       fmt.Sprintf("Indexed document (id: %s, result: %s)", result.ID, result.Result),
	}, nil
}

func (e *elasticsearchBackend) executeUpdate(ctx context.Context, index, id, query string) (*QueryResult, error) {
	docID := id
	var doc map[string]any
	if docID != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(query), &raw); err != nil {
			return nil, fmt.Errorf("invalid JSON document for update: %w", err)
		}
		if inner, ok := raw["doc"].(map[string]any); ok && len(raw) == 1 {
			doc = inner
		} else {
			doc = raw
		}
	} else {
		var parsed struct {
			ID  string         `json:"id"`
			Doc map[string]any `json:"doc"`
		}
		if err := json.Unmarshal([]byte(query), &parsed); err != nil {
			return nil, fmt.Errorf("invalid JSON (set the id parameter, or pass {\"id\": \"...\", \"doc\": {...}}): %w", err)
		}
		docID = parsed.ID
		doc = parsed.Doc
	}
	if docID == "" {
		return nil, fmt.Errorf("update requires a document id (set the id parameter or include \"id\" in the query)")
	}
	if doc == nil {
		return nil, fmt.Errorf("update requires document fields")
	}

	body, _ := json.Marshal(map[string]any{"doc": doc})

	res, err := e.writer.Update(
		index,
		docID,
		bytes.NewReader(body),
		e.writer.Update.WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("update error: %s", readBody(res.Body))
	}

	return &QueryResult{
		AffectedCount: 1,
		Message:       fmt.Sprintf("Updated document %s", docID),
	}, nil
}

func (e *elasticsearchBackend) executeDelete(ctx context.Context, index, id, query string) (*QueryResult, error) {
	if id == "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(query), &parsed); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if qid, ok := parsed["id"].(string); ok {
			id = qid
		}
	}

	if id != "" {
		res, err := e.writer.Delete(
			index,
			id,
			e.writer.Delete.WithContext(ctx),
		)
		if err != nil {
			return nil, fmt.Errorf("delete: %w", err)
		}
		defer func() { _ = res.Body.Close() }()

		if res.IsError() {
			return nil, fmt.Errorf("delete error: %s", readBody(res.Body))
		}

		return &QueryResult{
			AffectedCount: 1,
			Message:       fmt.Sprintf("Deleted document %s", id),
		}, nil
	}

	res, err := e.writer.DeleteByQuery(
		[]string{index},
		strings.NewReader(query),
		e.writer.DeleteByQuery.WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("delete_by_query: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("delete_by_query error: %s", readBody(res.Body))
	}

	var result deleteByQueryResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &QueryResult{
		AffectedCount: result.Deleted,
		Message:       fmt.Sprintf("Deleted %d documents", result.Deleted),
	}, nil
}

func (e *elasticsearchBackend) executeCount(ctx context.Context, index, query string) (*QueryResult, error) {
	var opts []func(*esapi.CountRequest)
	opts = append(opts, e.client.Count.WithContext(ctx))
	opts = append(opts, e.client.Count.WithIndex(index))

	if query != "" && query != "{}" {
		opts = append(opts, e.client.Count.WithBody(strings.NewReader(query)))
	}

	res, err := e.client.Count(opts...)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.IsError() {
		return nil, fmt.Errorf("count error: %s", readBody(res.Body))
	}

	var result countResponse
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &QueryResult{
		Documents: []map[string]any{{"count": result.Count}},
		Message:   fmt.Sprintf("Count: %d", result.Count),
	}, nil
}

type searchResponse struct {
	Hits struct {
		Total struct {
			Value int64 `json:"value"`
		} `json:"total"`
		Hits []searchHit `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]any `json:"aggregations,omitempty"`
}

type searchHit struct {
	ID     string         `json:"_id"`
	Score  float64        `json:"_score"`
	Source map[string]any `json:"_source"`
}

type indexResponse struct {
	ID     string `json:"_id"`
	Result string `json:"result"`
}

type deleteByQueryResponse struct {
	Deleted int64 `json:"deleted"`
}

type countResponse struct {
	Count int64 `json:"count"`
}

func readBody(r io.Reader) string {
	b, err := io.ReadAll(r)
	if err != nil {
		return "(failed to read response body)"
	}
	if len(b) > 500 {
		return string(b[:500]) + "..."
	}
	return string(b)
}
