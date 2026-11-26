// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func TestNextFireTime_MonthlyWeekdayForms(t *testing.T) {
	zone := func(name string) *time.Location {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		return loc
	}
	ny, tokyo, santiago := zone("America/New_York"), zone("Asia/Tokyo"), zone("America/Santiago")
	utc := func(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, time.UTC) }

	tests := []struct {
		name  string
		expr  string
		tz    string
		after time.Time
		want  time.Time
	}{
		{name: "first Monday", expr: "0 9 * * 1#1",
			after: utc(2026, 1, 1, 0, 0), want: utc(2026, 1, 5, 9, 0)},
		{name: "first Monday after firing moves to next month", expr: "0 9 * * 1#1",
			after: utc(2026, 1, 5, 9, 0), want: utc(2026, 2, 2, 9, 0)},
		{name: "second Sunday", expr: "30 8 * * 0#2",
			after: utc(2026, 2, 1, 0, 0), want: utc(2026, 2, 8, 8, 30)},
		{name: "fourth Thursday", expr: "0 12 * * 4#4",
			after: utc(2026, 11, 1, 0, 0), want: utc(2026, 11, 26, 12, 0)},
		{name: "later hour on the same matching day", expr: "0 9,17 * * 1#1",
			after: utc(2026, 1, 5, 9, 0), want: utc(2026, 1, 5, 17, 0)},
		{name: "fifth Friday skips months that have only four", expr: "0 9 * * 5#5",
			after: utc(2026, 1, 31, 0, 0), want: utc(2026, 5, 29, 9, 0)},
		{name: "month field still restricts", expr: "0 9 * 1,4,7,10 1#1",
			after: utc(2026, 1, 5, 10, 0), want: utc(2026, 4, 6, 9, 0)},
		{name: "question mark day-of-month", expr: "0 9 ? * 1#1",
			after: utc(2026, 1, 1, 0, 0), want: utc(2026, 1, 5, 9, 0)},
		{name: "last Friday in a 28-day month", expr: "0 17 * * 5L",
			after: utc(2026, 2, 1, 0, 0), want: utc(2026, 2, 27, 17, 0)},
		{name: "last Friday in a 31-day month", expr: "0 17 * * 5L",
			after: utc(2026, 2, 27, 17, 0), want: utc(2026, 3, 27, 17, 0)},
		{name: "last Friday in a 30-day month", expr: "0 17 * * 5L",
			after: utc(2026, 3, 27, 17, 0), want: utc(2026, 4, 24, 17, 0)},
		{name: "last Saturday on the 28th", expr: "0 9 * * 6L",
			after: utc(2026, 2, 1, 0, 0), want: utc(2026, 2, 28, 9, 0)},
		{name: "last Thursday on the 30th", expr: "0 9 * * 4L",
			after: utc(2026, 4, 1, 0, 0), want: utc(2026, 4, 30, 9, 0)},
		{name: "last Sunday on the 31st", expr: "0 9 * * 0L",
			after: utc(2026, 5, 1, 0, 0), want: utc(2026, 5, 31, 9, 0)},
		{name: "last Tuesday on a leap day", expr: "0 9 * * 2L",
			after: utc(2028, 2, 1, 0, 0), want: utc(2028, 2, 29, 9, 0)},
		{name: "lowercase last", expr: "0 9 * * 5l",
			after: utc(2026, 2, 1, 0, 0), want: utc(2026, 2, 27, 9, 0)},
		{name: "local date differs from the UTC date", expr: "30 22 * * 5L", tz: "America/New_York",
			after: utc(2026, 1, 1, 0, 0), want: time.Date(2026, 1, 30, 22, 30, 0, 0, ny)},
		{name: "local date ahead of the UTC date", expr: "0 7 * * 1#1", tz: "Asia/Tokyo",
			after: utc(2026, 1, 31, 0, 0), want: time.Date(2026, 2, 2, 7, 0, 0, 0, tokyo)},
		{name: "first Sunday on the autumn clock change", expr: "0 9 * * 0#1", tz: "America/New_York",
			after: utc(2026, 10, 31, 0, 0), want: time.Date(2026, 11, 1, 9, 0, 0, 0, ny)},
		{name: "first Sunday in a zone whose clock change skips midnight", expr: "0 9 * * 0#1", tz: "America/Santiago",
			after: utc(2026, 9, 1, 0, 0), want: time.Date(2026, 9, 6, 9, 0, 0, 0, santiago)},
		{name: "first Monday at midnight", expr: "0 0 * * 1#1",
			after: utc(2026, 1, 1, 0, 0), want: utc(2026, 1, 5, 0, 0)},
		{name: "fifth Sunday of February recurs 28 years later", expr: "0 9 * 2 0#5",
			after: utc(2032, 2, 29, 9, 0), want: utc(2060, 2, 29, 9, 0)},
		{name: "fifth Sunday of February recurs 40 years later across a non-leap century", expr: "0 9 * 2 0#5",
			after: utc(2088, 2, 29, 9, 0), want: utc(2128, 2, 29, 9, 0)},
		{name: "fifth Sunday of February from the start of a year without one", expr: "0 9 * 2 0#5",
			after: utc(2033, 1, 1, 0, 0), want: utc(2060, 2, 29, 9, 0)},
		{name: "fifth Monday of February recurs 28 years later", expr: "0 9 * 2 1#5",
			after: utc(2044, 2, 29, 9, 0), want: utc(2072, 2, 29, 9, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fires, err := NextFireTime(ScheduleTypeCron, tt.expr, tt.tz, time.Time{}, tt.after)
			if err != nil {
				t.Fatal(err)
			}
			if !fires || !got.Equal(tt.want) {
				t.Fatalf("NextFireTime(%q) = %s, %v; want %s", tt.expr, got, fires, tt.want)
			}
		})
	}
}

