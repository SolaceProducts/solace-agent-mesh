// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func fireSequence(t *testing.T, expr, tz string, start time.Time, n int) []time.Time {
	t.Helper()
	fires := make([]time.Time, 0, n)
	after := start
	for range n {
		next, ok, err := NextFireTime(ScheduleTypeCron, expr, tz, time.Time{}, after)
		if err != nil || !ok {
			t.Fatalf("NextFireTime(%q after %s) = %v, ok=%v, err=%v", expr, after, next, ok, err)
		}
		fires = append(fires, next)
		after = next
	}
	return fires
}

func TestNextFireTime_FallBackRepeatedHourFiresOncePerLocalDay(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, ny)

	fires := fireSequence(t, "30 1 * * *", "America/New_York", start, 3)

	want := []time.Time{
		time.Date(2026, 11, 1, 1, 30, 0, 0, ny),
		time.Date(2026, 11, 2, 1, 30, 0, 0, ny),
		time.Date(2026, 11, 3, 1, 30, 0, 0, ny),
	}
	for i := range want {
		if !fires[i].Equal(want[i]) {
			t.Errorf("fire %d = %s, want %s (all fires: %v)", i, fires[i], want[i], fires)
		}
	}
}

func TestNextFireTime_SpringForwardSkippedHourStillFiresThatDay(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2027, 3, 13, 12, 0, 0, 0, ny)

	fires := fireSequence(t, "30 2 * * *", "America/New_York", start, 2)

	if got := fires[0].In(ny); got.Year() != 2027 || got.Month() != time.March || got.Day() != 14 {
		t.Errorf("first fire = %s, want a fire on 2027-03-14 (the day the 02:00 hour does not exist); fires: %v", got, fires)
	}
}
