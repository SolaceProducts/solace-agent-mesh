// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func TestPeriodBounds(t *testing.T) {
	utc := func(y int, m time.Month, d, h, min, s, ns int) time.Time {
		return time.Date(y, m, d, h, min, s, ns, time.UTC)
	}
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		at         time.Time
		start, end time.Time
	}{
		{"mid-month", utc(2026, 10, 10, 14, 2, 11, 0), utc(2026, 10, 1, 0, 0, 0, 0), utc(2026, 11, 1, 0, 0, 0, 0)},
		{"the first instant", utc(2026, 10, 1, 0, 0, 0, 0), utc(2026, 10, 1, 0, 0, 0, 0), utc(2026, 11, 1, 0, 0, 0, 0)},
		{"the last nanosecond", utc(2026, 10, 31, 23, 59, 59, 999_999_999), utc(2026, 10, 1, 0, 0, 0, 0), utc(2026, 11, 1, 0, 0, 0, 0)},
		{"December into January", utc(2026, 12, 15, 0, 0, 0, 0), utc(2026, 12, 1, 0, 0, 0, 0), utc(2027, 1, 1, 0, 0, 0, 0)},
		{"a leap February", utc(2028, 2, 29, 12, 0, 0, 0), utc(2028, 2, 1, 0, 0, 0, 0), utc(2028, 3, 1, 0, 0, 0, 0)},
		{"a local time past UTC midnight", time.Date(2026, 10, 31, 21, 0, 0, 0, toronto), utc(2026, 11, 1, 0, 0, 0, 0), utc(2026, 12, 1, 0, 0, 0, 0)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := PeriodStart(c.at); !got.Equal(c.start) || got.Location() != time.UTC {
				t.Errorf("PeriodStart(%v) = %v, want %v in UTC", c.at, got, c.start)
			}
			if got := PeriodEnd(c.at); !got.Equal(c.end) {
				t.Errorf("PeriodEnd(%v) = %v, want %v", c.at, got, c.end)
			}
		})
	}
}

func TestEffectiveCap(t *testing.T) {
	for _, c := range []struct {
		name   string
		amount *int64
		topUp  int64
		want   *int64
	}{
		{"no top-up", new(int64(250)), 0, new(int64(250))},
		{"adds the top-up", new(int64(250)), 50, new(int64(300))},
		{"a zero budget with a top-up", new(int64(0)), 50, new(int64(50))},
		{"unlimited ignores the top-up", nil, 50, nil},
		{"unlimited with none", nil, 0, nil},
		{"saturates instead of overflowing", new(int64(math.MaxInt64 - 10)), 50, new(int64(math.MaxInt64))},
		{"reaches the maximum exactly", new(int64(math.MaxInt64 - 50)), 50, new(int64(math.MaxInt64))},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := EffectiveCap(c.amount, c.topUp)
			switch {
			case got == nil && c.want == nil:
			case got == nil || c.want == nil || *got != *c.want:
				t.Errorf("EffectiveCap = %s, want %s", show(got), show(c.want))
			}
		})
	}
}

func TestEffectiveCap_DoesNotAliasTheAmount(t *testing.T) {
	amount := int64(250)
	got := EffectiveCap(&amount, 0)
	*got = 1
	if amount != 250 {
		t.Errorf("writing through EffectiveCap's result changed the amount to %d", amount)
	}
}

func show(v *int64) string {
	if v == nil {
		return "unlimited"
	}
	return strconv.FormatInt(*v, 10)
}
