// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type QueryParams struct {
	Query string `json:"query" desc:"The SQL query to execute."`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("execute_sql_query",
			"Executes a SQL query against a configured database.",
			executeQuery,
			sdk.WithConfigSchema(
				sdk.ConfigSchemaField{Key: "tool_name", Type: "string", Required: true, Description: "The name of the tool as it will be invoked by the LLM."},
				sdk.ConfigSchemaField{Key: "tool_description", Type: "string", Description: "A description of what the tool does."},
				sdk.ConfigSchemaField{Key: "connection_string", Type: "string", Required: true, Secret: true, Description: "The full database connection string (e.g., 'postgresql+psycopg2://user:password@host:port/dbname')."},
				sdk.ConfigSchemaField{Key: "auto_detect_schema", Type: "boolean", Default: true, Description: "If true, automatically detect schema. If false, overrides must be provided."},
				sdk.ConfigSchemaField{Key: "schema_summary_override", Type: "string", Description: "Natural language summary of the schema if auto_detect_schema is false."},
				sdk.ConfigSchemaField{Key: "max_enum_cardinality", Type: "integer", Default: 100, Description: "Maximum number of distinct values to consider a column as an enum (default: 100)."},
				sdk.ConfigSchemaField{Key: "schema_sample_size", Type: "integer", Default: 100, Description: "Number of rows to sample per table for schema detection (default: 100)."},
			),
			sdk.WithDynamicSchema(dynamicSchema),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func dynamicSchema(config map[string]any) *sdk.SchemaOverride {
	connStr := configString(config, "connection_string", "")
	if connStr == "" {
		return nil
	}

	override := &sdk.SchemaOverride{}

	if name := configString(config, "tool_name", ""); name != "" {
		override.Name = name
	}

	baseDescription := configString(config, "tool_description", "")
	autoDetect := configBool(config, "auto_detect_schema", true)

	if !autoDetect {
		schemaSummary := configString(config, "schema_summary_override", "")
		if schemaSummary == "" {
			override.Description = baseDescription
			return override
		}
		override.Description = connectedDescription(baseDescription, schemaSummary)
		return override
	}

	db, dialect, err := openDB(connStr)
	if err != nil {
		return unavailableOverride(override, baseDescription, connStr, "failed to create database engine", err)
	}
	defer func() { _ = db.Close() }()

	cfg := IntrospectConfig{
		MaxEnumCardinality: configInt(config, "max_enum_cardinality", 100),
		SampleSize:         configInt(config, "schema_sample_size", 100),
	}

	schemaYAML, err := introspectSchema(context.Background(), db, dialect, cfg)
	if err != nil {
		return unavailableOverride(override, baseDescription, connStr, "schema detection failed", err)
	}

	override.Description = connectedDescription(baseDescription, schemaYAML)
	return override
}

func connectedDescription(base, schema string) string {
	return fmt.Sprintf("%s\n\nTables (table: column TYPE, ...):\n%s", base, schema)
}

func unavailableOverride(override *sdk.SchemaOverride, base, connStr, what string, err error) *sdk.SchemaOverride {
	reportUnavailable(what, connStr, err)
	override.Description = unavailableDescription(base)
	return override
}

func unavailableDescription(base string) string {
	return fmt.Sprintf(
		"%s\n\n❌ WARNING: This database is currently UNAVAILABLE.\nQueries to this database will fail until connectivity is restored.",
		base,
	)
}

func reportUnavailable(what, connStr string, err error) {
	detail := err.Error()
	if connStr != "" {
		detail = strings.ReplaceAll(detail, connStr, connectionStringOmitted)
	}
	fmt.Fprintf(os.Stderr, "sqltool: %s: %s\n", what, detail)
}

func executeQuery(ctx context.Context, p QueryParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	connStr := tc.GetConfigString("connection_string", "")
	if connStr == "" {
		return sdk.Error("connection_string not configured in tool_config"), nil
	}

	db, _, err := openDB(connStr)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	defer func() { _ = db.Close() }()

	query := strings.TrimSpace(p.Query)
	if query == "" {
		return sdk.Error("empty query"), nil
	}

	return runQueryInTx(ctx, db, query)
}

func runQueryInTx(ctx context.Context, db *sql.DB, query string) (*sdk.Result, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if queryReturnsRows(query) {
		rows, queryErr := tx.QueryContext(ctx, query)
		if queryErr != nil {
			return sdk.Error(queryErr.Error()), nil
		}
		results, scanErr := scanRowsToMaps(rows)
		_ = rows.Close()
		if scanErr != nil {
			return sdk.Error(scanErr.Error()), nil
		}
		if results == nil {
			results = []map[string]any{}
		}
		payload, marshalErr := json.Marshal(results)
		if marshalErr != nil {
			return sdk.Error(fmt.Sprintf("failed to encode query result: %v", marshalErr)), nil
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return sdk.Error(commitErr.Error()), nil
		}
		committed = true
		return sdk.OK(
			fmt.Sprintf("Query returned %d rows", len(results)),
			sdk.WithDataObjects(sdk.DataObject{
				Name:        fmt.Sprintf("sql_query_result_%s.json", uniqueSuffix()),
				Content:     payload,
				MIMEType:    "application/json",
				Disposition: sdk.DispositionInline,
				Description: fmt.Sprintf("SQL query result: %d rows", len(results)),
				Metadata: map[string]any{
					"row_count":    len(results),
					"source_query": truncateQuery(query),
				},
			}),
		), nil
	}

	res, execErr := tx.ExecContext(ctx, query)
	if execErr != nil {
		return sdk.Error(execErr.Error()), nil
	}
	affected, _ := res.RowsAffected()
	if commitErr := tx.Commit(); commitErr != nil {
		return sdk.Error(commitErr.Error()), nil
	}
	committed = true
	return sdk.OK(
		fmt.Sprintf("Query executed successfully, %d rows affected", affected),
		sdk.WithData(map[string]any{
			"result": []map[string]any{
				{"status": "success", "affected_rows": affected},
			},
		}),
	), nil
}

func queryReturnsRows(query string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	sanitized := strings.TrimSpace(sanitizeForDispatch(upper))
	for _, prefix := range []string{"SELECT", "SHOW", "DESCRIBE", "EXPLAIN", "PRAGMA"} {
		if strings.HasPrefix(sanitized, prefix) {
			return true
		}
	}
	for _, prefix := range []string{"INSERT", "UPDATE", "DELETE", "MERGE"} {
		if strings.HasPrefix(sanitized, prefix) {
			return hasReturningClause(sanitized)
		}
	}
	if strings.HasPrefix(sanitized, "WITH") {
		if !cteTerminatesInDML(sanitized) {
			return true
		}
		return hasReturningClause(sanitized)
	}
	return false
}

func sanitizeForDispatch(upper string) string {
	b := []byte(upper)
	n := len(b)
	for i := 0; i < n; {
		switch {
		case b[i] == '\'':
			j := i + 1
			for j < n {
				if b[j] == '\'' {
					if j+1 < n && b[j+1] == '\'' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			blankRange(b, i, j)
			i = j
		case b[i] == '"':
			j := i + 1
			for j < n {
				if b[j] == '"' {
					if j+1 < n && b[j+1] == '"' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			blankRange(b, i, j)
			i = j
		case b[i] == '`':
			j := i + 1
			for j < n && b[j] != '`' {
				j++
			}
			if j < n {
				j++
			}
			blankRange(b, i, j)
			i = j
		case b[i] == '-' && i+1 < n && b[i+1] == '-':
			j := i + 2
			for j < n && b[j] != '\n' {
				j++
			}
			blankRange(b, i, j)
			i = j
		case b[i] == '/' && i+1 < n && b[i+1] == '*':
			j := i + 2
			for j+1 < n && (b[j] != '*' || b[j+1] != '/') {
				j++
			}
			if j+1 < n {
				j += 2
			} else {
				j = n
			}
			blankRange(b, i, j)
			i = j
		default:
			i++
		}
	}
	return string(b)
}

func blankRange(b []byte, start, end int) {
	if end > len(b) {
		end = len(b)
	}
	for i := start; i < end; i++ {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
}

func hasReturningClause(upper string) bool {
	const kw = "RETURNING"
	const n = len(kw)
	for i := 0; i+n <= len(upper); i++ {
		if upper[i:i+n] != kw {
			continue
		}
		if i > 0 && isIdentChar(upper[i-1]) {
			continue
		}
		if i+n < len(upper) && isIdentChar(upper[i+n]) {
			continue
		}
		return true
	}
	return false
}

func isIdentChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func cteTerminatesInDML(upper string) bool {
	depth := 0
	for i := range len(upper) {
		switch upper[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				rest := strings.TrimSpace(upper[i+1:])
				if len(rest) > 0 && rest[0] == ',' {
					continue
				}
				for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "MERGE"} {
					if strings.HasPrefix(rest, kw) {
						return true
					}
				}
			}
		}
	}
	return false
}

func truncateQuery(query string) string {
	const maxRunes = 1000
	r := []rune(query)
	if len(r) <= maxRunes {
		return query
	}
	return string(r[:maxRunes]) + " …(truncated)"
}

func uniqueSuffix() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf[:])
}

func configString(config map[string]any, key, defaultVal string) string {
	v, ok := config[key]
	if !ok {
		return defaultVal
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal
	}
	return s
}

func configBool(config map[string]any, key string, defaultVal bool) bool {
	v, ok := config[key]
	if !ok {
		return defaultVal
	}
	b, ok := v.(bool)
	if !ok {
		return defaultVal
	}
	return b
}

func configInt(config map[string]any, key string, defaultVal int) int {
	v, ok := config[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return defaultVal
}
