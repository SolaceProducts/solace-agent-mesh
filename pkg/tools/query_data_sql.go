// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sqlitegate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/jmespath/go-jmespath"
	"gopkg.in/yaml.v3"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	defaultMaxResultPreviewRows  = 50
	defaultMaxResultPreviewBytes = 2048
	defaultMaxColumns            = 100
	defaultMinPropertyFreqPct    = 10
	defaultMaxResultBytes        = 256 << 20
)

const (
	mimeTypeJSON = "application/json"
	mimeTypeCSV  = "text/csv"
)

type QueryDataWithSQLTool struct {
	tool.SideEffectingTool
	artifactToolBase
	config map[string]any

	admitQuery func(tableDefinitions []string, query string) error
	logger     *slog.Logger
}

func (t *QueryDataWithSQLTool) admit(tableDefinitions []string, query string) error {
	if t.admitQuery != nil {
		return t.admitQuery(tableDefinitions, query)
	}
	return sqlitegate.CheckSingleReadOnlySelect(tableDefinitions, query)
}

func (t *QueryDataWithSQLTool) taskLogger(toolCtx *tool.Context) *slog.Logger {
	logger := t.logger
	if logger == nil {
		logger = slog.Default()
	}
	return logger.With("logger_name", "query_data_with_sql",
		"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID,
		"agentName", toolCtx.AgentName, "userID", toolCtx.UserID, "sessionID", toolCtx.SessionID)
}

func NewQueryDataWithSQLTool(config map[string]any) *QueryDataWithSQLTool {
	return &QueryDataWithSQLTool{config: config}
}

func (t *QueryDataWithSQLTool) Name() string { return "query_data_with_sql" }

func (t *QueryDataWithSQLTool) Description() string {
	return `Run one read-only SQLite SELECT over data artifacts. input_files maps table names to artifact filenames, each optionally suffixed ':version' (CSV; JSON as a list of objects or columnar; YAML; SQLite, whose first table is loaded). Returns a preview and saves the full result as a CSV or JSON artifact.

Pre-aggregate each table in a CTE before joining: a JOIN feeding SUM, AVG or COUNT with GROUP BY is rejected, because one-to-many rows inflate the totals.

Each table must be a flat list of rows holding strings, numbers or booleans. An object one level down is loaded as parent.child columns, which a query names in double quotes ("address.city"). An array inside a row, an object nested deeper, or a row with more than 100 columns fails the load.

jmespath_transforms (optional) reshapes a table's data before it is loaded, e.g. to extract an array nested in an object, or to keep only flat fields: items[].{sku: sku, city: address.city}.`
}

func (t *QueryDataWithSQLTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_files": map[string]any{
				"type":        "object",
				"description": "Table name to artifact filename, e.g. {\"orders\": \"orders.csv\"}.",
				"additionalProperties": map[string]any{
					"type": "string",
				},
			},
			"sql_query": map[string]any{
				"type":        "string",
				"description": "A single read-only SELECT statement to run against the loaded tables.",
			},
			"jmespath_transforms": map[string]any{
				"type":        "object",
				"description": "Optional table name to JMESPath expression applied before loading; not for SQLite artifacts.",
				"additionalProperties": map[string]any{
					"type": "string",
				},
				"nullable": true,
			},
			"output_filename": map[string]any{
				"type":        "string",
				"description": "Base name for the output artifact. Extension is added automatically based on output_format. If omitted, a name is auto-generated.",
				"nullable":    true,
			},
			"result_description": map[string]any{
				"type":        "string",
				"description": "Optional description for the result artifact's metadata.",
				"nullable":    true,
			},
			"output_format": map[string]any{
				"type":        "string",
				"enum":        []string{"csv", "json"},
				"description": "Desired output format: 'csv' or 'json'. Default is 'csv'.",
				"nullable":    true,
			},
		},
		Required: []string{"input_files", "sql_query"},
	}
}

func (t *QueryDataWithSQLTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "output_filename", typ: "string",
			description: "Name of the saved artifact holding the query results."},
		resultField{name: "output_version", typ: "integer",
			description: "Version of the saved result artifact."},
		resultField{name: "status", typ: "string",
			description: "Always \"success\" on this path."},
		resultField{name: "message", typ: "string",
			description: "Human-readable summary of the query outcome."},
		resultField{name: "result_rows", typ: "integer",
			description: "Number of rows the query returned."},
		resultField{name: "result_preview", typ: "array",
			description: "First rows of the result set, as objects keyed by column name. A preview only — read the artifact for the full set."},
		resultField{name: "result_truncated", typ: "boolean",
			description: "True when result_preview holds fewer rows than result_rows."},
	)
}

