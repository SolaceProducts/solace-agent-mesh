// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sqlitegate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	refusedQueryMessage     = "Only a single read-only SELECT statement is allowed."
	checkFailedQueryMessage = "The query could not be checked because of an internal error in the tool. This is not a problem with the SQL."
	otherDatabaseCanary     = "canary-from-another-database"

	peopleTableHint        = "Available tables: people."
	cancelledQueryMessage  = "SQL query failed: context canceled. " + peopleTableHint
	timedOutQueryMessage   = "SQL query failed: context deadline exceeded. " + peopleTableHint
	wrappedTimedOutMessage = "SQL query failed: reserve the connection: context deadline exceeded. " + peopleTableHint
	checkFailedLogMessage  = "query_data_with_sql: query could not be checked"

	peopleCSV = "name,age,city,profile\n" +
		"Ada,36,London,\"{\"\"team\"\":\"\"core\"\",\"\"tags\"\":[\"\"a\"\",\"\"b\"\"]}\"\n" +
		"Bob,25,Paris,\"{\"\"team\"\":\"\"edge\"\",\"\"tags\"\":[]}\"\n" +
		"Cy,41,London,\n"
	citiesCSV = "city,country\nLondon,UK\nParis,France\n"

	pagesLookalikeTable = "sqlİte_dbpage"
)

func seedOtherDatabase(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "other.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open the other database: %v", err)
	}
	for _, statement := range []string{
		"CREATE TABLE secrets (v TEXT)",
		"INSERT INTO secrets VALUES ('" + otherDatabaseCanary + "')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed the other database: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close the other database: %v", err)
	}
	return path
}

func runQuery(t *testing.T, sqlTool *QueryDataWithSQLTool, toolCtx *tool.Context, peopleTable, query string) *tool.Result {
	t.Helper()
	facade := toolCtx.Facade()
	saveDataToolArtifact(t, t.Context(), facade, "people.csv", []byte(peopleCSV), "text/csv")
	saveDataToolArtifact(t, t.Context(), facade, "cities.csv", []byte(citiesCSV), "text/csv")

	result, err := sqlTool.Execute(t.Context(), map[string]any{
		"input_files":   map[string]any{peopleTable: "people.csv", "cities": "cities.csv"},
		"sql_query":     query,
		"output_format": "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute(%q): %v", query, err)
	}
	return result
}

func runPeopleQuery(t *testing.T, query string) (*tool.Result, *tool.Facade) {
	t.Helper()
	toolCtx, facade := newDataToolContext(t)
	return runQuery(t, &QueryDataWithSQLTool{}, toolCtx, "people", query), facade
}

func directoryEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func capturedLogRecords(t *testing.T, captured *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.Lines(captured.String()) {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		delete(record, "time")
		records = append(records, record)
	}
	return records
}

