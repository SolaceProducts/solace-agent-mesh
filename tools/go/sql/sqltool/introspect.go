// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"
)

const introspectWorkers = 5

type IntrospectConfig struct {
	MaxEnumCardinality int
	SampleSize         int
}

type tableSchema struct {
	ColumnOrder []string
	Columns     map[string]*columnInfo
	PrimaryKeys []string
	ForeignKeys []string
	RowCount    *int64
}

type columnInfo struct {
	Type     string
	Indexed  bool
	Enum     []string
	Examples []string
}

func introspectSchema(ctx context.Context, db *sql.DB, dialect Dialect, cfg IntrospectConfig) (string, error) {
	if cfg.MaxEnumCardinality <= 0 {
		cfg.MaxEnumCardinality = 100
	}
	if cfg.SampleSize <= 0 {
		cfg.SampleSize = 100
	}

	var schema string
	if dialect == DialectMSSQL {
		schema = "dbo"
	}

	tables, err := listTables(ctx, db, dialect, schema)
	if err != nil {
		return "", fmt.Errorf("list tables: %w", err)
	}

	type enumCandidate struct {
		table  string
		column string
	}

	var mu sync.Mutex
	tableSchemas := make(map[string]*tableSchema, len(tables))
	var deferredEnums []enumCandidate
	var failedTables int

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(introspectWorkers)
	for _, table := range tables {
		g.Go(func() error {
			ts, deferred, err := processTable(gctx, db, dialect, table, schema, cfg)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				slog.Default().With("logger_name", "sql").Warn("introspect: failed to process table", "table", table, "error", err)
				failedTables++
				return nil
			}
			tableSchemas[table] = ts
			for _, col := range deferred {
				deferredEnums = append(deferredEnums, enumCandidate{table: table, column: col})
			}
			return nil
		})
	}
	_ = g.Wait()

	enumG, enumCtx := errgroup.WithContext(ctx)
	enumG.SetLimit(introspectWorkers)
	for _, ec := range deferredEnums {
		enumG.Go(func() error {
			vals, err := fetchDistinctValues(enumCtx, db, dialect, ec.table, ec.column, 50)
			if err != nil {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			if ts, ok := tableSchemas[ec.table]; ok {
				if col, ok := ts.Columns[ec.column]; ok {
					col.Enum = vals
				}
			}
			return nil
		})
	}
	_ = enumG.Wait()

	var buf strings.Builder
	if failedTables > 0 {
		fmt.Fprintf(&buf, "WARNING: %d of %d tables failed to introspect\n", failedTables, len(tables))
	}
	buf.WriteString(renderSchema(tables, tableSchemas))
	return buf.String(), nil
}

const noTables = "(no tables)"

func renderSchema(tables []string, schemas map[string]*tableSchema) string {
	var lines []string
	for _, table := range tables {
		ts, ok := schemas[table]
		if !ok || len(ts.ColumnOrder) == 0 {
			continue
		}
		lines = append(lines, renderTable(table, ts))
	}
	if len(lines) == 0 {
		return noTables + "\n"
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderTable(table string, ts *tableSchema) string {
	references := make(map[string]string, len(ts.ForeignKeys))
	for _, fk := range ts.ForeignKeys {
		if col, target, ok := strings.Cut(fk, " → "); ok {
			references[col] = target
		}
	}
	columns := make([]string, 0, len(ts.ColumnOrder))
	for _, name := range ts.ColumnOrder {
		col := ts.Columns[name]
		text := name + " " + col.Type
		if slices.Contains(ts.PrimaryKeys, name) {
			text += " PK"
		} else if col.Indexed {
			text += " indexed"
		}
		if target, ok := references[name]; ok {
			text += " → " + target
		}
		if len(col.Enum) > 0 {
			text += " one of " + enumValues(col.Enum)
		} else if len(col.Examples) > 0 {
			text += " e.g. " + exampleValues(col.Examples)
		}
		columns = append(columns, text)
	}
	head := table
	if ts.RowCount != nil && *ts.RowCount >= 0 {
		head += fmt.Sprintf(" (~%d rows)", *ts.RowCount)
	}
	return head + ": " + strings.Join(columns, ", ")
}

const sampleValueMaxRunes = 40

func exampleValues(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		if runes := []rune(v); len(runes) > sampleValueMaxRunes {
			v = string(runes[:sampleValueMaxRunes]) + "…"
		}
		quoted[i] = strconv.Quote(v)
	}
	return strings.Join(quoted, "|")
}

func enumValues(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		if utf8.RuneCountInString(v) <= sampleValueMaxRunes {
			quoted = append(quoted, strconv.Quote(v))
		}
	}
	if len(quoted) < len(values) {
		quoted = append(quoted, "…")
	}
	return strings.Join(quoted, "|")
}

