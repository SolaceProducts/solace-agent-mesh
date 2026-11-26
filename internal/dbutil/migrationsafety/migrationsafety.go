// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrationsafety

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type Finding struct {
	Statement string
	Reason    string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s", f.Reason, f.Statement)
}

var GrowingTables = map[string]bool{
	"task_events":                  true,
	"task_event_blobs":             true,
	"tasks":                        true,
	"task_records":                 true,
	"entrypoint_task_items":        true,
	"entrypoint_inbound_messages":  true,
	"mcp_artifact_links":           true,
	"chat_tasks":                   true,
	"sessions":                     true,
	"session_messages":             true,
	"session_running_tasks":        true,
	"sse_event_buffer":             true,
	"sse_task_consumed":            true,
	"feedback":                     true,
	"notifications":                true,
	"notify_dedup_keys":            true,
	"scheduled_task_executions":    true,
	"outbox_events":                true,
	"whatsapp_outbound":            true,
	"whatsapp_processed_messages":  true,
	"googlechat_processed_events":  true,
	"eval_run_results":             true,
	"eval_run_scores":              true,
	"traces":                       true,
	"trace_steps":                  true,
	"step_values":                  true,
	"trace_causes":                 true,
	"trace_judgments":              true,
	"trace_screens":                true,
	"ingestion_failures":           true,
	"findings":                     true,
	"finding_versions":             true,
	"finding_traces":               true,
	"token_spend_ledger":           true,
	"token_spend_ledger_details":   true,
	"token_spend_daily":            true,
	"token_spend_daily_activities": true,
	"token_spend_daily_models":     true,
	"token_spend_budget_period":    true,
	"workflow_checkpoint":          true,
}

func Check(src []byte) []Finding {
	var findings []Finding
	statements := splitStatements(stripComments(upSection(src)))
	created := createdTables(statements)
	for _, stmt := range statements {
		for _, f := range statementFindings(stmt) {
			if created[f.table] || !GrowingTables[f.table] {
				continue
			}
			findings = append(findings, Finding{Statement: stmt, Reason: f.table + ": " + f.reason})
		}
		findings = append(findings, asyncIndexFindings(stmt)...)
	}
	return findings
}

type tableFinding struct {
	table  string
	reason string
}

const ident = `"?(?:[a-z_][a-z0-9_]*"?\.)?"?([a-z_][a-z0-9_]*)"?`

var (
	createTablePattern = regexp.MustCompile(`^create (?:(?:temp|temporary|unlogged) )?table (?:if not exists )?` + ident)
	createIndexPattern = regexp.MustCompile(`^create (?:unique )?index (?:concurrently )?(?:if not exists )?(?:` + ident + ` )?on (?:only )?` + ident)
	alterTablePattern  = regexp.MustCompile(`^alter table (?:if exists )?(?:only )?` + ident + ` (.*)$`)
	updatePattern      = regexp.MustCompile(`^update (?:only )?` + ident)
	deletePattern      = regexp.MustCompile(`^delete from (?:only )?` + ident)
	insertSelect       = regexp.MustCompile(`^insert into ` + ident + `\b.*?\bselect\b`)
	createTableAs      = regexp.MustCompile(`^create (?:(?:temp|temporary|unlogged) )?table (?:if not exists )?` + ident + `\b.*?\bas \(?select\b`)
	createMatViewAs    = regexp.MustCompile(`^create materialized view (?:if not exists )?` + ident + `\b.*?\bas \(?select\b`)
	sourceTable        = regexp.MustCompile(`\b(?:from|join) ` + ident)
	mergePattern       = regexp.MustCompile(`^merge into ` + ident)
	rewritePattern     = regexp.MustCompile(`^(vacuum full|cluster|reindex|refresh materialized view)\b(?: (?:table |index |concurrently )*` + ident + `)?`)
	doBlockPattern     = regexp.MustCompile(`^do\b`)
	withQueryPattern   = regexp.MustCompile(`^with\b`)
	nestedStatementKey = regexp.MustCompile(`\b(update|delete from|insert into|merge into|create (?:unique )?index|alter table|vacuum full|cluster|reindex)\b`)
)

