// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"database/sql"
	"time"
)

const dbPoolStatsTTL = 2 * time.Second

func RegisterDBPoolGauges(reg *Registry, pools func() map[string]sql.DBStats) {
	if reg == nil || pools == nil {
		return
	}
	memo := NewTTLMemo(dbPoolStatsTTL, func() (map[string]sql.DBStats, error) {
		return pools(), nil
	})
	each := func(observe func(pool string, s sql.DBStats) []Observation) GaugeFunc {
		return func(context.Context) []Observation {
			stats, _ := memo.Value()
			var obs []Observation
			for pool, s := range stats {
				obs = append(obs, observe(pool, s)...)
			}
			return obs
		}
	}

	reg.ObservableGauge("db.pool.connections", each(func(pool string, s sql.DBStats) []Observation {
		return []Observation{
			NewObservation(float64(s.InUse), "pool", pool, "state", "in_use"),
			NewObservation(float64(s.Idle), "pool", pool, "state", "idle"),
		}
	}))
	reg.ObservableGauge("db.pool.max_open", each(func(pool string, s sql.DBStats) []Observation {
		return []Observation{NewObservation(float64(s.MaxOpenConnections), "pool", pool)}
	}))
	reg.ObservableGauge("db.pool.wait.count", each(func(pool string, s sql.DBStats) []Observation {
		return []Observation{NewObservation(float64(s.WaitCount), "pool", pool)}
	}))
	reg.ObservableGauge("db.pool.wait.seconds", each(func(pool string, s sql.DBStats) []Observation {
		return []Observation{NewObservation(s.WaitDuration.Seconds(), "pool", pool)}
	}))
	reg.ObservableGauge("db.pool.closed", each(func(pool string, s sql.DBStats) []Observation {
		return []Observation{
			NewObservation(float64(s.MaxIdleClosed), "pool", pool, "reason", "max_idle"),
			NewObservation(float64(s.MaxIdleTimeClosed), "pool", pool, "reason", "max_idle_time"),
			NewObservation(float64(s.MaxLifetimeClosed), "pool", pool, "reason", "max_lifetime"),
		}
	}))
}
