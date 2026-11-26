// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type QueryParams struct {
	Query  string         `json:"query" desc:"Cypher query to execute. E.g.: MATCH (n:Person) RETURN n.name LIMIT 10"`
	Params map[string]any `json:"params,omitempty" desc:"Optional query parameters for parameterized queries. E.g.: {\"name\": \"Alice\"}"`
	Limit  *int           `json:"limit" desc:"Maximum records to return (default 20). Only for read queries."`
}

const baseInstructionsGeneric = `This tool executes Cypher queries against a graph database.

## Query Guidelines
- Write Cypher queries to traverse and query the graph
- Use parameterized queries when possible for safety
- Use LIMIT to control result size on large graphs
- Avoid destructive operations (DELETE, DROP) unless explicitly requested`

func neo4jInstructions() string {
	return `This tool executes Cypher queries against a Neo4j graph database.

## Query Guidelines
- MATCH: traverse and read data — MATCH (n:Person)-[:KNOWS]->(f) RETURN f.name
- CREATE: create nodes/relationships — CREATE (n:Person {name: $name})
- MERGE: create if not exists — MERGE (n:Person {email: $email}) SET n.name = $name
- DELETE: remove nodes/relationships — MATCH (n:Person {name: $name}) DELETE n
- SET: update properties — MATCH (n:Person {name: $name}) SET n.age = $age
- Use $paramName syntax for parameterized queries and pass values in params
- Use LIMIT to control result count (default: 20)
- Use OPTIONAL MATCH for left-outer-join semantics
- Use WHERE for filtering: MATCH (n:Person) WHERE n.age > 30 RETURN n
- Aggregations: MATCH (n:Person) RETURN n.city, count(n) AS count ORDER BY count DESC`
}

func neptuneInstructions() string {
	return `This tool executes openCypher queries against an Amazon Neptune graph database.

## Query Guidelines
- MATCH: traverse and read data — MATCH (n:Person)-[:KNOWS]->(f) RETURN f.name
- CREATE: create nodes/relationships — CREATE (n:Person {name: $name})
- MERGE: create if not exists — MERGE (n:Person {email: $email}) SET n.name = $name
- DELETE: remove nodes/relationships — MATCH (n:Person {name: $name}) DELETE n
- SET: update properties — MATCH (n:Person {name: $name}) SET n.age = $age
- Use $paramName syntax for parameterized queries and pass values in params
- Use LIMIT to control result count (default: 20)
- Neptune supports a subset of Cypher — avoid APOC procedures and Neo4j-specific features
- Use OPTIONAL MATCH for left-outer-join semantics
- Aggregations: MATCH (n:Person) RETURN n.city, count(n) AS count ORDER BY count DESC`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("graph_db_query",
			"Execute Cypher queries against a graph database (Neo4j, Neptune)",
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
	case "neo4j":
		instructions = neo4jInstructions()
	case "neptune":
		instructions = neptuneInstructions()
	default:
		instructions = baseInstructionsGeneric
	}

	autoDetect := configBool(config, "auto_detect_schema", true)
	if !autoDetect {
		override.Instructions = instructions
		if summary := configString(config, "schema_summary_override", ""); summary != "" {
			override.Instructions += "\n\n## Graph Schema\n" + summary
		}
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
		MaxSampleSize: configInt(config, "max_sample_size", 100),
		MaxLabels:     configInt(config, "max_labels", 50),
	}

	schemaYAML, err := backend.IntrospectSchema(ctx, cfg)
	if err != nil {
		override.Instructions = instructions + "\n\n## Database Status\nSchema introspection failed: " + err.Error()
		return override
	}

	override.Instructions = instructions + "\n\n## Graph Schema\n```yaml\n" + schemaYAML + "```"
	return override
}

func executeQuery(ctx context.Context, p QueryParams, tc *sdk.ToolContext) (*sdk.Result, error) {
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

	result, err := backend.ExecuteQuery(ctx, p)
	if err != nil {
		return sdk.Error(fmt.Sprintf("query failed: %v", err)), nil
	}

	data := map[string]any{}

	if len(result.Records) > 0 {
		data["records"] = result.Records
		data["record_count"] = len(result.Records)
	}

	if result.AffectedCount > 0 {
		data["affected_count"] = result.AffectedCount
	}

	return sdk.OK(result.Message, sdk.WithData(data)), nil
}

func configFromToolContext(tc *sdk.ToolContext) map[string]any {
	config := map[string]any{}
	for _, key := range []string{
		"backend", "connection_string", "username", "password", "database",
		"tool_name", "tool_description", "auto_detect_schema",
		"max_sample_size", "max_labels", "schema_summary_override",
		"region", "aws_access_key_id", "aws_secret_access_key",
		"role_arn", "external_id", "session_name",
	} {
		if v, ok := tc.GetConfig(key); ok {
			config[key] = v
		}
	}
	return config
}
