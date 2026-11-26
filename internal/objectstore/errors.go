// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"errors"
	"fmt"
)

type NotFoundError struct {
	Key string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("resource not found: %s", e.Key)
}

func IsNotFound(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}
