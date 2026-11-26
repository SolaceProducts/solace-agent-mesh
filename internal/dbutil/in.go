// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"errors"
	"fmt"
	"strings"
)

const InClauseCap = 1000

var ErrTooManyValues = errors.New("dbutil.InClause: too many values; cap exceeded")

func InClause[T any](values []T) (placeholders string, args []any, err error) {
	if len(values) == 0 {
		return "", nil, nil
	}
	if len(values) > InClauseCap {
		return "", nil, fmt.Errorf("%w: got %d, max %d", ErrTooManyValues, len(values), InClauseCap)
	}
	args = make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return strings.Repeat("?,", len(values)-1) + "?", args, nil
}
