// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestFirstRunAt(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 30, 0, 0, time.UTC)
	oneTime := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC).UnixMilli()
	before, after := oneTime-1, oneTime
	tests := []struct {
		name            string
		task            store.ScheduledTask
		fireImmediately bool
		want            *int64
	}{
		{name: "one-time not yet fired",
			task: store.ScheduledTask{ScheduleType: "one_time", ScheduleExpression: "2026-10-21T12:00:00Z"}, want: &oneTime},
		{name: "one-time last run before its time, edited to a new time",
			task: store.ScheduledTask{ScheduleType: "one_time", ScheduleExpression: "2026-10-21T12:00:00Z", LastRunAt: &before}, want: &oneTime},
		{name: "one-time already fired",
			task: store.ScheduledTask{ScheduleType: "one_time", ScheduleExpression: "2026-10-21T12:00:00Z", LastRunAt: &after}},
		{name: "new interval fires now",
			task: store.ScheduledTask{ScheduleType: "interval", ScheduleExpression: "1h"}, fireImmediately: true, want: ptrInt64(now.UnixMilli())},
		{name: "cron",
			task: store.ScheduledTask{ScheduleType: "cron", ScheduleExpression: "0 11 * * *", Timezone: "UTC"},
			want: ptrInt64(time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC).UnixMilli())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FirstRunAt(&tt.task, now, tt.fireImmediately)
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Fatalf("FirstRunAt = %v, want %v", deref(got), deref(tt.want))
			}
		})
	}

	if _, err := FirstRunAt(&store.ScheduledTask{ScheduleType: "cron", ScheduleExpression: "bad"}, now, false); err == nil {
		t.Fatal("FirstRunAt accepted a bad cron expression")
	}
}

func ptrInt64(v int64) *int64 { return &v }

func deref(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