func processTable(ctx context.Context, db *sql.DB, dialect Dialect, table, schema string, cfg IntrospectConfig) (*tableSchema, []string, error) {
	columns, err := getColumns(ctx, db, dialect, table, schema)
	if err != nil {
		return nil, nil, err
	}

	pks, _ := getPrimaryKeys(ctx, db, dialect, table, schema)
	fks, _ := getForeignKeys(ctx, db, dialect, table, schema)
	indexedCols, _ := getIndexedColumns(ctx, db, dialect, table, schema)
	rowCount, _ := getApproximateRowCount(ctx, db, dialect, table, schema)

	ts := &tableSchema{
		Columns: make(map[string]*columnInfo),
	}

	for _, c := range columns {
		ts.ColumnOrder = append(ts.ColumnOrder, c.name)
		ts.Columns[c.name] = &columnInfo{Type: c.typeName, Indexed: indexedCols[c.name]}
	}

	if len(pks) > 0 {
		ts.PrimaryKeys = pks
	}
	if len(fks) > 0 {
		ts.ForeignKeys = fks
	}
	if rowCount != nil {
		ts.RowCount = rowCount
	}

	sampleRows, _ := getSampleRows(ctx, db, dialect, table, cfg.SampleSize)
	var deferredEnums []string

	for _, c := range columns {
		if !isStringType(c.typeName) {
			continue
		}

		var sampleValues []string
		uniqueVals := make(map[string]struct{})
		for _, row := range sampleRows {
			v, ok := row[c.name]
			if !ok || v == nil {
				continue
			}
			s := fmt.Sprintf("%v", v)
			if s == "" {
				continue
			}
			sampleValues = append(sampleValues, s)
			uniqueVals[s] = struct{}{}
		}

		cardinality := len(uniqueVals)
		if cardinality <= 1 || cardinality > cfg.MaxEnumCardinality {
			continue
		}

		total := len(sampleValues)
		cardinalityRatio := float64(cardinality) / float64(max(total, 1))
		isLikelyEnum := looksLikeEnumColumn(c.name) || cardinalityRatio < 0.3

		if !isLikelyEnum {
			if cardinality > 2 && cardinality <= 5 {
				ci := ts.Columns[c.name]
				end := min(3, len(sampleValues))
				ci.Examples = append([]string(nil), sampleValues[:end]...)
			}
			continue
		}

		ci := ts.Columns[c.name]
		if cardinality <= 10 {
			ci.Enum = sortedKeys(uniqueVals, 0)
		} else {
			deferredEnums = append(deferredEnums, c.name)
		}
	}

	return ts, deferredEnums, nil
}

type columnMeta struct {
	name     string
	typeName string
	nullable bool
}

const (
	postgresListTablesQuery = `SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p') AND pg_table_is_visible(c.oid)
			AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY c.relname`
	postgresColumnsQuery = `SELECT column_name, data_type, is_nullable FROM information_schema.columns
		WHERE table_name = $1 AND table_schema = (
			SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = $1 AND c.relkind IN ('r', 'p') AND pg_table_is_visible(c.oid))
		ORDER BY ordinal_position`
)

