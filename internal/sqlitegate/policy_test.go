// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqlitegate

import (
	"errors"
	"slices"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestReadPolicyAllowsOnlyReadingTheDefinedTables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		action        int32
		first         string
		second        string
		database      string
		want          bool
		wantToResolve []string
	}{
		{name: "select", action: sqlite3.SQLITE_SELECT, want: true},
		{name: "recursive cte", action: sqlite3.SQLITE_RECURSIVE, want: true},
		{name: "read a column of a defined table", action: sqlite3.SQLITE_READ, first: "people", second: "name", database: "main", want: true},
		{name: "read a defined table written in another case", action: sqlite3.SQLITE_READ, first: "PEOPLE", second: "", database: "", want: true},
		{name: "read with the database written in another case", action: sqlite3.SQLITE_READ, first: "people", second: "name", database: "MAIN", want: true},
		{name: "read the schema catalog", action: sqlite3.SQLITE_READ, first: "sqlite_master", second: "sql", database: "main", want: true},
		{name: "read a json table function", action: sqlite3.SQLITE_READ, first: "json_each", second: "value", database: "main", want: true},
		{name: "call an allowed function", action: sqlite3.SQLITE_FUNCTION, second: "count", want: true},
		{name: "call an allowed function written in another case", action: sqlite3.SQLITE_FUNCTION, second: "COUNT", want: true},
		{name: "unqualified name with no column is admitted pending resolution", action: sqlite3.SQLITE_READ, first: "adults", want: true, wantToResolve: []string{"adults"}},

		{name: "read a column of an unknown table", action: sqlite3.SQLITE_READ, first: "adults", second: "name", database: "main"},
		{name: "read an unknown table qualified with main", action: sqlite3.SQLITE_READ, first: "adults", database: "main"},
		{name: "read the raw page table", action: sqlite3.SQLITE_READ, first: "sqlite_dbpage", second: "data", database: "main"},
		{name: "read a defined table's name in another database", action: sqlite3.SQLITE_READ, first: "people", second: "name", database: "other"},
		{name: "read a defined table's name in temp", action: sqlite3.SQLITE_READ, first: "people", second: "name", database: "temp"},
		{name: "read the temp catalog with no column", action: sqlite3.SQLITE_READ, first: "sqlite_master", database: "temp"},
		{name: "call a function outside the allowlist", action: sqlite3.SQLITE_FUNCTION, second: "load_extension"},
		{name: "pragma", action: sqlite3.SQLITE_PRAGMA, first: "query_only", second: "OFF"},
		{name: "attach", action: sqlite3.SQLITE_ATTACH, first: "elsewhere.db"},
		{name: "detach", action: sqlite3.SQLITE_DETACH, first: "main"},
		{name: "transaction", action: sqlite3.SQLITE_TRANSACTION, first: "BEGIN"},
		{name: "savepoint", action: sqlite3.SQLITE_SAVEPOINT, first: "BEGIN", second: "s"},
		{name: "insert", action: sqlite3.SQLITE_INSERT, first: "people", database: "main"},
		{name: "update", action: sqlite3.SQLITE_UPDATE, first: "people", second: "name", database: "main"},
		{name: "delete", action: sqlite3.SQLITE_DELETE, first: "people", database: "main"},
		{name: "create table", action: sqlite3.SQLITE_CREATE_TABLE, first: "t", database: "main"},
		{name: "create temp table", action: sqlite3.SQLITE_CREATE_TEMP_TABLE, first: "t", database: "temp"},
		{name: "create index", action: sqlite3.SQLITE_CREATE_INDEX, first: "i", second: "people", database: "main"},
		{name: "create temp index", action: sqlite3.SQLITE_CREATE_TEMP_INDEX, first: "i", second: "t", database: "temp"},
		{name: "create view", action: sqlite3.SQLITE_CREATE_VIEW, first: "v", database: "main"},
		{name: "create temp view", action: sqlite3.SQLITE_CREATE_TEMP_VIEW, first: "v", database: "temp"},
		{name: "create trigger", action: sqlite3.SQLITE_CREATE_TRIGGER, first: "tr", second: "people", database: "main"},
		{name: "create temp trigger", action: sqlite3.SQLITE_CREATE_TEMP_TRIGGER, first: "tr", second: "people", database: "temp"},
		{name: "create virtual table", action: sqlite3.SQLITE_CREATE_VTABLE, first: "v", second: "fts5", database: "main"},
		{name: "drop table", action: sqlite3.SQLITE_DROP_TABLE, first: "people", database: "main"},
		{name: "drop temp table", action: sqlite3.SQLITE_DROP_TEMP_TABLE, first: "t", database: "temp"},
		{name: "drop index", action: sqlite3.SQLITE_DROP_INDEX, first: "i", second: "people", database: "main"},
		{name: "drop temp index", action: sqlite3.SQLITE_DROP_TEMP_INDEX, first: "i", second: "t", database: "temp"},
		{name: "drop view", action: sqlite3.SQLITE_DROP_VIEW, first: "v", database: "main"},
		{name: "drop temp view", action: sqlite3.SQLITE_DROP_TEMP_VIEW, first: "v", database: "temp"},
		{name: "drop trigger", action: sqlite3.SQLITE_DROP_TRIGGER, first: "tr", second: "people", database: "main"},
		{name: "drop temp trigger", action: sqlite3.SQLITE_DROP_TEMP_TRIGGER, first: "tr", second: "people", database: "temp"},
		{name: "drop virtual table", action: sqlite3.SQLITE_DROP_VTABLE, first: "v", second: "fts5", database: "main"},
		{name: "alter table", action: sqlite3.SQLITE_ALTER_TABLE, first: "main", second: "people"},
		{name: "reindex", action: sqlite3.SQLITE_REINDEX, first: "i", database: "main"},
		{name: "analyze", action: sqlite3.SQLITE_ANALYZE, first: "people", database: "main"},
		{name: "an action code SQLite does not define", action: 99},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			policy := newReadPolicy([]string{"people"})
			got := policy.allows(tc.action, tc.first, tc.second, tc.database)
			if got != tc.want {
				t.Errorf("allows(%d, %q, %q, %q) = %t, want %t", tc.action, tc.first, tc.second, tc.database, got, tc.want)
			}
			if !slices.Equal(policy.namesToResolve, tc.wantToResolve) {
				t.Errorf("names to resolve = %q, want %q", policy.namesToResolve, tc.wantToResolve)
			}
		})
	}
}