func TestQueryDataWithSQL_RefusesToReachOutsideItsOwnData(t *testing.T) {
	dir := t.TempDir()
	other := seedOtherDatabase(t, dir)
	created := filepath.Join(dir, "created.db")

	queries := map[string]string{
		"create a database file":          "PRAGMA query_only = OFF; ATTACH DATABASE '" + created + "' AS e; CREATE TABLE e.t (x); SELECT 1",
		"read another database file":      "ATTACH DATABASE '" + other + "' AS e; SELECT v FROM e.secrets",
		"attach alone":                    "ATTACH DATABASE '" + other + "' AS e",
		"copy the database to a file":     "VACUUM INTO '" + created + "'",
		"copy after lifting read-only":    "PRAGMA query_only = OFF; VACUUM INTO '" + created + "'; SELECT 1",
		"load an extension":               "SELECT load_extension('" + created + "')",
		"lift read-only":                  "PRAGMA query_only = OFF",
		"change the journal":              "PRAGMA journal_mode = DELETE",
		"move temporary storage to disk":  "PRAGMA temp_store = FILE",
		"redirect temporary storage":      "PRAGMA temp_store_directory = '" + dir + "'",
		"two statements":                  "SELECT name FROM people; SELECT city FROM cities",
		"write after a select":            "SELECT 1; DELETE FROM people",
		"lift read-only then write":       "PRAGMA query_only = OFF; CREATE TABLE t (x); SELECT 1",
		"temporary table":                 "CREATE TEMP TABLE t AS SELECT * FROM people",
		"transaction control":             "BEGIN",
		"detach":                          "DETACH DATABASE main",
		"raw database pages":              "SELECT * FROM sqlite_dbpage",
		"insert returning rows":           "INSERT INTO people (name) VALUES ('x') RETURNING name",
		"nothing to run":                  "-- nothing",
		"second statement behind comment": "SELECT 1 /* ; */ ; ATTACH DATABASE '" + other + "' AS e",
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			result, _ := runPeopleQuery(t, query)

			if result.Status != "error" {
				t.Fatalf("status = %s (%s), want the query refused", result.Status, result.Message)
			}
			if result.Message != refusedQueryMessage {
				t.Errorf("message = %q, want %q", result.Message, refusedQueryMessage)
			}
			if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
			}
			if strings.Contains(result.Message, dir) || strings.Contains(result.Message, otherDatabaseCanary) {
				t.Errorf("message leaks a path or another database's data: %q", result.Message)
			}
		})
	}

	if entries := directoryEntries(t, dir); len(entries) != 1 || entries[0] != filepath.Base(other) {
		t.Errorf("directory holds %v, want only %s: a refused query touched the disk", entries, filepath.Base(other))
	}
}

func TestQueryDataWithSQL_StillRunsReadOnlyAnalytics(t *testing.T) {
	queries := map[string]struct {
		query    string
		wantRows int
	}{
		"filter and order":         {`SELECT name, age FROM people WHERE city = 'London' ORDER BY age DESC`, 2},
		"join":                     {`SELECT p.name, c.country FROM people p JOIN cities c ON c.city = p.city`, 3},
		"aggregate":                {`SELECT city, count(*) AS n, avg(age) AS mean FROM people GROUP BY city HAVING n >= 1`, 2},
		"cte":                      {`WITH londoners AS (SELECT * FROM people WHERE city = 'London') SELECT count(*) AS n FROM londoners`, 1},
		"aggregate cte then join":  {`WITH per_city AS (SELECT city, sum(age) AS total FROM people GROUP BY city) SELECT c.country, per_city.total FROM per_city JOIN cities c ON c.city = per_city.city`, 2},
		"recursive cte":            {`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n WHERE x < 4) SELECT x FROM n`, 4},
		"window":                   {`SELECT name, row_number() OVER (PARTITION BY city ORDER BY age DESC) AS rn FROM people`, 3},
		"subquery":                 {`SELECT name FROM people WHERE age > (SELECT avg(age) FROM people)`, 2},
		"union":                    {`SELECT city FROM people UNION SELECT city FROM cities`, 2},
		"json functions":           {`SELECT name, json_extract(profile, '$.team') AS team FROM people WHERE profile IS NOT NULL`, 2},
		"json table function":      {`SELECT p.name, j.value AS tag FROM people p, json_each(p.profile, '$.tags') j`, 2},
		"date and math":            {`SELECT strftime('%Y', '2024-03-01') AS y, round(sqrt(age), 1) AS r FROM people`, 3},
		"schema catalog":           {`SELECT name FROM sqlite_master WHERE type = 'table'`, 2},
		"column listing":           {`SELECT name FROM pragma_table_info('people')`, 4},
		"trailing semicolon":       {`SELECT name FROM people;`, 3},
		"trailing comment":         {"SELECT name FROM people; -- all of them\n", 3},
		"semicolon inside literal": {`SELECT name || ';' AS v FROM people WHERE name <> 'a;b'`, 3},
	}
	for name, tc := range queries {
		t.Run(name, func(t *testing.T) {
			result, _ := runPeopleQuery(t, tc.query)
			if result.Status != "success" {
				t.Fatalf("status = %s: %s", result.Status, result.Message)
			}
			if result.Data["result_rows"] != tc.wantRows {
				t.Errorf("result_rows = %v, want %d", result.Data["result_rows"], tc.wantRows)
			}
		})
	}
}