func listTables(ctx context.Context, db *sql.DB, dialect Dialect, schema string) ([]string, error) {
	var query string
	var args []any
	switch dialect {
	case DialectPostgres:
		query = postgresListTablesQuery
	case DialectMySQL:
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name"
	case DialectSQLite:
		query = "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
	case DialectMSSQL:
		query = mssqlListTablesQuery
		args = append(args, schema)
	case DialectOracle:
		query = oracleListTablesQuery
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func getColumns(ctx context.Context, db *sql.DB, dialect Dialect, table, schema string) ([]columnMeta, error) {
	switch dialect {
	case DialectPostgres:
		return getColumnsInfoSchema(ctx, db, table,
			postgresColumnsQuery)
	case DialectMySQL:
		return getColumnsInfoSchema(ctx, db, table,
			"SELECT column_name, column_type, is_nullable FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position")
	case DialectSQLite:
		return getColumnsSQLite(ctx, db, table)
	case DialectMSSQL:
		return getColumnsMSSQL(ctx, db, table, schema)
	case DialectOracle:
		return getColumnsOracle(ctx, db, table)
	}
	return nil, fmt.Errorf("unsupported dialect: %s", dialect)
}

func getColumnsInfoSchema(ctx context.Context, db *sql.DB, table, query string) ([]columnMeta, error) {
	rows, err := db.QueryContext(ctx, query, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []columnMeta
	for rows.Next() {
		var name, typeName, isNullable string
		if err := rows.Scan(&name, &typeName, &isNullable); err != nil {
			return nil, err
		}
		cols = append(cols, columnMeta{
			name:     name,
			typeName: strings.ToUpper(typeName),
			nullable: strings.EqualFold(isNullable, "YES"),
		})
	}
	return cols, rows.Err()
}

func getColumnsSQLite(ctx context.Context, db *sql.DB, table string) ([]columnMeta, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteIdentifier(DialectSQLite, table)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []columnMeta
	for rows.Next() {
		var cid int
		var name, typeName string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typeName, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, columnMeta{
			name:     name,
			typeName: strings.ToUpper(typeName),
			nullable: notnull == 0,
		})
	}
	return cols, rows.Err()
}

func getColumnsMSSQL(ctx context.Context, db *sql.DB, table, schema string) ([]columnMeta, error) {
	rows, err := db.QueryContext(ctx, mssqlGetColumnsQuery, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []columnMeta
	for rows.Next() {
		var name, typeName, isNullable string
		if err := rows.Scan(&name, &typeName, &isNullable); err != nil {
			return nil, err
		}
		cols = append(cols, columnMeta{
			name:     name,
			typeName: strings.ToUpper(typeName),
			nullable: strings.EqualFold(isNullable, "YES"),
		})
	}
	return cols, rows.Err()
}

func getColumnsOracle(ctx context.Context, db *sql.DB, table string) ([]columnMeta, error) {
	rows, err := db.QueryContext(ctx, oracleGetColumnsQuery, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cols []columnMeta
	for rows.Next() {
		var name, typeName, nullable string
		if err := rows.Scan(&name, &typeName, &nullable); err != nil {
			return nil, err
		}
		cols = append(cols, columnMeta{
			name:     name,
			typeName: strings.ToUpper(typeName),
			nullable: nullable == "Y",
		})
	}
	return cols, rows.Err()
}

func getPrimaryKeys(ctx context.Context, db *sql.DB, dialect Dialect, table, schema string) ([]string, error) {
	var query string
	switch dialect {
	case DialectPostgres:
		query = `SELECT a.attname
			FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
			WHERE i.indrelid = $1::regclass AND i.indisprimary
			ORDER BY array_position(i.indkey, a.attnum)`
		return scanStringColumn(ctx, db, query, table)
	case DialectMySQL:
		query = `SELECT column_name FROM information_schema.key_column_usage
			WHERE table_schema = DATABASE() AND table_name = ? AND constraint_name = 'PRIMARY'
			ORDER BY ordinal_position`
		return scanStringColumn(ctx, db, query, table)
	case DialectSQLite:
		return getPrimaryKeysSQLite(ctx, db, table)
	case DialectMSSQL:
		return scanStringColumn(ctx, db, mssqlGetPrimaryKeysQuery, schema, table)
	case DialectOracle:
		return scanStringColumn(ctx, db, oracleGetPrimaryKeysQuery, table)
	}
	return nil, nil
}

func getPrimaryKeysSQLite(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteIdentifier(DialectSQLite, table)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var pks []string
	for rows.Next() {
		var cid int
		var name, typeName string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typeName, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		if pk > 0 {
			pks = append(pks, name)
		}
	}
	return pks, rows.Err()
}

func getForeignKeys(ctx context.Context, db *sql.DB, dialect Dialect, table, schema string) ([]string, error) {
	switch dialect {
	case DialectPostgres:
		query := `SELECT kcu.column_name, ccu.table_name, ccu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
				ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			JOIN information_schema.constraint_column_usage ccu
				ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
			WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_name = $1`
		return scanForeignKeys(ctx, db, query, table)
	case DialectMySQL:
		query := `SELECT column_name, referenced_table_name, referenced_column_name
			FROM information_schema.key_column_usage
			WHERE table_schema = DATABASE() AND table_name = ? AND referenced_table_name IS NOT NULL`
		return scanForeignKeys(ctx, db, query, table)
	case DialectSQLite:
		return getForeignKeysSQLite(ctx, db, table)
	case DialectMSSQL:
		return scanForeignKeys(ctx, db, mssqlGetForeignKeysQuery, schema, table)
	case DialectOracle:
		return scanForeignKeys(ctx, db, oracleGetForeignKeysQuery, table)
	}
	return nil, nil
}

func scanForeignKeys(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var fks []string
	for rows.Next() {
		var col, refTable, refCol string
		if err := rows.Scan(&col, &refTable, &refCol); err != nil {
			return nil, err
		}
		fks = append(fks, fmt.Sprintf("%s → %s.%s", col, refTable, refCol))
	}
	return fks, rows.Err()
}

func getForeignKeysSQLite(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA foreign_key_list(%s)", quoteIdentifier(DialectSQLite, table)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var fks []string
	for rows.Next() {
		var id, seq int
		var refTable, from, to string
		var onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		fks = append(fks, fmt.Sprintf("%s → %s.%s", from, refTable, to))
	}
	return fks, rows.Err()
}

func getIndexedColumns(ctx context.Context, db *sql.DB, dialect Dialect, table, schema string) (map[string]bool, error) {
	indexed := make(map[string]bool)
	switch dialect {
	case DialectPostgres:
		query := `SELECT a.attname
			FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
			WHERE i.indrelid = $1::regclass AND NOT i.indisprimary`
		cols, err := scanStringColumn(ctx, db, query, table)
		if err != nil {
			return indexed, err
		}
		for _, c := range cols {
			indexed[c] = true
		}
	case DialectMySQL:
		query := `SELECT DISTINCT column_name FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = ? AND index_name != 'PRIMARY'`
		cols, err := scanStringColumn(ctx, db, query, table)
		if err != nil {
			return indexed, err
		}
		for _, c := range cols {
			indexed[c] = true
		}
	case DialectMSSQL:
		qualifiedTable := schema + "." + table
		cols, err := scanStringColumn(ctx, db, mssqlGetIndexedColumnsQuery, qualifiedTable)
		if err != nil {
			return indexed, err
		}
		for _, c := range cols {
			indexed[c] = true
		}
	case DialectOracle:
		cols, err := scanStringColumn(ctx, db, oracleGetIndexedColumnsQuery, table)
		if err != nil {
			return indexed, err
		}
		for _, c := range cols {
			indexed[c] = true
		}
	case DialectSQLite:
		idxRows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%s)", quoteIdentifier(DialectSQLite, table)))
		if err != nil {
			return indexed, err
		}
		var indexNames []string
		for idxRows.Next() {
			var seq int
			var name, origin string
			var unique, partial int
			if err := idxRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				_ = idxRows.Close()
				return indexed, err
			}
			if origin != "pk" {
				indexNames = append(indexNames, name)
			}
		}
		_ = idxRows.Close()

		for _, idxName := range indexNames {
			colRows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%s)", quoteIdentifier(DialectSQLite, idxName)))
			if err != nil {
				continue
			}
			for colRows.Next() {
				var seqno, cid int
				var colName string
				if err := colRows.Scan(&seqno, &cid, &colName); err != nil {
					break
				}
				indexed[colName] = true
			}
			_ = colRows.Close()
		}
	}
	return indexed, nil
}

func getApproximateRowCount(ctx context.Context, db *sql.DB, dialect Dialect, table, schema string) (*int64, error) {
	var query string
	var args []any
	switch dialect {
	case DialectPostgres:
		query = "SELECT reltuples::bigint FROM pg_class WHERE relname = $1"
		args = []any{table}
	case DialectMySQL:
		query = "SELECT table_rows FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
		args = []any{table}
	case DialectMSSQL:
		qualifiedTable := schema + "." + table
		query = mssqlGetRowCountQuery
		args = []any{qualifiedTable}
	case DialectOracle:
		query = oracleGetRowCountQuery
		args = []any{table}
	case DialectSQLite:
		return nil, nil
	}
	var count sql.NullInt64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return nil, err
	}
	if !count.Valid {
		return nil, nil
	}
	return &count.Int64, nil
}

