// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"errors"
	"testing"
	"time"
)

func at(year int, month time.Month, d, hour, minute int) time.Time {
	return time.Date(year, month, d, hour, minute, 0, 0, time.UTC)
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func window(start, end, dayEnd time.Time) Window {
	return Window{Start: ms(start), End: ms(end), DayEnd: ms(dayEnd)}
}

func dates(start, end time.Time) WindowRequest {
	return WindowRequest{StartDate: &start, EndDate: &end}
}

var midOctober = at(2026, time.October, 15, 19, 20)

func TestResolveWindow_IsWholeUTCDaysEndingNoLaterThanNow(t *testing.T) {
	now := midOctober
	for name, tc := range map[string]struct {
		req  WindowRequest
		want Window
	}{
		"no period is this month": {WindowRequest{}, window(at(2026, 10, 1, 0, 0), now, at(2026, 11, 1, 0, 0))},
		"this month, budgeted to the month's end": {
			WindowRequest{Period: PeriodThisMonth}, window(at(2026, 10, 1, 0, 0), now, at(2026, 11, 1, 0, 0)),
		},
		"last month":                           {WindowRequest{Period: PeriodLastMonth}, window(at(2026, 9, 1, 0, 0), at(2026, 10, 1, 0, 0), at(2026, 10, 1, 0, 0))},
		"last 7 days":                          {WindowRequest{Period: PeriodLast7Days}, window(at(2026, 10, 9, 0, 0), now, at(2026, 10, 16, 0, 0))},
		"last 30 days":                         {WindowRequest{Period: PeriodLast30Days}, window(at(2026, 9, 16, 0, 0), now, at(2026, 10, 16, 0, 0))},
		"last 90 days":                         {WindowRequest{Period: PeriodLast90Days}, window(at(2026, 7, 18, 0, 0), now, at(2026, 10, 16, 0, 0))},
		"a start mid-day":                      {dates(at(2026, 10, 3, 10, 0), at(2026, 10, 5, 12, 0)), window(at(2026, 10, 3, 0, 0), at(2026, 10, 6, 0, 0), at(2026, 10, 6, 0, 0))},
		"an end at midnight is that whole day": {dates(at(2026, 10, 3, 0, 0), at(2026, 10, 5, 0, 0)), window(at(2026, 10, 3, 0, 0), at(2026, 10, 6, 0, 0), at(2026, 10, 6, 0, 0))},
		"a single day":                         {dates(at(2026, 10, 5, 0, 0), at(2026, 10, 5, 0, 0)), window(at(2026, 10, 5, 0, 0), at(2026, 10, 6, 0, 0), at(2026, 10, 6, 0, 0))},
		"today, under way":                     {dates(at(2026, 10, 15, 0, 0), at(2026, 10, 15, 0, 0)), window(at(2026, 10, 15, 0, 0), now, at(2026, 10, 16, 0, 0))},
		"an end later this month, clamped to now": {
			dates(at(2026, 10, 1, 0, 0), at(2026, 10, 20, 0, 0)), window(at(2026, 10, 1, 0, 0), now, at(2026, 10, 21, 0, 0)),
		},
		"an end on this month's last day": {
			dates(at(2026, 10, 1, 0, 0), at(2026, 10, 31, 23, 59)), window(at(2026, 10, 1, 0, 0), now, at(2026, 11, 1, 0, 0)),
		},
		"the first retained day": {dates(at(2026, 7, 18, 0, 0), at(2026, 7, 18, 0, 0)), window(at(2026, 7, 18, 0, 0), at(2026, 7, 19, 0, 0), at(2026, 7, 19, 0, 0))},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveWindow(tc.req, now)
			if err != nil || got != tc.want {
				t.Errorf("window = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestResolveWindow_RefusesWhatItCannotAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		req   WindowRequest
		field string
	}{
		"both forms":            {WindowRequest{Period: PeriodLastMonth, StartDate: new(at(2026, 10, 1, 0, 0)), EndDate: new(at(2026, 10, 2, 0, 0))}, "period"},
		"a start alone":         {WindowRequest{StartDate: new(at(2026, 10, 1, 0, 0))}, "endDate"},
		"an end alone":          {WindowRequest{EndDate: new(at(2026, 10, 1, 0, 0))}, "startDate"},
		"an end before start":   {dates(at(2026, 10, 5, 0, 0), at(2026, 10, 3, 0, 0)), "endDate"},
		"an end the day before": {dates(at(2026, 10, 5, 0, 0), at(2026, 10, 4, 23, 59)), "endDate"},
		"a start in the future": {dates(at(2026, 10, 16, 0, 0), at(2026, 10, 20, 0, 0)), "startDate"},
		"before the history":    {dates(at(2026, 7, 17, 0, 0), at(2026, 7, 20, 0, 0)), "startDate"},
		"an end next month":     {dates(at(2026, 10, 1, 0, 0), at(2026, 11, 1, 0, 0)), "endDate"},
		"an end years ahead":    {dates(at(2026, 10, 1, 0, 0), at(9999, 12, 31, 0, 0)), "endDate"},
		"an unknown period":     {WindowRequest{Period: "last_week"}, "period"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveWindow(tc.req, midOctober)
			var invalid *ValidationError
			if !errors.As(err, &invalid) || invalid.Field != tc.field {
				t.Errorf("err = %v, want a validation error on %s", err, tc.field)
			}
		})
	}
}

func TestComparison_IsTheSameSpanBefore(t *testing.T) {
	for name, tc := range map[string]struct {
		req  WindowRequest
		now  time.Time
		want *Window
	}{
		"this month: the same day and time last month": {
			WindowRequest{}, midOctober,
			new(window(at(2026, 9, 1, 0, 0), at(2026, 9, 15, 19, 20), at(2026, 9, 16, 0, 0))),
		},
		"this month on the 31st: September has 30 days": {
			WindowRequest{}, at(2026, 10, 31, 8, 0),
			new(window(at(2026, 9, 1, 0, 0), at(2026, 9, 30, 8, 0), at(2026, 10, 1, 0, 0))),
		},
		"this month on March 31st: February has 28": {
			WindowRequest{}, at(2026, 3, 31, 8, 0),
			new(window(at(2026, 2, 1, 0, 0), at(2026, 2, 28, 8, 0), at(2026, 3, 1, 0, 0))),
		},
		"this month on December 31st": {
			WindowRequest{}, at(2026, 12, 31, 23, 59),
			new(window(at(2026, 11, 1, 0, 0), at(2026, 11, 30, 23, 59), at(2026, 12, 1, 0, 0))),
		},
		"this month in January: last December": {
			WindowRequest{}, at(2027, 1, 10, 6, 0),
			new(window(at(2026, 12, 1, 0, 0), at(2026, 12, 10, 6, 0), at(2026, 12, 11, 0, 0))),
		},
		"last month: the month before": {
			WindowRequest{Period: PeriodLastMonth}, midOctober,
			new(window(at(2026, 8, 1, 0, 0), at(2026, 9, 1, 0, 0), at(2026, 9, 1, 0, 0))),
		},
		"last 7 days: the 7 before, to this time of day": {
			WindowRequest{Period: PeriodLast7Days}, midOctober,
			new(window(at(2026, 10, 2, 0, 0), at(2026, 10, 8, 19, 20), at(2026, 10, 9, 0, 0))),
		},
		"last 30 days, across a month boundary": {
			WindowRequest{Period: PeriodLast30Days}, midOctober,
			new(window(at(2026, 8, 17, 0, 0), at(2026, 9, 15, 19, 20), at(2026, 9, 16, 0, 0))),
		},
		"last 90 days: before the history": {WindowRequest{Period: PeriodLast90Days}, midOctober, nil},
		"a past range: the days before it": {
			dates(at(2026, 10, 3, 0, 0), at(2026, 10, 5, 0, 0)), midOctober,
			new(window(at(2026, 9, 30, 0, 0), at(2026, 10, 3, 0, 0), at(2026, 10, 3, 0, 0))),
		},
		"a range that is a whole past month: the month before": {
			dates(at(2026, 9, 1, 0, 0), at(2026, 9, 30, 0, 0)), midOctober,
			new(window(at(2026, 8, 1, 0, 0), at(2026, 9, 1, 0, 0), at(2026, 9, 1, 0, 0))),
		},
		"a range that is this month: as This Month": {
			dates(at(2026, 10, 1, 0, 0), at(2026, 10, 31, 0, 0)), midOctober,
			new(window(at(2026, 9, 1, 0, 0), at(2026, 9, 15, 19, 20), at(2026, 9, 16, 0, 0))),
		},
		"this month at midnight on the 1st: both empty": {
			WindowRequest{}, at(2026, 11, 1, 0, 0),
			new(window(at(2026, 10, 1, 0, 0), at(2026, 10, 1, 0, 0), at(2026, 10, 1, 0, 0))),
		},
		"this month on March 31st of a leap year": {
			WindowRequest{}, at(2028, 3, 31, 8, 0),
			new(window(at(2028, 2, 1, 0, 0), at(2028, 2, 29, 8, 0), at(2028, 3, 1, 0, 0))),
		},
		"a now in another zone": {
			WindowRequest{}, midOctober.In(time.FixedZone("UTC-5", -5*60*60)),
			new(window(at(2026, 9, 1, 0, 0), at(2026, 9, 15, 19, 20), at(2026, 9, 16, 0, 0))),
		},
		"last month in January: last November": {
			WindowRequest{Period: PeriodLastMonth}, at(2027, 1, 10, 6, 0),
			new(window(at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0), at(2026, 12, 1, 0, 0))),
		},
		"last month on March 1st of a leap year: January": {
			WindowRequest{Period: PeriodLastMonth}, at(2028, 3, 1, 5, 0),
			new(window(at(2028, 1, 1, 0, 0), at(2028, 2, 1, 0, 0), at(2028, 2, 1, 0, 0))),
		},
		"two whole months: the two before start before the history": {
			dates(at(2026, 8, 1, 0, 0), at(2026, 9, 30, 0, 0)), at(2026, 10, 30, 0, 0), nil,
		},
		"a range whose comparison starts before the history": {dates(at(2026, 7, 20, 0, 0), at(2026, 8, 20, 0, 0)), midOctober, nil},
	} {
		t.Run(name, func(t *testing.T) {
			w, err := ResolveWindow(tc.req, tc.now)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			got := Comparison(w, tc.now)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Errorf("comparison = %+v, want %+v", got, tc.want)
			}
		})
	}
}
