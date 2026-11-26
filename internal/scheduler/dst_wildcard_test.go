// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func TestNextFireTime_WildcardHoursKeepFiringThroughTheRepeatedHour(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		expr  string
		after time.Time
		want  time.Time
	}{
		{
			name:  "every minute continues into the second 01:00 hour",
			expr:  "* * * * *",
			after: time.Date(2026, 11, 1, 1, 59, 0, 0, ny),
			want:  time.Date(2026, 11, 1, 1, 0, 0, 0, ny).Add(time.Hour),
		},
		{
			name:  "hourly fires at the second 01:00",
			expr:  "0 * * * *",
			after: time.Date(2026, 11, 1, 1, 0, 0, 0, ny),
			want:  time.Date(2026, 11, 1, 1, 0, 0, 0, ny).Add(time.Hour),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := NextFireTime(ScheduleTypeCron, tt.expr, "America/New_York", time.Time{}, tt.after)
			if err != nil || !ok {
				t.Fatalf("NextFireTime = %v, ok=%v, err=%v", got, ok, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("next = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNextFireTime_FixedHourSkippedByTheSpringChangeFiresJustAfterIt(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2027, 3, 13, 12, 0, 0, 0, ny)

	got, ok, err := NextFireTime(ScheduleTypeCron, "30 2 * * *", "America/New_York", time.Time{}, after)
	if err != nil || !ok {
		t.Fatalf("NextFireTime = %v, ok=%v, err=%v", got, ok, err)
	}

	want := time.Date(2027, 3, 14, 3, 30, 0, 0, ny)
	if !got.Equal(want) {
		t.Errorf("next = %s, want %s", got, want)
	}
}

func TestNextFireTime_FixedHourScheduleIgnoresAnAfterInsideTheRepeatedHour(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	secondOccurrence := time.Date(2026, 11, 1, 1, 0, 0, 0, ny).Add(time.Hour).Add(15 * time.Minute)

	got, ok, err := NextFireTime(ScheduleTypeCron, "30 1 * * *", "America/New_York", time.Time{}, secondOccurrence)
	if err != nil || !ok {
		t.Fatalf("NextFireTime = %v, ok=%v, err=%v", got, ok, err)
	}

	want := time.Date(2026, 11, 2, 1, 30, 0, 0, ny)
	if !got.Equal(want) {
		t.Errorf("next = %s, want %s (no second 01:30 on the transition day)", got, want)
	}
}