func getSampleRows(ctx context.Context, db *sql.DB, dialect Dialect, table string, limit int) ([]map[string]any, error) {
	var query string
	switch dialect {
	case DialectMSSQL:
		query = fmt.Sprintf("SELECT TOP %d * FROM %s", limit, quoteIdentifier(dialect, table))
	case DialectOracle:
		query = fmt.Sprintf("SELECT * FROM %s FETCH FIRST %d ROWS ONLY", quoteIdentifier(dialect, table), limit)
	default:
		query = fmt.Sprintf("SELECT * FROM %s LIMIT %d", quoteIdentifier(dialect, table), limit)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRowsToMaps(rows)
}

func fetchDistinctValues(ctx context.Context, db *sql.DB, dialect Dialect, table, column string, limit int) ([]string, error) {
	col := quoteIdentifier(dialect, column)
	tbl := quoteIdentifier(dialect, table)
	var query string
	switch dialect {
	case DialectMSSQL:
		query = fmt.Sprintf("SELECT DISTINCT TOP %d %s FROM %s WHERE %s IS NOT NULL ORDER BY %s",
			limit, col, tbl, col, col)
	case DialectOracle:
		query = fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL ORDER BY %s FETCH FIRST %d ROWS ONLY",
			col, tbl, col, col, limit)
	default:
		query = fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL ORDER BY %s LIMIT %d",
			col, tbl, col, col, limit)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var vals []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return vals, rows.Err()
}

func scanStringColumn(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func scanRowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			v := values[i]
			if b, ok := v.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = v
			}
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

var enumPatterns = []string{
	"status", "state", "type", "kind", "category",
	"level", "priority", "role", "gender", "country",
	"region", "phase", "stage", "mode", "flag", "method",
	"payment", "fulfillment", "delivery", "shipping",
	"visibility", "access", "permission", "scope",
}

func looksLikeEnumColumn(name string) bool {
	lower := strings.ToLower(name)
	for _, pattern := range enumPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func isStringType(typeName string) bool {
	upper := strings.ToUpper(typeName)
	for _, prefix := range []string{
		"VARCHAR", "CHAR", "TEXT", "ENUM", "CHARACTER VARYING",
		"NVARCHAR", "NCHAR", "NTEXT", "VARCHAR2", "NVARCHAR2", "CLOB", "NCLOB", "LONG",
	} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func quoteIdentifier(dialect Dialect, name string) string {
	switch dialect {
	case DialectMySQL:
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	case DialectMSSQL:
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	default:
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
}

func sortedKeys(m map[string]struct{}, limit int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}
