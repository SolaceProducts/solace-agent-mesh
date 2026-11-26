// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"math"
	"testing"
)

func TestBelowMinimum(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   float64
		minimum int
		want    bool
	}{
		{"zero under one", 0, 1, true},
		{"the minimum itself", 1, 1, false},
		{"a huge value is above it, whatever the CPU", 1e30, 1, false},
		{"a hugely negative value is below it", -1e30, 1, true},
		{"not a number is not below it, which int() would call zero", math.NaN(), 1, false},
	} {
		if got := belowMinimum(tc.value, tc.minimum); got != tc.want {
			t.Errorf("%s: belowMinimum(%v, %d) = %v, want %v", tc.name, tc.value, tc.minimum, got, tc.want)
		}
	}
}