func (t *QueryDataWithSQLTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	inputFiles, ok := args["input_files"].(map[string]any)
	if !ok || len(inputFiles) == 0 {
		return invalidArgument("input_files is required and must be a non-empty object mapping table names to filenames"), nil
	}
	inputFiles = normalizeTableKeys(inputFiles)

	sqlQuery, _ := args["sql_query"].(string)
	if sqlQuery == "" {
		return invalidArgument("sql_query is required"), nil
	}

	outputFilename, _ := args["output_filename"].(string)
	resultDescription, _ := args["result_description"].(string)
	outputFormat, _ := args["output_format"].(string)
	if outputFormat == "" {
		outputFormat = "csv"
	}
	if outputFormat != "csv" && outputFormat != "json" {
		return invalidArgument("output_format must be 'csv' or 'json'"), nil
	}

	transforms, transformsErr := parseJMESPathTransforms(args["jmespath_transforms"], inputFiles)
	if transformsErr != nil {
		return invalidArgument(transformsErr.Error()), nil
	}

	facade := toolCtx.Facade()

	db, err := openInMemoryQueryDB()
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to create in-memory database: %s", err)), nil
	}
	defer func() { _ = db.Close() }()

	maxCols := layeredIntConfig(toolCtx, t.config, "max_columns", defaultMaxColumns)

	sourceTables := make([]map[string]any, 0, len(inputFiles))
	for tableName, filenameRaw := range inputFiles {
		filename, _ := filenameRaw.(string)
		if filename == "" {
			return invalidArgument(fmt.Sprintf("Empty filename for table '%s'", tableName)), nil
		}

		transform, hasTransform := transforms[tableName]
		rows, emptyCols, sourceInfo, errResult := loadInputTable(ctx, facade, tableName, filename, maxCols, transform, hasTransform)
		if errResult != nil {
			return errResult, nil
		}

		if createErr := createTableFromRows(ctx, db, tableName, rows, emptyCols); createErr != nil {
			return tool.Error(fmt.Sprintf("Failed to create table '%s': %s", tableName, createErr), tableShapeErrorCode(createErr)), nil
		}
		sourceTables = append(sourceTables, sourceInfo)
	}

	if riskMsg := detectJoinAggregationRisk(sqlQuery); riskMsg != "" {
		return tool.Error(riskMsg, tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
	}

	logger := t.taskLogger(toolCtx)
	queryConn, connErr := db.Conn(ctx)
	if connErr != nil {
		return hostFailureResult(ctx, logger, stageReserveConnection, connErr, inputFiles), nil
	}
	defer func() { _ = queryConn.Close() }()

	tableDefinitions, loadedErr := loadedTableDefinitions(ctx, queryConn)
	if loadedErr != nil {
		return hostFailureResult(ctx, logger, stageReadLoadedTables, loadedErr, inputFiles), nil
	}
	if checkErr := t.admit(tableDefinitions, sqlQuery); checkErr != nil {
		return unadmittedQueryResult(ctx, logger, queryConn, checkErr, sqlQuery, inputFiles), nil
	}
	if confineErr := confineToReadOnlyQueries(ctx, queryConn); confineErr != nil {
		return hostFailureResult(ctx, logger, stageConfineConnection, confineErr, inputFiles), nil
	}

	maxResultBytes := layeredIntConfig(toolCtx, t.config, "max_result_bytes", defaultMaxResultBytes)
	maxPreviewRows := layeredIntConfig(toolCtx, t.config, "max_result_preview_rows", defaultMaxResultPreviewRows)
	maxPreviewBytes := layeredIntConfig(toolCtx, t.config, "max_result_preview_bytes", defaultMaxResultPreviewBytes)
	outputContent, preview, resultRowCount, previewTruncated, nonFinite, queryErr := streamQueryResult(
		ctx, queryConn, sqlQuery, outputFormat, maxResultBytes, maxPreviewRows, maxPreviewBytes)
	if queryErr != nil {
		if errors.Is(queryErr, errResultTooLarge) {
			return tool.Error(queryErr.Error(), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)), nil
		}
		return failedQueryResult(ctx, queryConn, queryErr, inputFiles, sqlQuery), nil
	}
	outputMIME := mimeTypeCSV
	if outputFormat == "json" {
		outputMIME = mimeTypeJSON
	}

	if outputFilename == "" {
		firstTable := ""
		for tableName := range inputFiles {
			if firstTable == "" || tableName < firstTable {
				firstTable = tableName
			}
		}
		outputFilename = fmt.Sprintf("sql_result_%s.%s", firstTable, outputFormat)
	} else {
		outputFilename = sanitizeFilename(outputFilename)
		if !strings.HasSuffix(outputFilename, "."+outputFormat) {
			outputFilename += "." + outputFormat
		}
	}

	metaDesc := resultDescription
	if metaDesc == "" {
		metaDesc = fmt.Sprintf("SQL query result: %d rows", resultRowCount)
	}

	saveResult, saveErr := facade.SaveArtifactWithMetadata(ctx, outputFilename, outputContent, outputMIME, artifact.MetadataOpts{
		Description: metaDesc,
		Custom: map[string]any{
			"source_sql_query": sqlQuery,
			"result_rows":      resultRowCount,
			"source_tables":    sourceTables,
		},
	})
	if saveErr != nil {
		return tool.Error(fmt.Sprintf("Failed to save result artifact: %s", saveErr)), nil
	}

	nonFiniteNote := nonFiniteResultNote(nonFinite, outputFormat)
	data := map[string]any{
		"status":           "success",
		"message":          fmt.Sprintf("Query executed successfully. %d rows returned.%s", resultRowCount, nonFiniteNote),
		"output_filename":  outputFilename,
		"output_version":   saveResult.DataVersion,
		"result_preview":   preview,
		"result_truncated": previewTruncated,
		"result_rows":      resultRowCount,
	}

	return tool.OK(
		fmt.Sprintf("SQL query executed: %d rows returned, saved as '%s'.%s", resultRowCount, outputFilename, nonFiniteNote),
		tool.WithData(data),
		tool.WithWithheldBytes(withheldBytes(outputContent, preview, previewTruncated)),
	), nil
}

func nonFiniteResultNote(count int, outputFormat string) string {
	switch {
	case count == 0:
		return ""
	case outputFormat == "csv":
		return fmt.Sprintf(" %d NaN/Infinity value(s) in the result; the CSV artifact keeps them as text (+Inf/-Inf) and the preview shows any it includes as null.", count)
	default:
		return fmt.Sprintf(" %d NaN/Infinity value(s) in the result were written as null.", count)
	}
}

const (
	errOnlySingleReadOnlySelect = "Only a single read-only SELECT statement is allowed."
	errQueryCheckFailed         = "The query could not be checked because of an internal error in the tool. This is not a problem with the SQL."

	msgQueryRefused     = "query_data_with_sql: query refused"
	msgQueryCheckFailed = "query_data_with_sql: query could not be checked"

	stageReserveConnection = "reserve_connection"
	stageReadLoadedTables  = "read_loaded_tables"
	stageCheckQuery        = "check_query"
	stageConfineConnection = "confine_connection"
)

func unadmittedQueryResult(ctx context.Context, logger *slog.Logger, conn *sql.Conn, checkErr error, sqlQuery string, inputFiles map[string]any) *tool.Result {
	if refusal, ok := errors.AsType[*sqlitegate.Refusal](checkErr); ok {
		logger.WarnContext(ctx, msgQueryRefused, "queryLen", len(sqlQuery),
			"refusalReason", string(refusal.Reason), "sqliteAction", int(refusal.Action))
		return tool.Error(errOnlySingleReadOnlySelect, tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery))
	}
	if _, ok := errors.AsType[*sqlitegate.CompileError](checkErr); ok {
		return failedQueryResult(ctx, conn, checkErr, inputFiles, sqlQuery)
	}
	if internal, ok := errors.AsType[*sqlitegate.InternalError](checkErr); ok {
		logger.ErrorContext(ctx, msgQueryCheckFailed, "stage", stageCheckQuery, "queryLen", len(sqlQuery),
			"checkStage", string(internal.Stage), "sqliteResultCode", int(internal.ResultCode))
	} else {
		logger.ErrorContext(ctx, msgQueryCheckFailed, "stage", stageCheckQuery, "queryLen", len(sqlQuery),
			"errorType", fmt.Sprintf("%T", checkErr))
	}
	return tool.Error(errQueryCheckFailed)
}

func hostFailureResult(ctx context.Context, logger *slog.Logger, stage string, err error, inputFiles map[string]any) *tool.Result {
	if interruption := taskInterruption(ctx, err); interruption != nil {
		return failedQueryResult(ctx, nil, interruption, inputFiles, "")
	}
	logger.ErrorContext(ctx, msgQueryCheckFailed, "stage", stage, "error", err)
	return tool.Error(errQueryCheckFailed)
}

