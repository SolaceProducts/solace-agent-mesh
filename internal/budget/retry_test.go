// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetRetryAfter_RoundsUpAndNeverAsksForLessThanASecond(t *testing.T) {
	now := time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC)
	for _, tc := range []struct {
		resetsAt time.Time
		want     string
	}{
		{now.Add(90 * time.Second), "90"},
		{now.Add(1500 * time.Millisecond), "2"},
		{now, "1"},
		{now.Add(-time.Hour), "1"},
	} {
		rec := httptest.NewRecorder()
		SetRetryAfter(rec, tc.resetsAt, now)
		if got := rec.Header().Get("Retry-After"); got != tc.want {
			t.Errorf("Retry-After for a reset %s away = %q, want %q", tc.resetsAt.Sub(now), got, tc.want)
		}
	}
}
