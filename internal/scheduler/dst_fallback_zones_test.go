// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

type wallTime struct {
	year  int
	month time.Month
	day   int
	hour  int
	min   int
}

func TestNextFireTime_FallBackFiresOnceAtTheRequestedWallTimeInEveryZone(t *testing.T) {
	tests := []struct {
		name           string
		zone           string
		expr           string
		start          time.Time
		want           []wallTime
		onTheChangeUTC string
	}{
		{
			name: "Berlin repeated 02:30", zone: "Europe/Berlin", expr: "30 2 * * *",
			start:          time.Date(2027, 10, 29, 12, 0, 0, 0, time.UTC),
			want:           []wallTime{{2027, 10, 30, 2, 30}, {2027, 10, 31, 2, 30}, {2027, 11, 1, 2, 30}},
			onTheChangeUTC: "2027-10-31T01:30:00Z",
		},
		{
			name: "London repeated 01:30", zone: "Europe/London", expr: "30 1 * * *",
			start:          time.Date(2027, 10, 29, 12, 0, 0, 0, time.UTC),
			want:           []wallTime{{2027, 10, 30, 1, 30}, {2027, 10, 31, 1, 30}, {2027, 11, 1, 1, 30}},
			onTheChangeUTC: "2027-10-31T01:30:00Z",
		},
		{
			name: "Sydney repeated 02:30", zone: "Australia/Sydney", expr: "30 2 * * *",
			start:          time.Date(2027, 4, 2, 12, 0, 0, 0, time.UTC),
			want:           []wallTime{{2027, 4, 3, 2, 30}, {2027, 4, 4, 2, 30}, {2027, 4, 5, 2, 30}},
			onTheChangeUTC: "2027-04-03T16:30:00Z",
		},
		{
			name: "Auckland repeated 02:30", zone: "Pacific/Auckland", expr: "30 2 * * *",
			start:          time.Date(2027, 4, 2, 12, 0, 0, 0, time.UTC),
			want:           []wallTime{{2027, 4, 3, 2, 30}, {2027, 4, 4, 2, 30}, {2027, 4, 5, 2, 30}},
			onTheChangeUTC: "2027-04-03T14:30:00Z",
		},
		{
			name: "Lord Howe repeated 01:45 in a half-hour change", zone: "Australia/Lord_Howe", expr: "45 1 * * *",
			start:          time.Date(2027, 4, 2, 12, 0, 0, 0, time.UTC),
			want:           []wallTime{{2027, 4, 3, 1, 45}, {2027, 4, 4, 1, 45}, {2027, 4, 5, 1, 45}},
			onTheChangeUTC: "2027-04-03T15:15:00Z",
		},
		{
			name: "New York repeated 01:30", zone: "America/New_York", expr: "30 1 * * *",
			start:          time.Date(2027, 11, 5, 12, 0, 0, 0, time.UTC),
			want:           []wallTime{{2027, 11, 6, 1, 30}, {2027, 11, 7, 1, 30}, {2027, 11, 8, 1, 30}},
			onTheChangeUTC: "2027-11-07T05:30:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatalf("LoadLocation(%q): %v", tt.zone, err)
			}
			got := fireSequence(t, tt.expr, tt.zone, tt.start, len(tt.want))
			if onTheChange := utcAt(tt.onTheChangeUTC); !got[1].Equal(onTheChange) {
				t.Errorf("fire on the change day = %s, want %s", got[1].UTC().Format(time.RFC3339), tt.onTheChangeUTC)
			}
			for i, w := range tt.want {
				want := time.Date(w.year, w.month, w.day, w.hour, w.min, 0, 0, loc)
				if !got[i].Equal(want) {
					t.Errorf("fire %d = %s, want %s (all fires: %v)", i, got[i].In(loc), want, got)
				}
			}
		})
	}
}