func taskInterruption(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

const (
	maxColumnsListedPerTable = 40
	maxTableLineBytes        = 600
	maxSchemaHintBytes       = 2000
	maxEchoedErrorBytes      = 500
	minSuggestedNameRunes    = 4
)

var unknownColumnPattern = regexp.MustCompile(`no such column: (\S+)`)

type tableColumns struct {
	table   string
	columns []string
}

func failedQueryResult(ctx context.Context, conn *sql.Conn, queryErr error, inputFiles map[string]any, sqlQuery string) *tool.Result {
	var columns []tableColumns
	if conn != nil {
		if loaded, err := loadedTableColumns(ctx, conn); err == nil {
			columns = loaded
		}
	}
	message := fmt.Sprintf("SQL query failed: %s.", truncateRunes(queryErr.Error(), maxEchoedErrorBytes))
	if suggestion := suggestColumn(queryErr, sqlQuery, columns); suggestion != "" {
		message += " " + suggestion
	}
	message += " " + schemaHint(inputFiles, columns)
	return tool.Error(message, queryFailureCode(queryErr))
}

func truncateRunes(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

func schemaHint(inputFiles map[string]any, columns []tableColumns) string {
	if len(columns) == 0 {
		tableNames := make([]string, 0, len(inputFiles))
		for tableName := range inputFiles {
			tableNames = append(tableNames, tableName)
		}
		slices.Sort(tableNames)
		return "Available tables: " + strings.Join(tableNames, ", ") + "."
	}
	var hint strings.Builder
	hint.WriteString("Tables and columns:")
	for _, entry := range columns {
		line := tableLine(entry)
		if hint.Len()+len(line) > maxSchemaHintBytes {
			hint.WriteString(" ...")
			break
		}
		hint.WriteString(line)
	}
	return hint.String()
}

func tableLine(entry tableColumns) string {
	var line strings.Builder
	line.WriteString(" " + truncateRunes(entry.table, maxTableLineBytes) + "(")
	listed := 0
	for i, column := range entry.columns {
		piece := column
		if i > 0 {
			piece = ", " + column
		}
		if listed == maxColumnsListedPerTable || line.Len()+len(piece) > maxTableLineBytes {
			break
		}
		line.WriteString(piece)
		listed++
	}
	if extra := len(entry.columns) - listed; extra > 0 {
		if listed > 0 {
			line.WriteString(", ")
		}
		fmt.Fprintf(&line, "and %d more", extra)
	}
	line.WriteString(")")
	return line.String()
}

func suggestColumn(queryErr error, sqlQuery string, columns []tableColumns) string {
	match := unknownColumnPattern.FindStringSubmatch(queryErr.Error())
	if match == nil {
		return ""
	}
	unknown := strings.Trim(match[1], "\"`'[]")
	candidates := []string{unknown}
	if qualifier, suffix, dotted := strings.Cut(unknown, "."); dotted {
		if isLoadedTable(qualifier, columns) {
			candidates = []string{suffix}
		} else {
			candidates = append(candidates, suffix)
		}
	}
	candidates = slices.DeleteFunc(candidates, func(candidate string) bool {
		return utf8.RuneCountInString(candidate) < minSuggestedNameRunes
	})
	referenced := make([]tableColumns, 0, len(columns))
	others := make([]tableColumns, 0, len(columns))
	for _, entry := range columns {
		if queryMentionsTable(sqlQuery, entry.table) {
			referenced = append(referenced, entry)
		} else {
			others = append(others, entry)
		}
	}
	for _, group := range [][]tableColumns{referenced, others} {
		if best, table := closestColumn(candidates, group); best != "" {
			return fmt.Sprintf("Did you mean %q (table %s)?", best, table)
		}
	}
	return ""
}

func closestColumn(candidates []string, entries []tableColumns) (column, table string) {
	bestDistance := -1
	for _, candidate := range candidates {
		lowered := []rune(strings.ToLower(candidate))
		threshold := len(lowered)/3 + 1
		for _, entry := range entries {
			for _, name := range entry.columns {
				distance := editDistance(lowered, []rune(strings.ToLower(name)))
				if distance <= threshold && (bestDistance < 0 || distance < bestDistance) {
					column, table, bestDistance = name, entry.table, distance
				}
			}
		}
	}
	return column, table
}

func queryMentionsTable(sqlQuery, table string) bool {
	if sqlQuery == "" {
		return false
	}
	pattern := `(?i)(^|[^\w])` + regexp.QuoteMeta(table) + `($|[^\w])`
	mentioned, err := regexp.MatchString(pattern, sqlQuery)
	return err == nil && mentioned
}

func isLoadedTable(name string, columns []tableColumns) bool {
	return slices.ContainsFunc(columns, func(entry tableColumns) bool {
		return strings.EqualFold(entry.table, name)
	})
}

func editDistance(a, b []rune) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

const loadedTableColumnsQuery = `
SELECT m.name, p.name
FROM sqlite_master m, pragma_table_info(m.name) p
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
ORDER BY m.rowid, p.cid`

func loadedTableColumns(ctx context.Context, conn *sql.Conn) ([]tableColumns, error) {
	rows, err := conn.QueryContext(ctx, loadedTableColumnsQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var loaded []tableColumns
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		if len(loaded) == 0 || loaded[len(loaded)-1].table != table {
			loaded = append(loaded, tableColumns{table: table})
		}
		loaded[len(loaded)-1].columns = append(loaded[len(loaded)-1].columns, column)
	}
	return loaded, rows.Err()
}

func loadedTableDefinitions(ctx context.Context, conn *sql.Conn) ([]string, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var definitions []string
	for rows.Next() {
		var definition string
		if err := rows.Scan(&definition); err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, rows.Err()
}

func confineToReadOnlyQueries(ctx context.Context, conn *sql.Conn) error {
	if err := forbidAttachedDatabases(conn); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, "PRAGMA query_only = ON")
	return err
}

func forbidAttachedDatabases(conn *sql.Conn) error {
	if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_ATTACHED, 0); err != nil {
		return fmt.Errorf("set the attached-database limit: %w", err)
	}
	effective, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_ATTACHED, -1)
	if err != nil {
		return fmt.Errorf("read the attached-database limit: %w", err)
	}
	if effective != 0 {
		return fmt.Errorf("attached-database limit is %d, want 0", effective)
	}
	return nil
}

const soleMemoryDBConnection = 1

func openInMemoryQueryDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:?_pragma=temp_store(memory)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(soleMemoryDBConnection)
	db.SetMaxIdleConns(soleMemoryDBConnection)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if err := forbidAttachOnSoleConnection(db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func forbidAttachOnSoleConnection(db *sql.DB) error {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	return errors.Join(forbidAttachedDatabases(conn), conn.Close())
}

func normalizeTableKeys(inputFiles map[string]any) map[string]any {
	out := make(map[string]any, len(inputFiles))
	for name, v := range inputFiles {
		out[normalizeTableName(name)] = v
	}
	return out
}

func normalizeTableName(name string) string {
	if len(name) < 2 {
		return name
	}
	first, last := name[0], name[len(name)-1]
	if (first == '"' && last == '"') ||
		(first == '`' && last == '`') ||
		(first == '\'' && last == '\'') ||
		(first == '[' && last == ']') {
		return name[1 : len(name)-1]
	}
	return name
}

func parseFilenameVersion(s string) (string, *int) { return artifact.SplitVersionRef(s) }

var sqliteMagicHeader = []byte("SQLite format 3\x00")

func isSQLiteArtifact(mimeType, filename string, content []byte) bool {
	if mimeType == sqliteDBMIMEType || mimeType == "application/x-sqlite3" {
		return true
	}
	if len(content) >= len(sqliteMagicHeader) && bytes.Equal(content[:len(sqliteMagicHeader)], sqliteMagicHeader) {
		return true
	}
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".db")
}

type sqliteSerializer interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

func withSQLiteSerializer(ctx context.Context, db *sql.DB, fn func(sqliteSerializer) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite connection: %w", err)
	}
	rawErr := conn.Raw(func(driverConn any) error {
		serializer, ok := driverConn.(sqliteSerializer)
		if !ok {
			return fmt.Errorf("sqlite driver connection %T does not support serialization", driverConn)
		}
		return fn(serializer)
	})
	if closeErr := conn.Close(); closeErr != nil {
		return errors.Join(rawErr, fmt.Errorf("release sqlite connection: %w", closeErr))
	}
	return rawErr
}

const (
	sqliteWriteVersionOffset = 18
	sqliteReadVersionOffset  = 19
	sqliteRollbackJournalFmt = 1
	sqliteWALFmt             = 2
)

func withRollbackJournalHeader(content []byte) []byte {
	if len(content) <= sqliteReadVersionOffset || content[sqliteReadVersionOffset] != sqliteWALFmt {
		return content
	}
	patched := slices.Clone(content)
	patched[sqliteReadVersionOffset] = sqliteRollbackJournalFmt
	if patched[sqliteWriteVersionOffset] == sqliteWALFmt {
		patched[sqliteWriteVersionOffset] = sqliteRollbackJournalFmt
	}
	return patched
}

func loadRowsFromSQLiteArtifact(ctx context.Context, content []byte) ([]map[string]any, string, error) {
	if len(content) == 0 {
		return nil, "", errors.New("SQLite artifact is empty")
	}

	db, err := openInMemoryQueryDB()
	if err != nil {
		return nil, "", fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = db.Close() }()

	deserialize := func(s sqliteSerializer) error { return s.Deserialize(withRollbackJournalHeader(content)) }
	if err = withSQLiteSerializer(ctx, db, deserialize); err != nil {
		return nil, "", fmt.Errorf("load sqlite artifact: %w", err)
	}

	tableRows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY rowid`)
	if err != nil {
		return nil, "", fmt.Errorf("list tables: %w", err)
	}
	var tableNames []string
	for tableRows.Next() {
		var name string
		if scanErr := tableRows.Scan(&name); scanErr != nil {
			_ = tableRows.Close()
			return nil, "", fmt.Errorf("scan table name: %w", scanErr)
		}
		tableNames = append(tableNames, name)
	}
	if closeErr := tableRows.Close(); closeErr != nil {
		return nil, "", fmt.Errorf("close table list: %w", closeErr)
	}
	if err := tableRows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate tables: %w", err)
	}
	if len(tableNames) == 0 {
		return nil, "", fmt.Errorf("SQLite artifact contains no tables")
	}

	srcTable := tableNames[0]
	qSrcTable, quoteErr := quoteSQLiteIdent(srcTable)
	if quoteErr != nil {
		return nil, "", fmt.Errorf("table name %q from sqlite artifact: %w", srcTable, quoteErr)
	}
	query := fmt.Sprintf("SELECT * FROM %s", qSrcTable)
	rows, _, queryErr := executeQuery(ctx, db, query)
	if queryErr != nil {
		return nil, "", fmt.Errorf("read table %q: %w", srcTable, queryErr)
	}
	return rows, srcTable, nil
}

func emptyTableCode(hasTransform bool) tool.ResultOption {
	if hasTransform {
		return tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)
	}
	return tool.WithErrorCode("")
}

func loadInputTable(ctx context.Context, facade *tool.Facade, tableName, filename string, maxCols int, transform string, hasTransform bool) (rows []map[string]any, emptyCols []string, sourceInfo map[string]any, errResult *tool.Result) {
	artFilename, version := parseFilenameVersion(filename)

	content, mimeType, loadErr := facade.LoadArtifact(ctx, artFilename, version)
	if loadErr != nil {
		return nil, nil, nil, tool.Error(fmt.Sprintf("Failed to load artifact '%s': %s", filename, loadErr))
	}
	if content == nil {
		return nil, nil, nil, missingArtifactError(ctx, facade, artFilename, version)
	}
	mimeType = artifact.ResolveDataMIME(artFilename, mimeType, content)

	var sourceSQLiteTable string
	switch {
	case isSQLiteArtifact(mimeType, artFilename, content):
		if hasTransform {
			return nil, nil, nil, tool.Error(fmt.Sprintf("jmespath_transforms cannot be applied to table %q: artifact %q is a SQLite database; JMESPath transforms apply only to CSV, JSON, or YAML artifacts", tableName, filename), tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery))
		}
		sqliteRows, srcTable, sqliteErr := loadRowsFromSQLiteArtifact(ctx, content)
		if sqliteErr != nil {
			return nil, nil, nil, tool.Error(fmt.Sprintf("Failed to read SQLite artifact '%s': %s", filename, sqliteErr))
		}
		rows = sqliteRows
		sourceSQLiteTable = srcTable
	case hasTransform:
		parsedRows, parseErr := parseDataToRowsWithTransform(content, mimeType, maxCols, transform, tableName)
		if parseErr != nil {
			return nil, nil, nil, tool.Error(parseErr.Error(), tableShapeErrorCode(parseErr))
		}
		rows = parsedRows
	default:
		parsedRows, parseErr := parseDataToRows(content, mimeType, maxCols)
		if parseErr != nil {
			return nil, nil, nil, tool.Error(fmt.Sprintf("Failed to parse '%s': %s", filename, parseErr), tableShapeErrorCode(parseErr))
		}
		rows = parsedRows
	}
	if len(rows) == 0 {
		if hasTransform {
			if cols := jmespathProjectionColumns(transform); len(cols) > 0 {
				return nil, cols, emptySourceInfo(tableName, artFilename, version), nil
			}
		}
		return nil, nil, nil, tool.Error(fmt.Sprintf("No data rows found in '%s'", filename), emptyTableCode(hasTransform))
	}

	sourceInfo = map[string]any{
		"table_name": tableName,
		"filename":   artFilename,
		"row_count":  len(rows),
	}
	if version != nil {
		sourceInfo["version"] = *version
	}
	if sourceSQLiteTable != "" {
		sourceInfo["source_sqlite_table"] = sourceSQLiteTable
	}
	return rows, nil, sourceInfo, nil
}

func emptySourceInfo(tableName, artFilename string, version *int) map[string]any {
	info := map[string]any{
		"table_name": tableName,
		"filename":   artFilename,
		"row_count":  0,
	}
	if version != nil {
		info["version"] = *version
	}
	return info
}

type tableShapeError struct{ err error }

func (e *tableShapeError) Error() string { return e.err.Error() }

func (e *tableShapeError) Unwrap() error { return e.err }

func tableShapeErrorf(format string, args ...any) error {
	return &tableShapeError{err: fmt.Errorf(format, args...)}
}

func tableShapeErrorCode(err error) tool.ResultOption {
	if _, ok := errors.AsType[*tableShapeError](err); ok {
		return tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)
	}
	return tool.WithErrorCode("")
}

func queryFailureCode(err error) tool.ResultOption {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassTimeout))
	case errors.Is(err, context.Canceled):
		return tool.WithErrorCode("")
	}
	return tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery)
}

func parseDataToRows(content []byte, mimeType string, maxCols int) ([]map[string]any, error) {
	switch {
	case artifact.IsCSVMIME(mimeType):
		return parseCSVToRows(content)
	case artifact.IsJSONMIME(mimeType):
		return parseJSONToRows(content, maxCols)
	case artifact.IsYAMLMIME(mimeType):
		return parseYAMLToRows(content, maxCols)
	default:
		return nil, tableShapeErrorf("unsupported MIME type '%s'; expected CSV, JSON, YAML, or SQLite", mimeType)
	}
}

func jmespathProjectionColumns(expr string) []string {
	body, ok := trailingBraceBody(strings.TrimSpace(expr))
	if !ok {
		return nil
	}
	var cols []string
	for _, seg := range splitTopLevel(body, ',') {
		kv := splitTopLevel(seg, ':')
		if len(kv) < 2 {
			return nil
		}
		key := unquoteJMESPathKey(strings.TrimSpace(kv[0]))
		if key == "" {
			return nil
		}
		cols = append(cols, key)
	}
	return cols
}

func trailingBraceBody(expr string) (string, bool) {
	if !strings.HasSuffix(expr, "}") {
		return "", false
	}
	depth, openAt, quote := 0, -1, rune(0)
	for i, r := range expr {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '{':
			if depth == 0 {
				openAt = i
			}
			depth++
		case '}':
			depth--
		}
	}
	if depth != 0 || openAt < 0 {
		return "", false
	}
	return expr[openAt+1 : len(expr)-1], true
}

func splitTopLevel(s string, sep rune) []string {
	var parts []string
	depth, quote, start := 0, rune(0), 0
	for i, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + len(string(sep))
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func unquoteJMESPathKey(k string) string {
	if len(k) >= 2 {
		switch k[0] {
		case '"', '\'', '`':
			if k[len(k)-1] == k[0] {
				return k[1 : len(k)-1]
			}
		}
	}
	return k
}

