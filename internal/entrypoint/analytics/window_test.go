// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"testing"
	"time"
)

func TestParseWindowDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"2h30m", 2*time.Hour + 30*time.Minute, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"90d", MaxWindow, false},
		{"", 0, true},
		{"0h", 0, true},
		{"-5m", 0, true},
		{"91d", 0, true},
		{"1y", 0, true},
		{"foo", 0, true},
		{"d", 0, true},
		{"1.5d", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseWindowDuration(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseWindowDuration(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWindowDuration(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseWindowDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDurationBucketEdgesMs(t *testing.T) {
	edges := DurationBucketEdgesMs
	if len(edges) == 0 {
		t.Fatal("DurationBucketEdgesMs must not be empty")
	}
	for i := 1; i < len(edges); i++ {
		if edges[i] <= edges[i-1] {
			t.Errorf("edges not strictly ascending at %d: %d <= %d", i, edges[i], edges[i-1])
		}
	}
	if edges[0] <= 0 {
		t.Errorf("first edge must be positive, got %d", edges[0])
	}
}
