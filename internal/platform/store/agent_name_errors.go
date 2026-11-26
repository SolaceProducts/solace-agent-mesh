// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var agentNameConstraintsPostgres = []string{
	"agents_name_key",
	"idx_agents_name_normalized",
	"a2a_agents_name_key",
	"idx_a2a_agents_name_normalized",
}

var agentNameConstraintsSQLite = []string{
	"agents.name",
	"a2a_agents.name",
	"index 'idx_agents_name_normalized'",
	"index 'idx_a2a_agents_name_normalized'",
}

func IsAgentNameViolation(err error) bool {
	if !dbutil.IsUniqueViolation(err) {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return slices.Contains(agentNameConstraintsPostgres, pgErr.ConstraintName)
	}
	message := err.Error()
	return slices.ContainsFunc(agentNameConstraintsSQLite, func(ref string) bool { return strings.Contains(message, ref) })
}