func parseJMESPathTransforms(raw any, inputFiles map[string]any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("jmespath_transforms must be an object mapping table names to JMESPath expression strings")
	}
	transforms := make(map[string]string, len(m))
	for table, exprRaw := range m {
		table = normalizeTableName(table)
		expr, ok := exprRaw.(string)
		if !ok {
			return nil, fmt.Errorf("jmespath_transforms for table %q must be a JMESPath expression string", table)
		}
		if _, present := inputFiles[table]; !present {
			return nil, fmt.Errorf("jmespath_transforms references table %q, which is not present in input_files", table)
		}
		transforms[table] = expr
	}
	return transforms, nil
}

func parseDataToRowsWithTransform(content []byte, mimeType string, maxCols int, transform, tableName string) ([]map[string]any, error) {
	parsed, parseErr := parseDataToGeneric(content, mimeType)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse table %q before applying jmespath_transforms: %w", tableName, parseErr)
	}

	if len(transform) > maxJMESPathExpressionLen {
		return nil, tableShapeErrorf("jmespath_transforms for table %q is invalid: expression too long (%d bytes; max %d)", tableName, len(transform), maxJMESPathExpressionLen)
	}
	if depth := maxJMESPathDepth(transform); depth > maxJMESPathExpressionDepth {
		return nil, tableShapeErrorf("jmespath_transforms for table %q is invalid: expression too deeply nested (%d levels; max %d)", tableName, depth, maxJMESPathExpressionDepth)
	}
	compiled, compileErr := jmespath.Compile(transform)
	if compileErr != nil {
		return nil, tableShapeErrorf("jmespath_transforms for table %q is invalid: %w", tableName, compileErr)
	}
	result, searchErr := safeJMESPathSearch(compiled, parsed)
	if searchErr != nil {
		return nil, tableShapeErrorf("jmespath_transforms for table %q failed during evaluation: %w", tableName, searchErr)
	}

	rows, convErr := convertToRows(result, maxCols)
	if convErr != nil {
		return nil, tableShapeErrorf("jmespath_transforms output for table %q is not tabular: %w", tableName, convErr)
	}
	return rows, nil
}

func parseDataToGeneric(content []byte, mimeType string) (any, error) {
	switch {
	case artifact.IsCSVMIME(mimeType):
		return parseCSVForJMESPath(content)
	case artifact.IsJSONMIME(mimeType):
		var raw any
		if err := json.Unmarshal(content, &raw); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return raw, nil
	case artifact.IsYAMLMIME(mimeType):
		var raw any
		if err := yaml.Unmarshal(content, &raw); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
		return NormalizeYAMLForJMESPath(raw), nil
	default:
		return nil, tableShapeErrorf("unsupported MIME type '%s'; expected CSV, JSON, or YAML", mimeType)
	}
}

