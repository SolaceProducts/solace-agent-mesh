// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	evidenceStore    = "the evidence store"
	platformDatabase = "the platform database"
)

func withoutConnectDetail(store string, err error) error {
	if _, ok := errors.AsType[*pgconn.ConnectError](err); !ok {
		return err
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return fmt.Errorf("connect to %s: sqlstate %s", store, pgErr.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("connect to %s: %w", store, context.DeadlineExceeded)
	}
	return fmt.Errorf("connect to %s failed", store)
}
