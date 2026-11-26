// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"testing"
	"time"
)

func utcAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextFireTime_HourListsRangesAndStepRangesFireOncePerWallTimeAcrossDST(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		start string
		want  []string
	}{
		{
			name: "hour list on the New York fall-back day", expr: "30 1,13 * * *", start: "2026-10-31T12:00:00Z",
			want: []string{"2026-10-31T17:30:00Z", "2026-11-01T05:30:00Z", "2026-11-01T18:30:00Z", "2026-11-02T06:30:00Z"},
		},
		{
			name: "hour range on the New York fall-back day", expr: "30 1-3 * * *", start: "2026-10-31T12:00:00Z",
			want: []string{"2026-11-01T05:30:00Z", "2026-11-01T07:30:00Z", "2026-11-01T08:30:00Z", "2026-11-02T06:30:00Z"},
		},
		{
			name: "hour step-range on the New York fall-back day", expr: "30 1-23/2 * * *", start: "2026-11-01T04:00:00Z",
			want: []string{"2026-11-01T05:30:00Z", "2026-11-01T08:30:00Z", "2026-11-01T10:30:00Z"},
		},
		{
			name: "hour list on the New York spring-forward day", expr: "30 2,14 * * *", start: "2027-03-13T12:00:00Z",
			want: []string{"2027-03-13T19:30:00Z", "2027-03-14T07:30:00Z", "2027-03-14T18:30:00Z"},
		},
		{
			name: "hour step-range on the New York spring-forward day", expr: "30 0-22/2 * * *", start: "2027-03-14T05:00:00Z",
			want: []string{"2027-03-14T05:30:00Z", "2027-03-14T07:30:00Z", "2027-03-14T08:30:00Z"},
		},
		{
			name: "wildcard step keeps firing through the repeated hour", expr: "30 */1 * * *", start: "2026-11-01T05:00:00Z",
			want: []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z"},
		},
		{
			name: "a list with a wildcard element anywhere follows elapsed time", expr: "30 1,*/1 * * *", start: "2026-11-01T05:00:00Z",
			want: []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z"},
		},
		{
			name: "question-mark hours follow elapsed time", expr: "30 ? * * *", start: "2026-11-01T05:00:00Z",
			want: []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fireSequence(t, tt.expr, "America/New_York", utcAt(tt.start), len(tt.want))
			for i, w := range tt.want {
				if want := utcAt(w); !got[i].Equal(want) {
					t.Errorf("fire %d = %s, want %s (all fires: %v)", i, got[i].UTC().Format(time.RFC3339), w, got)
				}
			}
		})
	}
}