func parseCSVToRows(data []byte) ([]map[string]any, error) {
	cleanData := tryDecodeCSV(data)

	r := csv.NewReader(bytes.NewReader(cleanData))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	var rows []map[string]any
	for {
		record, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed to read CSV row: %w", readErr)
		}
		row := make(map[string]any, len(headers))
		for i, h := range headers {
			if i < len(record) && record[i] != "" {
				row[h] = record[i]
			} else {
				row[h] = nil
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func tryDecodeCSV(data []byte) []byte {
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data = data[3:]
	}
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) {
		data = data[2:]
	}
	if bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		data = data[2:]
	}
	return data
}

func parseJSONToRows(data []byte, maxCols int) ([]map[string]any, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return convertToRows(raw, maxCols)
}

func parseYAMLToRows(data []byte, maxCols int) ([]map[string]any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	raw = normalizeYAML(raw)
	return convertToRows(raw, maxCols)
}

func normalizeYAML(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, vv := range val {
			val[k] = normalizeYAML(vv)
		}
		return val
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[fmt.Sprintf("%v", k)] = normalizeYAML(vv)
		}
		return out
	case []any:
		for i, vv := range val {
			val[i] = normalizeYAML(vv)
		}
		return val
	default:
		return v
	}
}

func convertToRows(raw any, maxCols int) ([]map[string]any, error) {
	switch data := raw.(type) {
	case []any:
		return convertListToRows(data, maxCols)
	case map[string]any:
		if isColumnar(data) {
			return convertColumnarToRows(data, maxCols)
		}
		if len(data) > maxCols {
			return nil, tableShapeErrorf("the JSON object has %d properties, exceeding the maximum of %d columns for a SQL table. Use transform_data_with_jmespath to select only the fields you need", len(data), maxCols)
		}
		return []map[string]any{flattenMap(data)}, nil
	default:
		return nil, tableShapeErrorf("expected a JSON array of objects or a JSON object at the top level, but found a scalar value. query_data_with_sql needs tabular data; use transform_data_with_jmespath to extract the array from within the JSON structure first")
	}
}

func convertListToRows(items []any, maxCols int) ([]map[string]any, error) {
	rows := make([]map[string]any, 0, len(items))
	propCount := make(map[string]int)
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, tableShapeErrorf("expected a JSON array of objects (e.g., [{...}, {...}]), but found a non-object element (such as a string, number, or array) at position in the array. Ensure the data is a flat list of objects")
		}
		flat := flattenMap(obj)
		if len(flat) > maxCols {
			return nil, tableShapeErrorf("row has %d columns after flattening, exceeding the maximum of %d. The data has too many fields to load as a SQL table. Use transform_data_with_jmespath to select only the fields you need before loading into query_data_with_sql", len(flat), maxCols)
		}
		for k := range flat {
			propCount[k]++
		}
		rows = append(rows, flat)
	}

	if len(rows) > 0 {
		minFreq := defaultMinPropertyFreqPct
		threshold := float64(len(rows)) * float64(minFreq) / 100.0
		rareProps := 0
		for _, count := range propCount {
			if float64(count) < threshold {
				rareProps++
			}
		}
		if rareProps > 1 {
			return nil, tableShapeErrorf("data structure is too inconsistent for a SQL table: %d properties appear in fewer than %d%% of rows, suggesting the objects have different shapes. Use transform_data_with_jmespath to normalize the data into a consistent structure first, then load the result into query_data_with_sql", rareProps, minFreq)
		}
	}

	return rows, nil
}

func isColumnar(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	expectedLen := -1
	for _, v := range m {
		arr, ok := v.([]any)
		if !ok {
			return false
		}
		if expectedLen == -1 {
			expectedLen = len(arr)
		} else if len(arr) != expectedLen {
			return false
		}
	}
	return expectedLen >= 0
}

func convertColumnarToRows(data map[string]any, maxCols int) ([]map[string]any, error) {
	if len(data) > maxCols {
		return nil, tableShapeErrorf("columnar data has %d columns, exceeding max of %d", len(data), maxCols)
	}

	var rowCount int
	cols := make([]string, 0, len(data))
	arrays := make(map[string][]any, len(data))
	for k, v := range data {
		arr := v.([]any)
		cols = append(cols, k)
		arrays[k] = arr
		rowCount = len(arr)
	}

	rows := make([]map[string]any, rowCount)
	for i := range rows {
		row := make(map[string]any, len(cols))
		for _, col := range cols {
			row[col] = arrays[col][i]
		}
		rows[i] = row
	}
	return rows, nil
}

func flattenMap(m map[string]any) map[string]any {
	flat := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			for nk, nv := range nested {
				flat[k+"."+nk] = nv
			}
		} else {
			flat[k] = v
		}
	}
	return flat
}

func createTableFromRows(ctx context.Context, db *sql.DB, tableName string, rows []map[string]any, emptyCols []string) error {
	if len(rows) == 0 && len(emptyCols) == 0 {
		return fmt.Errorf("no rows to insert")
	}

	colSet := make(map[string]bool)
	for _, row := range rows {
		for k := range row {
			colSet[k] = true
		}
	}
	columns := make([]string, 0, len(colSet))
	for k := range colSet {
		columns = append(columns, k)
	}
	slices.Sort(columns)
	if len(rows) == 0 {
		columns = append(columns[:0], emptyCols...)
	}

	qTable, err := quoteSQLiteIdent(tableName)
	if err != nil {
		return fmt.Errorf("table %q: %w", tableName, err)
	}
	var createSQL strings.Builder
	fmt.Fprintf(&createSQL, "CREATE TABLE %s (", qTable)
	for i, col := range columns {
		if i > 0 {
			createSQL.WriteString(", ")
		}
		qCol, qErr := quoteSQLiteIdent(col)
		if qErr != nil {
			return fmt.Errorf("column %q: %w", col, qErr)
		}
		fmt.Fprintf(&createSQL, "%s NUMERIC", qCol)
	}
	createSQL.WriteString(")")

	if _, execErr := db.Exec(createSQL.String()); execErr != nil {
		return fmt.Errorf("CREATE TABLE: %w", execErr)
	}

	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	qCols, err := quoteColumns(columns)
	if err != nil {
		return fmt.Errorf("quote columns: %w", err)
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		qTable,
		qCols,
		strings.Join(placeholders, ", "))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare INSERT: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for rowIdx, row := range rows {
		vals := make([]any, len(columns))
		for i, col := range columns {
			v, ok := row[col]
			if !ok || v == nil {
				vals[i] = nil
			} else {
				switch v.(type) {
				case []any:
					return tableShapeErrorf("column '%s' in table '%s' contains an array (row %d). "+
						"SQL tables can only store scalar values (strings, numbers, booleans). "+
						"Use transform_data_with_jmespath to flatten or extract the nested data first, "+
						"then load the flattened result into query_data_with_sql. "+
						"Example: use a JMESPath expression like 'items[].{sku: sku, stock: stock}' to extract a flat list",
						col, tableName, rowIdx)
				case map[string]any:
					return tableShapeErrorf("column '%s' in table '%s' contains a nested object (row %d). "+
						"SQL tables can only store scalar values (strings, numbers, booleans). "+
						"Use transform_data_with_jmespath to flatten or extract the nested data first, "+
						"then load the flattened result into query_data_with_sql",
						col, tableName, rowIdx)
				default:
					vals[i] = v
				}
			}
		}
		if _, err := stmt.Exec(vals...); err != nil {
			return fmt.Errorf("failed to insert row %d into table '%s': %w", rowIdx, tableName, err)
		}
	}

	return tx.Commit()
}

