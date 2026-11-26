// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const unknownScheduleTypeFormat = "unknown schedule type %q"

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

var embeddedTimezonePrefixes = []string{"CRON_TZ=", "TZ="}

func hasEmbeddedTimezone(expression string) bool {
	return slices.ContainsFunc(embeddedTimezonePrefixes, func(prefix string) bool {
		return strings.HasPrefix(expression, prefix)
	})
}

func ParseCron(expression string) (cron.Schedule, error) {
	if hasEmbeddedTimezone(expression) && !strings.Contains(expression, " ") {
		return nil, fmt.Errorf("invalid cron expression %q: an embedded timezone must be followed by the schedule", expression)
	}
	sched, isMonthlyWeekday, err := parseMonthlyWeekday(expression)
	if !isMonthlyWeekday {
		sched, err = cronParser.Parse(expression)
	}
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expression, err)
	}
	return sched, nil
}

func parseAuthoredCron(expression string) (cron.Schedule, error) {
	if hasEmbeddedTimezone(expression) {
		return nil, fmt.Errorf("invalid cron expression %q: set the task timezone instead of embedding one in the expression", expression)
	}
	return ParseCron(expression)
}

func ParseInterval(expression string) (time.Duration, error) {
	d, err := parseDuration(expression)
	if err != nil {
		return 0, fmt.Errorf("invalid interval expression %q: %w", expression, err)
	}
	if d < minInterval {
		return 0, fmt.Errorf("interval %s is below minimum of %s", d, minInterval)
	}
	return d, nil
}

func ParseOneTime(expression, timezone string) (time.Time, error) {
	loc, err := loadTimezone(timezone)
	if err != nil {
		return time.Time{}, err
	}

	t, err := parseISO8601(expression, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid one-time expression %q: %w", expression, err)
	}
	return t, nil
}

func ValidateExpression(scheduleType ScheduleType, expression, timezone string) error {
	if _, err := loadTimezone(timezone); err != nil {
		return err
	}

	switch scheduleType {
	case ScheduleTypeCron:
		sched, err := parseAuthoredCron(expression)
		if err != nil {
			return err
		}
		if sched.Next(time.Now()).IsZero() {
			return fmt.Errorf("cron expression %q never fires", expression)
		}
		return nil
	case ScheduleTypeInterval:
		_, err := ParseInterval(expression)
		return err
	case ScheduleTypeOneTime:
		_, err := ParseOneTime(expression, timezone)
		return err
	default:
		return fmt.Errorf(unknownScheduleTypeFormat, scheduleType)
	}
}

func ValidateStoredExpression(scheduleType ScheduleType, expression, timezone string) error {
	if _, err := loadTimezone(timezone); err != nil {
		return err
	}
	if scheduleType == ScheduleTypeCron {
		_, err := ParseCron(expression)
		return err
	}
	return ValidateExpression(scheduleType, expression, timezone)
}

func PreviewNextTimes(scheduleType ScheduleType, expression, timezone string, count int) ([]time.Time, error) {
	return previewNextTimesFrom(time.Now(), scheduleType, expression, timezone, count)
}

func previewNextTimesFrom(now time.Time, scheduleType ScheduleType, expression, timezone string, count int) ([]time.Time, error) {
	if count <= 0 {
		return nil, nil
	}

	loc, err := loadTimezone(timezone)
	if err != nil {
		return nil, err
	}

	switch scheduleType {
	case ScheduleTypeCron:
		sched, err := parseAuthoredCron(expression)
		if err != nil {
			return nil, err
		}
		times := make([]time.Time, 0, count)
		t := now.In(loc)
		for range count {
			t = nextCronFire(expression, sched, t, loc)
			if t.IsZero() {
				break
			}
			times = append(times, t)
		}
		return times, nil

	case ScheduleTypeInterval:
		d, err := ParseInterval(expression)
		if err != nil {
			return nil, err
		}
		times := make([]time.Time, 0, count)
		t := now.In(loc)
		for range count {
			t = t.Add(d)
			times = append(times, t)
		}
		return times, nil

	case ScheduleTypeOneTime:
		t, err := ParseOneTime(expression, timezone)
		if err != nil {
			return nil, err
		}
		return []time.Time{t}, nil

	default:
		return nil, fmt.Errorf(unknownScheduleTypeFormat, scheduleType)
	}
}

func NextFireTime(scheduleType ScheduleType, expression, timezone string, anchor, after time.Time) (time.Time, bool, error) {
	loc, err := loadTimezone(timezone)
	if err != nil {
		return time.Time{}, false, err
	}
	switch scheduleType {
	case ScheduleTypeCron:
		sched, err := ParseCron(expression)
		if err != nil {
			return time.Time{}, false, err
		}
		next := nextCronFire(expression, sched, after, loc)
		return next, !next.IsZero(), nil
	case ScheduleTypeInterval:
		d, err := ParseInterval(expression)
		if err != nil {
			return time.Time{}, false, err
		}
		if anchor.IsZero() {
			anchor = after
		}
		if anchor.After(after) {
			return anchor, true, nil
		}
		steps := after.Sub(anchor)/d + 1
		return anchor.Add(steps * d), true, nil
	case ScheduleTypeOneTime:
		at, err := ParseOneTime(expression, timezone)
		if err != nil {
			return time.Time{}, false, err
		}
		return at, at.After(after), nil
	default:
		return time.Time{}, false, fmt.Errorf(unknownScheduleTypeFormat, scheduleType)
	}
}

func FirstFireTime(scheduleType ScheduleType, expression, timezone string, now time.Time, fireImmediately bool) (time.Time, bool, error) {
	switch scheduleType {
	case ScheduleTypeOneTime:
		at, err := ParseOneTime(expression, timezone)
		return at, err == nil, err
	case ScheduleTypeInterval:
		if fireImmediately {
			if _, err := ParseInterval(expression); err != nil {
				return time.Time{}, false, err
			}
			return now, true, nil
		}
	}
	return NextFireTime(scheduleType, expression, timezone, time.Time{}, now)
}

func loadTimezone(tz string) (*time.Location, error) {
	if tz == "" {
		tz = defaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}

func parseDuration(s string) (time.Duration, error) {
	if numStr, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q: %w", s, err)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}

func parseISO8601(s string, loc *time.Location) (time.Time, error) {
	offsetFormats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04-07:00",
	}
	for _, f := range offsetFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}

	localFormats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range localFormats {
		if t, err := time.ParseInLocation(f, s, loc); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse %q as ISO 8601 datetime", s)
}
