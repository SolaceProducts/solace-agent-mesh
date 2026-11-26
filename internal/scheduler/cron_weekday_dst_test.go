// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func TestNextCronFire_MonthlyWeekdayFormsAcrossSpringForward(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		expr  string
		after time.Time
		want  time.Time
	}{
		{
			name:  "first Monday after the change is at 09:00 EDT",
			expr:  "0 9 * * 1#1",
			after: time.Date(2027, 3, 1, 10, 0, 0, 0, ny),
			want:  time.Date(2027, 4, 5, 9, 0, 0, 0, ny),
		},
		{
			name:  "last Friday of the change month is at 17:00 EDT",
			expr:  "0 17 * * 5L",
			after: time.Date(2027, 2, 26, 17, 0, 0, 0, ny),
			want:  time.Date(2027, 3, 26, 17, 0, 0, 0, ny),
		},
		{
			name:  "second Sunday on the change day fires just after the skipped hour",
			expr:  "30 2 * * 0#2",
			after: time.Date(2027, 3, 1, 0, 0, 0, 0, ny),
			want:  time.Date(2027, 3, 14, 3, 30, 0, 0, ny),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, err := ParseCron(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			got := nextCronFire(tt.expr, sched, tt.after, ny)
			if !got.Equal(tt.want) {
				t.Fatalf("nextCronFire(%q after %s) = %s, want %s", tt.expr, tt.after, got, tt.want)
			}
		})
	}
}
