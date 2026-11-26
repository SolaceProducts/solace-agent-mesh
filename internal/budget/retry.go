// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

func RetryAt(err error) (time.Time, bool) {
	if unconfirmed, ok := errors.AsType[*UnconfirmedError](err); ok {
		return unconfirmed.RetryAt, true
	}
	return ResetsAt(err)
}

func ResetsAt(err error) (time.Time, bool) {
	exhausted, ok := errors.AsType[*ExhaustedError](err)
	if !ok {
		return time.Time{}, false
	}
	return exhausted.ResetsAt, true
}

func SetRetryAfter(w http.ResponseWriter, at, now time.Time) {
	seconds := int64(math.Ceil(at.Sub(now).Seconds()))
	w.Header().Set("Retry-After", strconv.FormatInt(max(seconds, 1), 10))
}
