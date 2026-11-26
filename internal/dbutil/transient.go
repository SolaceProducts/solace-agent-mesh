// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
)

var transientSQLStateClasses = []string{"08", "28", "53", "57", "58"}

var transientSQLStates = []string{"40001", "55P03"}

func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	_, isNetErr := errors.AsType[net.Error](err)
	_, isConnectErr := errors.AsType[*pgconn.ConnectError](err)
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled),
		isNetErr, isConnectErr, errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, driver.ErrBadConn), errors.Is(err, sql.ErrConnDone), errors.Is(err, pgconn.ErrConnClosed):
		return true
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return false
	}
	state := pgErr.Code
	return slices.Contains(transientSQLStates, state) ||
		(len(state) >= 2 && slices.Contains(transientSQLStateClasses, state[:2]))
}

var rejectedSQLStateClasses = []string{"22", "23"}

func IsRejected(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && len(pgErr.Code) >= 2 && slices.Contains(rejectedSQLStateClasses, pgErr.Code[:2])
}

var busySQLStates = []string{"57014", "40001", "55P03", "53300"}

func IsBusy(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && slices.Contains(busySQLStates, pgErr.Code)
}
