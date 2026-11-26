// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	cronFieldCount                = 5
	domField                      = 2
	dowField                      = 4
	nthMarker                     = "#"
	lastMarker                    = "L"
	lastNth                       = 0
	maxNthOfMonth                 = 5
	lastDayOfWeek                 = int(time.Saturday)
	longestWeekdayRecurrenceYears = 41
)

var errWeekdayFormNeedsAnyDom = errors.New("day-of-month must be * or ? when day-of-week uses # or L")

type monthlyWeekdaySchedule struct {
	timesOnAnyDay cron.Schedule
	weekday       time.Weekday
	nth           int
}

func parseMonthlyWeekday(expression string) (sched cron.Schedule, isMonthlyWeekdayForm bool, err error) {
	fields := strings.Fields(expression)
	if len(fields) != cronFieldCount || !strings.ContainsAny(fields[dowField], "#Ll") {
		return nil, false, nil
	}
	weekday, nth, err := parseWeekdayOccurrence(fields[dowField])
	if err != nil {
		return nil, true, err
	}
	if dom := fields[domField]; dom != "*" && dom != "?" {
		return nil, true, errWeekdayFormNeedsAnyDom
	}
	fields[dowField] = "*"
	timesOnAnyDay, err := cronParser.Parse(strings.Join(fields, " "))
	if err != nil {
		return nil, true, err
	}
	return &monthlyWeekdaySchedule{timesOnAnyDay: timesOnAnyDay, weekday: weekday, nth: nth}, true, nil
}

func parseWeekdayOccurrence(field string) (time.Weekday, int, error) {
	if dayStr, nthStr, found := strings.Cut(field, nthMarker); found {
		weekday, err := parseCronWeekday(dayStr, field)
		if err != nil {
			return 0, 0, err
		}
		nth, err := strconv.Atoi(nthStr)
		if err != nil || nth < 1 || nth > maxNthOfMonth {
			return 0, 0, fmt.Errorf("day-of-week %q: the occurrence after # must be 1-%d", field, maxNthOfMonth)
		}
		return weekday, nth, nil
	}
	dayStr, found := strings.CutSuffix(strings.ToUpper(field), lastMarker)
	if !found {
		return 0, 0, fmt.Errorf("day-of-week %q: L must follow a single weekday 0-%d, as in 5L", field, lastDayOfWeek)
	}
	weekday, err := parseCronWeekday(dayStr, field)
	return weekday, lastNth, err
}

func parseCronWeekday(s, field string) (time.Weekday, error) {
	day, err := strconv.Atoi(s)
	if err != nil || day < 0 || day > lastDayOfWeek {
		return 0, fmt.Errorf("day-of-week %q: # and L take a single weekday 0-%d (0 is Sunday), not a list, range or step", field, lastDayOfWeek)
	}
	return time.Weekday(day), nil
}

func (s *monthlyWeekdaySchedule) Next(t time.Time) time.Time {
	year, month, _ := t.Date()
	for i := range longestWeekdayRecurrenceYears * 12 {
		day, ok := s.dayOf(year, month+time.Month(i), t.Location())
		if !ok {
			continue
		}
		if at, ok := s.fireOn(day, t); ok {
			return at
		}
	}
	return time.Time{}
}

func (s *monthlyWeekdaySchedule) dayOf(year int, month time.Month, loc *time.Location) (day time.Time, monthHasOccurrence bool) {
	firstOfMonth := midday(year, month, 1, loc)
	if s.nth == lastNth {
		lastOfMonth := firstOfMonth.AddDate(0, 1, -1)
		back := (int(lastOfMonth.Weekday()) - int(s.weekday) + 7) % 7
		return lastOfMonth.AddDate(0, 0, -back), true
	}
	forward := (int(s.weekday)-int(firstOfMonth.Weekday())+7)%7 + 7*(s.nth-1)
	day = firstOfMonth.AddDate(0, 0, forward)
	return day, day.Month() == firstOfMonth.Month()
}

func (s *monthlyWeekdaySchedule) fireOn(day, after time.Time) (time.Time, bool) {
	cursor := justBeforeLocalMidnight(day)
	if after.After(cursor) {
		cursor = after
	}
	for {
		next := s.timesOnAnyDay.Next(cursor)
		if next.IsZero() {
			return time.Time{}, false
		}
		switch compareDates(next, day) {
		case 0:
			return next, true
		case 1:
			return time.Time{}, false
		}
		cursor = next
	}
}

func justBeforeLocalMidnight(day time.Time) time.Time {
	year, month, date := day.Date()
	return time.Date(year, month, date, 0, 0, 0, 0, day.Location()).Add(-time.Second)
}

func midday(year int, month time.Month, day int, loc *time.Location) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, loc)
}

func compareDates(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return midday(ay, am, ad, time.UTC).Compare(midday(by, bm, bd, time.UTC))
}