func TestQueryDataWithSQL_TableNameCaseFollowsSQLite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		table    string
		query    string
		wantRows int
	}{
		{"capitalised table queried as written", "People", `SELECT name FROM People`, 3},
		{"capitalised table queried in lower case", "People", `SELECT name FROM people`, 3},
		{"capitalised table queried in upper case", "People", `SELECT count(*) AS n FROM PEOPLE`, 1},
		{"non-ASCII table queried by its exact name", "Équipe", `SELECT name FROM "Équipe"`, 3},
		{"lookalike of a built-in table queried by its exact name", pagesLookalikeTable, `SELECT name FROM "` + pagesLookalikeTable + `"`, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toolCtx, _ := newDataToolContext(t)
			result := runQuery(t, &QueryDataWithSQLTool{}, toolCtx, tc.table, tc.query)
			if result.Status != "success" {
				t.Fatalf("status = %s: %s", result.Status, result.Message)
			}
			if result.Data["result_rows"] != tc.wantRows {
				t.Errorf("result_rows = %v, want %d", result.Data["result_rows"], tc.wantRows)
			}
		})
	}
}

func TestQueryDataWithSQL_LookalikeTableNameDoesNotOpenABuiltInTable(t *testing.T) {
	t.Parallel()
	toolCtx, _ := newDataToolContext(t)

	result := runQuery(t, &QueryDataWithSQLTool{}, toolCtx, pagesLookalikeTable, `SELECT pgno FROM sqlite_dbpage`)

	if result.Status != "error" || result.Message != refusedQueryMessage {
		t.Errorf("result = %s: %q, want the refusal %q", result.Status, result.Message, refusedQueryMessage)
	}
	if result.ErrorCode != "INVALID_QUERY" {
		t.Errorf("ErrorCode = %q, want INVALID_QUERY", result.ErrorCode)
	}
}

