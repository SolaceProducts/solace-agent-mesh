// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"reflect"
)

type Page[T any] struct {
	Items []T
	Total int
}

func Paginate[T any](ctx context.Context, q Querier, dataSQL string,
	args []any, page, size int) (*Page[T], error) {

	if page < 1 {
		page = 1
	}
	offset := (page - 1) * size
	dataArgs := make([]any, len(args), len(args)+2)
	copy(dataArgs, args)
	dataArgs = append(dataArgs, size, offset)

	wrappedSQL := "SELECT *, COUNT(*) OVER() AS _total FROM (" +
		dataSQL + ") _sub LIMIT ? OFFSET ?"

	rows, err := q.QueryContext(ctx, wrappedSQL, dataArgs...)
	if err != nil {
		return nil, err
	}

	items, total, err := scanRowsWithTotal[T](rows)
	if err != nil {
		return nil, err
	}
	return &Page[T]{Items: items, Total: total}, nil
}

func scanRowsWithTotal[T any](rows *sql.Rows) ([]T, int, error) {
	defer func() { _ = rows.Close() }()

	var zero T
	t := reflect.TypeOf(zero)
	fm, err := getFieldMap(t)
	if err != nil {
		return nil, 0, err
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}

	structCols := cols[:len(cols)-1]

	var total int
	var results []T

	dests := make([]any, len(cols))
	boolSlots := make([]int, 0, 4)
	boolTemps := make([]sql.NullInt64, 0, 4)

	for rows.Next() {
		var item T
		v := reflect.ValueOf(&item).Elem()
		boolSlots = boolSlots[:0]
		boolTemps = boolTemps[:0]

		for i, col := range structCols {
			mapping, ok := fm[col]
			if !ok {
				var discard any
				dests[i] = &discard
				continue
			}

			field := v.Field(mapping.index)
			if mapping.isBool {
				boolTemps = append(boolTemps, sql.NullInt64{})
				boolSlots = append(boolSlots, mapping.index)
				dests[i] = &boolTemps[len(boolTemps)-1]
			} else {
				dests[i] = field.Addr().Interface()
			}
		}

		dests[len(cols)-1] = &total

		if err := rows.Scan(dests...); err != nil {
			return nil, 0, err
		}

		for j, idx := range boolSlots {
			field := v.Field(idx)
			tmp := boolTemps[j]
			if field.Kind() == reflect.Pointer {
				if !tmp.Valid {
					continue
				}
				bp := reflect.New(field.Type().Elem())
				bp.Elem().SetBool(tmp.Int64 != 0)
				field.Set(bp)
			} else {
				field.SetBool(tmp.Valid && tmp.Int64 != 0)
			}
		}

		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if results == nil {
		return []T{}, 0, nil
	}
	return results, total, nil
}