func quoteSQLiteIdent(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty SQL identifier")
	}
	if strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("SQL identifier contains NULL byte")
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
}

func quoteColumns(cols []string) (string, error) {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		q, err := quoteSQLiteIdent(c)
		if err != nil {
			return "", fmt.Errorf("column %d: %w", i, err)
		}
		quoted[i] = q
	}
	return strings.Join(quoted, ", "), nil
}

func executeQuery(ctx context.Context, db *sql.DB, query string) ([]map[string]any, []string, error) {
	sqlRows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = sqlRows.Close() }()

	columns, err := sqlRows.Columns()
	if err != nil {
		return nil, nil, err
	}

	var results []map[string]any
	for sqlRows.Next() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if scanErr := sqlRows.Scan(ptrs...); scanErr != nil {
			return nil, nil, scanErr
		}

		row := make(map[string]any, len(columns))
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			row[col] = val
		}
		results = append(results, row)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, nil, err
	}
	return results, columns, nil
}

type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

var errResultTooLarge = errors.New("result too large")

func streamQueryResult(ctx context.Context, db rowQuerier, query, format string, maxBytes, previewRows, previewBytes int) (output []byte, preview any, rowCount int, truncated bool, nonFinite int, err error) {
	sqlRows, qErr := db.QueryContext(ctx, query)
	if qErr != nil {
		return nil, nil, 0, false, 0, qErr
	}
	defer func() { _ = sqlRows.Close() }()

	columns, cErr := sqlRows.Columns()
	if cErr != nil {
		return nil, nil, 0, false, 0, cErr
	}

	var buf bytes.Buffer
	var csvw *csv.Writer
	if format == "json" {
		buf.WriteString("[\n")
	} else {
		csvw = csv.NewWriter(&buf)
		if wErr := csvw.Write(columns); wErr != nil {
			return nil, nil, 0, false, 0, wErr
		}
	}

	values := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range values {
		ptrs[i] = &values[i]
	}
	rec := make([]string, len(columns))
	previewAcc := newPreviewAccumulator(previewRows, previewBytes)

	for sqlRows.Next() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, 0, false, 0, ctxErr
		}
		if scanErr := sqlRows.Scan(ptrs...); scanErr != nil {
			return nil, nil, 0, false, 0, scanErr
		}
		normalizeRowValues(values)

		if format == "json" {
			_, n := NullifyNonFinite(values)
			nonFinite += n
			if wErr := writeJSONRow(&buf, columns, values, rowCount == 0); wErr != nil {
				return nil, nil, 0, false, 0, wErr
			}
		} else {
			if wErr := writeCSVRow(csvw, values, rec); wErr != nil {
				return nil, nil, 0, false, 0, wErr
			}
			_, n := NullifyNonFinite(values)
			nonFinite += n
		}

		if !previewAcc.full() {
			previewAcc.add(rowToMap(columns, values))
		}
		rowCount++

		if buf.Len() >= maxBytes {
			return nil, nil, 0, false, 0, fmt.Errorf("%w: result exceeds %d bytes after %d rows; add a LIMIT clause, a WHERE filter, or an aggregation to reduce the result size", errResultTooLarge, maxBytes, rowCount)
		}
	}
	if rErr := sqlRows.Err(); rErr != nil {
		return nil, nil, 0, false, 0, rErr
	}

	if format == "json" {
		buf.WriteString("\n]")
	} else {
		csvw.Flush()
		if fErr := csvw.Error(); fErr != nil {
			return nil, nil, 0, false, 0, fErr
		}
	}

	prev, prevTrunc := truncateResultRows(previewAcc.rows, columns, previewRows, previewBytes)
	truncated = prevTrunc || rowCount > len(previewAcc.rows)
	return buf.Bytes(), prev, rowCount, truncated, nonFinite, nil
}

type previewAccumulator struct {
	rows     []map[string]any
	maxRows  int
	maxBytes int
	size     int
	stopped  bool
}

func newPreviewAccumulator(maxRows, maxBytes int) *previewAccumulator {
	return &previewAccumulator{
		rows:     make([]map[string]any, 0, min(maxRows, defaultMaxResultPreviewRows)),
		maxRows:  maxRows,
		maxBytes: maxBytes,
		size:     len("[]"),
	}
}

func (p *previewAccumulator) full() bool {
	return p.stopped || len(p.rows) >= p.maxRows || (len(p.rows) > 0 && p.size > p.maxBytes)
}

func (p *previewAccumulator) add(row map[string]any) {
	if p.full() {
		return
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		p.stopped = true
	} else {
		p.size += len(encoded) + len(",")
	}
	p.rows = append(p.rows, row)
}

func normalizeRowValues(values []any) {
	for i := range values {
		if b, ok := values[i].([]byte); ok {
			values[i] = string(b)
		}
	}
}

func writeJSONRow(buf *bytes.Buffer, columns []string, values []any, first bool) error {
	if !first {
		buf.WriteString(",\n")
	}
	buf.WriteString("  {\n")
	for j, col := range columns {
		if j > 0 {
			buf.WriteString(",\n")
		}
		keyJSON, kErr := json.Marshal(col)
		if kErr != nil {
			return kErr
		}
		valJSON, vErr := json.Marshal(values[j])
		if vErr != nil {
			return vErr
		}
		fmt.Fprintf(buf, "    %s:%s", keyJSON, valJSON)
	}
	buf.WriteString("\n  }")
	return nil
}

func writeCSVRow(csvw *csv.Writer, values []any, rec []string) error {
	for i := range rec {
		if values[i] == nil {
			rec[i] = ""
		} else {
			rec[i] = fmt.Sprintf("%v", values[i])
		}
	}
	if err := csvw.Write(rec); err != nil {
		return err
	}
	csvw.Flush()
	return csvw.Error()
}

func rowToMap(columns []string, values []any) map[string]any {
	row := make(map[string]any, len(columns))
	for i, col := range columns {
		row[col] = values[i]
	}
	return row
}

