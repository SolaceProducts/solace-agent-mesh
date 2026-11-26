// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"embed"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationFS embed.FS

var migrations = dbutil.MigrationSet{
	FS:           migrationFS,
	VersionTable: "volume_goose_version",
	SQLiteDir:    "migrations/sqlite",
	PostgresDir:  "migrations/postgres",
}
