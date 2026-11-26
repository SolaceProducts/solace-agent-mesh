// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func TestNextFireTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name       string
		typ        ScheduleType
		expr, tz   string
		anchor     time.Time
		after      time.Time
		want       time.Time
		wantFires  bool
		wantErrors bool
	}{
		{name: "cron next in UTC", typ: ScheduleTypeCron, expr: "0 9 * * *",
			after: at("2026-10-21T09:00:00Z"), want: at("2026-10-22T09:00:00Z"), wantFires: true},
		{name: "cron in the schedule's timezone", typ: ScheduleTypeCron, expr: "0 9 * * *", tz: "America/New_York",
			after: at("2026-10-21T12:00:00Z"), want: time.Date(2026, 10, 21, 9, 0, 0, 0, ny), wantFires: true},
		{name: "cron across the autumn clock change", typ: ScheduleTypeCron, expr: "0 9 * * *", tz: "America/New_York",
			after: time.Date(2026, 10, 31, 9, 0, 0, 0, ny), want: time.Date(2026, 11, 1, 9, 0, 0, 0, ny), wantFires: true},
		{name: "interval steps from the due time", typ: ScheduleTypeInterval, expr: "1h",
			anchor: at("2026-10-21T10:00:00Z"), after: at("2026-10-21T10:00:07Z"), want: at("2026-10-21T11:00:00Z"), wantFires: true},
		{name: "interval skips the times missed during an outage", typ: ScheduleTypeInterval, expr: "1h",
			anchor: at("2026-10-21T10:00:00Z"), after: at("2026-10-21T13:20:00Z"), want: at("2026-10-21T14:00:00Z"), wantFires: true},
		{name: "interval due exactly at after moves one step", typ: ScheduleTypeInterval, expr: "1h",
			anchor: at("2026-10-21T10:00:00Z"), after: at("2026-10-21T11:00:00Z"), want: at("2026-10-21T12:00:00Z"), wantFires: true},
		{name: "interval without an anchor steps from after", typ: ScheduleTypeInterval, expr: "90m",
			after: at("2026-10-21T10:00:00Z"), want: at("2026-10-21T11:30:00Z"), wantFires: true},
		{name: "interval anchor still ahead", typ: ScheduleTypeInterval, expr: "1h",
			anchor: at("2026-10-21T12:00:00Z"), after: at("2026-10-21T10:00:00Z"), want: at("2026-10-21T12:00:00Z"), wantFires: true},
		{name: "one-time still ahead", typ: ScheduleTypeOneTime, expr: "2026-10-21T15:00:00Z",
			after: at("2026-10-21T10:00:00Z"), want: at("2026-10-21T15:00:00Z"), wantFires: true},
		{name: "one-time in its timezone", typ: ScheduleTypeOneTime, expr: "2026-10-21T15:00:00", tz: "America/New_York",
			after: at("2026-10-21T10:00:00Z"), want: time.Date(2026, 10, 21, 15, 0, 0, 0, ny), wantFires: true},
		{name: "one-time already passed", typ: ScheduleTypeOneTime, expr: "2026-10-21T15:00:00Z",
			after: at("2026-10-21T15:00:00Z"), want: at("2026-10-21T15:00:00Z")},
		{name: "bad timezone", typ: ScheduleTypeCron, expr: "0 9 * * *", tz: "Mars/Olympus", wantErrors: true},
		{name: "bad cron", typ: ScheduleTypeCron, expr: "nope", wantErrors: true},
		{name: "bad interval", typ: ScheduleTypeInterval, expr: "5s", wantErrors: true},
		{name: "bad one-time", typ: ScheduleTypeOneTime, expr: "tomorrow", wantErrors: true},
		{name: "unknown type", typ: "weekly", expr: "x", wantErrors: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fires, err := NextFireTime(tt.typ, tt.expr, tt.tz, tt.anchor, tt.after)
			if tt.wantErrors {
				if err == nil {
					t.Fatalf("NextFireTime = %s, %v; want an error", got, fires)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fires != tt.wantFires || !got.Equal(tt.want) {
				t.Fatalf("NextFireTime = %s, %v; want %s, %v", got, fires, tt.want, tt.wantFires)
			}
		})
	}
}

func TestFirstFireTime(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name            string
		typ             ScheduleType
		expr            string
		fireImmediately bool
		want            time.Time
	}{
		{name: "cron", typ: ScheduleTypeCron, expr: "0 * * * *", want: time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC)},
		{name: "cron ignores fireImmediately", typ: ScheduleTypeCron, expr: "0 * * * *", fireImmediately: true,
			want: time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC)},
		{name: "new interval fires now", typ: ScheduleTypeInterval, expr: "2h", fireImmediately: true, want: now},
		{name: "edited interval waits one interval", typ: ScheduleTypeInterval, expr: "2h", want: now.Add(2 * time.Hour)},
		{name: "one-time in the past keeps its time", typ: ScheduleTypeOneTime, expr: "2026-10-21T10:25:00Z",
			want: time.Date(2026, 10, 21, 10, 25, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fires, err := FirstFireTime(tt.typ, tt.expr, "UTC", now, tt.fireImmediately)
			if err != nil || !fires || !got.Equal(tt.want) {
				t.Fatalf("FirstFireTime = %s, %v, %v; want %s", got, fires, err, tt.want)
			}
		})
	}

	if _, _, err := FirstFireTime(ScheduleTypeInterval, "5s", "UTC", now, true); err == nil {
		t.Fatal("FirstFireTime accepted an interval below the minimum")
	}
	if _, _, err := FirstFireTime(ScheduleTypeOneTime, "soon", "UTC", now, false); err == nil {
		t.Fatal("FirstFireTime accepted a bad one-time expression")
	}
}
