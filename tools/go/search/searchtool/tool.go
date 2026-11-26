// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"context"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type QueryParams struct {
	Index     string  `json:"index" desc:"Target index name. Required."`
	Query     string  `json:"query" desc:"Elasticsearch/OpenSearch Query DSL as JSON. For search: {\"query\": {\"match\": {\"field\": \"value\"}}}. For aggregations: include an \"aggs\" key. For index: the document body. For update (with id set): the partial document fields."`
	Operation *string `json:"operation" desc:"Operation: search, index, update, delete, count. Default: search."`
	ID        *string `json:"id" desc:"Document _id. For index: assign this id (optional; auto-generated when omitted). For update/delete: target this document by id."`
	Limit     *int    `json:"limit" desc:"Maximum documents to return (default 20). Only for search operations."`
}

const schemaResolutionTimeout = 30 * time.Second

const baseInstructionsGeneric = `This tool executes queries against a search engine.

## Query Guidelines
- Provide an index name and a Query DSL JSON body
- Use limit to control result size
- Avoid destructive operations unless explicitly requested`

func elasticsearchInstructions() string {
	return `This tool executes queries against an Elasticsearch cluster.

## Query Guidelines
- Set index to the target index name
- For search: query is Query DSL JSON, e.g. {"query": {"match": {"title": "search term"}}}
- For match_all: {"query": {"match_all": {}}}
- For bool queries: {"query": {"bool": {"must": [...], "filter": [...]}}}
- For aggregations: include "aggs" key, e.g. {"aggs": {"by_status": {"terms": {"field": "status"}}}}
- For index (insert): set operation to "index", query is the document JSON. Optionally set id to assign a specific _id (auto-generated when omitted)
- For update: set operation to "update". Either set id and put the changed fields in query, or pass query as {"id": "doc_id", "doc": {"field": "new_value"}}
- For delete: set operation to "delete". Set id to delete by document id, or pass a Query DSL in query for delete-by-query
- For count: set operation to "count", query is optional filter JSON
- Use limit to control result count (default: 20)
- Use _source filtering to reduce response size: {"_source": ["field1", "field2"], "query": {...}}`
}

func opensearchInstructions() string {
	return `This tool executes queries against an OpenSearch cluster.

## Query Guidelines
- Set index to the target index name
- For search: query is Query DSL JSON, e.g. {"query": {"match": {"title": "search term"}}}
- For match_all: {"query": {"match_all": {}}}
- For bool queries: {"query": {"bool": {"must": [...], "filter": [...]}}}
- For aggregations: include "aggs" key, e.g. {"aggs": {"by_status": {"terms": {"field": "status"}}}}
- For index (insert): set operation to "index", query is the document JSON. Optionally set id to assign a specific _id (auto-generated when omitted)
- For update: set operation to "update". Either set id and put the changed fields in query, or pass query as {"id": "doc_id", "doc": {"field": "new_value"}}
- For delete: set operation to "delete". Set id to delete by document id, or pass a Query DSL in query for delete-by-query
- For count: set operation to "count", query is optional filter JSON
- Use limit to control result count (default: 20)
- Use _source filtering to reduce response size: {"_source": ["field1", "field2"], "query": {...}}`
}