func TestQueryDataWithSQL_TextAfterANULByteNeverRuns(t *testing.T) {
	result, _ := runPeopleQuery(t, "SELECT name FROM people\x00; SELECT city FROM cities")
	if result.Status != "success" {
		t.Fatalf("status = %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 3 {
		t.Errorf("result_rows = %v, want the 3 rows of the checked statement", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_LogsARefusalWithoutTheQuery(t *testing.T) {
	t.Parallel()
	const query = "SELECT name FROM people; ATTACH DATABASE 'CANARY-refused-query.db' AS canary_schema"
	var captured bytes.Buffer
	sqlTool := &QueryDataWithSQLTool{logger: slog.New(slog.NewJSONHandler(&captured, nil))}
	toolCtx, _ := newDataToolContext(t)
	toolCtx.TraceID, toolCtx.ParentTaskID = "trace-1", "task-1"

	result := runQuery(t, sqlTool, toolCtx, "people", query)

	if result.Message != refusedQueryMessage {
		t.Fatalf("message = %q, want the refusal", result.Message)
	}
	want := []map[string]any{{
		"level":         "WARN",
		"msg":           "query_data_with_sql: query refused",
		"logger_name":   "query_data_with_sql",
		"traceID":       "trace-1",
		"taskID":        "task-1",
		"agentName":     "test-agent",
		"userID":        "test-user",
		"sessionID":     "test-session",
		"queryLen":      float64(83),
		"refusalReason": "denied_action",
		"sqliteAction":  float64(24),
	}}
	if got := capturedLogRecords(t, &captured); !reflect.DeepEqual(got, want) {
		t.Errorf("log records = %v, want %v", got, want)
	}
	for _, fromTheQuery := range []string{"CANARY-refused-query", "canary_schema", "ATTACH", "people"} {
		if strings.Contains(captured.String(), fromTheQuery) {
			t.Errorf("the log holds %q, which comes from the query: %s", fromTheQuery, captured.String())
		}
	}
}

func TestQueryDataWithSQL_CheckFailureIsNotBlamedOnTheSQL(t *testing.T) {
	t.Parallel()
	const unusableDefinition = `CREATE TABLE "payroll_table" ("salary_band" NUMERIC`
	var captured bytes.Buffer
	sqlTool := &QueryDataWithSQLTool{
		logger: slog.New(slog.NewJSONHandler(&captured, nil)),
		admitQuery: func(_ []string, query string) error {
			return sqlitegate.CheckSingleReadOnlySelect([]string{unusableDefinition}, query)
		},
	}
	toolCtx, _ := newDataToolContext(t)
	toolCtx.TraceID, toolCtx.ParentTaskID = "trace-1", "task-1"

	result := runQuery(t, sqlTool, toolCtx, "people", `SELECT name FROM people`)

	if result.Status != "error" || result.Message != checkFailedQueryMessage {
		t.Errorf("result = %s: %q, want the error %q", result.Status, result.Message, checkFailedQueryMessage)
	}
	if result.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want none: the SQL is not at fault", result.ErrorCode)
	}
	want := []map[string]any{{
		"level":            "ERROR",
		"msg":              checkFailedLogMessage,
		"logger_name":      "query_data_with_sql",
		"traceID":          "trace-1",
		"taskID":           "task-1",
		"agentName":        "test-agent",
		"userID":           "test-user",
		"sessionID":        "test-session",
		"stage":            "check_query",
		"queryLen":         float64(23),
		"checkStage":       "define",
		"sqliteResultCode": float64(1),
	}}
	if got := capturedLogRecords(t, &captured); !reflect.DeepEqual(got, want) {
		t.Errorf("log records = %v, want %v", got, want)
	}
	for _, fromTheData := range []string{"payroll_table", "salary_band", "incomplete input"} {
		if strings.Contains(captured.String(), fromTheData) {
			t.Errorf("the log holds %q, which comes from the table definition: %s", fromTheData, captured.String())
		}
	}
}

func TestQueryDataWithSQL_UnknownCheckErrorIsLoggedByItsTypeOnly(t *testing.T) {
	t.Parallel()
	var captured bytes.Buffer
	sqlTool := &QueryDataWithSQLTool{
		logger: slog.New(slog.NewJSONHandler(&captured, nil)),
		admitQuery: func([]string, string) error {
			return errors.New("could not read payroll_table")
		},
	}
	toolCtx, _ := newDataToolContext(t)
	toolCtx.TraceID, toolCtx.ParentTaskID = "trace-1", "task-1"

	result := runQuery(t, sqlTool, toolCtx, "people", `SELECT name FROM people`)

	if result.Status != "error" || result.Message != checkFailedQueryMessage || result.ErrorCode != "" {
		t.Errorf("result = %s: %q (code %q), want the error %q with no code", result.Status, result.Message, result.ErrorCode, checkFailedQueryMessage)
	}
	want := []map[string]any{{
		"level":       "ERROR",
		"msg":         checkFailedLogMessage,
		"logger_name": "query_data_with_sql",
		"traceID":     "trace-1",
		"taskID":      "task-1",
		"agentName":   "test-agent",
		"userID":      "test-user",
		"sessionID":   "test-session",
		"stage":       "check_query",
		"queryLen":    float64(23),
		"errorType":   "*errors.errorString",
	}}
	if got := capturedLogRecords(t, &captured); !reflect.DeepEqual(got, want) {
		t.Errorf("log records = %v, want %v", got, want)
	}
}

var (
	errConnectionLost  = errors.New("connection lost")
	errWrappedDeadline = fmt.Errorf("reserve the connection: %w", context.DeadlineExceeded)
)

func TestQueryDataWithSQL_CancelledTaskIsNotAToolFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var captured bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&captured, nil))

	result := hostFailureResult(ctx, logger, "reserve_connection", errConnectionLost, map[string]any{"people": "people.csv"})

	if result.Status != "error" || result.Message != cancelledQueryMessage || result.ErrorCode != "" {
		t.Errorf("result = %s: %q (code %q), want %q with no code", result.Status, result.Message, result.ErrorCode, cancelledQueryMessage)
	}
	if got := capturedLogRecords(t, &captured); len(got) != 0 {
		t.Errorf("log records = %v, want none", got)
	}
}

