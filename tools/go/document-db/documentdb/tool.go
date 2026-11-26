// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type MongoQueryParams struct {
	Operation         *string `json:"operation" desc:"Operation: aggregate (default, read), insert, update, delete."`
	PipelineStr       *string `json:"pipeline_str" desc:"For aggregate (read): the pipeline as a JSON array of stages. Simple filters can be expressed as [{\"$match\": {...}}]."`
	Documents         *string `json:"documents" desc:"For insert: a JSON array of documents to insert, e.g. [{\"name\":\"Widget\",\"price\":10}]."`
	Filter            *string `json:"filter" desc:"For update/delete: a JSON filter selecting the documents, e.g. {\"name\":\"Widget\"}."`
	Update            *string `json:"update" desc:"For update: a JSON update document using MongoDB operators, e.g. {\"$set\":{\"price\":12}}."`
	ResponseFormat    *string `json:"response_format,omitempty" desc:"Output format for read results: json, yaml, csv, or markdown. Default: json."`
	ResultDescription *string `json:"result_description,omitempty" desc:"Optional description stored in the artifact metadata."`
}

type DynamoQueryParams struct {
	Statement         string  `json:"statement" desc:"PartiQL statement (SELECT / INSERT / UPDATE / DELETE) targeting the configured table."`
	ResponseFormat    *string `json:"response_format,omitempty" desc:"Output format for SELECT results: json, yaml, csv, or markdown. Default: json. Ignored for write statements."`
	ResultDescription *string `json:"result_description,omitempty" desc:"Optional description stored in the artifact metadata."`
}

type GenericQueryParams struct {
	Collection string  `json:"collection" desc:"Target collection / table name."`
	Query      string  `json:"query" desc:"The query to execute."`
	Operation  *string `json:"operation,omitempty" desc:"Operation type (backend-specific)."`
	Limit      *int    `json:"limit,omitempty" desc:"Maximum number of documents to return (default 20)."`
}

type AnyParams map[string]any

const baseInstructionsGeneric = `This tool executes queries against a document database.

## Query Guidelines
- Provide a collection/table name and query appropriate to the backend
- Use limit to control result size on large collections
- Avoid destructive operations unless explicitly requested`

func mongoInstructions() string {
	return `This tool reads from and writes to a MongoDB collection.

## Query Guidelines
- operation selects the action: aggregate (default, read), insert, update, delete
- For aggregate (read): pipeline_str is a JSON string containing the aggregation pipeline (array of stages). Simple filters are expressed as [{"$match": {...}}]. Use $limit inside the pipeline to control result size on large collections
- For insert: documents is a JSON array of documents to insert, e.g. [{"name":"Widget","price":10}]
- For update: filter is a JSON object selecting documents and update is a JSON object using MongoDB operators, e.g. {"$set":{"price":12}}. All matching documents are updated
- For delete: filter is a JSON object selecting documents to remove. An empty filter is rejected to avoid deleting the whole collection
- response_format selects how read results are written to the artifact and returned inline (json | yaml | csv | markdown)
- result_description is stored on the artifact metadata`
}

func dynamoInstructions() string {
	return `This tool executes PartiQL statements against Amazon DynamoDB.

## Query Guidelines
- Write PartiQL statements (SQL-like syntax for DynamoDB)
- SELECT: SELECT * FROM "TableName" WHERE pk = 'value'
- INSERT: INSERT INTO "TableName" VALUE {'pk': 'val', 'sk': 'val', 'attr': 'val'}
- UPDATE: UPDATE "TableName" SET attr = 'val' WHERE pk = 'val' AND sk = 'val'
- DELETE: DELETE FROM "TableName" WHERE pk = 'val' AND sk = 'val'
- Always include the partition key in WHERE clauses for efficient queries
- response_format applies to SELECT results only and is written to the artifact (json | yaml | csv | markdown)
- result_description is stored on the artifact metadata`
}