type rule struct {
	pattern *regexp.Regexp
	reason  string
}

var alterTableRules = []rule{
	{regexp.MustCompile(`\balter column [^,]*?\b(?:set data )?type\b`), "changing a column type rewrites the table"},
	{regexp.MustCompile(`\bset not null\b`), "SET NOT NULL scans the table; add a CHECK (col IS NOT NULL) NOT VALID constraint instead"},
	{regexp.MustCompile(`\bvalidate constraint\b`), "VALIDATE CONSTRAINT scans the table"},
	{regexp.MustCompile(`\badd (?:constraint [a-z0-9_"]+ )?(?:unique|primary key|exclude)\b`), "adding a UNIQUE, PRIMARY KEY or EXCLUDE constraint builds an index over the table"},
	{regexp.MustCompile(`\bset (?:logged|unlogged|tablespace)\b`), "this rewrites the table"},
}

var addColumnRules = []rule{
	{regexp.MustCompile(`\b(?:serial|bigserial|smallserial)\b`), "a serial column is filled in for every row, rewriting the table"},
	{regexp.MustCompile(`\bgenerated\b`), "a generated or identity column is filled in for every row, rewriting the table"},
	{regexp.MustCompile(`\bdefault .*\b(?:random|gen_random_uuid|uuid_generate_v[1-5]|clock_timestamp|timeofday|nextval|statement_timestamp)\s*\(`), "a volatile default is evaluated for every row, rewriting the table"},
	{regexp.MustCompile(`\b(?:unique|primary key)\b`), "a UNIQUE or PRIMARY KEY column builds an index over the table"},
	{regexp.MustCompile(`\breferences\b`), "an inline foreign key is validated against every row; add the constraint NOT VALID instead"},
}

var (
	addCheckOrForeignKey = regexp.MustCompile(`\badd (?:constraint [a-z0-9_"]+ )?(?:check|foreign key)\b`)
	addColumnClause      = regexp.MustCompile(`\badd (?:column )?(?:if not exists )?([a-z0-9_"]+) ([^,]*)`)
	constraintKeywords   = map[string]bool{"constraint": true, "check": true, "foreign": true, "unique": true, "primary": true, "exclude": true}
)

func statementFindings(stmt string) []tableFinding {
	s := normalize(stmt)
	if doBlockPattern.MatchString(s) {
		return nestedFindings(s)
	}
	if withQueryPattern.MatchString(s) {
		return append(nestedFindings(s), sourceFindings(s, "a WITH query reads every matching row of the source table")...)
	}
	if m := createIndexPattern.FindStringSubmatch(s); m != nil {
		return []tableFinding{{m[2], "building an index scans the whole table, even CONCURRENTLY; queue it in the store's async index table instead"}}
	}
	if m := alterTablePattern.FindStringSubmatch(s); m != nil {
		return alterTableFindings(m[1], m[2])
	}
	if m := updatePattern.FindStringSubmatch(s); m != nil {
		return []tableFinding{{m[1], "UPDATE touches every matching row; backfill in paced background batches"}}
	}
	if m := deletePattern.FindStringSubmatch(s); m != nil {
		return []tableFinding{{m[1], "DELETE touches every matching row; delete in paced background batches"}}
	}
	if m := mergePattern.FindStringSubmatch(s); m != nil {
		return []tableFinding{{m[1], "MERGE touches every matching row"}}
	}
	if insertSelect.MatchString(s) {
		return sourceFindings(s, "INSERT ... SELECT reads every matching row of the source table")
	}
	if createTableAs.MatchString(s) {
		return sourceFindings(s, "CREATE TABLE ... AS reads every matching row of the source table")
	}
	if createMatViewAs.MatchString(s) && !strings.Contains(s, "with no data") {
		return sourceFindings(s, "CREATE MATERIALIZED VIEW reads every matching row of the source table")
	}
	if m := rewritePattern.FindStringSubmatch(s); m != nil {
		return []tableFinding{{m[2], strings.ToUpper(m[1]) + " rewrites or scans the whole table"}}
	}
	return nil
}

