// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

type withheldDatabase struct {
	openOptions func() []dbutil.OpenOption
	migrate     func(ctx context.Context, db *dbutil.DB, logger *slog.Logger)
	leaderJobs  func(db *dbutil.DB) []leader.Job
}

var withheldDatabases []withheldDatabase

func withheldOpenOptions() []dbutil.OpenOption {
	var opts []dbutil.OpenOption
	for _, w := range withheldDatabases {
		opts = append(opts, w.openOptions()...)
	}
	return opts
}

func migrateWithheld(ctx context.Context, db *dbutil.DB, logger *slog.Logger) {
	for _, w := range withheldDatabases {
		w.migrate(ctx, db, logger)
	}
}

func withheldLeaderJobs(db *dbutil.DB) []leader.Job {
	var jobs []leader.Job
	for _, w := range withheldDatabases {
		jobs = append(jobs, w.leaderJobs(db)...)
	}
	return jobs
}