func allowEveryAction(int32, string, string, string) bool { return true }

func TestOnlyAReadOnlySelectPassesAnAuthorizerThatAllowsEverything(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		want  Refusal
	}{
		{"a select is admitted", `SELECT name FROM people`, Refusal{}},
		{"a statement the authorizer is never asked about", `VACUUM`, Refusal{Reason: "not_read_only_select"}},
		{"an allowed statement that returns no columns", `BEGIN`, Refusal{Reason: "not_read_only_select"}},
		{"a write that returns rows", `UPDATE people SET name = 'x' RETURNING name`, Refusal{Reason: "not_read_only_select"}},
		{"an explain of a select", `EXPLAIN SELECT name FROM people`, Refusal{Reason: "not_read_only_select"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := openScratch()
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			if _, defineErr := s.define(peopleDefinition); defineErr != nil {
				t.Fatal(defineErr)
			}
			everything := &authorization{allows: allowEveryAction}
			defer s.authorizeWith(everything)()

			got := refusalOrAdmitted(t, s.requireSingleReadOnlySelectUnder(tc.query, everything))
			if got != tc.want {
				t.Errorf("%q = %+v, want %+v", tc.query, got, tc.want)
			}
		})
	}
}

func TestAnAuthorizerWithNoRuleIsAnInternalError(t *testing.T) {
	t.Parallel()
	s, err := openScratch()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	unregistered := lastAuthorizationID.Add(1)
	sqlite3.Xsqlite3_set_authorizer(s.tls, s.db, authorizerEntry, unregistered)

	err = s.requireSingleReadOnlySelectUnder(`SELECT 1`, &authorization{allows: allowEveryAction})

	internal, ok := errors.AsType[*InternalError](err)
	if !ok {
		t.Fatalf("got %v (%T), want an *InternalError", err, err)
	}
	if want := (InternalError{Stage: "authorize", ResultCode: 23}); *internal != want {
		t.Errorf("got %+v, want %+v", *internal, want)
	}
}
