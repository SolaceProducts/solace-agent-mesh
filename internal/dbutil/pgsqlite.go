// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"sync"

	"modernc.org/sqlite"
)

func WithPostgresSQL() OpenOption {
	return func(o *openOptions) { o.postgresSQL = true }
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("hashtext", 1, hashText)
}

func hashText(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if args[0] == nil {
		return nil, nil
	}
	h := fnv.New32a()
	if _, err := fmt.Fprint(h, args[0]); err != nil {
		return nil, err
	}
	return int64(int32(h.Sum32())), nil
}

var translatedSQL sync.Map

func postgresToSQLite(query string) string {
	if v, ok := translatedSQL.Load(query); ok {
		return v.(string)
	}
	out := translatePostgres(query)
	translatedSQL.Store(query, out)
	return out
}

var (
	castType   = regexp.MustCompile(`^::([A-Za-z_][A-Za-z0-9_]*)(\(\s*\d+\s*(,\s*\d+\s*)?\))?(\[\])?`)
	isDistinct = regexp.MustCompile(`(?i)^IS\s+(NOT\s+)?DISTINCT\s+FROM\b`)
	forUpdate  = regexp.MustCompile(`(?i)^\s+FOR\s+UPDATE(\s+SKIP\s+LOCKED)?\b`)
	renamedFn  = regexp.MustCompile(`(?i)^(char_length|greatest|least|bool_or|bool_and)\s*\(`)
)

var sqliteCastTypes = map[string]string{
	"text": "TEXT", "uuid": "TEXT", "varchar": "TEXT",
	"bigint": "INTEGER", "int": "INTEGER", "integer": "INTEGER", "smallint": "INTEGER", "int8": "INTEGER", "int4": "INTEGER",
	"float": "REAL", "float8": "REAL", "real": "REAL", "numeric": "REAL",
	"boolean": "INTEGER", "bool": "INTEGER",
	"bytea": "BLOB",
}

var renamedFns = map[string]string{
	"char_length": "length", "greatest": "max", "least": "min", "bool_or": "max", "bool_and": "min",
}

func translatePostgres(query string) string {
	var b strings.Builder
	b.Grow(len(query))
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case c == '\'' || c == '"':
			end := closingQuote(query, i)
			b.WriteString(query[i:end])
			i = end
		case c == ':' && strings.HasPrefix(query[i:], "::"):
			m := castType.FindStringSubmatch(query[i:])
			if len(m) < 5 || m[4] != "" {
				b.WriteString("::")
				i += 2
				continue
			}
			i += len(m[0])
			typ := strings.ToLower(strings.TrimSpace(m[1]))
			if typ == "json" || typ == "jsonb" {
				continue
			}
			target, ok := sqliteCastTypes[typ]
			if !ok {
				b.WriteString(m[0])
				continue
			}
			done := b.String()
			start := operandStart(done)
			b.Reset()
			b.WriteString(done[:start])
			b.WriteString("CAST(")
			b.WriteString(done[start:])
			b.WriteString(" AS ")
			b.WriteString(target)
			b.WriteString(")")
		case (c == 'I' || c == 'i') && wordBoundary(query, i):
			if m := isDistinct.FindStringSubmatch(query[i:]); m != nil {
				if m[1] != "" {
					b.WriteString("IS")
				} else {
					b.WriteString("IS NOT")
				}
				i += len(m[0])
				continue
			}
			b.WriteByte(c)
			i++
		case strings.ContainsRune("cCgGlLbB", rune(c)) && wordBoundary(query, i):
			if m := renamedFn.FindStringSubmatch(query[i:]); m != nil {
				b.WriteString(renamedFns[strings.ToLower(m[1])])
				b.WriteString("(")
				i += len(m[0])
				continue
			}
			b.WriteByte(c)
			i++
		case c == ' ' || c == '\n' || c == '\t':
			if m := forUpdate.FindString(query[i:]); m != "" {
				i += len(m)
				continue
			}
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func openingParen(s string, closing int) int {
	depth := 0
	for i := closing; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func closingQuote(s string, open int) int {
	q := s[open]
	for i := open + 1; i < len(s); i++ {
		if s[i] != q {
			continue
		}
		if i+1 < len(s) && s[i+1] == q {
			i++
			continue
		}
		return i + 1
	}
	return len(s)
}

func wordBoundary(s string, i int) bool {
	return i == 0 || !isIdentByte(s[i-1])
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func operandStart(s string) int {
	i := len(s)
	switch {
	case i == 0:
		return 0
	case s[i-1] == ')':
		i = openingParen(s, i-1)
		if i < 0 {
			return 0
		}
	case s[i-1] == '\'' || s[i-1] == '"':
		q := s[i-1]
		return max(strings.LastIndexByte(s[:i-1], q), 0)
	case s[i-1] == '?':
		return i - 1
	}
	for i > 0 && isIdentByte(s[i-1]) {
		i--
	}
	return i
}

func (db *DB) PostgresSQL() *DB {
	if db == nil || db.dialect != "sqlite" || db.postgresSQL {
		return db
	}
	translated := *db
	translated.postgresSQL = true
	return &translated
}
