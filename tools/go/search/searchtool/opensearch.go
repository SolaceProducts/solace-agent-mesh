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
	"net/url"
	"slices"
	"strings"

	opensearch "github.com/opensearch-project/opensearch-go/v4"
	opensearchapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/signer/awsv2"
)

const (
	serverlessHostSuffix      = ".aoss.amazonaws.com"
	managedDomainHostSuffix   = ".es.amazonaws.com"
	chinaPartitionSuffix      = ".cn"
	sigv4ServiceManagedDomain = "es"
	sigv4ServiceServerless    = "aoss"
)

var errServerlessDeleteByQuery = errors.New("delete-by-query is not supported on OpenSearch Serverless; set id to delete a single document")

type openSearchBackend struct {
	client     *opensearchapi.Client
	writer     *opensearchapi.Client
	config     map[string]any
	serverless bool
}

func newOpenSearchBackend(config map[string]any) *openSearchBackend {
	return &openSearchBackend{
		config:     config,
		serverless: isServerlessEndpoint(configString(config, "connection_string", "")),
	}
}

func (o *openSearchBackend) Connect(ctx context.Context) error {
	osCfg, err := o.clientConfig(ctx)
	if err != nil {
		return err
	}

	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: osCfg})
	if err != nil {
		return stripEndpointValues(fmt.Errorf("create opensearch client: %w", err), endpointValues(o.config)...)
	}

	writeCfg := osCfg
	writeCfg.DisableRetry = true
	writer, err := opensearchapi.NewClient(opensearchapi.Config{Client: writeCfg})
	if err != nil {
		return stripEndpointValues(fmt.Errorf("create opensearch write client: %w", err), endpointValues(o.config)...)
	}

	o.client = client
	o.writer = writer
	return nil
}

func (o *openSearchBackend) clientConfig(ctx context.Context) (opensearch.Config, error) {
	connStr := configString(o.config, "connection_string", "")
	if connStr == "" {
		return opensearch.Config{}, fmt.Errorf("connection_string not configured")
	}

	osCfg := opensearch.Config{
		Addresses: []string{connStr},
		Transport: outboundHTTPTransport(configBool(o.config, "insecure_skip_verify", false)),
	}

	if configString(o.config, "region", "") != "" {
		awsCfg, err := loadAWSConfig(ctx, authFromConfig(o.config))
		if err != nil {
			return opensearch.Config{}, fmt.Errorf("load aws config: %w", err)
		}
		awsSigner, err := awsv2.NewSignerWithService(awsCfg, o.signingService())
		if err != nil {
			return opensearch.Config{}, fmt.Errorf("create aws signer: %w", err)
		}
		osCfg.Signer = awsSigner
	}

	return osCfg, nil
}

func (o *openSearchBackend) signingService() string {
	if o.serverless {
		return sigv4ServiceServerless
	}
	return sigv4ServiceManagedDomain
}

func (o *openSearchBackend) Close(_ context.Context) error {
	return nil
}

func (o *openSearchBackend) ExecuteQuery(ctx context.Context, params QueryParams) (*QueryResult, error) {
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
		return o.executeSearch(ctx, params.Index, query, params.Limit)
	case "index":
		return o.executeIndex(ctx, params.Index, id, query)
	case "update":
		return o.executeUpdate(ctx, params.Index, id, query)
	case "delete":
		return o.executeDelete(ctx, params.Index, id, query)
	case "count":
		return o.executeCount(ctx, params.Index, query)
	default:
		return nil, fmt.Errorf("unsupported operation: %s (supported: search, index, update, delete, count)", op)
	}
}

func (o *openSearchBackend) executeSearch(ctx context.Context, index, query string, limit *int) (*QueryResult, error) {
	size := 20
	if limit != nil && *limit > 0 {
		size = *limit
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(query), &body); err != nil {
		return nil, fmt.Errorf("invalid JSON query: %w", err)
	}
	body["size"] = size

	buf, _ := json.Marshal(body)

	resp, err := o.client.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{index},
		Body:    bytes.NewReader(buf),
	})
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	rawBody, _ := io.ReadAll(resp.Inspect().Response.Body)
	var result searchResponse
	if err := json.Unmarshal(rawBody, &result); err != nil {
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

func (o *openSearchBackend) executeIndex(ctx context.Context, index, id, query string) (*QueryResult, error) {
	req := opensearchapi.IndexReq{
		Index: index,
		Body:  strings.NewReader(query),
	}
	if id != "" {
		req.DocumentID = id
	}
	resp, err := o.writer.Index(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}

	return &QueryResult{
		AffectedCount: 1,
		Message:       fmt.Sprintf("Indexed document (id: %s)", resp.ID),
	}, nil
}

func (o *openSearchBackend) executeUpdate(ctx context.Context, index, id, query string) (*QueryResult, error) {
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

	_, err := o.writer.Update(ctx, opensearchapi.UpdateReq{
		Index:      index,
		DocumentID: docID,
		Body:       bytes.NewReader(body),
	})
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	return &QueryResult{
		AffectedCount: 1,
		Message:       fmt.Sprintf("Updated document %s", docID),
	}, nil
}

func (o *openSearchBackend) executeDelete(ctx context.Context, index, id, query string) (*QueryResult, error) {
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
		_, err := o.writer.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{
			Index:      index,
			DocumentID: id,
		})
		if err != nil {
			return nil, fmt.Errorf("delete: %w", err)
		}

		return &QueryResult{
			AffectedCount: 1,
			Message:       fmt.Sprintf("Deleted document %s", id),
		}, nil
	}

	if o.serverless {
		return nil, errServerlessDeleteByQuery
	}

	resp, err := o.writer.Document.DeleteByQuery(ctx, opensearchapi.DocumentDeleteByQueryReq{
		Indices: []string{index},
		Body:    strings.NewReader(query),
	})
	if err != nil {
		return nil, fmt.Errorf("delete_by_query: %w", err)
	}

	return &QueryResult{
		AffectedCount: int64(resp.Deleted),
		Message:       fmt.Sprintf("Deleted %d documents", resp.Deleted),
	}, nil
}

