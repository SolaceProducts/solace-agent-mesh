// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"testing"
	"time"
)

func TestDurationMillisKeepsSubMillisecond(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want float64
	}{
		{name: "microseconds", in: 250 * time.Microsecond, want: 0.25},
		{name: "just under a millisecond", in: 999 * time.Microsecond, want: 0.999},
		{name: "a single microsecond", in: time.Microsecond, want: 0.001},
		{name: "fractional milliseconds", in: 1500 * time.Microsecond, want: 1.5},
		{name: "whole milliseconds", in: 42 * time.Millisecond, want: 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := durationMillis(tc.in)
			if got != tc.want {
				t.Errorf("durationMillis(%v) = %v, want %v; a truncating implementation "+
					"reports 0 here, which reads as 'no execution happened' rather than "+
					"'it happened very fast'", tc.in, got, tc.want)
			}
		})
	}
}
