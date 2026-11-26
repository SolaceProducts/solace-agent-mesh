// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import "testing"

func TestUpsertSQL_Postgres(t *testing.T) {
	got := UpsertSQL("postgres",
		"INSERT INTO t (id, name, age) VALUES ($1, $2, $3)",
		"id", []string{"name", "age"})
	want := "INSERT INTO t (id, name, age) VALUES ($1, $2, $3) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, age = EXCLUDED.age"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUpsertSQL_SQLite(t *testing.T) {
	got := UpsertSQL("sqlite",
		"INSERT INTO t (id, name, age) VALUES (?, ?, ?)",
		"id", []string{"name", "age"})
	want := "INSERT INTO t (id, name, age) VALUES (?, ?, ?) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, age = EXCLUDED.age"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUpsertSQL_MultipleColumns(t *testing.T) {
	got := UpsertSQL("postgres",
		"INSERT INTO t (id, a, b, c) VALUES ($1, $2, $3, $4)",
		"id", []string{"a", "b", "c"})
	want := "INSERT INTO t (id, a, b, c) VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO UPDATE SET a = EXCLUDED.a, b = EXCLUDED.b, c = EXCLUDED.c"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUpsertWhereSQL_AppendsConditionAfterSet(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		got := UpsertWhereSQL(dialect,
			"INSERT INTO t (id, owner, name) VALUES (?, ?, ?)",
			"id", []string{"name"}, "t.owner = EXCLUDED.owner")
		want := "INSERT INTO t (id, owner, name) VALUES (?, ?, ?) ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name WHERE t.owner = EXCLUDED.owner"
		if got != want {
			t.Errorf("%s: got:\n%s\nwant:\n%s", dialect, got, want)
		}
	}
}

func TestUpsertWhereSQL_EmptyConditionIsUpsertSQL(t *testing.T) {
	insert := "INSERT INTO t (id, name) VALUES (?, ?)"
	if got, want := UpsertWhereSQL("sqlite", insert, "id", []string{"name"}, ""), UpsertSQL("sqlite", insert, "id", []string{"name"}); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