func TestQueryDataWithSQL_TaskPastItsDeadlineIsNotAToolFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancel()
	var captured bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&captured, nil))

	result := hostFailureResult(ctx, logger, "read_loaded_tables", errConnectionLost, map[string]any{"people": "people.csv"})

	if result.Status != "error" || result.Message != timedOutQueryMessage || result.ErrorCode != "DISPATCH_TIMEOUT" {
		t.Errorf("result = %s: %q (code %q), want %q with DISPATCH_TIMEOUT", result.Status, result.Message, result.ErrorCode, timedOutQueryMessage)
	}
	if got := capturedLogRecords(t, &captured); len(got) != 0 {
		t.Errorf("log records = %v, want none", got)
	}
}

func TestQueryDataWithSQL_InterruptionCarriedByTheErrorIsNotAToolFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		err         error
		wantMessage string
		wantCode    string
	}{
		{name: "cancellation", err: context.Canceled, wantMessage: cancelledQueryMessage, wantCode: ""},
		{name: "deadline", err: context.DeadlineExceeded, wantMessage: timedOutQueryMessage, wantCode: "DISPATCH_TIMEOUT"},
		{name: "wrapped deadline", err: errWrappedDeadline, wantMessage: wrappedTimedOutMessage, wantCode: "DISPATCH_TIMEOUT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var captured bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&captured, nil))

			result := hostFailureResult(t.Context(), logger, "confine_connection", tc.err, map[string]any{"people": "people.csv"})

			if result.Status != "error" || result.Message != tc.wantMessage || result.ErrorCode != tc.wantCode {
				t.Errorf("result = %s: %q (code %q), want %q (code %q)", result.Status, result.Message, result.ErrorCode, tc.wantMessage, tc.wantCode)
			}
			if got := capturedLogRecords(t, &captured); len(got) != 0 {
				t.Errorf("log records = %v, want none", got)
			}
		})
	}
}

func TestQueryDataWithSQL_ToolFailureLeavesOneErrorRecordWithItsStage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		stage string
	}{
		{name: "reserving the connection", stage: "reserve_connection"},
		{name: "reading the loaded tables", stage: "read_loaded_tables"},
		{name: "limiting the connection", stage: "confine_connection"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var captured bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&captured, nil))

			result := hostFailureResult(t.Context(), logger, tc.stage, errConnectionLost, map[string]any{"people": "people.csv"})

			if result.Status != "error" || result.Message != checkFailedQueryMessage || result.ErrorCode != "" {
				t.Errorf("result = %s: %q (code %q), want the error %q with no code", result.Status, result.Message, result.ErrorCode, checkFailedQueryMessage)
			}
			want := []map[string]any{{
				"level": "ERROR",
				"msg":   checkFailedLogMessage,
				"stage": tc.stage,
				"error": "connection lost",
			}}
			if got := capturedLogRecords(t, &captured); !reflect.DeepEqual(got, want) {
				t.Errorf("log records = %v, want %v", got, want)
			}
		})
	}
}

func TestQueryDataWithSQL_TaskCancelledAroundTheCheckIsNotAToolFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var captured bytes.Buffer
	sqlTool := &QueryDataWithSQLTool{
		logger: slog.New(slog.NewJSONHandler(&captured, nil)),
		admitQuery: func([]string, string) error {
			cancel()
			return nil
		},
	}
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, t.Context(), facade, "people.csv", []byte(peopleCSV), "text/csv")

	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"people": "people.csv"},
		"sql_query":   `SELECT name FROM people`,
	}, toolCtx)

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "error" || result.Message != cancelledQueryMessage || result.ErrorCode != "" {
		t.Errorf("result = %s: %q (code %q), want %q with no code", result.Status, result.Message, result.ErrorCode, cancelledQueryMessage)
	}
	if got := capturedLogRecords(t, &captured); len(got) != 0 {
		t.Errorf("log records = %v, want none", got)
	}
}

