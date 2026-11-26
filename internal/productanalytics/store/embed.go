// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "embed"

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationFS embed.FS