func sourceFindings(s, reason string) []tableFinding {
	var findings []tableFinding
	for _, m := range sourceTable.FindAllStringSubmatch(s, -1) {
		findings = append(findings, tableFinding{m[1], reason})
	}
	return findings
}

func alterTableFindings(table, actions string) []tableFinding {
	var findings []tableFinding
	for _, r := range alterTableRules {
		if r.pattern.MatchString(actions) {
			findings = append(findings, tableFinding{table, r.reason})
		}
	}
	if addCheckOrForeignKey.MatchString(actions) && !strings.Contains(actions, "not valid") {
		findings = append(findings, tableFinding{table, "a CHECK or FOREIGN KEY constraint is validated against every row; add it NOT VALID"})
	}
	for _, m := range addColumnClause.FindAllStringSubmatch(actions, -1) {
		if constraintKeywords[m[1]] {
			continue
		}
		for _, r := range addColumnRules {
			if r.pattern.MatchString(m[2]) {
				findings = append(findings, tableFinding{table, r.reason})
			}
		}
	}
	return findings
}

func nestedFindings(block string) []tableFinding {
	var findings []tableFinding
	for _, piece := range strings.Split(block, ";") {
		for _, loc := range nestedStatementKey.FindAllStringIndex(piece, -1) {
			before := strings.TrimSpace(piece[:loc[0]])
			if strings.HasSuffix(before, " for") || strings.HasSuffix(before, " on") {
				continue
			}
			findings = append(findings, statementFindings(piece[loc[0]:])...)
			break
		}
	}
	return findings
}

func createdTables(statements []string) map[string]bool {
	created := map[string]bool{}
	for _, stmt := range statements {
		if m := createTablePattern.FindStringSubmatch(normalize(stmt)); m != nil {
			created[m[1]] = true
		}
	}
	return created
}

