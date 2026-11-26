// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func TestNextFireTime_SpringGapFiresJustAfterTheChangeInEveryZone(t *testing.T) {
	tests := []struct {
		name  string
		zone  string
		expr  string
		after time.Time
		want  func(loc *time.Location) time.Time
	}{
		{
			name: "Berlin 02:30 on the last Sunday of March", zone: "Europe/Berlin", expr: "30 2 * * *",
			after: time.Date(2027, 3, 27, 12, 0, 0, 0, time.UTC),
			want:  func(loc *time.Location) time.Time { return time.Date(2027, 3, 28, 3, 30, 0, 0, loc) },
		},
		{
			name: "London 01:30 on the last Sunday of March", zone: "Europe/London", expr: "30 1 * * *",
			after: time.Date(2027, 3, 27, 12, 0, 0, 0, time.UTC),
			want:  func(loc *time.Location) time.Time { return time.Date(2027, 3, 28, 2, 30, 0, 0, loc) },
		},
		{
			name: "Sydney 02:30 on the first Sunday of October", zone: "Australia/Sydney", expr: "30 2 * * *",
			after: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
			want:  func(loc *time.Location) time.Time { return time.Date(2026, 10, 4, 3, 30, 0, 0, loc) },
		},
		{
			name: "Auckland 02:30 on the last Sunday of September", zone: "Pacific/Auckland", expr: "30 2 * * *",
			after: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC),
			want:  func(loc *time.Location) time.Time { return time.Date(2026, 9, 27, 3, 30, 0, 0, loc) },
		},
		{
			name: "Lord Howe half-hour gap", zone: "Australia/Lord_Howe", expr: "15 2 * * *",
			after: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
			want:  func(loc *time.Location) time.Time { return time.Date(2026, 10, 4, 2, 45, 0, 0, loc) },
		},
		{
			name: "Cairo midnight gap", zone: "Africa/Cairo", expr: "0 0 * * *",
			after: time.Date(2027, 4, 29, 0, 0, 0, 0, time.UTC),
			want:  func(loc *time.Location) time.Time { return time.Date(2027, 4, 30, 1, 0, 0, 0, loc) },
		},
		{
			name: "New York keeps firing after the gap", zone: "America/New_York", expr: "30 2 * * *",
			after: time.Date(2027, 3, 13, 12, 0, 0, 0, time.UTC),
			want:  func(loc *time.Location) time.Time { return time.Date(2027, 3, 14, 3, 30, 0, 0, loc) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := NextFireTime(ScheduleTypeCron, tt.expr, tt.zone, time.Time{}, tt.after)
			if err != nil || !ok {
				t.Fatalf("NextFireTime = %v, ok=%v, err=%v", got, ok, err)
			}
			want := tt.want(loc)
			if !got.Equal(want) {
				t.Errorf("next = %s, want %s", got.In(loc), want)
			}
		})
	}
}
