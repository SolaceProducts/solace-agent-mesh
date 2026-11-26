// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func TestParseCronBareEmbeddedTimezoneIsAnErrorNotAPanic(t *testing.T) {
	for _, expr := range []string{"CRON_TZ=UTC", "TZ=UTC", "CRON_TZ=", "TZ="} {
		t.Run(expr, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseCron(%q) panicked: %v", expr, r)
				}
			}()
			if _, err := ParseCron(expr); err == nil {
				t.Fatalf("ParseCron(%q) = nil error, want error", expr)
			}
		})
	}
}

func TestValidateExpressionRejectsEmbeddedTimezoneForNewSchedules(t *testing.T) {
	for _, expr := range []string{
		"CRON_TZ=UTC",
		"TZ=UTC",
		"CRON_TZ=Asia/Tokyo 0 9 * * *",
		"TZ=Asia/Tokyo 0 9 * * *",
	} {
		t.Run(expr, func(t *testing.T) {
			if err := ValidateExpression(ScheduleTypeCron, expr, "UTC"); err == nil {
				t.Fatalf("ValidateExpression accepted %q, want error", expr)
			}
		})
	}
}

func TestPreviewNextTimesRejectsEmbeddedTimezone(t *testing.T) {
	if _, err := PreviewNextTimes(ScheduleTypeCron, "CRON_TZ=Asia/Tokyo 0 9 * * *", "UTC", 3); err == nil {
		t.Fatal("PreviewNextTimes accepted an embedded timezone, want error")
	}
}

func TestNextFireTimeStillFiresStoredRowsInTheirEmbeddedTimezone(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		taskZone string
		after    time.Time
		want     time.Time
	}{
		{
			name: "Tokyo prefix, task zone UTC", expr: "CRON_TZ=Asia/Tokyo 0 9 * * *", taskZone: "UTC",
			after: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), want: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Tokyo prefix, task zone New York", expr: "CRON_TZ=Asia/Tokyo 0 9 * * *", taskZone: "America/New_York",
			after: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), want: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "TZ prefix, task zone Auckland", expr: "TZ=Asia/Tokyo 0 9 * * *", taskZone: "Pacific/Auckland",
			after: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), want: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "daylight-saving embedded zone, task zone without it", expr: "CRON_TZ=America/New_York 0 9 * * *", taskZone: "Asia/Tokyo",
			after: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), want: time.Date(2026, 7, 1, 13, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := NextFireTime(ScheduleTypeCron, tt.expr, tt.taskZone, time.Time{}, tt.after)
			if err != nil || !ok {
				t.Fatalf("NextFireTime = %v, ok=%v, err=%v", got, ok, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("next = %s, want %s", got.UTC(), tt.want)
			}
		})
	}
}

func TestValidateExpressionRejectsCronThatNeverFires(t *testing.T) {
	err := ValidateExpression(ScheduleTypeCron, "0 0 30 2 *", "UTC")
	if err == nil {
		t.Fatal("ValidateExpression accepted 0 0 30 2 * (Feb 30), want error")
	}
}

func TestPreviewNextTimesFiresOnTheSpringChangeDayInEveryZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2027, 3, 25, 12, 0, 0, 0, time.UTC)

	got, err := previewNextTimesFrom(now, ScheduleTypeCron, "30 2 * * *", "Europe/Berlin", 5)
	if err != nil {
		t.Fatal(err)
	}

	want := time.Date(2027, 3, 28, 3, 30, 0, 0, berlin)
	found := false
	for _, fire := range got {
		found = found || fire.Equal(want)
	}
	if !found {
		t.Errorf("preview = %v, want it to contain %s", got, want)
	}
}
