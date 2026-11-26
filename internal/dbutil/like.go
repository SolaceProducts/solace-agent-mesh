// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import "strings"

const likeEscapeChar = `\`

const likeEscapeClause = ` ESCAPE '` + likeEscapeChar + `'`

var likeEscaper = strings.NewReplacer(
	likeEscapeChar, likeEscapeChar+likeEscapeChar,
	"%", likeEscapeChar+"%",
	"_", likeEscapeChar+"_",
)

func escapeLike(s string) string {
	return likeEscaper.Replace(s)
}

func LikeContainsFold(col, needle string) (predicate string, arg any) {
	return "LOWER(" + col + ") LIKE LOWER(?)" + likeEscapeClause, "%" + escapeLike(needle) + "%"
}

func LikeContains(col, needle string) (predicate string, arg any) {
	return col + " LIKE ?" + likeEscapeClause, "%" + escapeLike(needle) + "%"
}
