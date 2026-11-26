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
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsTransient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"deadline exceeded", fmt.Errorf("read: %w", context.DeadlineExceeded), true},
		{"canceled", context.Canceled, true},
		{"net error", &net.OpError{Op: "dial", Err: errors.New("refused")}, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"bad conn", driver.ErrBadConn, true},
		{"conn done", sql.ErrConnDone, true},
		{"connection closed", pgconn.ErrConnClosed, true},
		{"wrapped connection closed", fmt.Errorf("query: %w", pgconn.ErrConnClosed), true},
		{"connect error", &pgconn.ConnectError{}, true},
		{"serialization failure", &pgconn.PgError{Code: "40001"}, true},
		{"lock not available", &pgconn.PgError{Code: "55P03"}, true},
		{"connection exception class", &pgconn.PgError{Code: "08006"}, true},
		{"invalid authorization class", &pgconn.PgError{Code: "28P01"}, true},
		{"insufficient resources class", &pgconn.PgError{Code: "53300"}, true},
		{"operator intervention class", &pgconn.PgError{Code: "57014"}, true},
		{"system error class", &pgconn.PgError{Code: "58030"}, true},
		{"wrapped pg error", fmt.Errorf("query: %w", &pgconn.PgError{Code: "57P01"}), true},
		{"plain error", errors.New("x"), false},
		{"undefined table", &pgconn.PgError{Code: "42P01"}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsTransient(tc.err); got != tc.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsBusy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"deadline exceeded", fmt.Errorf("read: %w", context.DeadlineExceeded), true},
		{"statement timeout", &pgconn.PgError{Code: "57014"}, true},
		{"serialization failure", &pgconn.PgError{Code: "40001"}, true},
		{"lock not available", &pgconn.PgError{Code: "55P03"}, true},
		{"too many connections", fmt.Errorf("query: %w", &pgconn.PgError{Code: "53300"}), true},
		{"canceled", context.Canceled, false},
		{"terminated session", &pgconn.PgError{Code: "57P01"}, false},
		{"disk full", &pgconn.PgError{Code: "53100"}, false},
		{"bad password", &pgconn.PgError{Code: "28P01"}, false},
		{"connection cut", io.ErrUnexpectedEOF, false},
		{"undefined table", &pgconn.PgError{Code: "42P01"}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsBusy(tc.err); got != tc.want {
				t.Errorf("IsBusy(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsRejected(t *testing.T) {
	cases := []struct {
		code     string
		rejected bool
	}{
		{"22001", true},
		{"22P02", true},
		{"23502", true},
		{"25006", false},
		{"40P01", false},
		{"42P01", false},
		{"08006", false},
		{"XX000", false},
	}
	for _, c := range cases {
		if got := IsRejected(&pgconn.PgError{Code: c.code}); got != c.rejected {
			t.Errorf("IsRejected(%s) = %v, want %v", c.code, got, c.rejected)
		}
	}
	if IsRejected(errors.New("plain")) || IsRejected(nil) {
		t.Error("a non-database error is not a rejection")
	}
}
