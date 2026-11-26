// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func TestParseCron(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{name: "every minute", expr: "* * * * *"},
		{name: "9am daily", expr: "0 9 * * *"},
		{name: "weekdays at noon", expr: "0 12 * * 1-5"},
		{name: "first of month", expr: "0 0 1 * *"},
		{name: "invalid field count", expr: "* * *", wantErr: true},
		{name: "invalid value", expr: "99 * * * *", wantErr: true},
		{name: "empty", expr: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, err := ParseCron(tt.expr)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			next := sched.Next(time.Now())
			if next.IsZero() {
				t.Fatal("schedule produced zero next time")
			}
		})
	}
}

func TestParseInterval(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		want    time.Duration
		wantErr bool
	}{
		{name: "60 seconds", expr: "60s", want: 60 * time.Second},
		{name: "5 minutes", expr: "5m", want: 5 * time.Minute},
		{name: "1 hour", expr: "1h", want: time.Hour},
		{name: "2 hours 30 min", expr: "2h30m", want: 2*time.Hour + 30*time.Minute},
		{name: "1 day", expr: "1d", want: 24 * time.Hour},
		{name: "7 days", expr: "7d", want: 7 * 24 * time.Hour},
		{name: "below minimum", expr: "30s", wantErr: true},
		{name: "zero", expr: "0s", wantErr: true},
		{name: "invalid", expr: "abc", wantErr: true},
		{name: "negative", expr: "-5m", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInterval(tt.expr)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseOneTime(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		timezone string
		wantErr  bool
	}{
		{name: "RFC3339", expr: "2030-01-15T09:00:00Z", timezone: "UTC"},
		{name: "with offset", expr: "2030-06-15T14:30:00-04:00", timezone: "America/New_York"},
		{name: "no offset with tz", expr: "2030-01-15T09:00:00", timezone: "America/New_York"},
		{name: "date only", expr: "2030-01-15", timezone: "UTC"},
		{name: "space separator", expr: "2030-01-15 09:00:00", timezone: "UTC"},
		{name: "invalid", expr: "not-a-date", timezone: "UTC", wantErr: true},
		{name: "invalid timezone", expr: "2030-01-15T09:00:00", timezone: "Invalid/TZ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseOneTime(tt.expr, tt.timezone)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsZero() {
				t.Fatal("got zero time")
			}
		})
	}
}

func TestParseOneTimeTimezoneInterpretation(t *testing.T) {
	result, err := ParseOneTime("2030-06-15T09:00:00", "America/New_York")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loc, _ := time.LoadLocation("America/New_York")
	expected := time.Date(2030, 6, 15, 9, 0, 0, 0, loc)
	if !result.Equal(expected) {
		t.Fatalf("got %v, want %v", result, expected)
	}
}

func TestValidateExpression(t *testing.T) {
	tests := []struct {
		name     string
		typ      ScheduleType
		expr     string
		timezone string
		wantErr  bool
	}{
		{name: "valid cron", typ: ScheduleTypeCron, expr: "0 9 * * *", timezone: "UTC"},
		{name: "invalid cron", typ: ScheduleTypeCron, expr: "bad", timezone: "UTC", wantErr: true},
		{name: "valid interval", typ: ScheduleTypeInterval, expr: "5m", timezone: "UTC"},
		{name: "invalid interval", typ: ScheduleTypeInterval, expr: "10s", timezone: "UTC", wantErr: true},
		{name: "valid one-time", typ: ScheduleTypeOneTime, expr: "2030-01-15T09:00:00Z", timezone: "UTC"},
		{name: "invalid one-time", typ: ScheduleTypeOneTime, expr: "nope", timezone: "UTC", wantErr: true},
		{name: "bad timezone", typ: ScheduleTypeCron, expr: "* * * * *", timezone: "Fake/Zone", wantErr: true},
		{name: "unknown type", typ: "unknown", expr: "anything", timezone: "UTC", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExpression(tt.typ, tt.expr, tt.timezone)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestPreviewNextTimes(t *testing.T) {
	t.Run("cron returns requested count", func(t *testing.T) {
		times, err := PreviewNextTimes(ScheduleTypeCron, "* * * * *", "UTC", 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(times) != 5 {
			t.Fatalf("got %d times, want 5", len(times))
		}
		for i := 1; i < len(times); i++ {
			if !times[i].After(times[i-1]) {
				t.Fatalf("times not in order: %v >= %v", times[i-1], times[i])
			}
		}
	})

	t.Run("interval returns requested count", func(t *testing.T) {
		times, err := PreviewNextTimes(ScheduleTypeInterval, "5m", "UTC", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(times) != 3 {
			t.Fatalf("got %d times, want 3", len(times))
		}
		for i := 1; i < len(times); i++ {
			diff := times[i].Sub(times[i-1])
			if diff != 5*time.Minute {
				t.Fatalf("expected 5m interval, got %v", diff)
			}
		}
	})

	t.Run("one-time returns single time", func(t *testing.T) {
		times, err := PreviewNextTimes(ScheduleTypeOneTime, "2030-01-15T09:00:00Z", "UTC", 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(times) != 1 {
			t.Fatalf("got %d times, want 1", len(times))
		}
	})

	t.Run("zero count returns nil", func(t *testing.T) {
		times, err := PreviewNextTimes(ScheduleTypeCron, "* * * * *", "UTC", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if times != nil {
			t.Fatalf("expected nil, got %v", times)
		}
	})
}

func TestCronTimezoneAwareness(t *testing.T) {
	times, err := PreviewNextTimes(ScheduleTypeCron, "0 9 * * *", "America/New_York", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(times) == 0 {
		t.Fatal("no times returned")
	}
	loc, _ := time.LoadLocation("America/New_York")
	hour := times[0].In(loc).Hour()
	if hour != 9 {
		t.Fatalf("expected hour 9 in America/New_York, got %d", hour)
	}
}
