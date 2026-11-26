// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"fmt"
	"strings"
)

func UpsertSQL(dialect, insertSQL, conflictCol string, updateCols []string) string {
	return UpsertWhereSQL(dialect, insertSQL, conflictCol, updateCols, "")
}

func UpsertWhereSQL(_, insertSQL, conflictCol string, updateCols []string, where string) string {
	sets := make([]string, len(updateCols))
	for i, c := range updateCols {
		sets[i] = fmt.Sprintf("%s = EXCLUDED.%s", c, c)
	}
	stmt := insertSQL + fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s",
		conflictCol, strings.Join(sets, ", "))
	if where != "" {
		stmt += " WHERE " + where
	}
	return stmt
}