func formatResultAsJSON(rows []map[string]any, columns []string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("[\n")
	for i, row := range rows {
		if i > 0 {
			buf.WriteString(",\n")
		}
		buf.WriteString("  {\n")
		for j, col := range columns {
			if j > 0 {
				buf.WriteString(",\n")
			}
			keyJSON, _ := json.Marshal(col)
			valJSON, err := json.Marshal(row[col])
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&buf, "    %s:%s", keyJSON, valJSON)
		}
		buf.WriteString("\n  }")
	}
	buf.WriteString("\n]")
	return buf.Bytes(), nil
}

func truncateResultRows(rows []map[string]any, columns []string, maxRows, maxBytes int) (any, bool) {
	if len(rows) == 0 {
		return []any{}, false
	}
	return truncatePreviewList(rows, maxRows, maxBytes)
}

var (
	aggFuncPattern       = regexp.MustCompile(`(?i)\b(SUM|AVG|COUNT)\s*\(`)
	aggFuncDistinctCheck = regexp.MustCompile(`(?i)^\s*DISTINCT\b`)
	aliasDotColumnPat    = regexp.MustCompile(`(?i)\b([a-zA-Z_]\w*)\.\w+`)
	fromAliasPattern     = regexp.MustCompile(`(?i)\bFROM\s+[\w.]+\s+(?:AS\s+)?(\w+)\b`)
	joinKeywordPattern   = regexp.MustCompile(`(?i)\bJOIN\b`)
	groupByPattern       = regexp.MustCompile(`(?i)\bGROUP\s+BY\b`)
	cteNamePattern       = regexp.MustCompile(`(?i)\b(\w+)\s+AS\s*\(`)
)

var sqlKeywords = map[string]bool{
	"left": true, "right": true, "inner": true, "outer": true, "cross": true,
	"full": true, "natural": true, "join": true, "on": true, "where": true,
	"group": true, "order": true, "having": true, "limit": true, "union": true,
	"except": true, "intersect": true, "as": true, "select": true, "from": true, "set": true,
}

func hasBareAggregate(block string) bool {
	locs := aggFuncPattern.FindAllStringIndex(block, -1)
	for _, loc := range locs {
		after := block[loc[1]:]
		if !aggFuncDistinctCheck.MatchString(after) {
			return true
		}
	}
	return false
}

const errJoinFanout = `Query rejected: Using JOIN with aggregate functions (SUM/AVG/COUNT) and GROUP BY ` +
	`will produce incorrect results due to row duplication. When a JOIN creates multiple ` +
	`rows per key (one-to-many), SUM/AVG/COUNT values from the 'one' side get inflated. ` +
	`You MUST rewrite the query using CTEs or subqueries to aggregate each table ` +
	`independently before joining. For example:
  WITH table1_agg AS (SELECT key, SUM(value) as total FROM table1 GROUP BY key),
       table2_agg AS (SELECT key, SUM(amount) as total FROM table2 GROUP BY key)
  SELECT t1.key, t1.total, t2.total
  FROM table1_agg t1 LEFT JOIN table2_agg t2 ON t1.key = t2.key`

func detectJoinAggregationRisk(query string) string {
	upper := strings.ToUpper(query)

	if !strings.Contains(upper, "GROUP BY") {
		return ""
	}
	if !strings.Contains(upper, "JOIN") {
		return ""
	}
	if !hasBareAggregate(query) {
		return ""
	}

	ctes, mainQuery := extractCTEDefinitions(query)

	if len(ctes) == 0 {
		return errJoinFanout
	}

	joinCTEs := make(map[string]bool)
	for _, cte := range ctes {
		if joinKeywordPattern.MatchString(cte.body) {
			joinCTEs[strings.ToLower(cte.name)] = true
		}
	}

	for _, cte := range ctes {
		if !groupByPattern.MatchString(cte.body) || !hasBareAggregate(cte.body) {
			continue
		}
		if joinKeywordPattern.MatchString(cte.body) {
			aggAliases := extractAggregateAliases(cte.body)
			if len(aggAliases) > 1 {
				return errJoinFanout
			}
			if fromAlias := extractFromAlias(cte.body); fromAlias != "" {
				if aggAliases[fromAlias] {
					return errJoinFanout
				}
			}
		} else {
			fromAlias := extractFromAlias(cte.body)
			if fromAlias != "" && joinCTEs[fromAlias] {
				return errJoinFanout
			}
		}
	}

	if joinKeywordPattern.MatchString(mainQuery) &&
		groupByPattern.MatchString(mainQuery) &&
		hasBareAggregate(mainQuery) {
		return errJoinFanout
	}

	return ""
}

type cteDefinition struct {
	name string
	body string
}

func extractCTEDefinitions(query string) ([]cteDefinition, string) {
	matches := cteNamePattern.FindAllStringSubmatchIndex(query, -1)
	if len(matches) == 0 {
		return nil, query
	}

	var ctes []cteDefinition
	var lastEnd int

	for _, match := range matches {
		name := query[match[2]:match[3]]
		parenStart := strings.Index(query[match[0]:], "(")
		if parenStart < 0 {
			continue
		}
		parenStart += match[0]

		depth := 0
		bodyEnd := -1
		for i := parenStart; i < len(query); i++ {
			if query[i] == '(' {
				depth++
			} else if query[i] == ')' {
				depth--
				if depth == 0 {
					bodyEnd = i
					break
				}
			}
		}
		if bodyEnd < 0 {
			continue
		}

		body := query[parenStart+1 : bodyEnd]
		ctes = append(ctes, cteDefinition{name: name, body: body})
		lastEnd = bodyEnd + 1
	}

	mainQuery := query
	if lastEnd > 0 && lastEnd < len(query) {
		mainQuery = query[lastEnd:]
	}
	return ctes, mainQuery
}

func extractFromAlias(block string) string {
	match := fromAliasPattern.FindStringSubmatch(block)
	if match == nil {
		return ""
	}
	alias := strings.ToLower(match[1])
	if sqlKeywords[alias] {
		return ""
	}
	return alias
}

func extractAggregateAliases(block string) map[string]bool {
	aliases := make(map[string]bool)
	aggMatches := aggFuncPattern.FindAllStringIndex(block, -1)
	for _, loc := range aggMatches {
		start := loc[1]
		after := block[start:]
		if aggFuncDistinctCheck.MatchString(after) {
			continue
		}
		depth := 1
		end := start
		for i := start; i < len(block); i++ {
			if block[i] == '(' {
				depth++
			} else if block[i] == ')' {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		inner := block[start:end]
		for _, m := range aliasDotColumnPat.FindAllStringSubmatch(inner, -1) {
			alias := strings.ToLower(m[1])
			if !sqlKeywords[alias] {
				aliases[alias] = true
			}
		}
	}
	return aliases
}
