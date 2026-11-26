// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"math"
	"time"
)

func PeriodStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func PeriodEnd(t time.Time) time.Time {
	return PeriodStart(t).AddDate(0, 1, 0)
}

func EffectiveCap(amount *int64, topUp int64) *int64 {
	if amount == nil {
		return nil
	}
	total := *amount + topUp
	if topUp > 0 && total < *amount {
		total = math.MaxInt64
	}
	return &total
}