var (
	asyncQueueInsert = regexp.MustCompile(`^insert into (?:([a-z_][a-z0-9_]*)\.)?[a-z_]*_async_indexes\b`)
	quotedLiteral    = regexp.MustCompile(`'((?:[^']|'')*)'`)
	indexNameLiteral = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

func asyncIndexFindings(stmt string) []Finding {
	queue := asyncQueueInsert.FindStringSubmatch(normalize(stmt))
	if queue == nil {
		return nil
	}
	queueSchema := queue[1]
	var findings []Finding
	rowName := ""
	for _, m := range quotedLiteral.FindAllStringSubmatch(stmt, -1) {
		literal := strings.TrimSpace(m[1])
		lower := strings.ToLower(literal)
		if !strings.HasPrefix(lower, "create") && !strings.HasPrefix(lower, "drop") {
			if !indexNameLiteral.MatchString(literal) {
				findings = append(findings, Finding{Statement: literal, Reason: "a queued row's name and replaces must be plain lowercase index names; the builder refuses anything else"})
			}
			rowName = literal
			continue
		}
		indexName := queuedIndexName(literal)
		switch {
		case indexName != "" && indexName == rowName && queueSchema != "" && dbutil.AsyncIndexDropPattern.MatchString(literal):
		case indexName != "" && indexName == rowName && queueSchema != "" && dbutil.AsyncIndexTableSchema(literal) != queueSchema:
			findings = append(findings, Finding{Statement: literal, Reason: fmt.Sprintf("a build queued in %[1]s must name its table as %[1]s.<table>; the builder refuses it", queueSchema)})
		case indexName != "" && indexName == rowName:
		case indexName != "":
			findings = append(findings, Finding{Statement: literal, Reason: fmt.Sprintf("the queued definition names %s but the row is named %s; the builder refuses it", indexName, rowName)})
		case strings.HasPrefix(normalize(literal), "create unique"):
			findings = append(findings, Finding{Statement: literal, Reason: "a unique index cannot be queued: code relies on a constraint as soon as its migration has run, so it needs a planned rollout (see the migration guide)"})
		default:
			findings = append(findings, Finding{Statement: literal, Reason: "a queued operation must be CREATE INDEX CONCURRENTLY IF NOT EXISTS <name> ON ... or DROP INDEX CONCURRENTLY IF EXISTS <name>"})
		}
		rowName = ""
	}
	return findings
}

func queuedIndexName(definition string) string {
	for _, p := range []*regexp.Regexp{dbutil.AsyncIndexCreatePattern, dbutil.AsyncIndexDropPattern} {
		if m := p.FindStringSubmatch(definition); m != nil {
			return m[1]
		}
	}
	return ""
}

func upSection(src []byte) string {
	var up strings.Builder
	inUp := false
	for line := range strings.Lines(string(src)) {
		line = strings.TrimRight(line, "\r\n")
		switch gooseAnnotation(line) {
		case "up":
			inUp = true
			continue
		case "down":
			inUp = false
			continue
		}
		if inUp {
			up.WriteString(line)
			up.WriteByte('\n')
		}
	}
	return up.String()
}

func gooseAnnotation(line string) string {
	if !strings.HasPrefix(strings.TrimSpace(line), "--") || !strings.Contains(line, "+goose") {
		return ""
	}
	annotation := strings.Replace(strings.ReplaceAll(line, "--", ""), "+goose", "", 1)
	return strings.ToLower(strings.TrimSpace(annotation))
}

func stripComments(sql string) string {
	var out strings.Builder
	scanSQL(sql, func(chunk string, kind chunkKind) {
		if kind != comment {
			out.WriteString(chunk)
		}
	})
	return out.String()
}

func splitStatements(sql string) []string {
	var statements []string
	var current strings.Builder
	flush := func() {
		if stmt := strings.TrimSpace(current.String()); stmt != "" {
			statements = append(statements, stmt)
		}
		current.Reset()
	}
	scanSQL(sql, func(chunk string, kind chunkKind) {
		if kind != code {
			current.WriteString(chunk)
			return
		}
		parts := strings.Split(chunk, ";")
		for i, part := range parts {
			current.WriteString(part)
			if i < len(parts)-1 {
				flush()
			}
		}
	})
	flush()
	return statements
}

type chunkKind int

const (
	code chunkKind = iota
	quoted
	comment
)

var dollarTag = regexp.MustCompile(`^\$[A-Za-z_]*\$`)

func scanSQL(sql string, fn func(chunk string, kind chunkKind)) {
	start := 0
	emit := func(from, to int, kind chunkKind) {
		if start < from {
			fn(sql[start:from], code)
		}
		fn(sql[from:to], kind)
		start = to
	}
	for i := 0; i < len(sql); {
		var end int
		var kind chunkKind
		switch {
		case strings.HasPrefix(sql[i:], "--"):
			end, kind = indexAfter(sql, i, "\n"), comment
		case strings.HasPrefix(sql[i:], "/*"):
			end, kind = indexAfter(sql, i+2, "*/"), comment
		case sql[i] == '\'':
			end, kind = quoteEnd(sql, i), quoted
		case sql[i] == '$' && dollarTag.MatchString(sql[i:]):
			tag := dollarTag.FindString(sql[i:])
			end, kind = indexAfter(sql, i+len(tag), tag), quoted
		default:
			i++
			continue
		}
		emit(i, end, kind)
		i = end
	}
	if start < len(sql) {
		fn(sql[start:], code)
	}
}

func indexAfter(s string, from int, sep string) int {
	if n := strings.Index(s[from:], sep); n >= 0 {
		return from + n + len(sep)
	}
	return len(s)
}

func quoteEnd(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] != '\'' {
			continue
		}
		if j+1 < len(s) && s[j+1] == '\'' {
			j++
			continue
		}
		return j + 1
	}
	return len(s)
}

var whitespace = regexp.MustCompile(`\s+`)

func normalize(stmt string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(strings.ToLower(stmt), " "))
}
