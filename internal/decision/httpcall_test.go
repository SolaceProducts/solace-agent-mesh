// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryAfter(t *testing.T) {
	const limit = 5 * time.Second
	cases := map[string]struct {
		header   string
		min, max time.Duration
	}{
		"absent":                    {header: "", min: 0, max: 0},
		"delta seconds":             {header: "2", min: 2 * time.Second, max: 2 * time.Second},
		"delta above the cap":       {header: "3600", min: limit, max: limit},
		"delta that would overflow": {header: "10000000000", min: limit, max: limit},
		"delta beyond int64":        {header: "99999999999999999999", min: 0, max: 0},
		"zero":                      {header: "0", min: 0, max: 0},
		"negative":                  {header: "-4", min: 0, max: 0},
		"garbage":                   {header: "soon", min: 0, max: 0},
		"date ahead":                {header: time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat), min: time.Second, max: 3 * time.Second},
		"date past the cap":         {header: time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), min: limit, max: limit},
		"date in the past":          {header: time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), min: 0, max: 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := RetryAfter(tc.header, limit); got < tc.min || got > tc.max {
				t.Errorf("RetryAfter(%q) = %s, want within [%s, %s]", tc.header, got, tc.min, tc.max)
			}
		})
	}
}