func TestPreviewNextTimes_MonthlyWeekdayFiresOncePerMonth(t *testing.T) {
	times, err := PreviewNextTimes(ScheduleTypeCron, "0 9 * * 3L", "Europe/Berlin", 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 6 {
		t.Fatalf("got %d times, want 6", len(times))
	}
	for i, at := range times {
		if at.Weekday() != time.Wednesday || at.AddDate(0, 0, 7).Month() == at.Month() {
			t.Fatalf("time %d = %s is not the last Wednesday of its month", i, at)
		}
		if i > 0 && at.Month() == times[i-1].Month() {
			t.Fatalf("times %s and %s fall in the same month", times[i-1], at)
		}
	}
}

func TestValidateExpression_MonthlyWeekdayForms(t *testing.T) {
	tests := []struct {
		expr    string
		wantErr bool
	}{
		{expr: "0 9 * * 1#1"},
		{expr: "0 9 * * 0#5"},
		{expr: "0 9 * * 6L"},
		{expr: "15 8 * 3,6,9,12 2#3"},
		{expr: "0 9 * * 8#1", wantErr: true},
		{expr: "0 9 * * 7#1", wantErr: true},
		{expr: "0 9 * * 1#0", wantErr: true},
		{expr: "0 9 * * 1#6", wantErr: true},
		{expr: "0 9 * * #", wantErr: true},
		{expr: "0 9 * * #1", wantErr: true},
		{expr: "0 9 * * 1#", wantErr: true},
		{expr: "0 9 * * 1#1#2", wantErr: true},
		{expr: "0 9 * * L", wantErr: true},
		{expr: "0 9 * * 1LL", wantErr: true},
		{expr: "0 9 * * xL", wantErr: true},
		{expr: "0 9 * * 8L", wantErr: true},
		{expr: "0 9 * * 1#1,3#3", wantErr: true},
		{expr: "0 9 * * 1,3#2", wantErr: true},
		{expr: "0 9 * * 1-5L", wantErr: true},
		{expr: "0 9 * * */2#1", wantErr: true},
		{expr: "0 9 15 * 1#1", wantErr: true},
		{expr: "0 9 1-7 * 5L", wantErr: true},
		{expr: "99 9 * * 1#1", wantErr: true},
		{expr: "0 9 * * 1#1 2026", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			err := ValidateExpression(ScheduleTypeCron, tt.expr, "UTC")
			if tt.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
