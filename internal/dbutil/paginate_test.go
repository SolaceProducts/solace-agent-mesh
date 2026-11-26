// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

type paginateRow struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

func TestPaginate_MultiplePages(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`)
	for i := 1; i <= 25; i++ {
		_, _ = db.ExecContext(ctx, `INSERT INTO items VALUES (?, ?)`, i, fmt.Sprintf("item_%d", i))
	}

	p1, err := Paginate[paginateRow](ctx, db,
		`SELECT id, name FROM items ORDER BY id`,
		nil, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Total != 25 {
		t.Errorf("expected total=25, got %d", p1.Total)
	}
	if len(p1.Items) != 10 {
		t.Errorf("expected 10 items, got %d", len(p1.Items))
	}
	if p1.Items[0].ID != 1 {
		t.Errorf("expected first ID=1, got %d", p1.Items[0].ID)
	}

	p3, err := Paginate[paginateRow](ctx, db,
		`SELECT id, name FROM items ORDER BY id`,
		nil, 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p3.Total != 25 {
		t.Errorf("expected total=25, got %d", p3.Total)
	}
	if len(p3.Items) != 5 {
		t.Errorf("expected 5 items on page 3, got %d", len(p3.Items))
	}
	if p3.Items[0].ID != 21 {
		t.Errorf("expected first ID=21, got %d", p3.Items[0].ID)
	}
}

func TestPaginate_EmptyTable(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE items (id INTEGER, name TEXT)`)

	p, err := Paginate[paginateRow](ctx, db,
		`SELECT id, name FROM items ORDER BY id`,
		nil, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 0 {
		t.Errorf("expected total=0, got %d", p.Total)
	}
	if len(p.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(p.Items))
	}
}

func TestPaginate_WithArgs(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `CREATE TABLE items (id INTEGER, name TEXT, category TEXT)`)
	for i := 1; i <= 15; i++ {
		cat := "a"
		if i > 10 {
			cat = "b"
		}
		_, _ = db.ExecContext(ctx, `INSERT INTO items VALUES (?, ?, ?)`, i, fmt.Sprintf("item_%d", i), cat)
	}

	args := []any{"a"}
	p, err := Paginate[paginateRow](ctx, db,
		`SELECT id, name FROM items WHERE category = ? ORDER BY id`,
		args, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 10 {
		t.Errorf("expected total=10, got %d", p.Total)
	}
	if len(p.Items) != 5 {
		t.Errorf("expected 5 items, got %d", len(p.Items))
	}

	if len(args) != 1 {
		t.Errorf("caller args mutated: len=%d", len(args))
	}
}