func TestLoadedTableDefinitions_LeavesOutSQLiteOwnTables(t *testing.T) {
	t.Parallel()
	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE counted (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TABLE sqlitedata (x)`,
	} {
		if _, execErr := db.ExecContext(t.Context(), statement); execErr != nil {
			t.Fatal(execErr)
		}
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	got, err := loadedTableDefinitions(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`CREATE TABLE counted (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TABLE sqlitedata (x)`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("loaded table definitions = %q, want %q", got, want)
	}
}

func TestConfineToReadOnlyQueries_ConnectionCannotAttachOrWrite(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for _, statement := range []string{
		"CREATE TABLE people (name)",
		"ATTACH DATABASE ':memory:' AS attached_before",
		"DETACH DATABASE attached_before",
	} {
		if _, execErr := conn.ExecContext(t.Context(), statement); execErr != nil {
			t.Fatalf("%q before the connection is confined: %v", statement, execErr)
		}
	}

	if confineErr := confineToReadOnlyQueries(t.Context(), conn); confineErr != nil {
		t.Fatalf("confineToReadOnlyQueries: %v", confineErr)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	for _, statement := range []string{
		"ATTACH DATABASE '" + target + "' AS e",
		"ATTACH DATABASE ':memory:' AS e",
		"VACUUM INTO '" + target + "'",
		"CREATE TABLE t (x)",
		"INSERT INTO people (name) VALUES ('x')",
		"CREATE TEMP TABLE t (x)",
	} {
		if _, execErr := conn.ExecContext(t.Context(), statement); execErr == nil {
			t.Errorf("%q succeeded on the confined connection", statement)
		}
	}
	if entries := directoryEntries(t, dir); len(entries) != 0 {
		t.Errorf("directory holds %v, want nothing", entries)
	}
	attachedLimit, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_ATTACHED, -1)
	if err != nil || attachedLimit != 0 {
		t.Errorf("attached-database limit = %d (%v), want 0", attachedLimit, err)
	}
}

func admitEveryQuery([]string, string) error { return nil }

func TestQueryDataWithSQL_ConnectionLimitsHoldWhenTheCheckAdmitsEverything(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	tests := []struct {
		name       string
		query      string
		wantStatus string
	}{
		{"control: a statement the real check refuses now runs", `PRAGMA table_info(people)`, "success"},
		{"attach a database file", `ATTACH DATABASE '` + target + `' AS e`, "error"},
		{"copy the database to a file", `VACUUM INTO '` + target + `'`, "error"},
		{"write to a loaded table", `INSERT INTO people (name) VALUES ('x')`, "error"},
		{"create a table", `CREATE TABLE t (x)`, "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toolCtx, _ := newDataToolContext(t)
			result := runQuery(t, &QueryDataWithSQLTool{admitQuery: admitEveryQuery}, toolCtx, "people", tc.query)
			if result.Status != tc.wantStatus {
				t.Errorf("status = %s (%s), want %s", result.Status, result.Message, tc.wantStatus)
			}
		})
	}
	t.Cleanup(func() {
		if entries := directoryEntries(t, dir); len(entries) != 0 {
			t.Errorf("directory holds %v, want nothing", entries)
		}
	})
}

func TestOpenInMemoryQueryDB_RefusesAttach(t *testing.T) {
	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})
	if createErr := createTableFromRows(t.Context(), db, "people", []map[string]any{{"name": "Ada"}}, nil); createErr != nil {
		t.Fatal(createErr)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	for _, statement := range []string{
		"ATTACH DATABASE '" + target + "' AS e",
		"VACUUM INTO '" + target + "'",
	} {
		if _, execErr := db.ExecContext(t.Context(), statement); execErr == nil {
			t.Errorf("%q succeeded on a data-tool database", statement)
		}
	}
	if entries := directoryEntries(t, dir); len(entries) != 0 {
		t.Errorf("directory holds %v, want nothing", entries)
	}
}
