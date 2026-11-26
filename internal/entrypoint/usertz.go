// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"regexp"
	"strings"
	"time"
)

const maxUserTimezoneLen = 64

const hostLocalTimezoneName = "Local"

var userTZCharset = regexp.MustCompile(`^[A-Za-z0-9_+\-/]+$`)

func NormalizeUserTimezone(tz string) (string, bool) {
	tz = strings.TrimSpace(tz)
	if tz == "" || tz == hostLocalTimezoneName || len(tz) > maxUserTimezoneLen || !userTZCharset.MatchString(tz) {
		return "", false
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", false
	}
	return tz, true
}
