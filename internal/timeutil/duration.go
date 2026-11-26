// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package timeutil

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const day = 24 * time.Hour

const maxDays = float64(math.MaxInt64 / int64(day))

func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	if strings.HasSuffix(s, "d") {
		numStr := s[:len(s)-1]
		days, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		if math.IsNaN(days) || math.Abs(days) > maxDays {
			return 0, fmt.Errorf("invalid duration %q: out of range", s)
		}
		return time.Duration(days * float64(day)), nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}