func (o *openSearchBackend) executeCount(ctx context.Context, index, query string) (*QueryResult, error) {
	req := &opensearchapi.IndicesCountReq{
		Indices: []string{index},
	}
	if query != "" && query != "{}" {
		req.Body = strings.NewReader(query)
	}

	resp, err := o.client.Indices.Count(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	return &QueryResult{
		Documents: []map[string]any{{"count": resp.Count}},
		Message:   fmt.Sprintf("Count: %d", resp.Count),
	}, nil
}

func (o *openSearchBackend) IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error) {
	if cfg.MaxIndices <= 0 {
		cfg.MaxIndices = 20
	}

	indices, err := o.listIndices(ctx, cfg.MaxIndices)
	if err != nil {
		return "", fmt.Errorf("list indices: %w", err)
	}

	schemas := make(map[string]*indexSchema)
	var orderedNames []string

	for _, idx := range indices {
		schema, err := o.introspectIndex(ctx, idx)
		if err != nil {
			continue
		}
		schemas[idx] = schema
		orderedNames = append(orderedNames, idx)
	}

	return marshalOrderedYAML(orderedNames, schemas)
}

func (o *openSearchBackend) listIndices(ctx context.Context, maxIndices int) ([]string, error) {
	resp, err := o.client.Cat.Indices(ctx, nil)
	if err != nil {
		return nil, err
	}

	var indices []string
	for _, idx := range resp.Indices {
		if strings.HasPrefix(idx.Index, ".") {
			continue
		}
		indices = append(indices, idx.Index)
	}

	slices.Sort(indices)
	if len(indices) > maxIndices {
		indices = indices[:maxIndices]
	}
	return indices, nil
}

func (o *openSearchBackend) introspectIndex(ctx context.Context, index string) (*indexSchema, error) {
	schema := &indexSchema{
		Mappings: make(map[string]*mapping),
	}

	mappingResp, err := o.client.Indices.Mapping.Get(ctx, &opensearchapi.MappingGetReq{
		Indices: []string{index},
	})
	if err == nil {
		if idxData, ok := mappingResp.GetIndices()[index]; ok {
			var mappingBody struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if unmarshalErr := json.Unmarshal(idxData.Mappings, &mappingBody); unmarshalErr == nil {
				flattenOpenSearchMappings("", mappingBody.Properties, schema.Mappings)
			}
		}
	}

	countResp, err := o.client.Indices.Count(ctx, &opensearchapi.IndicesCountReq{
		Indices: []string{index},
	})
	if err == nil {
		count := int64(countResp.Count)
		schema.DocumentCount = &count
	}

	return schema, nil
}

func flattenOpenSearchMappings(prefix string, properties map[string]json.RawMessage, out map[string]*mapping) {
	for field, raw := range properties {
		fullName := field
		if prefix != "" {
			fullName = prefix + "." + field
		}

		var prop struct {
			Type       string                     `json:"type"`
			Analyzer   string                     `json:"analyzer"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			continue
		}

		if prop.Properties != nil {
			flattenOpenSearchMappings(fullName, prop.Properties, out)
			continue
		}

		if prop.Type == "" {
			continue
		}

		m := &mapping{Type: prop.Type}
		if prop.Analyzer != "" {
			m.Analyzer = &prop.Analyzer
		}
		out[fullName] = m
	}
}

func isServerlessEndpoint(endpoint string) bool {
	return hasAWSHostSuffix(endpoint, serverlessHostSuffix)
}

func isManagedDomainEndpoint(endpoint string) bool {
	return hasAWSHostSuffix(endpoint, managedDomainHostSuffix)
}

func hasAWSHostSuffix(endpoint, suffix string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return strings.HasSuffix(host, suffix) || strings.HasSuffix(host, suffix+chinaPartitionSuffix)
}
