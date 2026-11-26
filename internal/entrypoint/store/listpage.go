// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func listPageWithDerivedTotal[T any](ctx context.Context, db *DB, columns, table, where, orderBy string, args []any, limit, offset int, errPrefix string) ([]*T, int, error) {
	rows, err := dbutil.QueryRowPtrs[T](ctx, db,
		`SELECT `+columns+` FROM `+table+` `+where+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("%s query: %w", errPrefix, err)
	}

	total := offset + len(rows)
	fullPage := limit > 0 && len(rows) == limit
	emptyPastOffset := len(rows) == 0 && offset > 0
	if fullPage || emptyPastOffset {
		total, err = dbutil.QueryVal[int](ctx, db, `SELECT COUNT(*) FROM `+table+` `+where, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("%s count: %w", errPrefix, err)
		}
	}
	return rows, total, nil
}

type pageQuery struct {
	columns, table, where, orderBy string
	args                           []any
	limit, offset                  int
	errPrefix                      string
}

func listPageWithHasMore[T any](ctx context.Context, db *DB, q pageQuery) ([]*T, bool, error) {
	fetch := q.limit
	if q.limit > 0 {
		fetch = q.limit + 1
	}
	rows, err := dbutil.QueryRowPtrs[T](ctx, db,
		`SELECT `+q.columns+` FROM `+q.table+` `+q.where+` ORDER BY `+q.orderBy+` LIMIT ? OFFSET ?`,
		append(q.args, fetch, q.offset)...)
	if err != nil {
		return nil, false, fmt.Errorf("%s query: %w", q.errPrefix, err)
	}
	hasMore := q.limit > 0 && len(rows) > q.limit
	if hasMore {
		rows = rows[:q.limit]
	}
	return rows, hasMore, nil
}
