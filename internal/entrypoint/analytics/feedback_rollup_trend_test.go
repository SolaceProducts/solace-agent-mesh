// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"reflect"
	"testing"
	"time"
)

const (
	trendDayMs int64 = 86_400_000
	trendDay0  int64 = 19675 * trendDayMs
)

func TestBucketStart_WeeksStartOnMonday(t *testing.T) {
	monday := trendDay0 - trendDayMs
	tests := []struct {
		name string
		day  int64
		want int64
	}{
		{"tuesday rolls back to monday", trendDay0, monday},
		{"monday is its own week start", monday, monday},
		{"sunday belongs to the week before", monday + 6*trendDayMs, monday},
		{"next monday starts a new week", monday + 7*trendDayMs, monday + 7*trendDayMs},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bucketStart(tc.day, TrendWeek); got != tc.want {
				t.Errorf("bucketStart = %d, want %d", got, tc.want)
			}
		})
	}
	if got := bucketStart(trendDay0, TrendDay); got != trendDay0 {
		t.Errorf("day granularity moved the bucket: %d", got)
	}
}

func TestBuildTrend_ZeroFillsQuietDays(t *testing.T) {
	byDay := map[int64]bucketCounts{
		trendDay0:                {activities: 10, positive: 1, negative: 1},
		trendDay0 + 2*trendDayMs: {activities: 5, negative: 1},
	}
	got := buildTrend(byDay, trendDay0, trendDay0+3*trendDayMs-1, TrendDay, false)
	want := []FeedbackRollupPoint{
		{Date: "2023-11-14T00:00:00.000000+00:00", TotalActivities: new(10), PositiveFeedback: 1, NegativeFeedback: 1, NegativeRatio: 0.1},
		{Date: "2023-11-15T00:00:00.000000+00:00", TotalActivities: new(0)},
		{Date: "2023-11-16T00:00:00.000000+00:00", TotalActivities: new(5), NegativeFeedback: 1, NegativeRatio: 0.2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestBuildTrend_RollsDaysIntoMondayWeeks(t *testing.T) {
	nextMonday := trendDay0 + 6*trendDayMs
	byDay := map[int64]bucketCounts{
		trendDay0:  {activities: 10, negative: 1},
		nextMonday: {activities: 10, negative: 3},
	}
	got := buildTrend(byDay, trendDay0, trendDay0+7*trendDayMs-1, TrendWeek, false)
	want := []FeedbackRollupPoint{
		{Date: "2023-11-13T00:00:00.000000+00:00", TotalActivities: new(10), NegativeFeedback: 1, NegativeRatio: 0.1},
		{Date: "2023-11-20T00:00:00.000000+00:00", TotalActivities: new(10), NegativeFeedback: 3, NegativeRatio: 0.3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestBuildTrend_RatioIsZeroWithoutActivity(t *testing.T) {
	got := buildTrend(map[int64]bucketCounts{trendDay0: {negative: 2}}, trendDay0, trendDay0, TrendDay, false)
	if len(got) != 1 || got[0].NegativeRatio != 0 || got[0].NegativeFeedback != 2 {
		t.Errorf("got %+v, want one point with 2 negatives and ratio 0", got)
	}
}

func TestBuildTrend_NoneIsEmpty(t *testing.T) {
	got := buildTrend(map[int64]bucketCounts{trendDay0: {activities: 1}}, trendDay0, trendDay0, TrendNone, false)
	if got == nil || len(got) != 0 {
		t.Errorf("got %#v, want empty non-nil slice", got)
	}
}

func TestBuildTrendHourBuckets(t *testing.T) {
	start := time.Date(2026, 9, 1, 14, 37, 0, 0, time.UTC).UnixMilli()
	end := start + 3*int64(time.Hour/time.Millisecond)
	h14 := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC).UnixMilli()
	h16 := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC).UnixMilli()

	got := buildTrend(map[int64]bucketCounts{
		h14: {activities: 10, negative: 1},
		h16: {activities: 4, negative: 2},
	}, start, end, TrendHour, false)

	if len(got) != 4 {
		t.Fatalf("want 4 hourly buckets covering 14:00-17:00, got %d: %+v", len(got), got)
	}
	if got[0].Date != "2026-09-01T14:00:00.000000+00:00" {
		t.Fatalf("first bucket: want an ISO 8601 hour start, got %q", got[0].Date)
	}
	if *got[1].TotalActivities != 0 || got[1].NegativeFeedback != 0 {
		t.Fatalf("middle hour should be zero-filled, got %+v", got[1])
	}
	if got[2].NegativeRatio != 0.5 {
		t.Fatalf("third hour: want ratio 0.5, got %v", got[2].NegativeRatio)
	}
}

func TestBuildTrendEmitsISO8601AtEveryGranularity(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, tc := range []struct {
		g    TrendGranularity
		want string
	}{
		{TrendHour, "2026-09-01T00:00:00.000000+00:00"},
		{TrendDay, "2026-09-01T00:00:00.000000+00:00"},
		{TrendWeek, "2026-08-31T00:00:00.000000+00:00"},
	} {
		got := buildTrend(map[int64]bucketCounts{}, start, start, tc.g, false)
		if len(got) == 0 {
			t.Fatalf("%s: no buckets", tc.g)
		}
		if got[0].Date != tc.want {
			t.Fatalf("%s: want %q, got %q", tc.g, tc.want, got[0].Date)
		}
	}
}

func TestBuildTrendWeekStaysMondayAligned(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := start + 21*int64(24*time.Hour/time.Millisecond)
	got := buildTrend(map[int64]bucketCounts{}, start, end, TrendWeek, false)
	if len(got) != 4 {
		t.Fatalf("want 4 week buckets, got %d: %+v", len(got), got)
	}
	for _, p := range got {
		d, err := time.Parse("2006-01-02T15:04:05.000000-07:00", p.Date)
		if err != nil {
			t.Fatalf("unparseable date %q: %v", p.Date, err)
		}
		if d.Weekday() != time.Monday {
			t.Fatalf("bucket %q is a %s, want Monday", p.Date, d.Weekday())
		}
	}
}