const serverlessInstructions = `

## OpenSearch Serverless Limits
This cluster is an OpenSearch Serverless collection:
- delete only works by document id; delete-by-query is not supported
- update only works on SEARCH collections; time series and vector search collections reject it
- index with an explicit id only works on SEARCH collections; omit id on time series and vector search collections`

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("search_query",
			"Execute queries against a search engine (Elasticsearch, OpenSearch)",
			executeQuery,
			sdk.WithInstructions(baseInstructionsGeneric),
			sdk.WithDynamicSchema(dynamicSchema),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func dynamicSchema(config map[string]any) *sdk.SchemaOverride {
	backendType := configString(config, "backend", "")
	if backendType == "" {
		backendType = detectBackend(config)
	}
	if backendType == "" {
		return nil
	}

	override := &sdk.SchemaOverride{}

	if name := configString(config, "tool_name", ""); name != "" {
		override.Name = name
	}
	if desc := configString(config, "tool_description", ""); desc != "" {
		override.Description = desc
	}

	var instructions string
	switch backendType {
	case "elasticsearch":
		instructions = elasticsearchInstructions()
	case "opensearch":
		instructions = opensearchInstructions()
		if isServerlessEndpoint(configString(config, "connection_string", "")) {
			instructions += serverlessInstructions
		}
	default:
		instructions = baseInstructionsGeneric
	}

	autoDetect := configBool(config, "auto_detect_schema", true)
	if !autoDetect {
		override.Instructions = instructions
		if summary := configString(config, "schema_summary_override", ""); summary != "" {
			override.Instructions += "\n\n## Index Schema\n" + summary
		}
		return override
	}

	backend, err := newBackend(config)
	if err != nil {
		override.Instructions = instructions + "\n\n## Cluster Status\nUnavailable: " + modelSafeError(err, config)
		return override
	}

	ctx, cancel := context.WithTimeout(context.Background(), schemaResolutionTimeout)
	defer cancel()
	if connErr := backend.Connect(ctx); connErr != nil {
		override.Instructions = instructions + "\n\n## Cluster Status\nConnection failed: " + modelSafeError(connErr, config)
		return override
	}
	defer func() { _ = backend.Close(ctx) }()

	cfg := IntrospectConfig{
		MaxSampleSize: configInt(config, "max_sample_size", 100),
		MaxIndices:    configInt(config, "max_indices", 20),
	}

	schemaYAML, err := backend.IntrospectSchema(ctx, cfg)
	if err != nil {
		override.Instructions = instructions + "\n\n## Cluster Status\nSchema introspection failed: " + modelSafeError(err, config)
		return override
	}

	override.Instructions = instructions + "\n\n## Index Schema\n```yaml\n" + schemaYAML + "```"
	return override
}

func executeQuery(ctx context.Context, p QueryParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	config := configFromToolContext(tc)

	backend, err := newBackend(config)
	if err != nil {
		return sdk.Error("failed to create backend: " + modelSafeError(err, config)), nil
	}

	if connErr := backend.Connect(ctx); connErr != nil {
		return sdk.Error("failed to connect: " + modelSafeError(connErr, config)), nil
	}
	defer func() { _ = backend.Close(ctx) }()

	_ = tc.SendStatus("Executing query...")

	result, err := backend.ExecuteQuery(ctx, p)
	if err != nil {
		return sdk.Error("query failed: " + modelSafeError(err, config)), nil
	}

	data := map[string]any{}

	if len(result.Documents) > 0 {
		data["documents"] = result.Documents
		data["document_count"] = len(result.Documents)
	}

	if result.TotalHits > 0 {
		data["total_hits"] = result.TotalHits
	}

	if len(result.Aggregations) > 0 {
		data["aggregations"] = result.Aggregations
	}

	if result.AffectedCount > 0 {
		data["affected_count"] = result.AffectedCount
	}

	return sdk.OK(result.Message, sdk.WithData(data)), nil
}

func modelSafeError(err error, config map[string]any) string {
	return stripEndpointValues(err, endpointValues(config)...).Error()
}

func configFromToolContext(tc *sdk.ToolContext) map[string]any {
	config := map[string]any{}
	for _, key := range []string{
		"backend", "connection_string", "cloud_id", "api_key", "username", "password", "ca_cert",
		"tool_name", "tool_description", "auto_detect_schema",
		"max_sample_size", "max_indices", "schema_summary_override",
		"region", "aws_access_key_id", "aws_secret_access_key",
		"role_arn", "external_id", "session_name",
	} {
		if v, ok := tc.GetConfig(key); ok {
			config[key] = v
		}
	}
	return config
}
