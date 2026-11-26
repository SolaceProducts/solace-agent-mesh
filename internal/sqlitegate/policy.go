// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqlitegate

import (
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	mainDatabase  = "main"
	schemaCatalog = "sqlite_master"
)

type readPolicy struct {
	readable       map[string]struct{}
	namesToResolve []string
}

func foldASCII(name string) string {
	folded := []byte(name)
	for i, c := range folded {
		if 'A' <= c && c <= 'Z' {
			folded[i] = c + ('a' - 'A')
		}
	}
	return string(folded)
}

func newReadPolicy(tableNames []string) *readPolicy {
	readable := make(map[string]struct{}, len(tableNames)+len(readableSystemTables))
	for _, name := range readableSystemTables {
		readable[name] = struct{}{}
	}
	for _, name := range tableNames {
		readable[foldASCII(name)] = struct{}{}
	}
	return &readPolicy{readable: readable}
}

func (p *readPolicy) allows(action int32, first, second, database string) bool {
	switch action {
	case sqlite3.SQLITE_SELECT, sqlite3.SQLITE_RECURSIVE:
		return true
	case sqlite3.SQLITE_READ:
		return p.allowsRead(first, second, database)
	case sqlite3.SQLITE_FUNCTION:
		_, allowed := allowedFunctions[foldASCII(second)]
		return allowed
	}
	return false
}

func (p *readPolicy) allowsRead(table, column, database string) bool {
	if database != "" && foldASCII(database) != mainDatabase {
		return false
	}
	if _, readable := p.readable[foldASCII(table)]; readable {
		return true
	}
	if column != "" || database != "" {
		return false
	}
	p.namesToResolve = append(p.namesToResolve, table)
	return true
}

type tableDefinition struct {
	names []string
}

func (d *tableDefinition) allows(action int32, first, _, database string) bool {
	if foldASCII(database) != mainDatabase {
		return false
	}
	switch action {
	case sqlite3.SQLITE_CREATE_TABLE:
		d.names = append(d.names, first)
		return true
	case sqlite3.SQLITE_INSERT, sqlite3.SQLITE_UPDATE, sqlite3.SQLITE_READ:
		return foldASCII(first) == schemaCatalog
	}
	return false
}

var readableSystemTables = []string{
	schemaCatalog,
	"sqlite_schema",
	"json_each",
	"json_tree",
	"pragma_table_info",
	"pragma_table_xinfo",
}

var allowedFunctions = functionSet(
	"->", "->>",
	"abs", "char", "coalesce", "concat", "concat_ws", "format", "glob", "hex",
	"if", "ifnull", "iif", "instr", "length", "like", "likelihood", "likely",
	"lower", "ltrim", "max", "min", "nullif", "octet_length", "printf", "quote",
	"random", "randomblob", "replace", "round", "rtrim", "sign", "soundex",
	"substr", "substring", "subtype", "trim", "typeof", "unhex", "unicode",
	"unistr", "unistr_quote", "unlikely", "upper", "zeroblob",

	"avg", "count", "group_concat", "string_agg", "sum", "total",

	"cume_dist", "dense_rank", "first_value", "lag", "last_value", "lead",
	"nth_value", "ntile", "percent_rank", "rank", "row_number",

	"current_date", "current_time", "current_timestamp", "date", "datetime",
	"julianday", "strftime", "time", "timediff", "unixepoch",

	"acos", "acosh", "asin", "asinh", "atan", "atan2", "atanh", "ceil",
	"ceiling", "cos", "cosh", "degrees", "exp", "floor", "ln", "log", "log10",
	"log2", "mod", "pi", "pow", "power", "radians", "sin", "sinh", "sqrt",
	"tan", "tanh", "trunc",

	"json", "json_array", "json_array_insert", "json_array_length",
	"json_error_position", "json_extract", "json_group_array",
	"json_group_object", "json_insert", "json_object", "json_patch",
	"json_pretty", "json_quote", "json_remove", "json_replace", "json_set",
	"json_type", "json_valid",
	"jsonb", "jsonb_array", "jsonb_array_insert", "jsonb_extract",
	"jsonb_group_array", "jsonb_group_object", "jsonb_insert", "jsonb_object",
	"jsonb_patch", "jsonb_remove", "jsonb_replace", "jsonb_set",

	"changes", "last_insert_rowid", "total_changes",
	"sqlite_compileoption_get", "sqlite_compileoption_used", "sqlite_offset",
	"sqlite_source_id", "sqlite_version",
)

func functionSet(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}
