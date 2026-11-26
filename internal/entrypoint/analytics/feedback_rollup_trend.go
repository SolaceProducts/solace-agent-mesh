// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type TrendGranularity string

const (
	TrendHour TrendGranularity = "hour"
	TrendDay  TrendGranularity = "day"
	TrendWeek TrendGranularity = "week"
	TrendNone TrendGranularity = "none"
)

const (
	rollupDayMs  int64 = store.DayBucketMs
	rollupHourMs int64 = store.HourBucketMs
)

type FeedbackRollupPoint struct {
	Date             string  `json:"date"`
	TotalActivities  *int    `json:"totalActivities,omitempty"`
	PositiveFeedback int     `json:"positiveFeedback"`
	NegativeFeedback int     `json:"negativeFeedback"`
	NegativeRatio    float64 `json:"negativeRatio"`
}

type bucketCounts struct {
	activities, positive, negative int
}

func (c bucketCounts) add(o bucketCounts) bucketCounts {
	return bucketCounts{c.activities + o.activities, c.positive + o.positive, c.negative + o.negative}
}

func (c bucketCounts) activityCount(ratingsOnly bool) *int {
	if ratingsOnly {
		return nil
	}
	return new(c.activities)
}

func (c bucketCounts) negativeRatio(ratingsOnly bool) float64 {
	if ratingsOnly {
		return ratio(c.negative, c.positive+c.negative)
	}
	return ratio(c.negative, c.activities)
}

func floorTo(ms, widthMs int64) int64 { return (ms / widthMs) * widthMs }

func sourceBucketMs(g TrendGranularity) int64 {
	if g == TrendHour {
		return rollupHourMs
	}
	return rollupDayMs
}

func bucketStart(srcStartMs int64, g TrendGranularity) int64 {
	if g != TrendWeek {
		return srcStartMs
	}
	d := time.UnixMilli(srcStartMs).UTC()
	sinceMonday := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -sinceMonday).UnixMilli()
}

func advance(t time.Time, g TrendGranularity) time.Time {
	switch g {
	case TrendHour:
		return t.Add(time.Hour)
	case TrendWeek:
		return t.AddDate(0, 0, 7)
	default:
		return t.AddDate(0, 0, 1)
	}
}

func buildTrend(byDay map[int64]bucketCounts, startMs, endMs int64, g TrendGranularity, ratingsOnly bool) []FeedbackRollupPoint {
	if g == TrendNone {
		return []FeedbackRollupPoint{}
	}
	sums := make(map[int64]bucketCounts, len(byDay))
	for day, c := range byDay {
		b := bucketStart(day, g)
		sums[b] = sums[b].add(c)
	}
	src := sourceBucketMs(g)
	last := bucketStart(floorTo(endMs, src), g)
	out := []FeedbackRollupPoint{}
	for t := time.UnixMilli(bucketStart(floorTo(startMs, src), g)).UTC(); t.UnixMilli() <= last; t = advance(t, g) {
		c := sums[t.UnixMilli()]
		out = append(out, FeedbackRollupPoint{
			Date:             epochMsToISO8601(t.UnixMilli()),
			TotalActivities:  c.activityCount(ratingsOnly),
			PositiveFeedback: c.positive,
			NegativeFeedback: c.negative,
			NegativeRatio:    c.negativeRatio(ratingsOnly),
		})
	}
	return out
}