const (
	defaultMongoToolName  = "mongo_query"
	defaultDynamoToolName = "dynamodb_query"
	defaultGenericName    = "document_db_query"
)

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool(defaultGenericName,
			"Execute queries against a document database (MongoDB, DynamoDB)",
			executeQuery,
			sdk.WithInstructions(baseInstructionsGeneric),
			sdk.WithDynamicSchema(dynamicSchema),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func dynamicSchema(config map[string]any) *sdk.SchemaOverride {
	kind := effectiveBackend(config)

	override := &sdk.SchemaOverride{}

	explicitToolName := configString(config, "tool_name", "")
	if explicitToolName != "" {
		override.Name = explicitToolName
	} else {
		switch kind {
		case backendMongoDB:
			override.Name = defaultMongoToolName
		case backendDynamoDB:
			override.Name = defaultDynamoToolName
		default:
		}
	}

	if desc := configString(config, "tool_description", ""); desc != "" {
		override.Description = desc
	}

	switch kind {
	case backendMongoDB:
		override.Parameters = sdk.BuildSchema[MongoQueryParams]()
	case backendDynamoDB:
		override.Parameters = sdk.BuildSchema[DynamoQueryParams]()
	default:
		override.Parameters = sdk.BuildSchema[GenericQueryParams]()
	}

	var instructions string
	switch kind {
	case backendMongoDB:
		instructions = mongoInstructions()
	case backendDynamoDB:
		instructions = dynamoInstructions()
	default:
		instructions = baseInstructionsGeneric
	}

	autoDetect := configBool(config, "auto_detect_schema", true)
	if !autoDetect {
		override.Instructions = instructions
		if summary := configString(config, "schema_summary_override", ""); summary != "" {
			override.Instructions += "\n\n## Database Schema\n" + summary
		}
		return override
	}

	if kind != backendMongoDB && kind != backendDynamoDB {
		override.Instructions = instructions
		return override
	}

	backend, err := newBackend(config)
	if err != nil {
		override.Instructions = instructions + "\n\n## Database Status\nUnavailable: " + err.Error()
		return override
	}

	ctx := context.Background()
	if connErr := backend.Connect(ctx); connErr != nil {
		override.Instructions = instructions + "\n\n## Database Status\nConnection failed: " + connErr.Error()
		return override
	}
	defer func() { _ = backend.Close(ctx) }()

	cfg := IntrospectConfig{
		MaxSampleSize:  configInt(config, "max_sample_size", 100),
		MaxCollections: configInt(config, "max_collections", 50),
	}

	schemaYAML, err := backend.IntrospectSchema(ctx, cfg)
	if err != nil {
		override.Instructions = instructions + "\n\n## Database Status\nSchema introspection failed: " + err.Error()
		return override
	}

	override.Instructions = instructions + "\n\n## Database Schema\n```yaml\n" + schemaYAML + "```"
	return override
}

func executeQuery(ctx context.Context, p AnyParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	config := configFromToolContext(tc)

	backend, err := newBackend(config)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create backend: %v", err)), nil
	}

	if connErr := backend.Connect(ctx); connErr != nil {
		return sdk.Error(fmt.Sprintf("failed to connect: %v", connErr)), nil
	}
	defer func() { _ = backend.Close(ctx) }()

	_ = tc.SendStatus("Executing query...")

	result, err := backend.ExecuteQuery(ctx, map[string]any(p))
	if err != nil {
		return sdk.Error(fmt.Sprintf("query failed: %v", err)), nil
	}

	return buildResult(config, result)
}

func buildResult(config map[string]any, qr *QueryResult) (*sdk.Result, error) {
	if qr == nil {
		return sdk.Error("query backend returned nil result"), nil
	}

	if qr.IsWrite {
		msg := qr.Message
		if msg == "" {
			msg = "Statement executed successfully"
		}
		return sdk.OK(msg, sdk.WithData(map[string]any{
			"affected_count": qr.AffectedCount,
		})), nil
	}

	rowCount := len(qr.Documents)
	format := qr.ResponseFormat
	if format == "" {
		format = "json"
	}

	contentBytes, ext, mime, err := formatResults(qr.Documents, format)
	if err != nil {
		return sdk.Error(fmt.Sprintf("formatting results: %v", err)), nil
	}

	namePrefix := artifactPrefixForBackend(effectiveBackend(config))
	filename := fmt.Sprintf("%s_result_%s.%s", namePrefix, uniqueSuffix(), ext)

	version := 0
	artifactDesc := buildArtifactDescription(
		qr.ResultDescription,
		pythonDescriptionPrefix(effectiveBackend(config)),
		descriptionLabel(effectiveBackend(config)),
		qr.QueryText,
	)

	inlineContent, previewSkipped := clampInlineContent(string(contentBytes), rowCount, capFromConfig(config))

	messageToLLM := fmt.Sprintf("Aggregation executed. Results saved to artifact '%s' (version %d).", filename, version)
	if effectiveBackend(config) == backendDynamoDB {
		messageToLLM = fmt.Sprintf("Query executed. Results saved to artifact '%s' (version %d).", filename, version)
	}
	if previewSkipped {
		messageToLLM += fmt.Sprintf(" A preview is not available as the number of results (%d) exceeds the inline limit (%d).", rowCount, capFromConfig(config))
	}

	return sdk.OK(
		messageToLLM,
		sdk.WithData(map[string]any{
			"artifact_filename": filename,
			"artifact_version":  version,
			"row_count":         rowCount,
			"content":           inlineContent,
		}),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        filename,
			Content:     contentBytes,
			MIMEType:    mime,
			Disposition: sdk.DispositionArtifact,
			Description: artifactDesc,
			Metadata: map[string]any{
				"row_count": rowCount,
			},
		}),
	), nil
}

func artifactPrefixForBackend(kind backendKind) string {
	switch kind {
	case backendMongoDB:
		return "mongo_query"
	case backendDynamoDB:
		return "dynamodb_query"
	default:
		return "document_db_query"
	}
}

func pythonDescriptionPrefix(kind backendKind) string {
	switch kind {
	case backendMongoDB:
		return "Results of MongoDB aggregation."
	case backendDynamoDB:
		return "Results of DynamoDB query."
	default:
		return "Results of document database query."
	}
}

func descriptionLabel(kind backendKind) string {
	switch kind {
	case backendMongoDB:
		return "Pipeline"
	case backendDynamoDB:
		return "Statement"
	default:
		return "Query"
	}
}

func configFromToolContext(tc *sdk.ToolContext) map[string]any {
	config := map[string]any{}
	for _, key := range []string{
		"backend", "connection_string", "database", "collection",
		"tool_name", "tool_description", "auto_detect_schema",
		"max_sample_size", "max_collections", "max_inline_results",
		"schema_summary_override",
		"region", "table_name",
		"aws_access_key_id", "aws_secret_access_key",
		"role_arn", "external_id", "session_name",
	} {
		if v, ok := tc.GetConfig(key); ok {
			config[key] = v
		}
	}
	return config
}

func decodeTypedParams(args map[string]any, dst any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal args: %w", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("unmarshal into %T: %w", dst, err)
	}
	return nil
}
