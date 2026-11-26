// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type GatewaySlugConflictError struct {
	Slug string
}

func (e *GatewaySlugConflictError) Error() string {
	return fmt.Sprintf("entrypoint slug %q already exists", e.Slug)
}

func isSlugUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "ix_gateways_slug" {
		return true
	}
	return strings.Contains(err.Error(), "gateways.slug")
}
