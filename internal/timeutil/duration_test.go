// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package timeutil

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"seconds", "5s", 5 * time.Second, false},
		{"minutes", "1m", 1 * time.Minute, false},
		{"hours", "1h", 1 * time.Hour, false},
		{"milliseconds", "500ms", 500 * time.Millisecond, false},
		{"days", "1d", 24 * time.Hour, false},
		{"fractional days", "0.5d", 12 * time.Hour, false},
		{"multiple days", "7d", 7 * 24 * time.Hour, false},
		{"empty string", "", 0, false},
		{"whitespace", "  5s  ", 5 * time.Second, false},
		{"complex go duration", "1h30m", 90 * time.Minute, false},
		{"invalid", "abc", 0, true},
		{"invalid day", "xd", 0, true},
		{"infinite days", "infd", 0, true},
		{"not a number of days", "nand", 0, true},
		{"more days than a duration holds", "1e10d", 0, true},
		{"more negative days than a duration holds", "-1e10d", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
