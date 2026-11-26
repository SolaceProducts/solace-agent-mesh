// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const MaxWindow = 90 * 24 * time.Hour

func ParseWindowDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty window")
	}
	var dur time.Duration
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid day count in %q", s)
		}
		dur = time.Duration(days) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		dur = parsed
	}
	if dur <= 0 {
		return 0, fmt.Errorf("window must be positive, got %s", dur)
	}
	if dur > MaxWindow {
		return 0, fmt.Errorf("window %s exceeds %s maximum", dur, MaxWindow)
	}
	return dur, nil
}
