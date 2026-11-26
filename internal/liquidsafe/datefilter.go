// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package liquidsafe

import (
	"time"

	"github.com/osteele/liquid"
	"github.com/osteele/liquid/values"
	"github.com/osteele/tuesday"
)

var permissiveDateLayouts = []string{
	"2006-01-02T15:04:05.000-0700",
	"2006-01-02T15:04:05.000-07:00",
	"2006-01-02T15:04:05.000Z",
	time.RFC3339Nano,
	"2006-01-02T15:04:05-0700",
	"2006-01-02 15:04:05.000-0700",
	"2006-01-02 15:04:05.000-07:00",
}

func parsePermissiveDate(s string) (time.Time, error) {
	for _, layout := range permissiveDateLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return values.ParseDate(s)
}

func registerDateFilter(e *liquid.Engine) {
	e.RegisterFilter("date", func(s string, format func(string) string) (string, error) {
		t, err := parsePermissiveDate(s)
		if err != nil {
			return s, nil
		}
		f := format("%a, %b %d, %y")
		return tuesday.Strftime(f, t)
	})
}
