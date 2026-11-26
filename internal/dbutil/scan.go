// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

var fieldCache sync.Map

type fieldMapping struct {
	index  int
	kind   reflect.Kind
	isBool bool
}

func buildFieldMap(t reflect.Type) (map[string]fieldMapping, error) {
	m := make(map[string]fieldMapping, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)

		if f.Anonymous {
			return nil, fmt.Errorf("dbutil: embedded struct not supported (flat structs only)")
		}
		if !f.IsExported() {
			continue
		}

		tag := f.Tag.Get("db")
		if tag == "-" {
			continue
		}

		fk := f.Type.Kind()
		if fk == reflect.Pointer {
			fk = f.Type.Elem().Kind()
		}
		if fk == reflect.Struct {
			if !strings.HasPrefix(f.Type.Name(), "Null") {
				return nil, fmt.Errorf("dbutil: nested struct field %q not supported", f.Name)
			}
		}

		col := tag
		if col == "" {
			col = toSnakeCase(f.Name)
		}
		m[col] = fieldMapping{index: i, kind: f.Type.Kind(), isBool: fk == reflect.Bool}
	}
	return m, nil
}

func getFieldMap(t reflect.Type) (map[string]fieldMapping, error) {
	if v, ok := fieldCache.Load(t); ok {
		return v.(map[string]fieldMapping), nil
	}
	m, err := buildFieldMap(t)
	if err != nil {
		return nil, err
	}
	fieldCache.Store(t, m)
	return m, nil
}

func ScanRows[T any](rows *sql.Rows) ([]T, error) {
	defer func() { _ = rows.Close() }()

	var zero T
	t := reflect.TypeOf(zero)
	fm, err := getFieldMap(t)
	if err != nil {
		return nil, err
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []T
	dests := make([]any, len(cols))
	boolCols := 0
	for _, col := range cols {
		if mapping, ok := fm[col]; ok && mapping.isBool {
			boolCols++
		}
	}
	boolSlots := make([]int, 0, boolCols)
	boolTemps := make([]nullBool, 0, boolCols)

	for rows.Next() {
		var item T
		v := reflect.ValueOf(&item).Elem()
		boolSlots = boolSlots[:0]
		boolTemps = boolTemps[:0]

		for i, col := range cols {
			mapping, ok := fm[col]
			if !ok {
				var discard any
				dests[i] = &discard
				continue
			}

			field := v.Field(mapping.index)
			if mapping.isBool {
				boolTemps = append(boolTemps, nullBool{})
				boolSlots = append(boolSlots, mapping.index)
				dests[i] = &boolTemps[len(boolTemps)-1]
			} else {
				dests[i] = field.Addr().Interface()
			}
		}

		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}

		for j, idx := range boolSlots {
			field := v.Field(idx)
			tmp := boolTemps[j]
			if field.Kind() == reflect.Pointer {
				if !tmp.Valid {
					continue
				}
				bp := reflect.New(field.Type().Elem())
				bp.Elem().SetBool(tmp.Bool)
				field.Set(bp)
			} else {
				field.SetBool(tmp.Valid && tmp.Bool)
			}
		}

		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if results == nil {
		return []T{}, nil
	}
	return results, nil
}

func QueryRow[T any](ctx context.Context, q Querier, query string, args ...any) (*T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	items, err := ScanRows[T](rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func QueryRows[T any](ctx context.Context, q Querier, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return ScanRows[T](rows)
}

func ScanRowPtrs[T any](rows *sql.Rows) ([]*T, error) {
	items, err := ScanRows[T](rows)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*T, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	return ptrs, nil
}

func QueryRowPtrs[T any](ctx context.Context, q Querier, query string, args ...any) ([]*T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return ScanRowPtrs[T](rows)
}

func QueryVal[T any](ctx context.Context, q Querier, query string, args ...any) (T, error) {
	var val T
	err := q.QueryRowContext(ctx, query, args...).Scan(&val)
	if err == sql.ErrNoRows {
		return val, nil
	}
	return val, err
}

func QueryVals[T any](ctx context.Context, q Querier, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var vals []T
	for rows.Next() {
		var v T
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return vals, rows.Err()
}

type nullBool struct {
	Bool  bool
	Valid bool
}

func (n *nullBool) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		n.Bool, n.Valid = false, false
		return nil
	case bool:
		n.Bool, n.Valid = v, true
		return nil
	case int64:
		n.Bool, n.Valid = v != 0, true
		return nil
	case float64:
		if v != math.Trunc(v) {
			return fmt.Errorf("dbutil: cannot scan non-integral %v into bool", v)
		}
		n.Bool, n.Valid = v != 0, true
		return nil
	case []byte:
		return n.parse(string(v))
	case string:
		return n.parse(v)
	default:
		return fmt.Errorf("dbutil: cannot scan %T into bool", src)
	}
}

func (n *nullBool) parse(s string) error {
	if b, err := strconv.ParseBool(s); err == nil {
		n.Bool, n.Valid = b, true
		return nil
	}
	i, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return fmt.Errorf("dbutil: cannot scan %q into bool: %w", s, err)
	}
	n.Bool, n.Valid = i != 0, true
	return nil
}

func toSnakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				if unicode.IsLower(prev) {
					b.WriteByte('_')
				} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
