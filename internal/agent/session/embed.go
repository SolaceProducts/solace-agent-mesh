// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"embed"
	"io/fs"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrationFS embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrationFS embed.FS

func sqliteMigrations() fs.FS {
	sub, err := fs.Sub(sqliteMigrationFS, "migrations/sqlite")
	if err != nil {
		panic("embedded sqlite migration FS: " + err.Error())
	}
	return sub
}

func postgresMigrations() fs.FS {
	sub, err := fs.Sub(postgresMigrationFS, "migrations/postgres")
	if err != nil {
		panic("embedded postgres migration FS: " + err.Error())
	}
	return sub
}
