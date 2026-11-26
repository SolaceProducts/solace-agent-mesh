// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"database/sql"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/jackc/pgx/v5"
)

const unnamedPool = "unnamed"

func WithPoolName(name string) OpenOption {
	return func(o *openOptions) { o.poolName = name }
}

func applicationName(pool string) string {
	name := filepath.Base(os.Args[0])
	if pool == "" {
		return name
	}
	return name + "/" + pool
}

func setApplicationName(cfg *pgx.ConnConfig, pool string) {
	if cfg.RuntimeParams["application_name"] != "" {
		return
	}
	cfg.RuntimeParams["application_name"] = applicationName(pool)
}

var openPools = struct {
	sync.Mutex
	names   map[*sql.DB]string
	retired map[string]sql.DBStats
}{names: map[*sql.DB]string{}, retired: map[string]sql.DBStats{}}

func trackPool(db *sql.DB, name string) {
	if name == "" {
		name = unnamedPool
	}
	openPools.Lock()
	defer openPools.Unlock()
	openPools.names[db] = name
}

func untrackPool(db *sql.DB) {
	openPools.Lock()
	defer openPools.Unlock()
	name, ok := openPools.names[db]
	if !ok {
		return
	}
	delete(openPools.names, db)
	openPools.retired[name] = addPoolStats(openPools.retired[name], runningTotals(db.Stats()))
}

func runningTotals(s sql.DBStats) sql.DBStats {
	s.MaxOpenConnections = 0
	s.OpenConnections = 0
	s.InUse = 0
	s.Idle = 0
	return s
}

func PoolStats() map[string]sql.DBStats {
	openPools.Lock()
	pools := maps.Clone(openPools.names)
	stats := make(map[string]sql.DBStats, len(pools))
	for _, name := range pools {
		stats[name] = openPools.retired[name]
	}
	openPools.Unlock()

	for db, name := range pools {
		stats[name] = addPoolStats(stats[name], db.Stats())
	}
	return stats
}

func addPoolStats(a, b sql.DBStats) sql.DBStats {
	return sql.DBStats{
		MaxOpenConnections: a.MaxOpenConnections + b.MaxOpenConnections,
		OpenConnections:    a.OpenConnections + b.OpenConnections,
		InUse:              a.InUse + b.InUse,
		Idle:               a.Idle + b.Idle,
		WaitCount:          a.WaitCount + b.WaitCount,
		WaitDuration:       a.WaitDuration + b.WaitDuration,
		MaxIdleClosed:      a.MaxIdleClosed + b.MaxIdleClosed,
		MaxIdleTimeClosed:  a.MaxIdleTimeClosed + b.MaxIdleTimeClosed,
		MaxLifetimeClosed:  a.MaxLifetimeClosed + b.MaxLifetimeClosed,
	}
}

func (db *DB) Close() error {
	untrackPool(db.DB)
	return db.DB.Close()
}
