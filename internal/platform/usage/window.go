// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"errors"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type Period string

const (
	PeriodThisMonth  Period = "this_month"
	PeriodLastMonth  Period = "last_month"
	PeriodLast7Days  Period = "last_7_days"
	PeriodLast30Days Period = "last_30_days"
	PeriodLast90Days Period = "last_90_days"
)

var lastDays = map[Period]int{PeriodLast7Days: 7, PeriodLast30Days: 30, PeriodLast90Days: 90}

type WindowRequest struct {
	Period             Period
	StartDate, EndDate *time.Time
}

type Window struct {
	Start, End, DayEnd int64
}

func (w Window) rollupDays() (from, to int64) {
	return w.Start, store.SpendRollupDayEnd(w.End)
}

type ValidationError struct {
	Field, Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

type ValidationErrors []*ValidationError

func (e ValidationErrors) Error() string {
	parts := make([]string, len(e))
	for i, v := range e {
		parts[i] = v.Error()
	}
	return strings.Join(parts, "; ")
}

func (e ValidationErrors) Err() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

func Problems(err error) ValidationErrors {
	var many ValidationErrors
	if errors.As(err, &many) {
		return many
	}
	var one *ValidationError
	if errors.As(err, &one) {
		return ValidationErrors{one}
	}
	return nil
}

func ResolveWindow(req WindowRequest, now time.Time) (Window, error) {
	nowMs := now.UnixMilli()
	switch {
	case req.Period != "" && (req.StartDate != nil || req.EndDate != nil):
		return Window{}, &ValidationError{"period", "send a period or startDate and endDate, not both"}
	case req.StartDate == nil && req.EndDate != nil:
		return Window{}, &ValidationError{"startDate", "startDate and endDate go together"}
	case req.StartDate != nil && req.EndDate == nil:
		return Window{}, &ValidationError{"endDate", "startDate and endDate go together"}
	case req.StartDate != nil:
		return customWindow(*req.StartDate, *req.EndDate, now)
	}
	today := startOfDay(nowMs)
	monthStart := budget.PeriodStart(now).UnixMilli()
	switch period := req.Period; {
	case period == "" || period == PeriodThisMonth:
		return Window{Start: monthStart, End: nowMs, DayEnd: budget.PeriodEnd(now).UnixMilli()}, nil
	case period == PeriodLastMonth:
		start := budget.PeriodStart(time.UnixMilli(monthStart - 1)).UnixMilli()
		return Window{Start: start, End: monthStart, DayEnd: monthStart}, nil
	case lastDays[period] > 0:
		return Window{Start: today - int64(lastDays[period]-1)*dayMillis, End: nowMs, DayEnd: today + dayMillis}, nil
	}
	return Window{}, &ValidationError{"period", "period must be this_month, last_month, last_7_days, last_30_days or last_90_days"}
}

func customWindow(startDate, endDate time.Time, now time.Time) (Window, error) {
	nowMs := now.UnixMilli()
	start, dayEnd := startOfDay(startDate.UnixMilli()), startOfDay(endDate.UnixMilli())+dayMillis
	switch {
	case start >= dayEnd:
		return Window{}, &ValidationError{"endDate", "endDate is before startDate"}
	case start > nowMs:
		return Window{}, &ValidationError{"startDate", "startDate is in the future"}
	case start < governor.RetentionFloor(now):
		return Window{}, &ValidationError{"startDate", "startDate is before the retained usage history"}
	case dayEnd > budget.PeriodEnd(now).UnixMilli():
		return Window{}, &ValidationError{"endDate", "endDate is after the end of this month"}
	}
	return Window{Start: start, End: min(dayEnd, nowMs), DayEnd: dayEnd}, nil
}

func Comparison(w Window, now time.Time) *Window {
	var c Window
	months, whole := wholeMonths(w)
	switch {
	case whole && months == 1 && w.End < w.DayEnd:
		prevStart := budget.PeriodStart(time.UnixMilli(w.Start - 1))
		day := min(time.UnixMilli(w.End).UTC().Day(), time.UnixMilli(w.Start-1).UTC().Day())
		end := prevStart.AddDate(0, 0, day-1).UnixMilli() + w.End - startOfDay(w.End)
		c = Window{Start: prevStart.UnixMilli(), End: end, DayEnd: store.SpendRollupDayEnd(end)}
	case whole && w.End == w.DayEnd:
		start := time.UnixMilli(w.Start).UTC().AddDate(0, -months, 0).UnixMilli()
		c = Window{Start: start, End: w.Start, DayEnd: w.Start}
	default:
		shift := w.DayEnd - w.Start
		c = Window{Start: w.Start - shift, End: w.End - shift, DayEnd: w.Start}
	}
	if c.Start < governor.RetentionFloor(now) {
		return nil
	}
	return &c
}

func wholeMonths(w Window) (int, bool) {
	start, end := time.UnixMilli(w.Start).UTC(), time.UnixMilli(w.DayEnd).UTC()
	if start.Day() != 1 || end.Day() != 1 || end.Hour() != 0 {
		return 0, false
	}
	return (end.Year()-start.Year())*12 + int(end.Month()-start.Month()), true
}
