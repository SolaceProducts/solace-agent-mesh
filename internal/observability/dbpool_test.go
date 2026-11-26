// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRegisterDBPoolGauges(t *testing.T) {
	stats := map[string]sql.DBStats{
		"platform": {MaxOpenConnections: 65, InUse: 2, Idle: 3, WaitCount: 4, WaitDuration: 1500 * time.Millisecond, MaxIdleClosed: 5, MaxIdleTimeClosed: 6, MaxLifetimeClosed: 7},
		"notify":   {MaxOpenConnections: 10, Idle: 1, MaxIdleClosed: 8, MaxLifetimeClosed: 9},
	}
	tests := []struct {
		metric string
		want   []string
	}{
		{metric: "test.db.pool.connections", want: []string{
			"pool=notify state=idle 1", "pool=notify state=in_use 0",
			"pool=platform state=idle 3", "pool=platform state=in_use 2",
		}},
		{metric: "test.db.pool.max_open", want: []string{"pool=notify 10", "pool=platform 65"}},
		{metric: "test.db.pool.wait.count", want: []string{"pool=notify 0", "pool=platform 4"}},
		{metric: "test.db.pool.wait.seconds", want: []string{"pool=notify 0", "pool=platform 1.5"}},
		{metric: "test.db.pool.closed", want: []string{
			"pool=notify reason=max_idle 8", "pool=notify reason=max_idle_time 0", "pool=notify reason=max_lifetime 9",
			"pool=platform reason=max_idle 5", "pool=platform reason=max_idle_time 6", "pool=platform reason=max_lifetime 7",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.metric, func(t *testing.T) {
			t.Parallel()
			reg, reader := newTestRegistry(t)
			RegisterDBPoolGauges(reg, func() map[string]sql.DBStats { return stats })

			var got []string
			for _, dp := range collectGauge(t, reader, tc.metric) {
				var labels []string
				for _, kv := range dp.Attributes.ToSlice() {
					labels = append(labels, fmt.Sprintf("%s=%s", kv.Key, kv.Value.AsString()))
				}
				slices.Sort(labels)
				got = append(got, fmt.Sprintf("%s %g", strings.Join(labels, " "), dp.Value))
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s points = %q, want %q", tc.metric, got, tc.want)
			}
		})
	}
}
